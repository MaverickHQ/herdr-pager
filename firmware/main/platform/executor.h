/* The action executor: runs the app core's actions on the kit, the link and storage. */
#pragma once

#include <stdbool.h>

#include "app.h"

/**
 * Runs out's actions in order. A failed APP_ACT_STORE or APP_ACT_ERASE stops
 * the batch there and returns false, so the caller sends APP_EV_STORE_FAILED;
 * otherwise returns true. Never logs a stored value or a line's text.
 */
bool executor_run(const app_state_t *s, const app_out_t *out);
