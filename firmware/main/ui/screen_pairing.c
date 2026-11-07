/* Pairing: a prov_offer to confirm, or confirmed and waiting for its prov_set (parent 2.5; ADR 0011). The title
 * names the Mac; under it the bridge's and the device's full fingerprints, each wrapped onto a second line if it
 * doesn't fit. The hint cells say what BOOT and PWR do. */
#include "fonts.h"
#include "theme.h"
#include "ui.h"

#define MARGIN 12
#define TITLE_Y 4
#define HOST_Y 34
#define DEV_Y 88 /* room for the host's fingerprint on two lines */

static lv_obj_t *s_title;
static lv_obj_t *s_host;
static lv_obj_t *s_dev;

static lv_obj_t *line(lv_obj_t *body, int32_t y)
{
    lv_obj_t *l = lv_label_create(body);
    theme_label(l, font_body);
    lv_label_set_long_mode(l, LV_LABEL_LONG_MODE_WRAP);
    lv_obj_set_width(l, LCD349_LCD_W - 2 * MARGIN);
    lv_obj_set_pos(l, MARGIN, y);
    return l;
}

void screen_pairing_draw(lv_obj_t *body, const app_render_t *r, bool fresh)
{
    if (fresh) {
        s_title = line(body, TITLE_Y);
        lv_label_set_long_mode(s_title, LV_LABEL_LONG_MODE_DOTS);
        s_host = line(body, HOST_Y);
        s_dev = line(body, DEV_Y);
    }
    ui_set_text(s_title, r->title);
    ui_set_text(s_host, r->lines[0]);
    ui_set_text(s_dev, r->lines[1]);
}
