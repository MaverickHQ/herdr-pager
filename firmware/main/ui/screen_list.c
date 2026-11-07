/* The agent list (parent 5.1-5.3, 5.5; parent design §UI): four 36 px rows
 * from the render model's first_row. A row is the glyph, the name, the kind
 * and the age, in fixed columns: the font is proportional, so the parent's
 * 40-column row format becomes columns at fixed x.
 *
 * - The selected row is inverted. A paged agent's glyph is inverted within
 *   its row (so in the selected row it shows the other way round).
 * - A seen done agent's row is dimmed by drawing it in the small font: the
 *   palette has one contrast, which the kit's lint holds, so dim means
 *   smaller, not paler.
 * - With more rows than fit, a bar at the right edge shows where the view is.
 * - An empty list says why, in the core's two lines, centred: NO AGENTS on a
 *   live link, else what keeps the list away (open-items O-77).
 * - The ages change with the time: goldens mask them. */
#include "fonts.h"
#include "screen_status.h"
#include "theme.h"
#include "ui.h"

#define ROWS 4
#define ROW_H 36
#define GLYPH_X 10
#define GLYPH_W 24
#define GLYPH_H 28
#define NAME_X 44
#define NAME_W 300
#define KIND_X 360
#define KIND_W 150
#define AGE_X 520
#define AGE_W 100
#define BAR_X 632
#define BAR_W 4
#define ROW_W (BAR_X - 4) /* the selection stops short of the bar's column */

typedef struct {
    lv_obj_t *row, *glyph_box, *glyph, *name, *kind, *age;
    int sel, paged, dim; /* as drawn; -1 before the first draw */
} row_t;

static row_t s_rows[ROWS];
static lv_obj_t *s_bar;
static lv_obj_t *s_empty;      /* the empty list's title ... */
static lv_obj_t *s_empty_line; /* ... and the line under it */
static int32_t s_empty_y;      /* the title's offset from the middle, as aligned */

#define EMPTY_MARGIN 12

static lv_obj_t *column(lv_obj_t *row, int32_t x, int32_t w, lv_text_align_t align)
{
    lv_obj_t *l = lv_label_create(row);
    theme_label(l, font_body);
    lv_label_set_long_mode(l, LV_LABEL_LONG_MODE_DOTS);
    lv_obj_set_width(l, w);
    lv_obj_set_style_text_align(l, align, 0);
    lv_obj_align(l, LV_ALIGN_LEFT_MID, x, 0);
    return l;
}

static void row_create(lv_obj_t *body, row_t *w, int i)
{
    w->row = lv_obj_create(body);
    theme_container(w->row);
    lv_obj_set_pos(w->row, 0, i * ROW_H);
    lv_obj_set_size(w->row, ROW_W, ROW_H);
    w->glyph_box = lv_obj_create(w->row);
    theme_container(w->glyph_box);
    lv_obj_set_pos(w->glyph_box, GLYPH_X, (ROW_H - GLYPH_H) / 2);
    lv_obj_set_size(w->glyph_box, GLYPH_W, GLYPH_H);
    w->glyph = lv_label_create(w->glyph_box);
    theme_label(w->glyph, font_body);
    lv_obj_center(w->glyph);
    w->name = column(w->row, NAME_X, NAME_W, LV_TEXT_ALIGN_LEFT);
    w->kind = column(w->row, KIND_X, KIND_W, LV_TEXT_ALIGN_LEFT);
    w->age = column(w->row, AGE_X, AGE_W, LV_TEXT_ALIGN_RIGHT);
    screen_mask_volatile(w->age);
    w->sel = w->paged = w->dim = -1;
}

static void set_font(lv_obj_t *label, const lv_font_t *font)
{
    if (lv_obj_get_style_text_font(label, LV_PART_MAIN) != font) {
        lv_obj_set_style_text_font(label, font, 0);
    }
}

static void row_draw(row_t *w, const app_row_t *a, bool sel)
{
    char glyph[2] = {a->glyph, '\0'};
    ui_set_text(w->glyph, glyph);
    ui_set_text(w->name, a->name);
    ui_set_text(w->kind, a->kind);
    ui_set_text(w->age, a->age);
    if (w->dim != a->dim) {
        const lv_font_t *font = a->dim ? font_status : font_body;
        set_font(w->glyph, font);
        set_font(w->name, font);
        set_font(w->kind, font);
        set_font(w->age, font);
        w->dim = a->dim;
    }
    if (w->sel != sel || w->paged != a->paged) {
        screen_invert(w->row, sel);
        /* The glyph's cell: inverted against its row while the agent's page is active. */
        bool inverted = sel != a->paged;
        lv_obj_set_style_bg_color(w->glyph_box, inverted ? theme_fg() : theme_bg(), 0);
        lv_obj_set_style_bg_opa(w->glyph_box, a->paged ? LV_OPA_COVER : LV_OPA_TRANSP, 0);
        lv_obj_set_style_text_color(w->glyph, inverted ? theme_bg() : theme_fg(), 0);
        w->sel = sel;
        w->paged = a->paged;
    }
}

void screen_list_draw(lv_obj_t *body, const app_render_t *r, bool fresh)
{
    if (fresh) {
        for (int i = 0; i < ROWS; i++) {
            row_create(body, &s_rows[i], i);
        }
        s_bar = lv_obj_create(body);
        theme_container(s_bar);
        lv_obj_set_style_bg_color(s_bar, theme_fg(), 0);
        lv_obj_set_style_bg_opa(s_bar, LV_OPA_COVER, 0);
        lv_obj_set_x(s_bar, BAR_X);
        lv_obj_set_width(s_bar, BAR_W);
        s_empty = lv_label_create(body);
        theme_label(s_empty, font_title);
        lv_label_set_long_mode(s_empty, LV_LABEL_LONG_MODE_DOTS);
        lv_obj_set_style_text_align(s_empty, LV_TEXT_ALIGN_CENTER, 0);
        lv_obj_set_width(s_empty, LCD349_LCD_W - 2 * EMPTY_MARGIN);
        s_empty_line = lv_label_create(body);
        theme_label(s_empty_line, font_body);
        lv_label_set_long_mode(s_empty_line, LV_LABEL_LONG_MODE_DOTS);
        lv_obj_set_style_text_align(s_empty_line, LV_TEXT_ALIGN_CENTER, 0);
        lv_obj_set_width(s_empty_line, LCD349_LCD_W - 2 * EMPTY_MARGIN);
        lv_obj_align(s_empty_line, LV_ALIGN_CENTER, 0, 30);
    }
    for (int i = 0; i < ROWS; i++) {
        int k = r->first_row + i;
        bool shown = k < r->n_rows;
        lv_obj_set_hidden(s_rows[i].row, !shown);
        if (shown) {
            row_draw(&s_rows[i], &r->rows[k], k == r->selected);
        }
    }
    bool bar = r->n_rows > ROWS;
    lv_obj_set_hidden(s_bar, !bar);
    if (bar) {
        int32_t h = UI_BODY_H * ROWS / r->n_rows, y = UI_BODY_H * r->first_row / r->n_rows;
        if (lv_obj_get_y(s_bar) != y || lv_obj_get_height(s_bar) != h) {
            lv_obj_set_pos(s_bar, BAR_X, y);
            lv_obj_set_height(s_bar, h);
        }
    }
    bool empty = r->n_rows == 0 && r->lines[0][0];
    bool line = empty && r->lines[1][0];
    ui_set_text(s_empty, r->lines[0]);
    ui_set_text(s_empty_line, r->lines[1]);
    int32_t y = line ? -18 : 0; /* alone, NO AGENTS sits in the middle */
    if (fresh || y != s_empty_y) {
        lv_obj_align(s_empty, LV_ALIGN_CENTER, 0, y);
        s_empty_y = y;
    }
    lv_obj_set_hidden(s_empty, !empty);
    lv_obj_set_hidden(s_empty_line, !line);
}
