/* The pager's fonts, by role. Every string on screen is printable ASCII
 * (design D2-10), so a font needs only 0x20-0x7E.
 *
 * The parent design names Terminus and DSEG7 (OFL-1.1) converted with
 * lv_font_conv; neither the fonts nor the converter are on this machine, so
 * these roles use LVGL's built-in Montserrat, as the kit's design guide
 * advises. Swapping a font changes only fonts.c (and moves the goldens).
 */
#pragma once

#include "lvgl.h"

extern const lv_font_t *const font_hint;   /**< the hint cells: two lines in the 28 px band (12 px) */
extern const lv_font_t *const font_status; /**< the status band's left part (14 px) */
extern const lv_font_t *const font_body;   /**< rows, card lines, menus (20 px) */
extern const lv_font_t *const font_title;  /**< one big line: PAIR VIA USB, NO WIFI (28 px) */
