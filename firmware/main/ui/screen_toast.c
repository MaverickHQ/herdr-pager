/* A toast: a result or a confirmation, for 2 s over the screen (parent 6.7,
 * 8.4, 8.5): OK, SCREEN CHANGED, X and the reason, SILENT ON or OFF, a
 * setting's new level. Its text is big and centred, wrapping onto a second
 * line if need be; the hint cells are blank, since no button acts on it. */
#include "fonts.h"
#include "theme.h"
#include "ui.h"

#define MARGIN 12

static lv_obj_t *s_text;

void screen_toast_draw(lv_obj_t *body, const app_render_t *r, bool fresh)
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
