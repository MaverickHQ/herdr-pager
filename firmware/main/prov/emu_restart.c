/* A restart in the kit's emulator (emu_restart.h). */
#include "emu_restart.h"

#include "sdkconfig.h"

#if CONFIG_IDF_TARGET_LINUX
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>

#include "esp_system.h"

#define KIT_EXIT_RESET 3 /* LCD349_EXIT_RESET in the kit's emu.h: the code `lcd349 power reset` uses */

static void restart_as_reset(void)
{
    fflush(stdout);
    exit(KIT_EXIT_RESET);
}

void emu_restart_install(void)
{
    static bool done;
    if (!done) {
        done = esp_register_shutdown_handler(restart_as_reset) == ESP_OK;
    }
}
#else
void emu_restart_install(void)
{
}
#endif
