#include "settings.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "esp_log.h"
#include "lcd349.h"
#include "mbedtls/platform_util.h" /* mbedtls_platform_zeroize: a wipe the compiler keeps */
#include "prov.h"

static const char *TAG = "settings";

#define DEFAULT_VOLUME 70

static void get_str(const char *key, char *out, size_t size)
{
    size_t len = size;
    if (lcd349_kv_get_str(APP_NVS_NS, key, out, &len) != LCD349_OK) {
        out[0] = '\0';
    }
}

/* A stored number, clamped to 0-max; dflt when it isn't stored. */
static uint8_t get_uint(const char *key, uint8_t dflt, uint8_t max)
{
    char buf[8];
    get_str(key, buf, sizeof(buf));
    if (!buf[0]) {
        return dflt;
    }
    long v = strtol(buf, NULL, 10);
    return v < 0 ? 0 : v > max ? max : (uint8_t)v;
}

void settings_load_boot(app_boot_t *b)
{
    memset(b, 0, sizeof(*b));
    snprintf(b->fw, sizeof(b->fw), "%s", PAGER_FW_VERSION);
    b->no_sound = (lcd349_board_status() & LCD349_SUB_AUDIO) != 0; /* the kit carries on silent (O-49) */
    switch (lcd349_boot_reason()) { /* device info's STARTED (O-77) */
    case LCD349_BOOT_POWER_ON:
        b->start = APP_START_POWER_ON;
        break;
    case LCD349_BOOT_SOFTWARE:
        b->start = APP_START_RESTART;
        break;
    case LCD349_BOOT_CRASH:
        b->start = APP_START_CRASH;
        break;
    case LCD349_BOOT_BROWNOUT:
        b->start = APP_START_BROWNOUT;
        break;
    default:
        b->start = APP_START_RESET;
        break;
    }
    get_str(APP_KEY_NAME, b->name, sizeof(b->name));
    get_str(APP_KEY_HOST_KEY, b->host_key, sizeof(b->host_key));
    get_str(APP_KEY_HOST_NAME, b->host_name, sizeof(b->host_name));
    get_str(APP_KEY_HOST, b->host, sizeof(b->host)); /* where it dials, for device info (O-77) */
    char port[8];
    get_str(APP_KEY_PORT, port, sizeof(port));
    long p = strtol(port, NULL, 10);
    b->port = p > 0 && p <= UINT16_MAX ? (uint16_t)p : 0;
    b->paired = b->host_key[0] != '\0';
    b->volume = get_uint(APP_KEY_VOLUME, DEFAULT_VOLUME, 100);
    b->brightness = get_uint(APP_KEY_BRIGHTNESS, LCD349_BACKLIGHT_DEFAULT_PCT, 100);
    b->silent = get_uint(APP_KEY_SILENT, 0, 1) != 0;
    /* Unclamped: the core takes a number that's no palette as ORANGE. */
    b->colour = get_uint(APP_KEY_COLOUR, APP_PALETTE_ORANGE, UINT8_MAX);
    b->done_tone = get_uint(APP_KEY_DONE_TONE, 1, 1) != 0;
    if (prov_identity(b->pubkey, sizeof(b->pubkey)) != 0) {
        ESP_LOGW(TAG, "no device key yet");
    }
    if (lcd349_rtc_get(&b->rtc_s) != LCD349_OK) {
        b->rtc_s = 0;
    }
    b->seed = (uint32_t)(lcd349_time_us() * 2654435761u) ^ (uint32_t)b->rtc_s;
}

void settings_start_wifi(void)
{
    lcd349_wifi_net_t nets[LCD349_WIFI_MAX_NETWORKS];
    size_t n = get_uint(APP_KEY_WIFI_N, 0, LCD349_WIFI_MAX_NETWORKS);
    if (n == 0) {
        /* Unpaired: the radio was on only for the device key (prov.c), and the join's retry (pump.c) mustn't
         * keep it on. Pairing always saves 1-4 networks. */
        ESP_LOGI(TAG, "wifi: no network saved, so not started");
        return;
    }
    memset(nets, 0, sizeof(nets));
    for (size_t i = 0; i < n; i++) {
        char key[24];
        snprintf(key, sizeof(key), "wifi%u_ssid", (unsigned)i);
        get_str(key, nets[i].ssid, sizeof(nets[i].ssid));
        snprintf(key, sizeof(key), "wifi%u_pass", (unsigned)i);
        get_str(key, nets[i].password, sizeof(nets[i].password));
    }
    lcd349_err_t err = lcd349_wifi_set_networks(nets, n);
    if (err == LCD349_OK) {
        err = lcd349_wifi_start();
    }
    ESP_LOGI(TAG, "wifi: %u networks, %s", (unsigned)n, lcd349_err_str(err));
    mbedtls_platform_zeroize(nets, sizeof(nets)); /* the passwords; a memset here is a dead store */
}
