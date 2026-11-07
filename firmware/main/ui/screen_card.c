/* A card (parent 7.2, 7.7, 18.3; design D2-10), top to bottom:
 * - a danger card's banner, DANGER: <label>, inverted, as the body's first row;
 * - the card's first line in the body font: a permission card's command or
 *   path (the bridge puts it first, ADR 0006), or an MCP call's server and
 *   tool (its arguments are lines 1-3, O-90), or COMMAND NOT READ on one the
 *   bridge couldn't read in full, or a question card's QUESTION - ANSWER AT
 *   KEYBOARD;
 * - the summary (the prompt box's title) and the other lines, in the small font.
 * Every line ends in "..." if it doesn't fit. The hint cells say what the
 * buttons do, and BOOT's fills while it's held towards an approve
 * (screen_status.c). */
#include "fonts.h"
#include "screen_status.h"
#include "theme.h"
#include "ui.h"

#define MARGIN 12
#define BANNER_H 30
#define FIRST_H 30 /* the first line's row */
#define SMALL_H 20 /* each small line's row */
#define SMALL_N APP_BODY_LINES /* the summary, then lines 1-3 */

static lv_obj_t *s_banner;
static lv_obj_t *s_banner_text;
static lv_obj_t *s_first;
static lv_obj_t *s_small[SMALL_N];

static lv_obj_t *line(lv_obj_t *parent, const lv_font_t *font)
{
    lv_obj_t *l = lv_label_create(parent);
    theme_label(l, font);
    lv_label_set_long_mode(l, LV_LABEL_LONG_MODE_DOTS);
    lv_obj_set_width(l, LCD349_LCD_W - 2 * MARGIN);
    lv_obj_set_x(l, MARGIN);
    return l;
}

/* Puts a label in the row at y, centred in its height h, or hides it when text is empty. */
static int32_t place(lv_obj_t *l, const char *text, int32_t y, int32_t h)
{
    lv_obj_set_hidden(l, text[0] == '\0');
    if (text[0] == '\0') {
        return y;
    }
    ui_set_text(l, text);
    int32_t ly = y + (h - lv_font_get_line_height(lv_obj_get_style_text_font(l, LV_PART_MAIN))) / 2;
    if (lv_obj_get_y(l) != ly) {
        lv_obj_set_y(l, ly);
    }
    return y + h;
}

void screen_card_draw(lv_obj_t *body, const app_render_t *r, bool fresh)
{
    if (fresh) {
        s_banner = lv_obj_create(body);
        theme_container(s_banner);
        lv_obj_set_size(s_banner, LCD349_LCD_W, BANNER_H);
        s_banner_text = line(s_banner, font_body);
        lv_obj_set_y(s_banner_text, (BANNER_H - lv_font_get_line_height(font_body)) / 2);
        screen_invert(s_banner, true);
        s_first = line(body, font_body);
        for (int i = 0; i < SMALL_N; i++) {
            s_small[i] = line(body, font_status);
        }
    }
    int32_t y = 0;
    lv_obj_set_hidden(s_banner, r->banner[0] == '\0');
    if (r->banner[0]) {
        ui_set_text(s_banner_text, r->banner);
        y = BANNER_H + 4;
    }
    y = place(s_first, r->lines[0], y, FIRST_H);
    y = place(s_small[0], r->title, y, SMALL_H);
    for (int i = 1; i < SMALL_N; i++) {
        y = place(s_small[i], r->lines[i], y, SMALL_H);
    }
}
