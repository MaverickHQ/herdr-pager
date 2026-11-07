/* The boot screen: starting, unpaired (PAIR VIA USB), or with no device key (NO DEVICE KEY): the title, and up
 * to two lines under it. */
#include "fonts.h"
#include "theme.h"
#include "ui.h"

static lv_obj_t *s_title;
static lv_obj_t *s_line;
static lv_obj_t *s_line2;

void screen_boot_draw(lv_obj_t *body, const app_render_t *r, bool fresh)
{
    if (fresh) {
        s_title = lv_label_create(body);
        theme_label(s_title, font_title);
        lv_obj_align(s_title, LV_ALIGN_CENTER, 0, -18);
        s_line = lv_label_create(body);
        theme_label(s_line, font_body);
        lv_obj_align(s_line, LV_ALIGN_CENTER, 0, 24);
        s_line2 = lv_label_create(body);
        theme_label(s_line2, font_body);
        lv_obj_align(s_line2, LV_ALIGN_CENTER, 0, 50);
    }
    ui_set_text(s_title, r->title);
    ui_set_text(s_line, r->lines[0]);
    ui_set_text(s_line2, r->lines[1]);
}
