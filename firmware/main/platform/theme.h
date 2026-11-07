/* The pager's look: two palettes, each one colour pair, picked with the
 * global menu's COLOUR (the owner's choice at the desk checkpoint,
 * 2026-10-24; it was the "Bravo" green LCD look, then Claude's orange
 * #D97757):
 *
 * - ORANGE, the default: near-black text, #0A0605 on #CC5A2A, 4.95:1;
 * - GREEN, a backlit Motorola pager: #0B1A0B on #6FBF5A, 7.98:1.
 *
 * Contrast is on RGB565-quantised colours, above the kit's 4.5:1 rule. In
 * each pair, each channel of FG + BG stays within 0xFF, as
 * theme_invert_overlay needs. */
#pragma once

#include <stdint.h>

#include "lvgl.h"

#define THEME_ORANGE_FG_HEX 0x0A0605
#define THEME_ORANGE_BG_HEX 0xCC5A2A
#define THEME_GREEN_FG_HEX 0x0B1A0B
#define THEME_GREEN_BG_HEX 0x6FBF5A

/** The current palette's colours. */
lv_color_t theme_fg(void);
lv_color_t theme_bg(void);

/**
 * Switches to palette (app.h's app_palette_t; anything else is ORANGE) and
 * recolours the screen as it stands: every object on the active screen and
 * the top layer whose local colour is the old palette's fg, bg or overlay
 * gets the new one's. Call on lcd349_ui or with the UI lock held.
 */
void theme_set(uint8_t palette);

/** Styles a screen: the ground colour, no scrolling. */
void theme_screen(lv_obj_t *scr);

/** Makes obj a bare container: transparent, no border, padding or scrolling. */
void theme_container(lv_obj_t *obj);

/** Styles a label in the foreground colour with font. */
void theme_label(lv_obj_t *label, const lv_font_t *font);

/** Swaps the palette under an overlay (blend: difference with fg + bg), for a screen flash. */
void theme_invert_overlay(lv_obj_t *overlay);
