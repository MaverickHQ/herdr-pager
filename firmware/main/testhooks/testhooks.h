/* Test hooks (design D2-4): @T lines on the serial console, such as
 * "@T press BOOT 1000", injected as raw button edges through the event pump,
 * and the app core's timestamped log ("@L <ms> ..."), which the board checks
 * in tools/measure read. Compiled in only with CONFIG_PAGER_TEST_HOOKS
 * (firmware/sdkconfig.test): a release build ignores @T lines (the pump never
 * passes them on) and has no @L log.
 */
#pragma once

#include "app.h"

/** Handles one console line starting "@T ". "@T press BOOT|PWR <ms>" holds the button for ms (1-10000). */
void testhooks_line(const char *line);

/**
 * Logs ev as the app core takes it at now, on the core's clock:
 *   "@L <t> button BOOT|PWR down|up"  t the edge's own time
 *   "@L <now> link <state>"           down, connecting, connected, auth_refused, host_key_changed
 *   "@L <now> wifi up|down"
 *   "@L <now> line <t>"               a line from the bridge, by its "t" (snap, card, ...)
 */
void testhooks_log(const app_event_t *ev, app_ms_t now);

/** Logs the pages among the core's actions: "@L <now> page blocked|done|danger" for each tone but a click. */
void testhooks_log_out(const app_out_t *out, app_ms_t now);

/**
 * Called about once a second (the console task): logs free memory at the first call and every
 * 10 minutes after, "@L <now> heap <internal free> <internal lowest> <PSRAM free>" in bytes.
 */
void testhooks_tick(app_ms_t now);
