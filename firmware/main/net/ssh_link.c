/* The SSH link (ssh_link.h): libssh's client API on its own task, as phase 0's
 * spike proved it on the board and the Mac (spikes/ssh_esp/RESULTS.md):
 * - the link opens the TCP socket itself and hands it over (SSH_OPTIONS_FD),
 *   and closes it after ssh_disconnect, which never does;
 * - HOME must be set, or ssh_connect fails with "Couldn't apply options";
 * - no config files and no known_hosts: the pinned key is the only host check;
 * - a read that times out returns SSH_AGAIN, never a byte count;
 * - a 20 KB task stack (its measured peak is about 14 KB).
 * The TCP connection, with the host handed to getaddrinfo as stored so that
 * `.local` names resolve, is ssh_link_addr.c's; the line assembler, which cuts
 * an over-long line for the core to drop, is ssh_link_lines.c's. */
#include "ssh_link.h"

#include <netdb.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include <libssh/libssh.h>

#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"
#include "lcd349.h"
#include "mbedtls/platform_util.h" /* mbedtls_platform_zeroize: a wipe the compiler keeps */
#include "ssh_link_addr.h"
#include "ssh_link_lines.h"

static const char *TAG = "ssh_link";

#define LINK_STACK (20 * 1024)
#define LINK_PRIO 1 /* the emulator's tasks share the C library's locks unprotected: app_main's priority */
#define TX_QUEUE_LEN 16
#define CONNECT_TIMEOUT_S 10
#define POLL_MS 50                 /* the read wait between sends: the verdict's latency budget (N2) */
#define PING_MS 60000              /* the protocol's keepalive (parent design §SSH link) */
#define SILENT_MS 150000           /* no bytes for this long: the link is dead */
#define PING_ID_BASE 0x80000000u   /* the core's request ids count up from 1; these never meet them */
#define READ_CHUNK 1024

typedef struct {
    char *data; /* malloc'd; freed by the link task */
    size_t len;
} tx_t;

static ssh_link_cbs_t s_cbs;
static QueueHandle_t s_tx;
static TaskHandle_t s_task;
static volatile bool s_want;      /* a connect was asked for */
static volatile bool s_busy;      /* a session is being opened or is open */
static volatile bool s_connected; /* lines may be sent */
static volatile bool s_stop;      /* disconnect asked for */

static app_ms_t now_ms(void)
{
    return lcd349_time_us() / 1000;
}

static void state(app_link_t st)
{
    if (s_cbs.on_state) {
        s_cbs.on_state(st, s_cbs.ctx);
    }
}

static void drain_tx(void)
{
    tx_t tx;
    while (s_tx && xQueueReceive(s_tx, &tx, 0) == pdTRUE) {
        free(tx.data);
    }
}

/* ---- Keys ---- */

/* "<type> <base64>" of key into out. */
static int openssh_line(ssh_key key, char *out, size_t size)
{
    char *b64 = NULL;
    if (ssh_pki_export_pubkey_base64(key, &b64) != SSH_OK) {
        return -1;
    }
    int n = snprintf(out, size, "%s %s", ssh_key_type_to_char(ssh_key_type(key)), b64);
    ssh_string_free_char(b64);
    return n > 0 && (size_t)n < size ? 0 : -1;
}

int ssh_link_keygen(char *priv, size_t priv_size, char *pub, size_t pub_size)
{
    ssh_key key = NULL;
    int rc = -1;
    char *pem = NULL;
    /* libssh seeds its generator once, at startup (its constructor), before any entropy source is on, and an
     * ed25519 key draws from it without a reseed. A strong draw reseeds it from the RNG first (mbedTLS's
     * prediction resistance on the board), so the key carries the entropy that's on now. */
    unsigned char reseed[32];
    int seeded = ssh_get_random(reseed, sizeof(reseed), 1);
    mbedtls_platform_zeroize(reseed, sizeof(reseed));
    if (seeded != 1) {
        return -1;
    }
    if (ssh_pki_generate_key(SSH_KEYTYPE_ED25519, NULL, &key) != SSH_OK) { /* 0.12's replacement for ssh_pki_generate */
        return -1;
    }
    if (ssh_pki_export_privkey_base64(key, NULL, NULL, NULL, &pem) == SSH_OK && strlen(pem) < priv_size &&
        openssh_line(key, pub, pub_size) == 0) {
        snprintf(priv, priv_size, "%s", pem);
        rc = 0;
    }
    if (pem) {
        mbedtls_platform_zeroize(pem, strlen(pem));
        ssh_string_free_char(pem);
    }
    ssh_key_free(key);
    return rc;
}

int ssh_link_pubkey(const char *priv, char *pub, size_t pub_size)
{
    ssh_key key = NULL, pk = NULL;
    int rc = -1;
    if (ssh_pki_import_privkey_base64(priv, NULL, NULL, NULL, &key) == SSH_OK &&
        ssh_key_type(key) == SSH_KEYTYPE_ED25519 && ssh_pki_export_privkey_to_pubkey(key, &pk) == SSH_OK) {
        rc = openssh_line(pk, pub, pub_size);
    }
    ssh_key_free(pk);
    ssh_key_free(key);
    return rc;
}

/* The pinned host key, from its stored OpenSSH line. */
static ssh_key pinned_key(const char *line)
{
    const char *b64 = strchr(line, ' ');
    ssh_key key = NULL;
    if (!b64 || strncmp(line, "ssh-ed25519 ", 12) != 0 ||
        ssh_pki_import_pubkey_base64(b64 + 1, SSH_KEYTYPE_ED25519, &key) != SSH_OK) {
        return NULL;
    }
    return key;
}

/* ---- One session ---- */

typedef struct {
    char host[APP_HOST_NAME_MAX + 1];
    uint16_t port;
    char host_key[APP_KEY_MAX + 1];
    char priv[SSH_LINK_PRIV_MAX];
} config_t;

static bool load_str(const char *key, char *out, size_t size)
{
    size_t len = size;
    return lcd349_kv_get_str(APP_NVS_NS, key, out, &len) == LCD349_OK && out[0];
}

static bool load_config(config_t *c)
{
    char port[8];
    size_t len = sizeof(c->priv) - 1;
    if (!load_str(APP_KEY_HOST, c->host, sizeof(c->host)) || !load_str(APP_KEY_PORT, port, sizeof(port)) ||
        !load_str(APP_KEY_HOST_KEY, c->host_key, sizeof(c->host_key)) ||
        lcd349_kv_get_blob(APP_NVS_NS, SSH_LINK_KEY_DEV, c->priv, &len) != LCD349_OK) {
        return false;
    }
    c->priv[len] = '\0';
    long p = strtol(port, NULL, 10);
    c->port = p > 0 && p <= 65535 ? (uint16_t)p : 0;
    return c->port != 0;
}

static int write_line(ssh_channel ch, const char *data, size_t len)
{
    if (ssh_channel_write(ch, data, (uint32_t)len) != (int)len || ssh_channel_write(ch, "\n", 1) != 1) {
        return -1;
    }
    return 0;
}

static ssh_link_lines_t s_lines; /* the bridge's bytes into lines for on_line */

/* Talks over an open channel until it ends or a disconnect is asked for. */
static void run_channel(ssh_session ses, ssh_channel ch)
{
    static char buf[READ_CHUNK];
    app_ms_t heard = now_ms(), pinged = heard;
    uint32_t ping_id = PING_ID_BASE;
    ssh_link_lines_reset(&s_lines);
    while (!s_stop) {
        tx_t tx;
        bool ok = true;
        while (ok && xQueueReceive(s_tx, &tx, 0) == pdTRUE) {
            ok = write_line(ch, tx.data, tx.len) == 0;
            free(tx.data);
        }
        app_ms_t now = now_ms();
        if (ok && now - pinged >= PING_MS) {
            char ping[48];
            int n = snprintf(ping, sizeof(ping), "{\"t\":\"ping\",\"id\":%u}", (unsigned)ping_id++);
            ok = write_line(ch, ping, (size_t)n) == 0;
            pinged = now;
        }
        if (!ok) {
            ESP_LOGW(TAG, "write failed: %s", ssh_get_error(ses));
            return;
        }
        int r = ssh_channel_read_timeout(ch, buf, sizeof(buf), 0, POLL_MS);
        if (r == SSH_AGAIN) {
            r = 0;
        }
        if (r < 0) {
            ESP_LOGW(TAG, "read failed: %s", ssh_get_error(ses));
            return;
        }
        if (r > 0) {
            heard = now_ms();
            ssh_link_lines_take(&s_lines, buf, (size_t)r, s_cbs.on_line, s_cbs.ctx);
        } else if (ssh_channel_is_eof(ch) || ssh_channel_is_closed(ch) || !ssh_is_connected(ses)) {
            ESP_LOGI(TAG, "closed by the bridge");
            return;
        } else if (now_ms() - heard >= SILENT_MS) {
            ESP_LOGW(TAG, "silent for %d s: dropping the link", SILENT_MS / 1000);
            return;
        }
    }
}

/* Opens one session to the stored host and runs it. Returns the state the link ends in. */
static app_link_t session(void)
{
    static config_t c; /* the link task's: holds the private key while a session opens */
    app_link_t end = APP_LINK_DOWN;
    if (!load_config(&c)) {
        ESP_LOGW(TAG, "not paired: nothing to dial");
        return end;
    }
    ESP_LOGI(TAG, "dialling the bridge");
    ssh_key pin = pinned_key(c.host_key), dev = NULL, srv = NULL;
    ssh_session ses = ssh_new();
    ssh_channel ch = NULL;
    int fd = -1;
    if (ssh_pki_import_privkey_base64(c.priv, NULL, NULL, NULL, &dev) != SSH_OK) {
        dev = NULL;
    }
    mbedtls_platform_zeroize(c.priv, sizeof(c.priv));
    if (!pin || !dev || !ses) {
        ESP_LOGE(TAG, "the stored keys don't load");
        goto out;
    }
    long timeout = CONNECT_TIMEOUT_S;
    bool no = false;
    unsigned int port = c.port;
    if (ssh_options_set(ses, SSH_OPTIONS_HOST, c.host) < 0 || ssh_options_set(ses, SSH_OPTIONS_PORT, &port) < 0 ||
        ssh_options_set(ses, SSH_OPTIONS_USER, "pager") < 0 || ssh_options_set(ses, SSH_OPTIONS_TIMEOUT, &timeout) < 0 ||
        ssh_options_set(ses, SSH_OPTIONS_PROCESS_CONFIG, &no) < 0 ||
        ssh_options_set(ses, SSH_OPTIONS_SSH_DIR, "/nonexistent") < 0 ||
        ssh_options_set(ses, SSH_OPTIONS_KNOWNHOSTS, "/dev/null") < 0 ||
        ssh_options_set(ses, SSH_OPTIONS_GLOBAL_KNOWNHOSTS, "/dev/null") < 0 ||
        ssh_options_set(ses, SSH_OPTIONS_HOSTKEYS, "ssh-ed25519") < 0) {
        ESP_LOGE(TAG, "options: %s", ssh_get_error(ses));
        goto out;
    }
    const char *why = NULL;
    if ((fd = ssh_link_tcp_connect(c.host, c.port, CONNECT_TIMEOUT_S, getaddrinfo, &why)) < 0) {
        ESP_LOGW(TAG, "tcp: %s", why);
        goto out;
    }
    socket_t sfd = fd;
    if (ssh_options_set(ses, SSH_OPTIONS_FD, &sfd) < 0 || ssh_connect(ses) != SSH_OK) {
        ESP_LOGW(TAG, "ssh: %s", ssh_get_error(ses));
        goto out;
    }
    if (ssh_get_server_publickey(ses, &srv) != SSH_OK || ssh_key_cmp(srv, pin, SSH_KEY_CMP_PUBLIC) != 0) {
        ESP_LOGE(TAG, "the bridge's host key isn't the pinned one: HOST KEY CHANGED");
        end = APP_LINK_HOST_KEY_CHANGED;
        goto out;
    }
    int rc = ssh_userauth_publickey(ses, NULL, dev);
    if (rc == SSH_AUTH_DENIED || rc == SSH_AUTH_PARTIAL) {
        ESP_LOGW(TAG, "the bridge refused this device's key: NOT AUTHORISED");
        end = APP_LINK_AUTH_REFUSED;
        goto out;
    }
    if (rc != SSH_AUTH_SUCCESS) {
        ESP_LOGW(TAG, "auth: %s", ssh_get_error(ses));
        goto out;
    }
    ch = ssh_channel_new(ses);
    if (!ch || ssh_channel_open_session(ch) != SSH_OK || ssh_channel_request_exec(ch, "pager") != SSH_OK) {
        ESP_LOGW(TAG, "channel: %s", ssh_get_error(ses));
        goto out;
    }
    drain_tx(); /* nothing from before this session is sent in it */
    s_connected = true;
    state(APP_LINK_CONNECTED);
    run_channel(ses, ch);
out:
    s_connected = false;
    drain_tx();
    if (ch) {
        ssh_channel_free(ch);
    }
    if (ses) {
        if (ssh_is_connected(ses)) {
            ssh_disconnect(ses);
        }
        ssh_free(ses);
    }
    if (fd >= 0) {
        close(fd);
    }
    ssh_key_free(srv);
    ssh_key_free(dev);
    ssh_key_free(pin);
    return end;
}

static void link_task(void *arg)
{
    (void)arg;
    for (;;) {
        ulTaskNotifyTake(pdTRUE, portMAX_DELAY);
        if (!s_want) {
            continue;
        }
        s_want = false;
        state(APP_LINK_CONNECTING);
        app_link_t end = session();
        s_busy = false;
        state(end);
    }
}

/* ---- The interface ---- */

int ssh_link_init(const ssh_link_cbs_t *cbs)
{
    s_cbs = *cbs;
    if (!getenv("HOME")) {
        setenv("HOME", "/nonexistent", 0);
    }
    if (ssh_init() != SSH_OK) {
        return -1;
    }
    s_tx = xQueueCreate(TX_QUEUE_LEN, sizeof(tx_t));
    if (!s_tx || xTaskCreate(link_task, "pager_link", LINK_STACK, NULL, LINK_PRIO, &s_task) != pdPASS) {
        return -1;
    }
    return 0;
}

int ssh_link_connect(void)
{
    if (!s_task || s_busy) {
        return -1;
    }
    s_stop = false;
    s_busy = true;
    s_want = true;
    xTaskNotifyGive(s_task);
    return 0;
}

void ssh_link_disconnect(void)
{
    if (s_busy) {
        s_stop = true; /* the link task reports DOWN as the session ends */
    } else {
        state(APP_LINK_DOWN);
    }
}

int ssh_link_send(const char *line, size_t len)
{
    if (!s_connected) {
        return -1;
    }
    tx_t tx = {.data = malloc(len ? len : 1), .len = len};
    if (!tx.data) {
        return -1;
    }
    memcpy(tx.data, line, len);
    if (xQueueSend(s_tx, &tx, 0) != pdTRUE) {
        free(tx.data);
        return -1;
    }
    return 0;
}

const char *ssh_link_library_version(void)
{
    return ssh_version(0);
}
