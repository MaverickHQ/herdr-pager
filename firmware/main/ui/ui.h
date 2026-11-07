/* The screens, drawn from the app core's render model (app.h).
 *
 * Every screen has two bands (design D2-10): the 28 px status band on top,
 * with the status on the left and the hint cells at its right (platform/hints.h),
 * and the 144 px body. ui.c owns the bands and picks the body's screen; each
 * screen_*.c draws one body, and screen_status.c draws the status band on
 * every screen. Call everything here on lcd349_ui or with the UI lock held.
 */
#pragma once

#include <stdbool.h>

#include "app.h"
#include "lcd349.h"

#define UI_BAND_H 28
#define UI_BODY_Y UI_BAND_H
#define UI_BODY_H (LCD349_LCD_H - UI_BAND_H)

/** Builds the bands on the active screen. */
void ui_init(void);

/** Draws r: the status band, then its screen's body. */
void ui_render(const app_render_t *r);

/** Flashes the screen: inverts it 3 times at 150 ms. */
void ui_flash(void);

/** Sets a label's text only if it differs (every change costs a full-frame flush). */
void ui_set_text(lv_obj_t *label, const char *text);

/**
 * A screen draws into its band or body from r. fresh is true when the band
 * or body was just cleared for it: it creates its objects then; otherwise it
 * updates them, setting text only where it changed (every change costs a
 * full-frame flush).
 */
typedef void (*ui_draw_fn)(lv_obj_t *parent, const app_render_t *r, bool fresh);

void screen_status_draw(lv_obj_t *band, const app_render_t *r, bool fresh);
void screen_boot_draw(lv_obj_t *body, const app_render_t *r, bool fresh);
void screen_list_draw(lv_obj_t *body, const app_render_t *r, bool fresh);
void screen_card_draw(lv_obj_t *body, const app_render_t *r, bool fresh);
void screen_menu_draw(lv_obj_t *body, const app_render_t *r, bool fresh);
void screen_confirm_draw(lv_obj_t *body, const app_render_t *r, bool fresh);
void screen_toast_draw(lv_obj_t *body, const app_render_t *r, bool fresh);
void screen_info_draw(lv_obj_t *body, const app_render_t *r, bool fresh);
void screen_pairing_draw(lv_obj_t *body, const app_render_t *r, bool fresh);
void screen_link_draw(lv_obj_t *body, const app_render_t *r, bool fresh);
