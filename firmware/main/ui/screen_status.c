/* The status band, on every screen (design D2-10): the status on the left,
 * and a hint cell under each button at the right, PWR then BOOT
 * (platform/hints.h). A cell holds up to two lines of the small font; a
 * hold's progress fills it from the left, inverting what it covers. The boot
 * and pairing screens show no status: before pairing, none of it applies.
 *
 * The status is three labels in a row, two spaces apart: the counts (and
 * "+N" for agents beyond the 16), or on an open card the name of the agent
 * it's from; the flags, HERDR OFFLINE, ON BATTERY and SILENT (hidden when
 * none is set); then the clock, the link, the network and the list's LAST
 * SEEN. The clock's digits differ in width, so everything from it on moves
 * with the time: that label is masked in goldens. The flags are states, so
 * they come before it, and goldens show them. */
#include "screen_status.h"

#include <stdio.h>
#include <string.h>

#include "fonts.h"
#include "hints.h"
#include "lcd349.h"
#include "theme.h"
#include "ui.h"

#define MARGIN 12
#define HINT_LINE_SPACE -3 /* two 15 px lines of the 12 px font fit the band */
#define MASK_TAG "golden-mask"

typedef struct {
    lv_obj_t *label;
    lv_obj_t *fill;
    int32_t w;
} cell_t;

static lv_obj_t *s_counts;
static lv_obj_t *s_flags;
static lv_obj_t *s_rest;
static cell_t s_pwr, s_boot;

void screen_mask_volatile(lv_obj_t *label)
{
    lcd349_ui_tag(label, MASK_TAG);
}

static void invert_labels(lv_obj_t *obj, lv_color_t text)
{
    for (uint32_t i = 0; i < lv_obj_get_child_count(obj); i++) {
        lv_obj_t *c = lv_obj_get_child(obj, (int32_t)i);
        if (lv_obj_check_type(c, &lv_label_class)) {
            lv_obj_set_style_text_color(c, text, 0);
        }
        invert_labels(c, text);
    }
}

void screen_invert(lv_obj_t *obj, bool on)
{
    lv_obj_set_style_bg_color(obj, theme_fg(), 0);
    lv_obj_set_style_bg_opa(obj, on ? LV_OPA_COVER : LV_OPA_TRANSP, 0);
    invert_labels(obj, on ? theme_bg() : theme_fg());
}

static void cell_create(lv_obj_t *band, cell_t *c, int32_t x, int32_t w)
{
    lv_obj_t *box = lv_obj_create(band);
    theme_container(box);
    lv_obj_set_pos(box, x, 0);
    lv_obj_set_size(box, w, UI_BAND_H - 1);
    c->w = w;
    c->label = lv_label_create(box);
    theme_label(c->label, font_hint);
    lv_obj_set_style_text_align(c->label, LV_TEXT_ALIGN_CENTER, 0);
    lv_obj_set_style_text_line_space(c->label, HINT_LINE_SPACE, 0);
    lv_obj_set_width(c->label, w);
    lv_obj_align(c->label, LV_ALIGN_CENTER, 0, 0);
    /* The fill is drawn over the label and swaps the palette under it. */
    c->fill = lv_obj_create(box);
    theme_container(c->fill);
    theme_invert_overlay(c->fill);
    lv_obj_set_pos(c->fill, 0, 0);
    lv_obj_set_size(c->fill, 0, UI_BAND_H - 1);
}

static void cell_draw(cell_t *c, const app_hint_t *h)
{
    ui_set_text(c->label, h->text);
    int32_t fill = (int32_t)(h->fill_permille > 1000 ? 1000 : h->fill_permille) * c->w / 1000;
    if (lv_obj_get_width(c->fill) != fill) {
        lv_obj_set_width(c->fill, fill);
    }
}

static void status_text(const app_render_t *r, char *counts, size_t counts_size, char *flags, size_t flags_size,
                        char *rest, size_t rest_size)
{
    const app_band_t *b = &r->band;
    const char *const set[] = {
        b->herdr_offline ? "HERDR OFFLINE" : NULL,
        b->on_battery ? "ON BATTERY" : NULL,
        b->silent ? "SILENT" : NULL,
    };
    static const char link_glyph[] = {
        [APP_LINK_DOWN] = '~',
        [APP_LINK_CONNECTING] = '~',
        [APP_LINK_CONNECTED] = 'o',
        [APP_LINK_AUTH_REFUSED] = 'x',
        [APP_LINK_HOST_KEY_CHANGED] = '#',
    };
    counts[0] = flags[0] = rest[0] = '\0';
    if (b->blank) { /* the core's say: the pairing screens, and FACTORY RESET? over them (O-32) */
        return;
    }
    char more[16] = "";
    if (r->more) {
        snprintf(more, sizeof(more), " +%u", (unsigned)r->more);
    }
    if (b->agent[0]) {
        snprintf(counts, counts_size, "%s", b->agent);
    } else if (b->all_clear) { /* nothing blocked or done unseen (open-items O-85) */
        snprintf(counts, counts_size, "ALL CLEAR >%u%s", b->working, more);
    } else {
        snprintf(counts, counts_size, "!%u *%u >%u%s", b->blocked, b->done, b->working, more);
    }
    for (size_t i = 0; i < sizeof(set) / sizeof(set[0]); i++) {
        size_t n = strlen(flags);
        if (set[i]) {
            snprintf(flags + n, flags_size - n, "%s%s", n ? "  " : "", set[i]);
        }
    }
    /* On the list, its title says how old it is while the link is down (parent 16.4). */
    const char *last_seen = r->screen == APP_SCREEN_LIST ? r->title : "";
    char net[APP_SSID_MAX + 16];
    if (b->wifi) {
        snprintf(net, sizeof(net), "%s", b->ssid);
    } else if (b->failed[0]) { /* a wrong password, most likely (parent design §Error handling; O-49) */
        snprintf(net, sizeof(net), "WIFI %s FAILED", b->failed);
    } else {
        snprintf(net, sizeof(net), "NO WIFI");
    }
    snprintf(rest, rest_size, "%s  %c %s%s%s", b->clock,
             b->link <= APP_LINK_HOST_KEY_CHANGED ? link_glyph[b->link] : '?', net, last_seen[0] ? "  " : "",
             last_seen);
}

void screen_status_draw(lv_obj_t *band, const app_render_t *r, bool fresh)
{
    if (fresh) {
        lv_obj_t *row = lv_obj_create(band);
        theme_container(row);
        lv_obj_set_pos(row, MARGIN, 0);
        lv_obj_set_size(row, HINT_PWR_X - 2 * MARGIN, UI_BAND_H);
        lv_obj_set_flex_flow(row, LV_FLEX_FLOW_ROW);
        lv_obj_set_flex_align(row, LV_FLEX_ALIGN_START, LV_FLEX_ALIGN_CENTER, LV_FLEX_ALIGN_CENTER);
        /* Two spaces apart, as one line of text would set them. */
        lv_obj_set_style_pad_column(row, 2 * lv_font_get_glyph_width(font_status, ' ', ' '), 0);
        s_counts = lv_label_create(row);
        theme_label(s_counts, font_status);
        s_flags = lv_label_create(row);
        theme_label(s_flags, font_status);
        s_rest = lv_label_create(row);
        theme_label(s_rest, font_status);
        lv_label_set_long_mode(s_rest, LV_LABEL_LONG_MODE_DOTS);
        lv_obj_set_flex_grow(s_rest, 1);
        screen_mask_volatile(s_rest);
        cell_create(band, &s_pwr, HINT_PWR_X, HINT_PWR_W);
        cell_create(band, &s_boot, HINT_BOOT_X, HINT_BOOT_W);
    }
    char counts[32], flags[48], rest[128];
    status_text(r, counts, sizeof(counts), flags, sizeof(flags), rest, sizeof(rest));
    ui_set_text(s_counts, counts);
    ui_set_text(s_flags, flags);
    if (lv_obj_has_flag(s_flags, LV_OBJ_FLAG_HIDDEN) != (flags[0] == '\0')) {
        lv_obj_set_hidden(s_flags, flags[0] == '\0'); /* none: no gap for it either */
    }
    ui_set_text(s_rest, rest);
    cell_draw(&s_pwr, &r->band.pwr);
    cell_draw(&s_boot, &r->band.boot);
}
