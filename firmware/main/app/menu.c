/* Menus and confirms (parent 9; parent design §UI: a strip of tiles, BOOT
 * next, PWR select). A confirm puts each button's meaning in its own hint
 * cell, so no string fixes the buttons' order (R8.3). */
#include <stdio.h>
#include <string.h>

#include "app_internal.h"

enum {
    ITEM_JUMP,
    ITEM_INTERRUPT,
    ITEM_SETTINGS, /* the global menu, from an agent's (otherwise it's reachable only with no agents) */
    ITEM_SILENT,
    ITEM_VOLUME,
    ITEM_DONE_TONE,
    ITEM_BRIGHTNESS,
    ITEM_COLOUR,
    ITEM_INFO,
    ITEM_PAIR,
};

/* No snooze, quiet hours or power off in this phase (parent 9.3, 9.6: phase 3). */
static const char *const ITEM_TEXT[] = {
    [ITEM_JUMP] = "JUMP",        [ITEM_INTERRUPT] = "INTERRUPT", [ITEM_SETTINGS] = "SETTINGS",
    [ITEM_SILENT] = "SILENT",    [ITEM_VOLUME] = "VOLUME",       [ITEM_DONE_TONE] = "DONE TONE",
    [ITEM_BRIGHTNESS] = "BRIGHTNESS", [ITEM_COLOUR] = "COLOUR",  [ITEM_INFO] = "DEVICE INFO",
    [ITEM_PAIR] = "PAIR AGAIN",
};

static const char *const PALETTE_TEXT[APP_PALETTE_COUNT] = {
    [APP_PALETTE_ORANGE] = "ORANGE",
    [APP_PALETTE_GREEN] = "GREEN",
};

#define LEVEL_STEP 20 /* volume 0-100 and brightness 20-100, in steps */

static const app_agent_t *agent(const app_state_t *s, const char *pane)
{
    for (uint8_t i = 0; i < s->model.n; i++) {
        if (strcmp(s->model.agents[i].pane, pane) == 0) {
            return &s->model.agents[i];
        }
    }
    return NULL;
}

/* Whether a is working, can be interrupted, and is still at seq: what Interrupt was offered for (parent 9.5; the
 * owner's decision, 2026-10-27, open-items O-27). */
static bool interruptible(const app_agent_t *a, uint64_t seq)
{
    return a && a->st == APP_ST_WORKING && (a->can & APP_CAN_INTERRUPT) && a->seq == seq;
}

static void add(app_state_t *s, uint8_t item)
{
    if (s->menu.n < APP_MENU_MAX) {
        s->menu.items[s->menu.n++] = item;
    }
}

void menu_open_global(app_state_t *s)
{
    memset(&s->menu, 0, sizeof(s->menu));
    s->menu.open = true;
    static const uint8_t items[] = {ITEM_SILENT,     ITEM_VOLUME, ITEM_DONE_TONE, ITEM_BRIGHTNESS,
                                    ITEM_COLOUR,     ITEM_INFO,   ITEM_PAIR};
    for (size_t i = 0; i < sizeof(items); i++) {
        add(s, items[i]);
    }
}

void menu_open_agent(app_state_t *s, const char *pane)
{
    const app_agent_t *a = agent(s, pane);
    if (!a) {
        return;
    }
    memset(&s->menu, 0, sizeof(s->menu));
    s->menu.open = true;
    snprintf(s->menu.pane, sizeof(s->menu.pane), "%s", pane);
    s->menu.seq = a->seq;
    add(s, ITEM_JUMP);
    if (interruptible(a, a->seq)) { /* parent 9.5, as narrowed by R6.3 */
        add(s, ITEM_INTERRUPT);
    }
    add(s, ITEM_SETTINGS);
}

void menu_close(app_state_t *s)
{
    s->menu.open = false;
}

void menu_open_reset(app_state_t *s)
{
    s->menu.open = false;
    s->menu.info = false;
    s->confirm.kind = APP_CONFIRM_RESET;
}

void menu_next(app_state_t *s)
{
    if (s->menu.n) {
        s->menu.selected = (uint8_t)((s->menu.selected + 1) % s->menu.n);
    }
}

/* A setting that steps on each select: the menu stays open under its toast. */
static void step_setting(app_state_t *s, uint8_t item, app_ms_t now, app_out_t *out)
{
    char text[24];
    if (item == ITEM_SILENT) {
        model_toggle_silent(s, now, out);
        return;
    }
    if (item == ITEM_VOLUME) {
        s->set.volume = s->set.volume >= 100 ? 0 : (uint8_t)((s->set.volume / LEVEL_STEP + 1) * LEVEL_STEP);
        app_store(out, APP_KEY_VOLUME, "%u", s->set.volume);
        snprintf(text, sizeof(text), "VOLUME %u", s->set.volume);
    } else if (item == ITEM_DONE_TONE) {
        s->set.done_tone = !s->set.done_tone;
        app_store(out, APP_KEY_DONE_TONE, "%d", s->set.done_tone);
        snprintf(text, sizeof(text), "DONE TONE %s", s->set.done_tone ? "ON" : "OFF");
    } else if (item == ITEM_COLOUR) { /* the platform recolours the screen as it is: nothing is rebuilt */
        s->set.colour = (uint8_t)((s->set.colour + 1) % APP_PALETTE_COUNT);
        app_store(out, APP_KEY_COLOUR, "%u", s->set.colour);
        app_action_t *a = app_out_push(out, APP_ACT_THEME);
        if (a) {
            a->level = s->set.colour;
        }
        snprintf(text, sizeof(text), "COLOUR %s", PALETTE_TEXT[s->set.colour]);
    } else {
        s->set.brightness =
            s->set.brightness >= 100 ? LEVEL_STEP : (uint8_t)((s->set.brightness / LEVEL_STEP + 1) * LEVEL_STEP);
        app_store(out, APP_KEY_BRIGHTNESS, "%u", s->set.brightness);
        app_action_t *a = app_out_push(out, APP_ACT_BACKLIGHT);
        if (a) {
            a->level = s->set.brightness;
        }
        snprintf(text, sizeof(text), "BRIGHTNESS %u", s->set.brightness);
    }
    model_toast(s, text, now);
}

void menu_select(app_state_t *s, app_ms_t now, app_out_t *out)
{
    const app_agent_t *a = agent(s, s->menu.pane);
    uint8_t item = s->menu.items[s->menu.selected];
    switch (item) {
    case ITEM_SILENT:
    case ITEM_VOLUME:
    case ITEM_DONE_TONE:
    case ITEM_BRIGHTNESS:
    case ITEM_COLOUR:
        step_setting(s, item, now, out);
        return;
    default:
        break;
    }
    s->menu.open = false;
    if (item == ITEM_SETTINGS) {
        menu_open_global(s);
    } else if (item == ITEM_JUMP && a) { /* the bridge calls agent.focus (parent 9.4); its result is a toast (O-83) */
        s->menu.jump_id = codec_next_id(s);
        codec_send(out, "{\"t\":\"jump\",\"id\":%u,\"pane\":\"%s\"}", (unsigned)s->menu.jump_id, a->pane);
    } else if (item == ITEM_INTERRUPT && !interruptible(a, s->menu.seq)) { /* the menu's items are from its opening */
        model_toast(s, "SCREEN CHANGED", now);
    } else if (item == ITEM_INTERRUPT) {
        s->confirm.kind = APP_CONFIRM_INTERRUPT;
        snprintf(s->confirm.pane, sizeof(s->confirm.pane), "%s", a->pane);
        snprintf(s->confirm.name, sizeof(s->confirm.name), "%s", a->name);
        s->confirm.seq = a->seq;
    } else if (item == ITEM_INFO) {
        s->menu.info = true;
    } else if (item == ITEM_PAIR) {
        s->confirm.kind = APP_CONFIRM_PAIR;
    }
}

void menu_confirm(app_state_t *s, bool yes, app_ms_t now, app_out_t *out)
{
    uint8_t kind = s->confirm.kind;
    s->confirm.kind = APP_CONFIRM_NONE;
    if (!yes) {
        return;
    }
    if (kind == APP_CONFIRM_INTERRUPT && s->net.link != APP_LINK_CONNECTED) {
        model_toast(s, "X NO LINK", now);
    } else if (kind == APP_CONFIRM_INTERRUPT && !interruptible(agent(s, s->confirm.pane), s->confirm.seq)) {
        model_toast(s, "SCREEN CHANGED", now); /* it changed while the question was up (O-27) */
    } else if (kind == APP_CONFIRM_INTERRUPT) {
        s->verdict.id = codec_next_id(s);
        s->verdict.deny = false;
        s->verdict.interrupt = true;
        s->verdict.sent_at = now;
        codec_send(out, "{\"t\":\"interrupt\",\"id\":%u,\"pane\":\"%s\",\"seq\":%llu}", (unsigned)s->verdict.id,
                   s->confirm.pane, (unsigned long long)s->confirm.seq);
    } else if (kind == APP_CONFIRM_PAIR) { /* parent 2.10: pairing mode, keeping the key pair */
        s->card.open = false;
        app_action_t *a = app_out_push(out, APP_ACT_LINK);
        if (a) {
            a->link_op = APP_LINK_OP_DISCONNECT;
        }
        pairing_enter(s, now, out);
    } else if (kind == APP_CONFIRM_RESET) { /* parent 2.2: erase everything, key pair included, then start over */
        app_out_push(out, APP_ACT_ERASE); /* .key "" is every key */
        app_out_push(out, APP_ACT_REBOOT);
    }
}

static void render_info(const app_state_t *s, app_render_t *r)
{
    char fp[APP_FP_MAX + 1];
    r->screen = APP_SCREEN_INFO;
    snprintf(r->title, sizeof(r->title), "%s", s->boot.name);
    /* Audio that failed at start-up: the pager carries on silent, and says so here (parent design §Error
     * handling; the owner's decision, 2026-10-27, open-items O-49). */
    /* Then why it last started, and where it dials: what tells a pager away from home that it restarted, and
     * which Mac it looks for (the owner's decision, 2026-10-27, open-items O-77). */
    static const char *const STARTED[] = {
        [APP_START_POWER_ON] = "POWER ON", [APP_START_RESTART] = "RESTART", [APP_START_CRASH] = "CRASH",
        [APP_START_BROWNOUT] = "BROWNOUT", [APP_START_RESET] = "RESET",
    };
    snprintf(r->lines[0], sizeof(r->lines[0]), "FW %.*s%s  STARTED: %s", APP_BODY_LINE_MAX - 32, s->boot.fw,
             s->boot.no_sound ? "  NO SOUND" : "", s->boot.start <= APP_START_RESET ? STARTED[s->boot.start] : "RESET");
    snprintf(r->lines[1], sizeof(r->lines[1]), "DEV %s", app_fingerprint(s->boot.pubkey, fp, sizeof(fp)) ? fp : "-");
    snprintf(r->lines[2], sizeof(r->lines[2]), "HOST %s",
             app_fingerprint(s->boot.host_key, fp, sizeof(fp)) ? fp : "-");
    snprintf(r->lines[3], sizeof(r->lines[3]), "IP %s  BRIDGE %.*s:%u", s->net.ip[0] ? s->net.ip : "-",
             APP_BODY_LINE_MAX - 33, s->boot.host, (unsigned)s->boot.port);
    snprintf(r->band.pwr.text, sizeof(r->band.pwr.text), "BACK");
    snprintf(r->band.boot.text, sizeof(r->band.boot.text), "BACK");
}

void menu_render(const app_state_t *s, app_render_t *r)
{
    if (s->confirm.kind != APP_CONFIRM_NONE) {
        bool pwr_yes = s->confirm.kind == APP_CONFIRM_INTERRUPT;
        r->screen = APP_SCREEN_CONFIRM;
        if (pwr_yes) {
            snprintf(r->title, sizeof(r->title), "INTERRUPT %s?", s->confirm.name);
        } else if (s->confirm.kind == APP_CONFIRM_RESET) {
            snprintf(r->title, sizeof(r->title), "FACTORY RESET?");
        } else {
            snprintf(r->title, sizeof(r->title), "PAIR AGAIN?");
        }
        bool reset = s->confirm.kind == APP_CONFIRM_RESET;
        snprintf(r->band.pwr.text, sizeof(r->band.pwr.text), "%s", pwr_yes ? "YES" : reset ? "CANCEL" : "NO");
        snprintf(r->band.boot.text, sizeof(r->band.boot.text), "%s", pwr_yes ? "NO" : reset ? "RESET" : "YES");
    } else if (s->menu.info) {
        render_info(s, r);
    } else if (s->menu.open) {
        r->screen = APP_SCREEN_MENU;
        r->n_items = s->menu.n;
        r->selected_item = s->menu.selected;
        for (uint8_t i = 0; i < s->menu.n; i++) {
            snprintf(r->items[i], sizeof(r->items[i]), "%s", ITEM_TEXT[s->menu.items[i]]);
        }
        snprintf(r->band.pwr.text, sizeof(r->band.pwr.text), "SELECT");
        snprintf(r->band.boot.text, sizeof(r->band.boot.text), "NEXT\nHOLD: EXIT");
    }
}
