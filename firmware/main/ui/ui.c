/* The two bands, and the body's screen. */
#include "ui.h"

#include <string.h>

#include "theme.h"

#define FLASH_STEP_MS 150
#define FLASH_STEPS 6 /* 3 inversions, each on then off */

static lv_obj_t *s_band;
static lv_obj_t *s_body;
static lv_obj_t *s_flash;
static lv_timer_t *s_flash_timer;
static int s_flash_step;
static bool s_band_drawn;
static int s_screen = -1;

static const ui_draw_fn s_screens[APP_SCREEN_COUNT] = {
    [APP_SCREEN_BOOT] = screen_boot_draw,
    [APP_SCREEN_LIST] = screen_list_draw,
    [APP_SCREEN_CARD] = screen_card_draw,
    [APP_SCREEN_MENU] = screen_menu_draw,
    [APP_SCREEN_CONFIRM] = screen_confirm_draw,
    [APP_SCREEN_TOAST] = screen_toast_draw,
    [APP_SCREEN_INFO] = screen_info_draw,
    [APP_SCREEN_PAIRING] = screen_pairing_draw,
    [APP_SCREEN_LINK] = screen_link_draw,
};

static lv_obj_t *band_create(lv_obj_t *parent, int32_t y, int32_t h)
{
    lv_obj_t *obj = lv_obj_create(parent);
    theme_container(obj);
    lv_obj_set_pos(obj, 0, y);
    lv_obj_set_size(obj, LCD349_LCD_W, h);
    return obj;
}

void ui_init(void)
{
    lv_obj_t *scr = lv_screen_active();
    theme_screen(scr);
    s_band = band_create(scr, 0, UI_BAND_H);
    s_body = band_create(scr, UI_BODY_Y, UI_BODY_H);
    /* A rule along the band's bottom row separates the bands. */
    lv_obj_t *rule = band_create(scr, UI_BAND_H - 1, 1);
    lv_obj_set_style_bg_color(rule, theme_fg(), 0);
    lv_obj_set_style_bg_opa(rule, LV_OPA_COVER, 0);
}

void ui_set_text(lv_obj_t *label, const char *text)
{
    if (strcmp(lv_label_get_text(label), text) != 0) {
        lv_label_set_text(label, text);
    }
}

void ui_render(const app_render_t *r)
{
    screen_status_draw(s_band, r, !s_band_drawn);
    s_band_drawn = true;

    bool fresh = (int)r->screen != s_screen;
    if (fresh) {
        lv_obj_clean(s_body);
        s_screen = (int)r->screen;
    }
    if (r->screen < APP_SCREEN_COUNT && s_screens[r->screen]) {
        s_screens[r->screen](s_body, r, fresh);
    }
}

static void flash_step(lv_timer_t *t)
{
    (void)t;
    s_flash_step++;
    if (s_flash_step >= FLASH_STEPS) {
        lv_obj_delete(s_flash);
        s_flash = NULL;
        lv_timer_delete(s_flash_timer);
        s_flash_timer = NULL;
        return;
    }
    lv_obj_set_hidden(s_flash, s_flash_step % 2);
}

void ui_flash(void)
{
    if (s_flash) {
        return; /* a flash is already playing */
    }
    s_flash = lv_obj_create(lv_layer_top());
    theme_container(s_flash);
    lv_obj_set_size(s_flash, LCD349_LCD_W, LCD349_LCD_H);
    theme_invert_overlay(s_flash);
    s_flash_step = 0;
    s_flash_timer = lv_timer_create(flash_step, FLASH_STEP_MS, NULL);
}
