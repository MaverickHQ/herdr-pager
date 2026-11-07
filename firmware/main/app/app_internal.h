/* The app core's areas: its implementation, behind app_handle (design D2-1).
 * app.c dispatches each event to the area that owns it; an area adds actions
 * with app_out_push() and asks for its next call with app_out_deadline().
 * Tests never call these: they go through app_handle.
 *
 * Timers are derived from the state: app_handle runs each area's *_tick()
 * first, for every event, and ends by restating the earliest *_deadline(), so
 * no event can lose a timer that another area set. */
#pragma once

#include "app.h"

/* ---- app.c: the actions out ---- */

/** Appends an action of kind k and returns it, zeroed but for its kind; NULL when out is full. */
app_action_t *app_out_push(app_out_t *out, app_action_kind_t k);

/** Moves out's deadline earlier to t, if t is earlier. */
void app_out_deadline(app_out_t *out, app_ms_t t);

/* ---- codec.c: bridge lines in (APP_EV_LINE), and the lines the core sends ---- */

void codec_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out);

/** Sends one line to the bridge, built like printf. */
void codec_send(app_out_t *out, const char *fmt, ...) __attribute__((format(printf, 2, 3)));

/** The next request id. */
uint32_t codec_next_id(app_state_t *s);

/* ---- model.c: agents, link, Wi-Fi, power and settings; the render model ---- */

void model_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out);

/** Runs the model's timers due by now: the card's timeout, and a request the bridge hasn't answered. */
void model_tick(app_state_t *s, app_ms_t now, app_out_t *out);

/** A snapshot the codec has read. */
void model_snapshot(app_state_t *s, const app_agent_t *agents, uint8_t n, uint32_t more, bool herdr_offline,
                    int64_t unix_now, app_ms_t now, app_out_t *out);

/** A card the codec has read. */
void model_card(app_state_t *s, const app_card_t *card, app_ms_t now, app_out_t *out);

/** The bridge's result for request id: err is NULL when ok, else its code. */
void model_result(app_state_t *s, uint32_t id, const char *err, app_ms_t now, app_out_t *out);

/** Asks the bridge for the selected agent's card (PWR on the list). */
void model_request_card(app_state_t *s, app_ms_t now, app_out_t *out);

/** Sends verb ("approve", "deny") for the open card, with its pane, seq and hash. */
void model_verdict(app_state_t *s, const char *verb, app_ms_t now, app_out_t *out);

/** Moves the list's selection down one row, wrapping at the end. */
void model_select_next(app_state_t *s);

/** Turns silent mode over, stores it and says so. */
void model_toggle_silent(app_state_t *s, app_ms_t now, app_out_t *out);

/** Shows text over the screen for 2 s. */
void model_toast(app_state_t *s, const char *text, app_ms_t now);

/** Rebuilds the render model from the state, and asks for a render when it changed. app_handle calls it last. */
void model_render(app_state_t *s, app_ms_t now, app_out_t *out);

/** Stores the formatted value under key (APP_ACT_STORE). */
void app_store(app_out_t *out, const char *key, const char *fmt, ...) __attribute__((format(printf, 3, 4)));

/** Copies len bytes of src into dst (cap bytes, NUL included), each byte outside 0x20-0x7E as '?'. */
void app_filter_text(char *dst, size_t cap, const char *src, size_t len);

/* Marks: (pane, seq) sets kept as rings (app_marks_t). */
bool marks_has(const app_marks_t *m, const char *pane, uint64_t seq);
void marks_add(app_marks_t *m, const char *pane, uint64_t seq);

/** True when card c takes a verdict: a permission card whose `can` offers approve and deny (parent 8.6). */
static inline bool app_card_verdicts(const app_card_t *c)
{
    return c->kind == APP_CARD_PERMISSION && (c->can & APP_CAN_APPROVE) && (c->can & APP_CAN_DENY);
}

/** True when card c takes a deny alone: a permission card the bridge couldn't read in full, whose `can` offers
 * deny without approve (the owner's decision, 2026-11-01: fail closed). */
static inline bool app_card_deny_only(const app_card_t *c)
{
    return c->kind == APP_CARD_PERMISSION && !(c->can & APP_CAN_APPROVE) && (c->can & APP_CAN_DENY);
}

/* ---- alerts.c: pages, reminders, auto-clear, the backlight's limits ---- */

/** Runs every alert timer due by now. */
void alerts_tick(app_state_t *s, app_ms_t now, app_out_t *out);

/** When the next alert timer is due, or APP_NEVER. */
app_ms_t alerts_deadline(const app_state_t *s);

/** A new snapshot is in s->model: watch its blocked agents, and drop the rest. */
void alerts_snapshot(app_state_t *s, app_ms_t now);
/* The done tone for the snapshot just taken; first: it's the first since boot (alerts.c). */
void alerts_done(app_state_t *s, bool first, app_ms_t now, app_out_t *out);

/** The link has gone: watches that haven't paged start over from the next fresh snapshot. */
void alerts_link_lost(app_state_t *s);

/** A press at t: the backlight stays on at least 30 s more. */
void alerts_activity(app_state_t *s, app_ms_t t);

/** Turns the backlight on at t, after it went off. */
void alerts_wake(app_state_t *s, app_ms_t t, app_out_t *out);

/** (pane, seq)'s card was shown: its page is acknowledged, and its reminders stop. */
void alerts_ack(app_state_t *s, const char *pane, uint64_t seq);

/** The ms since pane's standing page fired, or -1 (show's pg). */
int64_t alerts_since_page(const app_state_t *s, const char *pane, app_ms_t now);

/** True when pane's page has fired and still stands (its row is highlighted). */
bool alerts_page_active(const app_state_t *s, const char *pane);

/* ---- router.c: button edges into gestures, and gestures into actions by context; touch never acts ---- */

void router_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out);

/** When the next hold threshold passes or the fill bar steps, or APP_NEVER. */
app_ms_t router_deadline(const app_state_t *s, app_ms_t now);

/** A card has just appeared: a press already down was meant for what was there before. */
void router_card_shown(app_state_t *s);

/** An approve hold's progress on a permission card, 0-1000; 0 when none. */
uint16_t router_approve_fill(const app_state_t *s, app_ms_t now);

/* ---- menu.c: menus and confirms ---- */

enum {
    APP_CONFIRM_NONE,
    APP_CONFIRM_INTERRUPT, /**< PWR yes, BOOT no */
    APP_CONFIRM_PAIR,      /**< BOOT yes, PWR no */
    APP_CONFIRM_RESET,     /**< factory reset: BOOT reset, PWR cancel */
};
void menu_open_reset(app_state_t *s);
void menu_open_agent(app_state_t *s, const char *pane);
void menu_open_global(app_state_t *s);
void menu_close(app_state_t *s);
void menu_next(app_state_t *s);
void menu_select(app_state_t *s, app_ms_t now, app_out_t *out);
void menu_confirm(app_state_t *s, bool yes, app_ms_t now, app_out_t *out);
/** Draws the open menu or confirm, if any, over r. */
void menu_render(const app_state_t *s, app_render_t *r);

/* ---- link.c: when to dial the bridge: on Wi-Fi, then with backoff after each drop; hello on connecting ---- */

void link_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out);
void link_tick(app_state_t *s, app_ms_t now, app_out_t *out);
app_ms_t link_deadline(const app_state_t *s);

/* ---- pairing.c: pairing mode and the @P lines (parent design §Provisioning) ---- */

void pairing_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out);

/** Enters pairing mode, keeping the key pair (first boot, or Pair again). */
void pairing_enter(app_state_t *s, app_ms_t now, app_out_t *out);

/** A short press of btn on the boot or pairing screen, where the router leaves the buttons to pairing.c. */
void pairing_press(app_state_t *s, app_btn_t btn, app_ms_t now, app_out_t *out);

/** When pairing.c next needs a call (APP_EV_DEADLINE), or APP_NEVER. */
app_ms_t pairing_deadline(const app_state_t *s);

/** In pairing mode, its screen (PAIR VIA USB, or a prov_offer to confirm or confirmed) into r, and true; else
 * false. */
bool pairing_screen(const app_state_t *s, app_render_t *r);

/** The factory-reset chord was held: an offer on screen, confirmed or not, is answered cancelled. */
void pairing_reset_asked(app_state_t *s, app_out_t *out);

/* ---- keys.c ---- */

#define APP_FP_MAX 50 /**< a fingerprint: "SHA256:" and 43 base64 characters */

/** key's full fingerprint, "SHA256:<base64>", from an OpenSSH line "<type> <base64>[ comment]". */
bool app_fingerprint(const char *key, char *out, size_t cap);
