/* Test hooks (design D2-4): test builds only. */
#include "sdkconfig.h"

#if CONFIG_PAGER_TEST_HOOKS

#include <stdio.h>
#include <string.h>

#include "esp_log.h"
#if !CONFIG_IDF_TARGET_LINUX
#include "esp_heap_caps.h"
#endif
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include "pump.h"
#include "testhooks.h"

static const char *TAG = "testhooks";

#define PRESS_MAX_MS 10000

/* The press is the button's down edge now and its up edge once the core's clock
 * has moved ms on. The console task waits it out, so presses never overlap. */
void testhooks_line(const char *line)
{
    char name[8], extra;
    unsigned ms;
    if (sscanf(line, "@T press %7s %u %c", name, &ms, &extra) != 2 || ms < 1 || ms > PRESS_MAX_MS ||
        (strcmp(name, "BOOT") != 0 && strcmp(name, "PWR") != 0)) {
        ESP_LOGW(TAG, "not a hook: %.40s", line);
        return;
    }
    app_btn_t btn = strcmp(name, "BOOT") == 0 ? APP_BTN_BOOT : APP_BTN_PWR;
    app_ms_t down = pump_now();
    pump_post_button(btn, true, down);
    vTaskDelay(pdMS_TO_TICKS(ms));
    while (pump_now() < down + ms) {
        vTaskDelay(1);
    }
    pump_post_button(btn, false, pump_now());
}

/* A bridge line's "t", such as snap: the protocol puts it first, so the start of the line is enough. */
static void line_type(const app_event_t *ev, char *out, size_t size)
{
    static const char key[] = "\"t\":\"";
    size_t n = ev->line.len < 64 ? ev->line.len : 64, i = 0;
    const char *p = ev->line.data, *end = p + n;
    for (; p + sizeof(key) - 1 <= end && memcmp(p, key, sizeof(key) - 1) != 0; p++) {
    }
    if (p + sizeof(key) - 1 <= end) {
        for (p += sizeof(key) - 1; p < end && *p != '"' && i + 1 < size; p++) {
            out[i++] = *p;
        }
    }
    out[i] = '\0';
}

void testhooks_log(const app_event_t *ev, app_ms_t now)
{
    static const char *const links[] = {
        [APP_LINK_DOWN] = "down",
        [APP_LINK_CONNECTING] = "connecting",
        [APP_LINK_CONNECTED] = "connected",
        [APP_LINK_AUTH_REFUSED] = "auth_refused",
        [APP_LINK_HOST_KEY_CHANGED] = "host_key_changed",
    };
    char t[16];
    switch (ev->kind) {
    case APP_EV_BUTTON:
        ESP_LOGI(TAG, "@L %llu button %s %s", (unsigned long long)ev->button.t,
                 ev->button.btn == APP_BTN_BOOT ? "BOOT" : "PWR", ev->button.down ? "down" : "up");
        break;
    case APP_EV_LINK:
        ESP_LOGI(TAG, "@L %llu link %s", (unsigned long long)now,
                 (unsigned)ev->link < sizeof(links) / sizeof(links[0]) ? links[ev->link] : "?");
        break;
    case APP_EV_WIFI:
        ESP_LOGI(TAG, "@L %llu wifi %s", (unsigned long long)now, ev->wifi.connected ? "up" : "down");
        break;
    case APP_EV_LINE:
        line_type(ev, t, sizeof(t));
        ESP_LOGI(TAG, "@L %llu line %s", (unsigned long long)now, t[0] ? t : "?");
        break;
    default:
        break;
    }
}

void testhooks_log_out(const app_out_t *out, app_ms_t now)
{
    /* A page is any tone but the click, which has no name here. */
    static const char *const tones[] = {[APP_TONE_BLOCKED] = "blocked", [APP_TONE_DONE] = "done", [APP_TONE_DANGER] = "danger"};
    for (size_t i = 0; i < out->n; i++) {
        const app_action_t *a = &out->a[i];
        if (a->kind == APP_ACT_TONE && (unsigned)a->tone < sizeof(tones) / sizeof(tones[0]) && tones[a->tone]) {
            ESP_LOGI(TAG, "@L %llu page %s", (unsigned long long)now, tones[a->tone]);
        }
    }
}

#define HEAP_EVERY_MS (10 * 60 * 1000)

void testhooks_tick(app_ms_t now)
{
    static bool logged;
    static app_ms_t last;
    if (logged && now - last < HEAP_EVERY_MS) {
        return;
    }
    logged = true;
    last = now;
#if CONFIG_IDF_TARGET_LINUX
    ESP_LOGI(TAG, "@L %llu heap 0 0 0", (unsigned long long)now); /* the emulator's heap isn't the board's */
#else
    ESP_LOGI(TAG, "@L %llu heap %u %u %u", (unsigned long long)now,
             (unsigned)heap_caps_get_free_size(MALLOC_CAP_INTERNAL),
             (unsigned)heap_caps_get_minimum_free_size(MALLOC_CAP_INTERNAL),
             (unsigned)heap_caps_get_free_size(MALLOC_CAP_SPIRAM));
#endif
}

#endif
