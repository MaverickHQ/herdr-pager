/* The event pump: kit, link and console events → app events, one at a time,
 * into the app core on the app task, whose actions the executor runs.
 *
 *   buttons (lcd349_evt)  raw edges, with the edge's own time
 *   touch (lcd349_evt)    samples, which the core ignores (R1.3)
 *   Wi-Fi (lcd349_evt)    connected or not, and the network; a failed join is
 *                         tried again with backoff, from the console task
 *   power (lcd349_evt)    the power source: once at start, then each change
 *   net/ssh_link          link states and the bridge's lines
 *   the console task      "@P " lines to the core; "@T " lines to the test
 *                         hooks in test builds only; every other line ignored
 *   the app task          boot, then the core's deadline
 */
#pragma once

#include <stdbool.h>

#include "app.h"

/** Starts the pump: subscribes to the kit and the link, and starts the app and console tasks. */
void pump_start(void);

/** The core's clock: milliseconds since start. */
app_ms_t pump_now(void);

/** Posts a raw button edge, as the kit would (the test hooks inject presses with it). */
void pump_post_button(app_btn_t btn, bool down, app_ms_t t);
