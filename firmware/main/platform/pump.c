#include "pump.h"

#include <stdio.h>
#include <string.h>

#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"
#include "lcd349.h"
#include "sdkconfig.h"

#include "evq.h"
#include "wifi_retry.h"
#include "executor.h"
#include "settings.h"
#include "ssh_link.h"
#if CONFIG_PAGER_TEST_HOOKS
#include "testhooks.h"
#endif

static const char *TAG = "pump";

#define QUEUE_LEN 32
/* The emulator's tasks share the C library's locks unprotected: stay at app_main's priority (kit docs/sdk.md). */
#define TASK_PRIO 1
#define APP_STACK 8192
#define CONSOLE_STACK 4096
#define MAX_WAIT_MS 60000

typedef struct {
    app_event_t ev;
    char *copy; /* the line's bytes, for APP_EV_LINE and APP_EV_PROV_LINE; freed after handling */
} item_t;

static QueueHandle_t s_queue;
static app_state_t s_state;
static app_out_t s_out;
static app_boot_t s_boot;

app_ms_t pump_now(void)
{
    return lcd349_time_us() / 1000;
}

/* The kit stamps edges with lcd349_time_ms(), which wraps; place them on the core's clock. */
static app_ms_t from_kit_ms(uint32_t t_ms)
{
    app_ms_t now = pump_now();
    uint32_t age = (uint32_t)now - t_ms;
    return age <= now ? now - age : 0;
}

/* Several kit tasks post; evq.c decides what goes in. */
static void post(item_t *it)
{
    bool button = it->ev.kind == APP_EV_BUTTON;
    if (!evq_admit(uxQueueMessagesWaiting(s_queue), QUEUE_LEN, button) || xQueueSend(s_queue, it, 0) != pdTRUE) {
        ESP_LOGW(TAG, "queue full: event %d dropped", (int)it->ev.kind);
        lcd349_free(it->copy);
    }
}

static void post_line(app_event_kind_t kind, const char *data, size_t len)
{
    item_t it = {.ev.kind = kind};
    it.copy = lcd349_alloc(len ? len : 1, LCD349_MEM_ANY);
    if (!it.copy) {
        ESP_LOGW(TAG, "no memory: line dropped");
        return;
    }
    memcpy(it.copy, data, len);
    it.ev.line.data = it.copy;
    it.ev.line.len = len;
    post(&it);
}

void pump_post_button(app_btn_t btn, bool down, app_ms_t t)
{
    item_t it = {.ev.kind = APP_EV_BUTTON};
    it.ev.button.btn = btn;
    it.ev.button.down = down;
    it.ev.button.t = t;
    post(&it);
}

static void on_button(const lcd349_btn_event_t *e, void *ctx)
{
    (void)ctx;
    pump_post_button(e->btn == LCD349_BTN_BOOT ? APP_BTN_BOOT : APP_BTN_PWR, e->down, from_kit_ms(e->t_ms));
}

static void on_touch(const lcd349_touch_sample_t *t, void *ctx)
{
    (void)ctx;
    static evq_touch_t last; /* the kit's touch task is the only caller */
    if (!evq_touch_news(&last, t->pressed, t->x, t->y)) {
        return;
    }
    item_t it = {.ev.kind = APP_EV_TOUCH};
    it.ev.touch.x = t->x;
    it.ev.touch.y = t->y;
    it.ev.touch.pressed = t->pressed;
    post(&it);
}

/* A failed Wi-Fi join is tried again (platform/wifi_retry.c says when). on_wifi (the kit's event task) sets
 * when; the console task, which wakes each second, makes the call. Times are pump_now()'s low 32 bits. */
static volatile uint32_t s_wifi_retry_at; /* 0: none due */
static wifi_retry_t s_wifi_retry;         /* the kit's event task's only */

static void wifi_retry_due(void)
{
    uint32_t at = s_wifi_retry_at;
    if (at != 0 && (int32_t)((uint32_t)pump_now() - at) >= 0) {
        s_wifi_retry_at = 0;
        settings_start_wifi();
    }
}

static void on_wifi(const lcd349_wifi_status_t *st, void *ctx)
{
    (void)ctx;
    if (st->state == LCD349_WIFI_CONNECTED) {
        wifi_retry_joined(&s_wifi_retry);
        s_wifi_retry_at = 0;
    } else if (st->state == LCD349_WIFI_DISCONNECTED && st->reason != LCD349_WIFI_REASON_STOPPED) {
        bool refused = st->reason == LCD349_WIFI_REASON_AUTH;
        uint32_t ms = wifi_retry_failed(&s_wifi_retry, refused, s_wifi_retry_at != 0);
        if (ms == 0) { /* at once: the console task's next wake is up to a second away */
            ESP_LOGI(TAG, "wifi: join refused; trying again at once");
            s_wifi_retry_at = 0;
            settings_start_wifi();
        } else if (ms != WIFI_RETRY_KEEP) {
            ESP_LOGI(TAG, "wifi: join %s (reason %d); next try in %u ms", refused ? "refused" : "failed",
                     (int)st->reason, (unsigned)ms);
            s_wifi_retry_at = ((uint32_t)pump_now() + ms) | 1u; /* never 0 */
        }
    }
    item_t it = {.ev.kind = APP_EV_WIFI};
    it.ev.wifi.connected = st->state == LCD349_WIFI_CONNECTED;
    if (st->state == LCD349_WIFI_DISCONNECTED && st->reason != LCD349_WIFI_REASON_STOPPED) {
        it.ev.wifi.fail = st->reason == LCD349_WIFI_REASON_AUTH ? APP_WIFI_FAIL_REFUSED : APP_WIFI_FAIL_OTHER;
    }
    /* The network in use, or the one that refused the join (WIFI <ssid> FAILED, open-items O-49). */
    bool named = it.ev.wifi.connected || it.ev.wifi.fail == APP_WIFI_FAIL_REFUSED;
    snprintf(it.ev.wifi.ssid, sizeof(it.ev.wifi.ssid), "%s", named ? st->ssid : "");
    if (it.ev.wifi.connected) { /* device info's IP; the kit's ip4 is in network byte order */
        const uint8_t *b = (const uint8_t *)&st->ip4;
        snprintf(it.ev.wifi.ip, sizeof(it.ev.wifi.ip), "%u.%u.%u.%u", b[0], b[1], b[2], b[3]);
    }
    post(&it);
}

static void post_power(lcd349_power_source_t src)
{
    item_t it = {.ev.kind = APP_EV_POWER};
    it.ev.power = src == LCD349_POWER_SOURCE_BATTERY ? APP_POWER_BATTERY
                  : src == LCD349_POWER_SOURCE_USB   ? APP_POWER_USB
                                                     : APP_POWER_UNKNOWN;
    post(&it);
}

static void on_power_source(lcd349_power_source_t src, void *ctx)
{
    (void)ctx;
    post_power(src);
}

static void on_link_state(app_link_t state, void *ctx)
{
    (void)ctx;
    item_t it = {.ev.kind = APP_EV_LINK};
    it.ev.link = state;
    post(&it);
}

static void on_link_line(const char *line, size_t len, void *ctx)
{
    (void)ctx;
    post_line(APP_EV_LINE, line, len);
}

static void handle(const app_event_t *ev)
{
    app_ms_t now = pump_now();
#if CONFIG_PAGER_TEST_HOOKS
    testhooks_log(ev, now);
#endif
    app_handle(&s_state, ev, now, &s_out);
#if CONFIG_PAGER_TEST_HOOKS
    testhooks_log_out(&s_out, now);
#endif
    if (!executor_run(&s_state, &s_out)) {
        app_event_t failed = {.kind = APP_EV_STORE_FAILED};
        app_handle(&s_state, &failed, pump_now(), &s_out);
        executor_run(&s_state, &s_out);
    }
}

static TickType_t until_deadline(void)
{
    if (s_out.deadline == APP_NEVER) {
        return portMAX_DELAY;
    }
    app_ms_t now = pump_now();
    if (s_out.deadline <= now) {
        return 0;
    }
    app_ms_t wait = s_out.deadline - now;
    if (wait > MAX_WAIT_MS) {
        wait = MAX_WAIT_MS;
    }
    return pdMS_TO_TICKS((uint32_t)wait) + 1; /* round up: never wake before the deadline */
}

static void app_task(void *arg)
{
    (void)arg;
    app_init(&s_state);
    settings_load_boot(&s_boot);
    static const char *const started[] = {
        [APP_START_POWER_ON] = "switched on", [APP_START_RESTART] = "after a restart",
        [APP_START_CRASH] = "after a crash",  [APP_START_BROWNOUT] = "after a brownout",
        [APP_START_RESET] = "after a reset",
    };
    ESP_LOGI(TAG, "boot: %s, %s", s_boot.paired ? "paired" : "unpaired",
             s_boot.start <= APP_START_RESET ? started[s_boot.start] : "after a reset"); /* O-77 */
    app_event_t boot = {.kind = APP_EV_BOOT, .boot = &s_boot};
    handle(&boot);
    if (s_boot.paired) {
        settings_start_wifi();
    }

    for (;;) {
        item_t it;
        if (xQueueReceive(s_queue, &it, until_deadline()) == pdTRUE) {
            handle(&it.ev);
            lcd349_free(it.copy);
        } else if (s_out.deadline != APP_NEVER && pump_now() >= s_out.deadline) {
            app_event_t deadline = {.kind = APP_EV_DEADLINE};
            handle(&deadline);
        }
    }
}

static void console_task(void *arg)
{
    (void)arg;
    static char line[LCD349_CONSOLE_LINE_MAX + 1];
    for (;;) {
        lcd349_err_t err = lcd349_console_read_line(line, sizeof(line), 1000);
        wifi_retry_due();
#if CONFIG_PAGER_TEST_HOOKS
        testhooks_tick(pump_now());
#endif
        if (err == LCD349_ERR_TIMEOUT) {
            continue;
        }
        if (err != LCD349_OK) {
            vTaskDelay(pdMS_TO_TICKS(1000));
            continue;
        }
        if (strncmp(line, "@P ", 3) == 0) {
            post_line(APP_EV_PROV_LINE, line, strlen(line));
        }
#if CONFIG_PAGER_TEST_HOOKS
        else if (strncmp(line, "@T ", 3) == 0) {
            testhooks_line(line);
        }
#endif
    }
}

void pump_start(void)
{
    s_queue = xQueueCreate(QUEUE_LEN, sizeof(item_t));
    ssh_link_cbs_t cbs = {.on_state = on_link_state, .on_line = on_link_line};
    ssh_link_init(&cbs);
    lcd349_buttons_subscribe(on_button, NULL);
    lcd349_touch_subscribe(on_touch, NULL);
    lcd349_wifi_subscribe(on_wifi, NULL);
    /* The kit calls back on a change only: the source at boot is posted once, after subscribing so no change is lost. */
    lcd349_power_on_source_change(on_power_source, NULL);
    post_power(lcd349_power_source());
    if (xTaskCreate(app_task, "pager_app", APP_STACK, NULL, TASK_PRIO, NULL) != pdPASS ||
        xTaskCreate(console_task, "pager_console", CONSOLE_STACK, NULL, TASK_PRIO, NULL) != pdPASS) {
        ESP_LOGE(TAG, "no memory for the app and console tasks");
    }
}
