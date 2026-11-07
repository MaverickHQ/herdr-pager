#include "executor.h"

#include <stdio.h>
#include <string.h>
#include <unistd.h>

#include "esp_log.h"
#include "esp_system.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "lcd349.h"
#include "ssh_link.h"
#include "theme.h"
#include "ui.h"

static const char *TAG = "exec";

#define ARRAY_LEN(a) (sizeof(a) / sizeof((a)[0]))

/* How long a reboot waits for the console's last line to reach the host. */
#define REBOOT_DRAIN_MS 250

/* Tone patterns (parent design §Firmware components); hz 0 is a rest. */
static const lcd349_tone_t TONE_BLOCKED[] = {
    {880, 120}, {0, 80}, {880, 120}, {0, 400}, {880, 120}, {0, 80}, {880, 120},
};
static const lcd349_tone_t TONE_DONE[] = {{660, 150}, {990, 300}};
static const lcd349_tone_t TONE_DANGER[] = {{1200, 80}, {0, 60}, {1200, 80}, {0, 60}, {1200, 80}};
static const lcd349_tone_t TONE_CLICK[] = {{2000, 15}};

static void play(app_tone_t tone, uint8_t volume)
{
    switch (tone) {
    case APP_TONE_BLOCKED:
        lcd349_tone_seq(TONE_BLOCKED, ARRAY_LEN(TONE_BLOCKED), volume);
        break;
    case APP_TONE_DONE:
        lcd349_tone_seq(TONE_DONE, ARRAY_LEN(TONE_DONE), volume);
        break;
    case APP_TONE_DANGER:
        lcd349_tone_seq(TONE_DANGER, ARRAY_LEN(TONE_DANGER), volume);
        break;
    case APP_TONE_CLICK:
        lcd349_tone_seq(TONE_CLICK, ARRAY_LEN(TONE_CLICK), volume);
        break;
    }
}

static void ui_do(void (*fn)(const app_state_t *), const app_state_t *s)
{
    if (!lcd349_display()) {
        return;
    }
    lcd349_ui_lock();
    fn(s);
    lcd349_ui_unlock();
}

/* The palette changes live: theme_set recolours what is on screen, so nothing is rebuilt. */
static void ui_theme(uint8_t palette)
{
    if (!lcd349_display()) {
        return;
    }
    lcd349_ui_lock();
    theme_set(palette);
    lcd349_ui_unlock();
}

static void render(const app_state_t *s)
{
    ui_render(app_render(s));
}

static void flash(const app_state_t *s)
{
    (void)s;
    ui_flash();
}

/* Writes an @P reply and its \n in one stdio call, so no other task's log
 * line can land between them and break the line for the tool. */
static void serial_line(const char *text, size_t len)
{
    char buf[APP_SEND_MAX + 1];
    if (len > APP_SEND_MAX) {
        len = APP_SEND_MAX;
    }
    memcpy(buf, text, len);
    buf[len] = '\n';
    fwrite(buf, 1, len + 1, stdout);
    fflush(stdout);
}

bool executor_run(const app_state_t *s, const app_out_t *out)
{
    for (size_t i = 0; i < out->n; i++) {
        const app_action_t *a = &out->a[i];
        lcd349_err_t err;
        switch (a->kind) {
        case APP_ACT_RENDER:
            ui_do(render, s);
            break;
        case APP_ACT_TONE:
            play(a->tone, a->level);
            break;
        case APP_ACT_FLASH:
            ui_do(flash, s);
            break;
        case APP_ACT_BACKLIGHT:
            if (a->level == 0) {
                lcd349_display_sleep(true);
            } else {
                lcd349_display_sleep(false);
                lcd349_backlight_set(a->level);
            }
            break;
        case APP_ACT_SEND:
            if (ssh_link_send(a->text, a->len) != 0) {
                ESP_LOGW(TAG, "send: link down, line dropped");
            }
            break;
        case APP_ACT_SERIAL:
            serial_line(a->text, a->len);
            break;
        case APP_ACT_STORE:
            err = lcd349_kv_set_str(APP_NVS_NS, a->key, a->text);
            if (err != LCD349_OK) {
                ESP_LOGE(TAG, "store %s: %s", a->key, lcd349_err_str(err));
                return false;
            }
            break;
        case APP_ACT_ERASE:
            err = lcd349_kv_erase(APP_NVS_NS, a->key[0] ? a->key : NULL);
            if (err != LCD349_OK) {
                ESP_LOGE(TAG, "erase %s: %s", a->key[0] ? a->key : "all", lcd349_err_str(err));
                return false;
            }
            break;
        case APP_ACT_RTC:
            lcd349_rtc_set(a->unix_s);
            break;
        case APP_ACT_REBOOT:
            /* The restart resets the USB Serial/JTAG: a line still on its way to
             * the host (pairing's prov_result) arrives cut short, with the ROM's
             * boot banner on the same line, and provision rejects it (the desk
             * checkpoint, 2026-10-24). So the line drains first. */
            fflush(stdout);
            fsync(fileno(stdout));
            vTaskDelay(pdMS_TO_TICKS(REBOOT_DRAIN_MS));
            esp_restart();
            break;
        case APP_ACT_LINK:
            if (a->link_op == APP_LINK_OP_CONNECT) {
                if (ssh_link_connect() != 0) {
                    ESP_LOGW(TAG, "link: connect not started");
                }
            } else {
                ssh_link_disconnect();
            }
            break;
        case APP_ACT_THEME:
            ui_theme(a->level);
            break;
        }
    }
    return true;
}
