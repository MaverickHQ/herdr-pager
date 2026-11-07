/* Device info (parent 9.6): the device's name, then its firmware version and
 * why it last started, both full key fingerprints (DEV, then HOST: the
 * bridge's), and its IP and the bridge's address (O-77), in a column. The fingerprints are never cut: one that doesn't fit wraps onto a
 * second line, and the lines under it move down. Both buttons go back (the
 * hint cells say so).
 *
 * The keys are made at pairing, so the fingerprints differ on every run:
 * goldens mask them. So does the IP line's bridge address: the emulator's
 * bridge listens on a port of its own each run. */
#include "fonts.h"
#include "screen_status.h"
#include "theme.h"
#include "ui.h"

#define MARGIN 12
#define TOP 6
#define LINE_GAP 4

static lv_obj_t *s_name;
static lv_obj_t *s_lines[APP_BODY_LINES];

void screen_info_draw(lv_obj_t *body, const app_render_t *r, bool fresh)
{
    if (fresh) {
        lv_obj_t *col = lv_obj_create(body);
        theme_container(col);
        lv_obj_set_pos(col, MARGIN, TOP);
        lv_obj_set_size(col, LCD349_LCD_W - 2 * MARGIN, UI_BODY_H - TOP);
        lv_obj_set_flex_flow(col, LV_FLEX_FLOW_COLUMN);
        lv_obj_set_style_pad_row(col, LINE_GAP, 0);
        s_name = lv_label_create(col);
        theme_label(s_name, font_body);
        lv_label_set_long_mode(s_name, LV_LABEL_LONG_MODE_DOTS);
        lv_obj_set_width(s_name, LV_PCT(100));
        for (int i = 0; i < APP_BODY_LINES; i++) {
            s_lines[i] = lv_label_create(col);
            theme_label(s_lines[i], font_status);
            lv_label_set_long_mode(s_lines[i], LV_LABEL_LONG_MODE_WRAP);
            lv_obj_set_width(s_lines[i], LV_PCT(100));
        }
        screen_mask_volatile(s_lines[1]); /* DEV <fingerprint> */
        screen_mask_volatile(s_lines[2]); /* HOST <fingerprint> */
        screen_mask_volatile(s_lines[3]); /* IP <ip>  BRIDGE <host>:<port> */
    }
    ui_set_text(s_name, r->title);
    for (int i = 0; i < APP_BODY_LINES; i++) {
        ui_set_text(s_lines[i], r->lines[i]);
    }
}
