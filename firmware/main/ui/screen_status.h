/* What the screens share with the status band (screen_status.c). */
#pragma once

#include <stdbool.h>

#include "lvgl.h"

/** Tags a label whose text changes with the time (the clock, an age), so goldens mask it
 * (bridge/emutest's MaskTag). */
void screen_mask_volatile(lv_obj_t *label);

/** Draws obj inverted, or not: its ground in the foreground colour and the labels in it in the
 * ground's (the selected row or tile, a paged agent's glyph). The contrast stays the palette's. */
void screen_invert(lv_obj_t *obj, bool on);
