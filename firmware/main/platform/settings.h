/* What the device has stored (namespace "pager", app.h's APP_KEY_*), read at boot. */
#pragma once

#include "app.h"

/** Fills b from storage, the device's identity (prov/) and the RTC. */
void settings_load_boot(app_boot_t *b);

/** Gives the kit the stored networks, in priority order, and starts Wi-Fi. */
void settings_start_wifi(void);
