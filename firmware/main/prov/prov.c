/* The device's identity (prov.h): its ed25519 key pair, made on first use and kept as an NVS blob. The key
 * itself is made and read through net/ssh_link, the only file that reaches the SSH library. */
#include "prov.h"

#include <stdbool.h>
#include <string.h>

#include "app.h"
#include "emu_restart.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "lcd349.h"
#include "ssh_link.h"

static const char *TAG = "prov";

#define RADIO_WAIT_MS 5000 /* the kit's station has its radio on in well under a second */
#define RADIO_POLL_MS 10

/* The board's RNG is truly random only while an entropy source is on, and the radio is the one the kit
 * gives the pager (parent design §Provisioning, T12). Starts the kit's station if it isn't started, and
 * waits until it leaves IDLE: the kit reports SCANNING only once its radio is on, with or without a
 * network saved. Sets *started if this call started it. Returns whether the radio is on. */
static bool radio_on(bool *started)
{
    lcd349_wifi_status_t st;
    *started = false;
    if (lcd349_wifi_status(&st) != LCD349_OK) {
        return false;
    }
    if (st.state == LCD349_WIFI_IDLE) {
        if (lcd349_wifi_start() != LCD349_OK) {
            return false;
        }
        *started = true;
    }
    for (int waited = 0; waited < RADIO_WAIT_MS; waited += RADIO_POLL_MS) {
        if (lcd349_wifi_status(&st) == LCD349_OK && st.state != LCD349_WIFI_IDLE) {
            return true;
        }
        vTaskDelay(pdMS_TO_TICKS(RADIO_POLL_MS));
    }
    return false;
}

/* Makes and stores a new device key, with the radio on while it's made. No radio, no key: a later boot
 * tries again, rather than a key from an RNG that may not be random. */
static int make_key(char *priv, size_t priv_size, char *out, size_t size)
{
    bool started = false;
    int rc = -1;
    if (!radio_on(&started)) {
        ESP_LOGE(TAG, "device key: the radio didn't come on, so no key is made");
    } else {
        ESP_LOGI(TAG, "device key: the radio is on");
        if (ssh_link_keygen(priv, priv_size, out, size) == 0) {
            rc = lcd349_kv_set_blob(APP_NVS_NS, SSH_LINK_KEY_DEV, priv, strlen(priv)) == LCD349_OK ? 0 : -1;
            ESP_LOGI(TAG, "new device key: %s", rc == 0 ? "stored" : "NOT stored");
        } else {
            ESP_LOGE(TAG, "can't make a device key");
        }
    }
    if (started) {
        lcd349_wifi_stop(); /* an unpaired pager has no network to join; a paired one starts its station next */
    }
    return rc;
}

int prov_identity(char *out, size_t size)
{
    static char priv[SSH_LINK_PRIV_MAX]; /* the private key: wiped after use, never logged */
    size_t len = sizeof(priv) - 1;
    int rc = -1;
    emu_restart_install(); /* at boot, before any pairing can restart the device */
    if (size > 0) {
        out[0] = '\0';
    }
    lcd349_err_t err = lcd349_kv_get_blob(APP_NVS_NS, SSH_LINK_KEY_DEV, priv, &len);
    if (err == LCD349_OK) {
        priv[len] = '\0';
        rc = ssh_link_pubkey(priv, out, size);
        if (rc != 0) {
            ESP_LOGE(TAG, "the stored device key doesn't load");
        }
    } else if (err == LCD349_ERR_NOT_FOUND) {
        rc = make_key(priv, sizeof(priv), out, size);
    } else {
        /* A key that's there but can't be read is kept: a new one would lose the pairing without a word
         * (open-items O-07). Without a key the pager shows NO DEVICE KEY; a factory reset clears it. */
        ESP_LOGE(TAG, "the stored device key can't be read (%s), so no key is made", lcd349_err_str(err));
    }
    memset(priv, 0, sizeof(priv));
    if (rc != 0 && size > 0) {
        out[0] = '\0';
    }
    return rc;
}
