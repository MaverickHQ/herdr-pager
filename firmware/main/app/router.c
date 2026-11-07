/* The input router: raw button edges into gestures (design D2-9), and
 * gestures into actions by context (parent design §Firmware components,
 * Input router). Touch never acts (R1.3).
 *
 * A hold's threshold passes while the button is down, so it is a timer: the
 * core asks for a call at it. An edge reaches the core at least 10 ms after
 * it happened, stamped with when it did (lcd349_input.h), so a release
 * stamped before a threshold can arrive after it. Thresholds are checked at
 * an edge's own stamp, and on a deadline only EDGE_GRACE_MS late: a release
 * stamped before a threshold still wins when it arrives within the grace
 * (the owner's decision, 2026-10-27, open-items O-22). */
#include <stdio.h>

#include "app_internal.h"

#define EDGE_GRACE_MS 30   /* a deadline fires a threshold this long after it: the kit's edges come >= 10 ms late */
#define SHORT_MS 600       /* a release before this is a short press */
#define CARD_GUARD_MS 500  /* a press starting this soon after a card appears is ignored, release and all */
#define FILL_STEP_MS 100   /* the approve fill bar is redrawn this often */
#define APPROVE_MS 1000
#define APPROVE_DANGER_MS 2000 /* parent 18.4 */

typedef enum {
    G_BOOT_SHORT,
    G_PWR_SHORT,
    G_BOOT_HOLD1, /* 1 s, while held */
    G_BOOT_HOLD,  /* 1.5 s, while held */
    G_BOOT_HOLD2, /* 2 s, while held */
    G_PWR_HOLD2,  /* 2 s, while held */
} gesture_t;

typedef struct {
    app_ms_t ms;
    gesture_t g;
} hold_t;

/* Each button's hold thresholds, in order. */
static const hold_t BOOT_HOLDS[] = {{1000, G_BOOT_HOLD1}, {1500, G_BOOT_HOLD}, {2000, G_BOOT_HOLD2}};
static const hold_t PWR_HOLDS[] = {{2000, G_PWR_HOLD2}};

static const hold_t *holds(app_btn_t btn, size_t *n)
{
    if (btn == APP_BTN_BOOT) {
        *n = sizeof(BOOT_HOLDS) / sizeof(BOOT_HOLDS[0]);
        return BOOT_HOLDS;
    }
    *n = sizeof(PWR_HOLDS) / sizeof(PWR_HOLDS[0]);
    return PWR_HOLDS;
}

/* Where a gesture lands (parent design §Firmware components, Input router). */
typedef enum {
    CTX_NONE, /* the boot and pairing screens: their short presses go to pairing.c */
    CTX_LIST,
    CTX_CARD_PERMISSION,
    CTX_CARD_DANGER, /* a permission card flagged danger: approve needs 2 s */
    CTX_CARD_DENY,   /* a permission card that failed closed: PWR denies, and no hold approves (2026-11-01) */
    CTX_CARD_OTHER,  /* a question or status card */
    CTX_MENU,
    CTX_INFO,
    CTX_CONFIRM_PWR_YES,  /* interrupt: PWR yes, BOOT no */
    CTX_CONFIRM_BOOT_YES, /* factory reset, pair again: BOOT yes, PWR no */
    CTX_LINK_ERROR,       /* NOT AUTHORISED, HOST KEY CHANGED: PWR device info, BOOT held the global menu */
} ctx_t;

static ctx_t context(const app_state_t *s)
{
    /* First: the factory-reset chord's question can stand over the pairing screens (open-items O-28). */
    if (s->confirm.kind == APP_CONFIRM_INTERRUPT) {
        return CTX_CONFIRM_PWR_YES;
    }
    if (s->confirm.kind != APP_CONFIRM_NONE) {
        return CTX_CONFIRM_BOOT_YES;
    }
    if (s->render.screen == APP_SCREEN_BOOT || s->render.screen == APP_SCREEN_PAIRING) {
        return CTX_NONE;
    }
    if (s->menu.info) {
        return CTX_INFO;
    }
    if (s->menu.open) {
        return CTX_MENU;
    }
    if (s->link_ctl.host_key_changed || s->link_ctl.refused) {
        return CTX_LINK_ERROR;
    }
    if (s->card.open) {
        if (app_card_deny_only(&s->card.c)) {
            return CTX_CARD_DENY;
        }
        if (!app_card_verdicts(&s->card.c)) {
            return CTX_CARD_OTHER;
        }
        return s->card.c.danger ? CTX_CARD_DANGER : CTX_CARD_PERMISSION;
    }
    return CTX_LIST;
}

/* A card that can be approved: BOOT held fills towards an approve. */
static bool permission(ctx_t c)
{
    return c == CTX_CARD_PERMISSION || c == CTX_CARD_DANGER;
}

/* A card that takes a deny: one that can be approved, or one that failed closed. */
static bool deniable(ctx_t c)
{
    return permission(c) || c == CTX_CARD_DENY;
}

void router_card_shown(app_state_t *s)
{
    s->router.btn[APP_BTN_BOOT].consumed |= s->router.btn[APP_BTN_BOOT].down;
    s->router.btn[APP_BTN_PWR].consumed |= s->router.btn[APP_BTN_PWR].down;
}

/* True while BOOT is held towards an approve on a permission card. */
static bool filling(const app_state_t *s)
{
    const app_press_t *b = &s->router.btn[APP_BTN_BOOT];
    return permission(context(s)) && b->down && !b->consumed && s->verdict.id == 0;
}

uint16_t router_approve_fill(const app_state_t *s, app_ms_t now)
{
    if (!filling(s)) {
        return 0;
    }
    app_ms_t full = context(s) == CTX_CARD_DANGER ? APPROVE_DANGER_MS : APPROVE_MS;
    app_ms_t held = now - s->router.btn[APP_BTN_BOOT].t_down;
    return (uint16_t)(held >= full ? 1000 : held * 1000 / full);
}

/* Acts on a gesture in the current context; returns true when it consumes the rest of the press. */
static bool route(app_state_t *s, gesture_t g, app_ms_t now, app_out_t *out)
{
    ctx_t c = context(s);
    switch (g) {
    case G_BOOT_SHORT:
        if (c == CTX_NONE) {
            pairing_press(s, APP_BTN_BOOT, now, out);
        } else if (c == CTX_LIST) {
            model_select_next(s);
        } else if (c == CTX_CARD_OTHER || deniable(c)) {
            s->card.open = false; /* back to the list: never a verdict */
        } else if (c == CTX_MENU) {
            menu_next(s);
        } else if (c == CTX_CONFIRM_PWR_YES || c == CTX_CONFIRM_BOOT_YES) {
            menu_confirm(s, c == CTX_CONFIRM_BOOT_YES, now, out);
        } else if (c == CTX_INFO) {
            s->menu.info = false;
        }
        return false;
    case G_PWR_SHORT:
        if (c == CTX_NONE) {
            pairing_press(s, APP_BTN_PWR, now, out);
        } else if (c == CTX_LIST && s->model.n == 0 && s->net.link != APP_LINK_CONNECTED) {
            s->menu.info = true; /* nothing to open: the network, the IP and the bridge's address (O-77) */
        } else if (c == CTX_LIST) {
            model_request_card(s, now, out);
        } else if (c == CTX_CARD_OTHER) {
            s->card.open = false;
        } else if (deniable(c) && s->verdict.id == 0) {
            model_verdict(s, "deny", now, out);
        } else if (c == CTX_MENU) {
            menu_select(s, now, out);
        } else if (c == CTX_CONFIRM_PWR_YES || c == CTX_CONFIRM_BOOT_YES) {
            menu_confirm(s, c == CTX_CONFIRM_PWR_YES, now, out);
        } else if (c == CTX_INFO) {
            s->menu.info = false;
        } else if (c == CTX_LINK_ERROR) {
            s->menu.info = true; /* the fingerprints, to check against the bridge's */
        }
        return false;
    case G_BOOT_HOLD:
        if (c == CTX_LIST) { /* the selected agent's menu, or the global one when there's none */
            if (s->model.n > 0) {
                menu_open_agent(s, s->sel.pane);
            } else {
                menu_open_global(s);
            }
            return true;
        }
        if (c == CTX_CARD_OTHER) {
            menu_open_agent(s, s->card.c.pane);
            return true;
        }
        if (c == CTX_MENU) {
            menu_close(s);
            return true;
        }
        if (c == CTX_LINK_ERROR) { /* to reach Pair again */
            menu_open_global(s);
            return true;
        }
        return false;
    case G_BOOT_HOLD1: /* 1 s: approve on a permission card */
    case G_BOOT_HOLD2: /* 2 s: approve on a danger card (parent 18.4) */
        if (c == (g == G_BOOT_HOLD1 ? CTX_CARD_PERMISSION : CTX_CARD_DANGER) && s->verdict.id == 0) {
            model_verdict(s, "approve", now, out);
            return true;
        }
        return false;
    case G_PWR_HOLD2: /* only where the parent's table has it: the list and the cards (open-items O-23) */
        if (c != CTX_LIST && c != CTX_CARD_OTHER && !deniable(c)) {
            return false;
        }
        model_toggle_silent(s, now, out);
        return true;
    default:
        return false;
    }
}

#define CHORD_MS 10000 /* both held this long: the factory-reset confirmation (parent 2.2, 9.7) */

/* Everywhere while the firmware runs, the pairing screens included (parent 2.2; open-items O-28): there it's
 * the way out for a pager whose key can't be read. An offer on screen, confirmed or not, is cancelled. */
static void tick_chord(app_state_t *s, app_ms_t known, app_out_t *out)
{
    if (s->router.chord && s->router.btn[APP_BTN_BOOT].down && s->router.btn[APP_BTN_PWR].down &&
        known >= s->router.chord_at + CHORD_MS) {
        s->router.chord = false;
        pairing_reset_asked(s, out);
        menu_open_reset(s);
    }
}

/* Fires btn's thresholds that the press held to, as the buttons are known up to time known; acts at now. */
static void tick_btn(app_state_t *s, app_btn_t btn, app_ms_t known, app_ms_t now, app_out_t *out)
{
    app_press_t *b = &s->router.btn[btn];
    size_t n;
    const hold_t *h = holds(btn, &n);
    while (b->down && !b->consumed && b->reached < n && known >= b->t_down + h[b->reached].ms) {
        gesture_t g = h[b->reached].g;
        b->reached++;
        if (route(s, g, now, out)) {
            b->consumed = true;
        }
    }
}

/* The thresholds passed by time known: an edge's stamp, or a deadline's time less EDGE_GRACE_MS. */
static void tick(app_state_t *s, app_ms_t known, app_ms_t now, app_out_t *out)
{
    tick_btn(s, APP_BTN_BOOT, known, now, out);
    tick_btn(s, APP_BTN_PWR, known, now, out);
    tick_chord(s, known, out);
}

static void on_edge(app_state_t *s, app_btn_t btn, bool down, app_ms_t t, app_out_t *out)
{
    app_press_t *b = &s->router.btn[btn];
    app_press_t *other = &s->router.btn[btn == APP_BTN_BOOT ? APP_BTN_PWR : APP_BTN_BOOT];
    tick(s, t, t, out); /* the edge's own stamp: what was held to it was held, whenever it's handed over */
    if (s->card.open) {
        s->card.idle_from = t; /* a press, even one that does nothing, holds a card open (parent 7.6) */
    }
    if (down) {
        if (b->down) {
            return; /* a repeated edge */
        }
        b->down = true;
        b->consumed = false;
        b->reached = 0;
        b->t_down = t;
        if (!s->disp.awake) { /* the press that wakes the screen does nothing else (parent 10.7) */
            alerts_wake(s, t, out);
            s->router.locked = true;
        } else {
            alerts_activity(s, t);
        }
        b->consumed = s->router.locked || (s->card.open && t < s->card.shown_at + CARD_GUARD_MS);
        if (other->down && !s->router.locked) {
            size_t n;
            const hold_t *h = holds(btn == APP_BTN_BOOT ? APP_BTN_PWR : APP_BTN_BOOT, &n);
            if (!s->router.chord && t - other->t_down < h[0].ms) {
                /* Down together, before any hold threshold of the first: the chord, and no single-button
                 * gesture of either press (parent 9.7). */
                s->router.chord = true;
                s->router.chord_at = t;
                other->consumed = true;
            }
            b->consumed = true; /* otherwise the first press's gesture stands, and this one is consumed */
        }
        return;
    }
    if (!b->down) {
        return;
    }
    b->down = false;
    s->router.chord = false;
    if (!s->router.btn[APP_BTN_BOOT].down && !s->router.btn[APP_BTN_PWR].down) {
        s->router.locked = false;
    }
    if (!b->consumed && t - b->t_down < SHORT_MS) {
        route(s, btn == APP_BTN_BOOT ? G_BOOT_SHORT : G_PWR_SHORT, t, out);
    }
}

app_ms_t router_deadline(const app_state_t *s, app_ms_t now)
{
    app_ms_t d = APP_NEVER;
    for (int btn = 0; btn < 2; btn++) {
        const app_press_t *b = &s->router.btn[btn];
        size_t n;
        const hold_t *h = holds((app_btn_t)btn, &n);
        if (b->down && !b->consumed && b->reached < n) {
            app_ms_t t = b->t_down + h[b->reached].ms + EDGE_GRACE_MS;
            d = t < d ? t : d;
        }
    }
    if (s->router.chord) {
        app_ms_t t = s->router.chord_at + CHORD_MS + EDGE_GRACE_MS;
        d = t < d ? t : d;
    }
    if (filling(s)) {
        app_ms_t next = now + FILL_STEP_MS - (now - s->router.btn[APP_BTN_BOOT].t_down) % FILL_STEP_MS;
        d = next < d ? next : d;
    }
    return d;
}

void router_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out)
{
    if (ev->kind == APP_EV_BUTTON) {
        on_edge(s, ev->button.btn, ev->button.down, ev->button.t, out);
    } else if (ev->kind == APP_EV_DEADLINE) {
        tick(s, now >= EDGE_GRACE_MS ? now - EDGE_GRACE_MS : 0, now, out); /* an edge may still be on its way */
    }
}
