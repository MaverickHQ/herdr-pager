/* The test rig: one app core on a fake clock, driven the way the platform drives it (main/platform/pump.c).
 * It keeps only the latest out.deadline, delivers APP_EV_DEADLINE at it when the clock passes it, and logs
 * every action of every call, so a test asserts on what the device would have done. */
#pragma once

#include <stdbool.h>
#include <stddef.h>

#include "app.h"

#define RIG_LOG_MAX 4096

typedef struct {
    app_state_t s;
    app_out_t out;  /**< the latest call's */
    app_ms_t now;
    app_boot_t boot;
    size_t n_log;
    app_action_t log[RIG_LOG_MAX];
    app_ms_t at[RIG_LOG_MAX]; /**< when each logged action was returned */
} rig_t;

extern rig_t rig;

/** A fresh core, booted at time 0; paired, it has Wi-Fi ("home") and the link up. The log is cleared. */
void rig_boot(bool paired);
/** The same, with the reconnect jitter seeded by seed (rig_boot's is 1). */
void rig_boot_seed(bool paired, uint32_t seed);

/** Advances the clock to t, delivering every deadline the core asks for on the way. */
void rig_at(app_ms_t t);

void rig_event(app_ms_t t, const app_event_t *ev);
/** Hands ev over at t before any deadline due by then, as the platform's pump does with a queued event
 * (main/platform/pump.c: the queue first, the deadline only when it's empty). */
void rig_event_before_deadline(app_ms_t t, const app_event_t *ev);
void rig_line(app_ms_t t, const char *line);
void rig_line_n(app_ms_t t, const char *data, size_t len);
/** Feeds bridge/testdata/protocol/<rel> as a line. */
void rig_fixture(app_ms_t t, const char *rel);
void rig_link(app_ms_t t, app_link_t link);
void rig_wifi(app_ms_t t, bool connected, const char *ssid);
void rig_wifi_ip(app_ms_t t, bool connected, const char *ssid, const char *ip);
/** A Wi-Fi status for a join that failed: refused by ssid, or for another reason (ssid ""). */
void rig_wifi_fail(app_ms_t t, app_wifi_fail_t fail, const char *ssid);

/* The provisioning fixtures' keys and their fingerprints (bridge/testdata/provisioning/README.md). A paired
 * rig_boot stores both; an unpaired one only the device's. */
#define RIG_DEVICE_KEY "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAnULfN18UYEHFPLj/PhfEMf7NvNdFoELzBJBtMsCVgj"
#define RIG_DEVICE_FP "SHA256://a7FlX9Y4t0WSS+W8ANyY4w+5+axSEVbGzNXp6txjY"
#define RIG_HOST_KEY "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIITD0UXh5rvWrJ9ECHIk1KDIInpniagO0TKg6w/6c/tO"
#define RIG_HOST_FP "SHA256:dsn3PXzY9otII88ZqYD/4URNANyDaFaYUI57pc+mMe0"
void rig_power(app_ms_t t, app_power_t power);
void rig_btn(app_ms_t t, app_btn_t btn, bool down);
/** A press of btn from t for ms, and its release. */
void rig_press(app_btn_t btn, app_ms_t t, app_ms_t ms);
void rig_touch(app_ms_t t, uint16_t x, uint16_t y, bool pressed);

/** A snapshot at t that pages nothing: tests (w3:p1) and migrations (w3:p2) working, spike-ble (w4:p1) idle. */
void rig_snap_quiet(app_ms_t t);

/** The id in a sent line. */
uint32_t rig_sent_id(const char *line);
/** Feeds the fixture rel, a card or a result, its id replaced by id (the request it answers). */
void rig_card(app_ms_t t, const char *rel, uint32_t id);
/** From t: wakes the screen if it sleeps, selects the row named name with BOOT presses, opens its card with
 *  PWR and answers the show with the card fixture rel. Returns a time after it all. */
app_ms_t rig_open_card(app_ms_t t, const char *name, const char *rel);
/** The same, with a card line written by the test. */
void rig_card_line(app_ms_t t, const char *line, uint32_t id);
app_ms_t rig_open_card_line(app_ms_t t, const char *name, const char *line);

/** Reads bridge/testdata/protocol/<rel> into a static buffer, without its \n. */
const char *rig_read_fixture(const char *rel, size_t *len);

void rig_clear_log(void);
size_t rig_count(app_action_kind_t kind);
/** The i-th logged SEND line (0 is the first), or NULL. */
const char *rig_sent(size_t i);
size_t rig_n_sent(void);
/** The last logged action of kind; the test fails when there's none. */
const app_action_t *rig_last(app_action_kind_t kind);
/** The last logged APP_ACT_STORE of key; the test fails when there's none. */
const app_action_t *rig_last_store(const char *key);

const app_render_t *rig_render(void);
