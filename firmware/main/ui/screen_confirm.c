/* A confirm (parent 9.5, 2.2, 2.10): the question, big and centred in the
 * body (INTERRUPT <name>?, FACTORY RESET?, PAIR AGAIN?), wrapping onto a
 * second line if need be. Each button's meaning is in its own hint cell
 * (R8.3), so nothing here says which button is which. */
#include "fonts.h"
#include "theme.h"
#include "ui.h"

#define MARGIN 12

static lv_obj_t *s_text;

void screen_confirm_draw(lv_obj_t *body, const app_render_t *r, bool fresh)
{
    if (fresh) {
        s_text = lv_label_create(body);
        theme_label(s_text, font_title);
        lv_label_set_long_mode(s_text, LV_LABEL_LONG_MODE_WRAP);
        lv_obj_set_width(s_text, LCD349_LCD_W - 2 * MARGIN);
        lv_obj_set_style_text_align(s_text, LV_TEXT_ALIGN_CENTER, 0);
        lv_obj_center(s_text);
    }
    ui_set_text(s_text, r->title);
}
