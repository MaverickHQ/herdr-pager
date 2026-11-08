/* The test rig (test_rig.h). It registers no tests. */
#include "test_rig.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "unity.h"

rig_t rig;

static void log_out(void)
{
    for (size_t i = 0; i < rig.out.n; i++) {
        TEST_ASSERT_LESS_THAN_size_t_MESSAGE(RIG_LOG_MAX, rig.n_log, "the rig's action log is full");
        rig.at[rig.n_log] = rig.now;
        rig.log[rig.n_log++] = rig.out.a[i];
    }
}

static void handle(const app_event_t *ev)
{
    app_handle(&rig.s, ev, rig.now, &rig.out);
    log_out();
}

void rig_at(app_ms_t t)
{
    while (rig.out.deadline != APP_NEVER && rig.out.deadline <= t) {
        if (rig.out.deadline > rig.now) {
            rig.now = rig.out.deadline;
        }
        app_event_t ev = {.kind = APP_EV_DEADLINE};
        handle(&ev);
    }
    if (t > rig.now) {
        rig.now = t;
    }
}

void rig_event(app_ms_t t, const app_event_t *ev)
{
    rig_at(t);
    handle(ev);
}

void rig_event_before_deadline(app_ms_t t, const app_event_t *ev)
{
    if (t > rig.now) {
        rig.now = t;
    }
    handle(ev);
}

void rig_boot(bool paired)
{
    rig_boot_seed(paired, 1);
}

void rig_boot_seed(bool paired, uint32_t seed)
{
    memset(&rig.s, 0, sizeof(rig.s)); /* not the log: the pocket fuzz boots 10,000 times */
    memset(&rig.out, 0, sizeof(rig.out));
    memset(&rig.boot, 0, sizeof(rig.boot));
    rig.now = 0;
    rig.n_log = 0;
    rig.out.deadline = APP_NEVER;
    app_init(&rig.s);
    rig.boot.paired = paired;
    snprintf(rig.boot.name, sizeof(rig.boot.name), "%s", paired ? "pager-01" : "");
    snprintf(rig.boot.fw, sizeof(rig.boot.fw), "0.1.0");
    /* The provisioning fixtures' keys (bridge/testdata/provisioning/README.md). */
    snprintf(rig.boot.pubkey, sizeof(rig.boot.pubkey), "%s", RIG_DEVICE_KEY);
    if (paired) {
        snprintf(rig.boot.host_key, sizeof(rig.boot.host_key), "%s", RIG_HOST_KEY);
        snprintf(rig.boot.host_name, sizeof(rig.boot.host_name), "studio-mac");
        snprintf(rig.boot.host, sizeof(rig.boot.host), "studio-mac.local");
        rig.boot.port = 2277;
    }
    rig.boot.volume = 60;
    rig.boot.brightness = 80;
    rig.boot.done_tone = true; /* nothing stored */
    rig.boot.seed = seed;
    app_event_t ev = {.kind = APP_EV_BOOT, .boot = &rig.boot};
    rig_event(0, &ev);
    if (paired) {
        rig_wifi(0, true, "home");
        rig_link(0, APP_LINK_CONNECTED);
    }
    rig_clear_log();
}

void rig_line_n(app_ms_t t, const char *data, size_t len)
{
    app_event_t ev = {.kind = APP_EV_LINE};
    ev.line.data = data;
    ev.line.len = len;
    rig_event(t, &ev);
}

void rig_line(app_ms_t t, const char *line)
{
    rig_line_n(t, line, strlen(line));
}

const char *rig_read_fixture(const char *rel, size_t *len)
{
    static char buf[APP_LINE_MAX + 64];
    char path[512];
    snprintf(path, sizeof(path), "%s/protocol/%s", PAGER_TESTDATA, rel);
    FILE *f = fopen(path, "rb");
    TEST_ASSERT_NOT_NULL_MESSAGE(f, path);
    size_t n = fread(buf, 1, sizeof(buf) - 1, f);
    fclose(f);
    if (n > 0 && buf[n - 1] == '\n') {
        n--;
    }
    buf[n] = '\0';
    *len = n;
    return buf;
}

void rig_fixture(app_ms_t t, const char *rel)
{
    size_t len;
    const char *line = rig_read_fixture(rel, &len);
    rig_line_n(t, line, len);
}

void rig_snap_quiet(app_ms_t t)
{
    rig_line(t, "{\"t\":\"snap\",\"now\":1760220001,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                "{\"pane\":\"w3:p1\",\"name\":\"tests\",\"kind\":\"claude\",\"st\":\"working\",\"seq\":50,\"dseq\":4,"
                "\"since\":1760219700,\"can\":[\"show\",\"jump\",\"interrupt\"]},"
                "{\"pane\":\"w3:p2\",\"name\":\"migrations\",\"kind\":\"claude\",\"st\":\"working\",\"seq\":44,"
                "\"dseq\":0,\"since\":1760219750,\"can\":[\"show\",\"jump\",\"interrupt\"]},"
                "{\"pane\":\"w4:p1\",\"name\":\"spike-ble\",\"kind\":\"claude\",\"st\":\"idle\",\"seq\":2,\"dseq\":0,"
                "\"since\":1760200000,\"can\":[\"show\",\"jump\"]}]}");
}

uint32_t rig_sent_id(const char *line)
{
    const char *at = line ? strstr(line, "\"id\":") : NULL;
    TEST_ASSERT_NOT_NULL_MESSAGE(at, "a sent line without an id");
    return (uint32_t)strtoul(at + 5, NULL, 10);
}

void rig_card(app_ms_t t, const char *rel, uint32_t id)
{
    size_t len;
    rig_card_line(t, rig_read_fixture(rel, &len), id);
}

void rig_card_line(app_ms_t t, const char *line, uint32_t id)
{
    const char *at = strstr(line, "\"id\":");
    TEST_ASSERT_NOT_NULL_MESSAGE(at, line);
    const char *rest = at + 5;
    while (*rest >= '0' && *rest <= '9') {
        rest++;
    }
    static char buf[APP_LINE_MAX + 64];
    int n = snprintf(buf, sizeof(buf), "%.*s\"id\":%u%s", (int)(at - line), line, (unsigned)id, rest);
    rig_line_n(t, buf, (size_t)n);
}

app_ms_t rig_open_card(app_ms_t t, const char *name, const char *rel)
{
    size_t len;
    return rig_open_card_line(t, name, rig_read_fixture(rel, &len));
}

app_ms_t rig_open_card_line(app_ms_t t, const char *name, const char *line)
{
    static char card[APP_LINE_MAX + 64];
    snprintf(card, sizeof(card), "%s", line); /* line may be the fixture buffer, which the presses don't touch */
    if (!rig.s.disp.awake) {
        rig_press(APP_BTN_BOOT, t, 100);
        t += 200;
    }
    for (int i = 0; i <= APP_MAX_AGENTS && strcmp(rig_render()->rows[rig_render()->selected].name, name) != 0; i++) {
        rig_press(APP_BTN_BOOT, t, 100);
        t += 200;
    }
    TEST_ASSERT_EQUAL_STRING_MESSAGE(name, rig_render()->rows[rig_render()->selected].name, "rig_open_card");
    size_t before = rig_n_sent();
    rig_press(APP_BTN_PWR, t, 100);
    t += 200;
    TEST_ASSERT_EQUAL_size_t_MESSAGE(before + 1, rig_n_sent(), "rig_open_card: no show sent");
    rig_card_line(t, card, rig_sent_id(rig_sent(before)));
    return t + 100;
}

void rig_link(app_ms_t t, app_link_t link)
{
    app_event_t ev = {.kind = APP_EV_LINK, .link = link};
    rig_event(t, &ev);
}

void rig_wifi(app_ms_t t, bool connected, const char *ssid)
{
    rig_wifi_ip(t, connected, ssid, "");
}

void rig_wifi_ip(app_ms_t t, bool connected, const char *ssid, const char *ip)
{
    app_event_t ev = {.kind = APP_EV_WIFI};
    ev.wifi.connected = connected;
    snprintf(ev.wifi.ssid, sizeof(ev.wifi.ssid), "%s", ssid);
    snprintf(ev.wifi.ip, sizeof(ev.wifi.ip), "%s", ip);
    rig_event(t, &ev);
}

void rig_wifi_fail(app_ms_t t, app_wifi_fail_t fail, const char *ssid)
{
    app_event_t ev = {.kind = APP_EV_WIFI};
    ev.wifi.fail = fail;
    snprintf(ev.wifi.ssid, sizeof(ev.wifi.ssid), "%s", ssid);
    rig_event(t, &ev);
}

void rig_power(app_ms_t t, app_power_t power)
{
    app_event_t ev = {.kind = APP_EV_POWER, .power = power};
    rig_event(t, &ev);
}

void rig_btn(app_ms_t t, app_btn_t btn, bool down)
{
    app_event_t ev = {.kind = APP_EV_BUTTON};
    ev.button.btn = btn;
    ev.button.down = down;
    ev.button.t = t;
    rig_event(t, &ev);
}

void rig_press(app_btn_t btn, app_ms_t t, app_ms_t ms)
{
    rig_btn(t, btn, true);
    rig_btn(t + ms, btn, false);
}

void rig_touch(app_ms_t t, uint16_t x, uint16_t y, bool pressed)
{
    app_event_t ev = {.kind = APP_EV_TOUCH};
    ev.touch.x = x;
    ev.touch.y = y;
    ev.touch.pressed = pressed;
    rig_event(t, &ev);
}

void rig_clear_log(void)
{
    rig.n_log = 0;
}

size_t rig_count(app_action_kind_t kind)
{
    size_t n = 0;
    for (size_t i = 0; i < rig.n_log; i++) {
        n += rig.log[i].kind == kind;
    }
    return n;
}

const char *rig_sent(size_t i)
{
    for (size_t k = 0; k < rig.n_log; k++) {
        if (rig.log[k].kind == APP_ACT_SEND && i-- == 0) {
            return rig.log[k].text;
        }
    }
    return NULL;
}

size_t rig_n_sent(void)
{
    return rig_count(APP_ACT_SEND);
}

const app_action_t *rig_last(app_action_kind_t kind)
{
    for (size_t i = rig.n_log; i-- > 0;) {
        if (rig.log[i].kind == kind) {
            return &rig.log[i];
        }
    }
    TEST_FAIL_MESSAGE("rig_last: no such action in the log");
    return NULL;
}

const app_action_t *rig_last_store(const char *key)
{
    for (size_t i = rig.n_log; i-- > 0;) {
        if (rig.log[i].kind == APP_ACT_STORE && strcmp(rig.log[i].key, key) == 0) {
            return &rig.log[i];
        }
    }
    TEST_FAIL_MESSAGE(key);
    return NULL;
}

const app_render_t *rig_render(void)
{
    return app_render(&rig.s);
}
