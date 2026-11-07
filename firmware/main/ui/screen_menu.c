/* The agent and global menus (parent 9.1, 9.2, 9.6; parent design §UI): a
 * strip of tiles, each at least 120 x 96 px, the selected one inverted.
 * BOOT steps to the next tile and PWR selects it (the hint cells say so).
 *
 * Four tiles fit across. With more, the view keeps the selection in sight,
 * moving in whole tiles (never kinetic), and a bar under the tiles shows
 * where the view is, as the list's does at its right edge. */
#include "fonts.h"
#include "screen_status.h"
#include "theme.h"
#include "ui.h"

#define TILES 4
#define MARGIN 12
#define GAP 8
#define TILE_W ((LCD349_LCD_W - 2 * MARGIN - (TILES - 1) * GAP) / TILES) /* 148 */
#define TILE_H 96
#define TILE_Y 16
#define BORDER 2
#define BAR_Y (TILE_Y + TILE_H + 12)
#define BAR_H 4
#define STRIP_W (LCD349_LCD_W - 2 * MARGIN)

typedef struct {
    lv_obj_t *tile, *text;
    int sel; /* as drawn; -1 before the first draw */
} tile_t;

static tile_t s_tiles[TILES];
static lv_obj_t *s_bar;

static void tile_create(lv_obj_t *body, tile_t *t, int i)
{
    t->tile = lv_obj_create(body);
    theme_container(t->tile);
    lv_obj_set_style_border_color(t->tile, theme_fg(), 0);
    lv_obj_set_style_border_width(t->tile, BORDER, 0);
    lv_obj_set_pos(t->tile, MARGIN + i * (TILE_W + GAP), TILE_Y);
    lv_obj_set_size(t->tile, TILE_W, TILE_H);
    t->text = lv_label_create(t->tile);
    theme_label(t->text, font_body);
    lv_label_set_long_mode(t->text, LV_LABEL_LONG_MODE_WRAP);
    lv_obj_set_width(t->text, TILE_W - 4 * BORDER);
    lv_obj_set_style_text_align(t->text, LV_TEXT_ALIGN_CENTER, 0);
    lv_obj_center(t->text);
    t->sel = -1;
}

void screen_menu_draw(lv_obj_t *body, const app_render_t *r, bool fresh)
{
    if (fresh) {
        for (int i = 0; i < TILES; i++) {
            tile_create(body, &s_tiles[i], i);
        }
        s_bar = lv_obj_create(body);
        theme_container(s_bar);
        lv_obj_set_style_bg_color(s_bar, theme_fg(), 0);
        lv_obj_set_style_bg_opa(s_bar, LV_OPA_COVER, 0);
        lv_obj_set_y(s_bar, BAR_Y);
        lv_obj_set_height(s_bar, BAR_H);
    }
    int first = r->selected_item < TILES ? 0 : r->selected_item - (TILES - 1);
    for (int i = 0; i < TILES; i++) {
        tile_t *t = &s_tiles[i];
        int k = first + i;
        bool shown = k < r->n_items;
        lv_obj_set_hidden(t->tile, !shown);
        if (!shown) {
            continue;
        }
        ui_set_text(t->text, r->items[k]);
        int sel = k == r->selected_item;
        if (t->sel != sel) {
            screen_invert(t->tile, sel);
            t->sel = sel;
        }
    }
    bool bar = r->n_items > TILES;
    lv_obj_set_hidden(s_bar, !bar);
    if (bar) {
        int32_t w = STRIP_W * TILES / r->n_items, x = MARGIN + STRIP_W * first / r->n_items;
        if (lv_obj_get_x(s_bar) != x || lv_obj_get_width(s_bar) != w) {
            lv_obj_set_x(s_bar, x);
            lv_obj_set_width(s_bar, w);
        }
    }
}
