/* The hint cells' positions: one cell under each button, at the right of the
 * status band, in the buttons' physical order, PWR then BOOT (design D2-10).
 *
 * The buttons' x positions are the kit's generated LCD349_BUTTON_*_X_PX
 * (lcd349_board.h, phase 1's task 1.4).
 */
#pragma once

#include "lcd349.h"

/* Each cell is centred under its button; they meet halfway between the two,
 * and the BOOT cell ends at the screen's edge. */
#define HINT_CELL_W (LCD349_BUTTON_BOOT_X_PX - LCD349_BUTTON_PWR_X_PX)
#define HINT_PWR_X (LCD349_BUTTON_PWR_X_PX - HINT_CELL_W / 2)
#define HINT_PWR_W HINT_CELL_W
#define HINT_BOOT_X (HINT_PWR_X + HINT_PWR_W)
#define HINT_BOOT_W (LCD349_LCD_W - HINT_BOOT_X)
