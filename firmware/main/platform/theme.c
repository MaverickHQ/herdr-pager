#include "theme.h"

#include "app.h"

#define ARRAY_LEN(a) (sizeof(a) / sizeof((a)[0]))

typedef struct {
    uint32_t fg, bg;
} palette_t;

static const palette_t PALETTES[APP_PALETTE_COUNT] = {
    [APP_PALETTE_ORANGE] = {THEME_ORANGE_FG_HEX, THEME_ORANGE_BG_HEX},
    [APP_PALETTE_GREEN] = {THEME_GREEN_FG_HEX, THEME_GREEN_BG_HEX},
};

static uint8_t s_palette = APP_PALETTE_ORANGE;

/* Every colour property LVGL has: the screens set bg, border and text colours, and one they set later is
 * recoloured too. */
static const lv_style_prop_t COLOUR_PROPS[] = {
    LV_STYLE_BG_COLOR,      LV_STYLE_BG_GRAD_COLOR, LV_STYLE_BG_IMAGE_RECOLOR, LV_STYLE_BORDER_COLOR,
    LV_STYLE_OUTLINE_COLOR, LV_STYLE_SHADOW_COLOR,  LV_STYLE_IMAGE_RECOLOR,    LV_STYLE_LINE_COLOR,
    LV_STYLE_ARC_COLOR,     LV_STYLE_TEXT_COLOR,
};

/* A palette's three colours on screen: fg, bg and the overlay (fg + bg, no channel overflowing). */
enum { FG, BG, OVERLAY, ROLES };

typedef struct {
    lv_color_t from[ROLES], to[ROLES];
} recolour_t;

static lv_color_t overlay_of(const palette_t *p)
{
    return lv_color_hex(p->fg + p->bg);
}

static void roles(const palette_t *p, lv_color_t *c)
{
    c[FG] = lv_color_hex(p->fg);
    c[BG] = lv_color_hex(p->bg);
    c[OVERLAY] = overlay_of(p);
}

lv_color_t theme_fg(void)
{
    return lv_color_hex(PALETTES[s_palette].fg);
}

lv_color_t theme_bg(void)
{
    return lv_color_hex(PALETTES[s_palette].bg);
}

/* The screens set every colour locally, on the main part's default state (selector 0). */
static lv_obj_tree_walk_res_t recolour(lv_obj_t *obj, void *user_data)
{
    const recolour_t *rc = user_data;
    for (size_t i = 0; i < ARRAY_LEN(COLOUR_PROPS); i++) {
        lv_style_value_t v;
        if (lv_obj_get_local_style_prop(obj, COLOUR_PROPS[i], &v, 0) != LV_STYLE_RES_FOUND) {
            continue;
        }
        for (int k = 0; k < ROLES; k++) {
            if (lv_color_eq(v.color, rc->from[k])) {
                v.color = rc->to[k];
                lv_obj_set_local_style_prop(obj, COLOUR_PROPS[i], v, 0);
                break;
            }
        }
    }
    return LV_OBJ_TREE_WALK_NEXT;
}

void theme_set(uint8_t palette)
{
    if (palette >= APP_PALETTE_COUNT) {
        palette = APP_PALETTE_ORANGE;
    }
    if (palette == s_palette) {
        return;
    }
    recolour_t rc;
    roles(&PALETTES[s_palette], rc.from);
    roles(&PALETTES[palette], rc.to);
    s_palette = palette; /* theme_fg and theme_bg give the new colours from here: what is drawn next has them */
    lv_obj_t *const trees[] = {lv_screen_active(), lv_layer_top()};
    for (size_t i = 0; i < ARRAY_LEN(trees); i++) {
        if (trees[i]) {
            lv_obj_tree_walk(trees[i], recolour, &rc);
            lv_obj_invalidate(trees[i]);
        }
    }
}

void theme_screen(lv_obj_t *scr)
{
    lv_obj_set_style_bg_color(scr, theme_bg(), 0);
    lv_obj_set_style_bg_opa(scr, LV_OPA_COVER, 0);
    lv_obj_set_style_text_color(scr, theme_fg(), 0);
    lv_obj_set_scrollable(scr, false);
}

void theme_container(lv_obj_t *obj)
{
    lv_obj_remove_style_all(obj);
    lv_obj_set_scrollable(obj, false);
    lv_obj_set_clickable(obj, false);
}

void theme_label(lv_obj_t *label, const lv_font_t *font)
{
    lv_obj_set_style_text_color(label, theme_fg(), 0);
    lv_obj_set_style_text_font(label, font, 0);
}

void theme_invert_overlay(lv_obj_t *overlay)
{
    /* |bg - (fg + bg)| = fg and |fg - (fg + bg)| = bg, channel by channel (no channel overflows). */
    lv_obj_set_style_bg_color(overlay, overlay_of(&PALETTES[s_palette]), 0);
    lv_obj_set_style_bg_opa(overlay, LV_OPA_COVER, 0);
    lv_obj_set_style_blend_mode(overlay, LV_BLEND_MODE_DIFFERENCE, 0);
}
