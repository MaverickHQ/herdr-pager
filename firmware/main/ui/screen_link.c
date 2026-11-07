/* The link's states that stop the pager: NOT AUTHORISED (parent 3.5) and HOST KEY CHANGED - RE-PAIR VIA USB
 * (parent 3.3). The title, wrapped and centred, and a line under it; the hint cells say what each button does.
 * (NO WIFI is the status band's: the list stays.) */
#include "fonts.h"
#include "theme.h"
#include "ui.h"

#define MARGIN 12

static lv_obj_t *s_title;
static lv_obj_t *s_line;

void screen_link_draw(lv_obj_t *body, const app_render_t *r, bool fresh)
{
    if (fresh) {
        s_title = lv_label_create(body);
        theme_label(s_title, font_title);
        lv_label_set_long_mode(s_title, LV_LABEL_LONG_MODE_WRAP);
        lv_obj_set_width(s_title, LCD349_LCD_W - 2 * MARGIN);
        lv_obj_set_style_text_align(s_title, LV_TEXT_ALIGN_CENTER, 0);
        lv_obj_align(s_title, LV_ALIGN_CENTER, 0, -18);
        s_line = lv_label_create(body);
        theme_label(s_line, font_body);
        lv_obj_align(s_line, LV_ALIGN_CENTER, 0, 40);
    }
    ui_set_text(s_title, r->title);
    ui_set_text(s_line, r->lines[0]);
}
