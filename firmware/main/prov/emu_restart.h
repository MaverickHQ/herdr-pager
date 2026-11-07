/* In the kit's emulator (the linux target), esp_restart() exits 0, which the
 * kit's supervisor takes as the product ending for good: it restarts only a
 * product that exits with the kit's reset code, 3. A pairing ends in a restart
 * (app/pairing.c), so on linux this installs a shutdown handler, which
 * esp_restart() runs first, that exits with the reset code instead. On the
 * board it does nothing.
 */
#pragma once

/** Makes esp_restart() a reset in the emulator. Call once at boot; more calls do nothing. */
void emu_restart_install(void);
