/* The model: agents, link, Wi-Fi, power and settings, and the render model
 * built from them (parent 5; parent design §Firmware components, Model). */
#include <stdarg.h>
#include <stdio.h>
#include <string.h>

#include "app_internal.h"

bool marks_has(const app_marks_t *m, const char *pane, uint64_t seq)
{
    for (uint8_t i = 0; i < m->n; i++) {
        if (m->m[i].seq == seq && strcmp(m->m[i].pane, pane) == 0) {
            return true;
        }
    }
    return false;
}

void marks_add(app_marks_t *m, const char *pane, uint64_t seq)
{
    if (marks_has(m, pane, seq)) {
        return;
    }
    app_mark_t *k = &m->m[m->next];
    snprintf(k->pane, sizeof(k->pane), "%s", pane);
    k->seq = seq;
    m->next = (uint8_t)((m->next + 1) % APP_MARKS);
    if (m->n < APP_MARKS) {
        m->n++;
    }
}

/* The device's own filter (parent design §Sanitizer): the fonts are ASCII, and a NUL must not end a field. */
void app_filter_text(char *dst, size_t cap, const char *src, size_t len)
{
    if (len >= cap) {
        len = cap - 1;
    }
    for (size_t i = 0; i < len; i++) {
        unsigned char c = (unsigned char)src[i];
        dst[i] = c >= 0x20 && c <= 0x7e ? (char)c : '?';
    }
    dst[len] = '\0';
}

static bool seen_done(const app_state_t *s, const app_agent_t *a)
{
    return a->st == APP_ST_DONE && marks_has(&s->model.seen, a->pane, a->dseq);
}

/* Sort rank (parent 5.1): blocked, done unseen, unknown, working, then idle with seen done. */
static int rank(const app_state_t *s, const app_agent_t *a)
{
    switch (a->st) {
    case APP_ST_BLOCKED:
        return 0;
    case APP_ST_DONE:
        return seen_done(s, a) ? 4 : 1;
    case APP_ST_UNKNOWN:
        return 2;
    case APP_ST_WORKING:
        return 3;
    default:
        return 4;
    }
}

/* The agents' indices in list order; ties go to the oldest state change, then to the bridge's order. */
static void sorted(const app_state_t *s, uint8_t *order)
{
    uint8_t n = s->model.n;
    for (uint8_t i = 0; i < n; i++) {
        order[i] = i;
    }
    for (uint8_t i = 1; i < n; i++) { /* insertion sort: stable, and n <= 16 */
        uint8_t k = order[i];
        const app_agent_t *a = &s->model.agents[k];
        int j = i - 1;
        while (j >= 0) {
            const app_agent_t *b = &s->model.agents[order[j]];
            int ra = rank(s, a), rb = rank(s, b);
            if (rb < ra || (rb == ra && b->since <= a->since)) {
                break;
            }
            order[j + 1] = order[j];
            j--;
        }
        order[j + 1] = k;
    }
}

static char glyph(const app_agent_t *a)
{
    switch (a->st) {
    case APP_ST_BLOCKED:
        return '!';
    case APP_ST_DONE:
        return '*';
    case APP_ST_UNKNOWN:
        return '?';
    case APP_ST_WORKING:
        return '>';
    default:
        return '.';
    }
}

/* The wall clock now, in Unix seconds; only when s->clock.known. */
static int64_t wall_s(const app_state_t *s, app_ms_t now)
{
    return s->clock.unix_s + (int64_t)((now - s->clock.at_ms) / 1000);
}

static void set_clock(app_state_t *s, int64_t unix_s, app_ms_t now)
{
    s->clock.known = true;
    s->clock.unix_s = unix_s;
    s->clock.at_ms = now;
}

void model_snapshot(app_state_t *s, const app_agent_t *agents, uint8_t n, uint32_t more, bool herdr_offline,
                    int64_t unix_now, app_ms_t now, app_out_t *out)
{
    int64_t drift = s->clock.known ? wall_s(s, now) - unix_now : 0;
    if (!s->clock.known || drift > 2 || drift < -2) { /* parent 4.6: more than 2 s off, set it and the RTC */
        set_clock(s, unix_now, now);
        app_action_t *a = app_out_push(out, APP_ACT_RTC);
        if (a) {
            a->unix_s = unix_now;
        }
    }
    s->model.herdr_offline = herdr_offline;
    if (herdr_offline) {
        return; /* the list freezes as it was, marked HERDR OFFLINE; nothing pages from it (parent 4.4) */
    }
    bool first = !s->model.have;
    memcpy(s->model.agents, agents, n * sizeof(agents[0]));
    s->model.n = n;
    s->model.more = more;
    s->model.have = true;
    s->model.snap_at = now;
    alerts_snapshot(s, now);
    alerts_done(s, first, now, out);
}

/* The selected agent, or NULL when the list is empty. */
static const app_agent_t *selected_agent(const app_state_t *s)
{
    for (uint8_t i = 0; i < s->model.n; i++) {
        if (strcmp(s->model.agents[i].pane, s->sel.pane) == 0) {
            return &s->model.agents[i];
        }
    }
    return NULL;
}

static void request_card(app_state_t *s, const char *pane, app_ms_t now, app_out_t *out)
{
    if (s->card.want_id == 0) { /* asking again keeps the first's deadline (acceptance day 1, R5) */
        s->card.want_at = now;
    }
    s->card.want_id = codec_next_id(s);
    codec_send(out, "{\"t\":\"show\",\"id\":%u,\"pane\":\"%s\",\"pg\":%lld}", (unsigned)s->card.want_id, pane,
               (long long)alerts_since_page(s, pane, now));
}

void model_request_card(app_state_t *s, app_ms_t now, app_out_t *out)
{
    const app_agent_t *a = selected_agent(s);
    if (!a) {
        return;
    }
    if (s->net.link != APP_LINK_CONNECTED) { /* the card can't come: say so now, as a verdict does (O-77) */
        model_toast(s, "X NO LINK", now);
        return;
    }
    request_card(s, a->pane, now, out);
}

/* Toasts err, a result's error: "X" and the reason, in capitals. */
static void toast_error(app_state_t *s, const char *err, app_ms_t now)
{
    char text[APP_BODY_LINE_MAX + 1];
    int n = snprintf(text, sizeof(text), "X %s", strcmp(err, "unknown_pane") == 0 ? "GONE" : err);
    for (int i = 0; i < n && text[i]; i++) {
        text[i] = text[i] == '_' ? ' ' : (char)(text[i] >= 'a' && text[i] <= 'z' ? text[i] - 32 : text[i]);
    }
    model_toast(s, text, now);
}

void model_result(app_state_t *s, uint32_t id, const char *err, app_ms_t now, app_out_t *out)
{
    if (id != 0 && id == s->card.want_id) { /* a show the bridge couldn't answer with a card */
        s->card.want_id = 0;
        if (err) {
            toast_error(s, err, now);
        }
        return;
    }
    if (id != 0 && id == s->menu.jump_id) { /* JUMP says what happened (the owner's decision, 2026-11-01, O-83) */
        s->menu.jump_id = 0;
        if (err) {
            toast_error(s, err, now);
        } else {
            model_toast(s, "JUMPED", now);
        }
        return;
    }
    if (id == 0 || id != s->verdict.id) {
        return;
    }
    s->verdict.id = 0;
    bool from_card = !s->verdict.interrupt;
    if (from_card) {
        s->card.open = false;
    }
    if (!err) {
        model_toast(s, "OK", now);
    } else if (strcmp(err, "stale") == 0) {
        /* parent 8.4: say so, and ask for the card as it is now; the verdict is never resent */
        model_toast(s, "SCREEN CHANGED", now);
        if (from_card) {
            request_card(s, s->card.c.pane, now, out);
        }
    } else {
        toast_error(s, err, now);
    }
}

void model_verdict(app_state_t *s, const char *verb, app_ms_t now, app_out_t *out)
{
    const app_card_t *c = &s->card.c;
    if (s->net.link != APP_LINK_CONNECTED) { /* parent 8.7: never sent later, so never queued */
        model_toast(s, "X NO LINK", now);
        return;
    }
    s->verdict.id = codec_next_id(s);
    s->verdict.deny = strcmp(verb, "deny") == 0;
    s->verdict.interrupt = false;
    s->verdict.sent_at = now;
    codec_send(out, "{\"t\":\"%s\",\"id\":%u,\"pane\":\"%s\",\"seq\":%llu,\"hash\":\"%s\"}", verb,
               (unsigned)s->verdict.id, c->pane, (unsigned long long)c->seq, c->hash);
}

void model_card(app_state_t *s, const app_card_t *card, app_ms_t now, app_out_t *out)
{
    if (card->id != s->card.want_id || card->id == 0) {
        return; /* it answers no request of ours, or one we've given up */
    }
    s->card.want_id = 0;
    s->card.open = true;
    s->card.c = *card;
    s->card.shown_at = now > s->toast.until ? now : s->toast.until; /* under a toast, it's shown when that ends */
    s->card.idle_from = now;
    router_card_shown(s);
    if (card->danger && !s->set.silent) {
        app_action_t *a = app_out_push(out, APP_ACT_TONE);
        if (a) {
            a->tone = APP_TONE_DANGER;
            a->level = s->set.volume;
        }
    }
    if (card->st == APP_ST_BLOCKED) {
        alerts_ack(s, card->pane, card->seq);
    }
    for (uint8_t i = 0; i < s->model.n; i++) {
        const app_agent_t *a = &s->model.agents[i];
        if (a->st == APP_ST_DONE && card->st == APP_ST_DONE && strcmp(a->pane, card->pane) == 0) {
            marks_add(&s->model.seen, a->pane, a->dseq);
        }
    }
}

/* An age in its largest whole unit (parent 5.3: "3m", "2h"), and when that text next changes (Unix s). */
static int64_t format_age(int64_t age, char *out, size_t cap)
{
    static const struct {
        int64_t unit;
        char c;
    } units[] = {{86400, 'd'}, {3600, 'h'}, {60, 'm'}};
    if (age < 0) {
        age = 0;
    }
    for (int i = 0; i < 3; i++) {
        if (age >= units[i].unit || units[i].c == 'm') {
            int64_t n = age / units[i].unit;
            if (n > 999) {
                n = 999;
            }
            snprintf(out, cap, "%d%c", (int)n, units[i].c);
            return (age / units[i].unit + 1) * units[i].unit - age;
        }
    }
    return 60;
}

#define VISIBLE_ROWS 4 /* 144 px of body, 36 px rows */

/* Puts the selection back on its agent after a re-sort, or keeps it in the list when the agent went, and
 * scrolls the view to keep it visible. */
static void fix_selection(app_state_t *s, const uint8_t *order)
{
    uint8_t n = s->model.n;
    if (n == 0) {
        s->sel.selected = s->sel.first_row = 0;
        s->sel.pane[0] = '\0';
        return;
    }
    for (uint8_t i = 0; i < n; i++) {
        if (strcmp(s->model.agents[order[i]].pane, s->sel.pane) == 0) {
            s->sel.selected = i;
        }
    }
    if (s->sel.selected >= n) {
        s->sel.selected = n - 1;
    }
    snprintf(s->sel.pane, sizeof(s->sel.pane), "%s", s->model.agents[order[s->sel.selected]].pane);
    if (s->sel.selected < s->sel.first_row) {
        s->sel.first_row = s->sel.selected;
    } else if (s->sel.selected >= s->sel.first_row + VISIBLE_ROWS) {
        s->sel.first_row = s->sel.selected - VISIBLE_ROWS + 1;
    }
}

void model_select_next(app_state_t *s)
{
    uint8_t order[APP_MAX_AGENTS];
    if (s->model.n == 0) {
        return;
    }
    sorted(s, order);
    fix_selection(s, order);
    s->sel.selected = (uint8_t)((s->sel.selected + 1) % s->model.n);
    snprintf(s->sel.pane, sizeof(s->sel.pane), "%s", s->model.agents[order[s->sel.selected]].pane);
    fix_selection(s, order);
}

static void build_rows(app_state_t *s, app_ms_t now, app_render_t *r)
{
    uint8_t order[APP_MAX_AGENTS];
    sorted(s, order);
    fix_selection(s, order);
    r->selected = s->sel.selected;
    r->first_row = s->sel.first_row;
    r->n_rows = s->model.n;
    r->more = s->model.more;
    for (uint8_t i = 0; i < s->model.n; i++) {
        const app_agent_t *a = &s->model.agents[order[i]];
        app_row_t *row = &r->rows[i];
        row->glyph = glyph(a);
        snprintf(row->name, sizeof(row->name), "%s", a->name);
        snprintf(row->kind, sizeof(row->kind), "%s", a->at[0] ? a->at : a->kind); /* where it works, else its kind */
        row->dim = seen_done(s, a);
        row->paged = a->st == APP_ST_BLOCKED && alerts_page_active(s, a->pane);
        if (s->clock.known) {
            format_age(wall_s(s, now) - a->since, row->age, sizeof(row->age));
        }
    }
}

/* When the clock or an age next changes on screen. */
static app_ms_t next_change(const app_state_t *s, app_ms_t now)
{
    app_ms_t last_seen = APP_NEVER;
    if (s->net.link != APP_LINK_CONNECTED && s->model.have) {
        char age[6];
        app_ms_t ms = now - s->model.snap_at;
        last_seen = now + (app_ms_t)format_age((int64_t)(ms / 1000), age, sizeof(age)) * 1000 - ms % 1000;
    }
    if (!s->clock.known) {
        return last_seen;
    }
    int64_t w = wall_s(s, now);
    int64_t in_s = 60 - w % 60;
    for (uint8_t i = 0; i < s->model.n; i++) {
        char age[6];
        int64_t k = format_age(w - s->model.agents[i].since, age, sizeof(age));
        if (k < in_s) {
            in_s = k;
        }
    }
    /* The wall second w began at at_ms + (w - unix_s) s. */
    app_ms_t t = s->clock.at_ms + (app_ms_t)(w + in_s - s->clock.unix_s) * 1000;
    return t < last_seen ? t : last_seen;
}

/* The status band's left part (parent 5.2, 16.4; R1.1). The time is UTC: the protocol carries no zone. */
static void build_band(const app_state_t *s, app_ms_t now, app_band_t *b)
{
    for (uint8_t i = 0; i < s->model.n; i++) {
        const app_agent_t *a = &s->model.agents[i];
        b->blocked += a->st == APP_ST_BLOCKED;
        b->done += a->st == APP_ST_DONE && !seen_done(s, a);
        b->working += a->st == APP_ST_WORKING;
    }
    /* Nothing needs you, and the list is live (the owner's decision, 2026-11-01, open-items O-85): not a list
     * kept through a drop (LAST SEEN), nor one frozen while herdr is down. */
    b->all_clear = s->model.n > 0 && !b->blocked && !b->done && s->model.have && !s->model.herdr_offline &&
                   s->net.link == APP_LINK_CONNECTED;
    if (s->clock.known) {
        int64_t day_s = wall_s(s, now) % 86400;
        if (day_s < 0) {
            day_s += 86400; /* a time before 1970 is still a time of day */
        }
        /* The % 24 changes nothing; it shows the compiler the hour has two digits. */
        unsigned h = (unsigned)day_s / 3600 % 24, m = (unsigned)day_s / 60 % 60;
        snprintf(b->clock, sizeof(b->clock), "%02u:%02u", h, m);
    }
    /* A refusal outlives the session it ended, as its screen does (parent 3.3, 3.5): the band keeps its
     * glyph through the drop and the redial, until the link connects or the pager is paired again. */
    b->link = s->link_ctl.host_key_changed ? APP_LINK_HOST_KEY_CHANGED
              : s->link_ctl.refused        ? APP_LINK_AUTH_REFUSED
                                           : s->net.link;
    b->wifi = s->net.wifi;
    snprintf(b->ssid, sizeof(b->ssid), "%s", s->net.ssid);
    snprintf(b->failed, sizeof(b->failed), "%s", s->net.wifi ? "" : s->net.failed);
    b->herdr_offline = s->model.herdr_offline;
    b->on_battery = s->net.power == APP_POWER_BATTERY;
}

/* With no agent to show, the list's body says why (parent 16.4; the owner's decision, 2026-10-27, open-items
 * O-77): a pager that had restarted on a phone's hotspot without the Mac showed only the band's "~" and network.
 * On a live link it says NO AGENTS only when herdr's list is empty, not before the first snapshot or while herdr
 * is down (O-84). A dial counts as failed once one on this network ends without connecting; until then it's
 * CONNECTING. */
static void build_empty(const app_state_t *s, app_render_t *r)
{
    if (s->net.link == APP_LINK_CONNECTED && s->model.herdr_offline) {
        snprintf(r->lines[0], sizeof(r->lines[0]), "HERDR OFFLINE");
        snprintf(r->lines[1], sizeof(r->lines[1]), "Is herdr running on the Mac?");
    } else if (s->net.link == APP_LINK_CONNECTED && !s->model.have) {
        snprintf(r->lines[0], sizeof(r->lines[0]), "WAITING FOR HERDR");
    } else if (s->net.link == APP_LINK_CONNECTED) {
        snprintf(r->lines[0], sizeof(r->lines[0]), "NO AGENTS");
    } else if (!s->net.wifi && s->net.failed[0]) { /* a saved network refused it three times (O-49) */
        snprintf(r->lines[0], sizeof(r->lines[0]), "WRONG WIFI PASSWORD");
        snprintf(r->lines[1], sizeof(r->lines[1]), "%.*s refused it: pair again in SETTINGS", APP_BODY_LINE_MAX - 35,
                 s->net.failed);
    } else if (!s->net.wifi) {
        snprintf(r->lines[0], sizeof(r->lines[0]), "NO WIFI");
        snprintf(r->lines[1], sizeof(r->lines[1]), "Looking for a saved network");
    } else if (!s->net.dial_failed) {
        snprintf(r->lines[0], sizeof(r->lines[0]), "CONNECTING");
        snprintf(r->lines[1], sizeof(r->lines[1]), "to the bridge at %.*s", APP_BODY_LINE_MAX - 17, s->boot.host);
    } else {
        snprintf(r->lines[0], sizeof(r->lines[0]), "NO BRIDGE ON THIS NETWORK");
        snprintf(r->lines[1], sizeof(r->lines[1]), "Is the Mac on %s too?", s->net.ssid);
    }
}

/* A status card with no text of its own: the bridge found no prompt on the agent's screen (O-82, the owner's
 * report of a blank screen, 2026-11-01). Its first line says nothing needs you, or, for a blocked agent whose prompt
 * couldn't be read, to answer at the keyboard (O-56); then what the agent is doing and for how long, then its kind
 * and where it works. */
static void build_status(const app_state_t *s, const app_agent_t *a, app_ms_t now, app_render_t *r)
{
    const app_card_t *c = &s->card.c;
    snprintf(r->lines[0], sizeof(r->lines[0]), "%s",
             c->st == APP_ST_BLOCKED ? "WAITING - ANSWER AT KEYBOARD" : "NOTHING NEEDS YOU");
    if (!a) {
        return;
    }
    char age[6] = "";
    if (s->clock.known) {
        format_age(wall_s(s, now) - a->since, age, sizeof(age));
    }
    const char *state = c->st == APP_ST_BLOCKED   ? "Blocked"
                        : c->st == APP_ST_DONE    ? "Done"
                        : c->st == APP_ST_WORKING ? "Working"
                        : c->st == APP_ST_UNKNOWN ? "State unknown"
                                                  : "Idle";
    if (!age[0]) {
        snprintf(r->title, sizeof(r->title), "%s", state);
    } else if (c->st == APP_ST_DONE) {
        snprintf(r->title, sizeof(r->title), "Done %s ago", age);
    } else {
        snprintf(r->title, sizeof(r->title), "%s for %s", state, age);
    }
    if (a->at[0]) {
        snprintf(r->lines[1], sizeof(r->lines[1]), "%s in %s", a->kind, a->at);
    } else {
        snprintf(r->lines[1], sizeof(r->lines[1]), "%s", a->kind);
    }
}

/* The open card's body and hints (parent design §UI), and the session it's from in the band. */
static void build_card(const app_state_t *s, app_ms_t now, app_render_t *r)
{
    const app_card_t *c = &s->card.c;
    const app_agent_t *agent = NULL;
    for (uint8_t i = 0; i < s->model.n; i++) { /* no agent with its pane: the band keeps the counts */
        if (strcmp(s->model.agents[i].pane, c->pane) == 0) {
            agent = &s->model.agents[i];
            snprintf(r->band.agent, sizeof(r->band.agent), "%s", agent->name);
            break;
        }
    }
    r->band.all_clear = false; /* the band names the card's agent, or keeps the counts */
    if (c->kind == APP_CARD_STATUS && c->summary[0] == '\0' && c->n_lines == 0) {
        build_status(s, agent, now, r);
        snprintf(r->band.pwr.text, sizeof(r->band.pwr.text), "BACK");
        snprintf(r->band.boot.text, sizeof(r->band.boot.text), "HOLD: MENU");
        return;
    }
    if (c->danger) {
        snprintf(r->banner, sizeof(r->banner), "DANGER: %s", c->rl);
    }
    snprintf(r->title, sizeof(r->title), "%s", c->summary);
    uint8_t row = 0;
    if (c->kind == APP_CARD_QUESTION) { /* parent 7.7: answered at the keyboard, never here */
        snprintf(r->lines[row++], sizeof(r->lines[0]), "QUESTION - ANSWER AT KEYBOARD");
    }
    for (uint8_t i = 0; i < c->n_lines && row < APP_BODY_LINES; i++) {
        snprintf(r->lines[row++], sizeof(r->lines[0]), "%s", c->lines[i]);
    }
    if (app_card_verdicts(c)) {
        bool sent = s->verdict.id != 0;
        snprintf(r->band.pwr.text, sizeof(r->band.pwr.text), "%s", sent ? (s->verdict.deny ? "SENT" : "") : "DENY");
        snprintf(r->band.boot.text, sizeof(r->band.boot.text), "%s",
                 sent                 ? (s->verdict.deny ? "" : "SENT")
                 : c->danger          ? "HOLD 2S:\nAPPROVE"
                                      : "HOLD:\nAPPROVE");
        r->band.boot.fill_permille = router_approve_fill(s, now);
    } else if (app_card_deny_only(c)) {
        /* Failed closed: deny here, approve at the keyboard (the owner's decision, 2026-11-01). `APPROVE AT` is
         * 80 px in the hint font, over the cell's 72; `APPROVE:` is 64 and `KEYBOARD` 71. */
        bool sent = s->verdict.id != 0;
        snprintf(r->band.pwr.text, sizeof(r->band.pwr.text), "%s", sent ? "SENT" : "DENY");
        snprintf(r->band.boot.text, sizeof(r->band.boot.text), "%s", sent ? "" : "APPROVE:\nKEYBOARD");
    } else { /* a question or status card: PWR or BOOT goes back, a BOOT hold opens the agent's menu */
        snprintf(r->band.pwr.text, sizeof(r->band.pwr.text), "BACK");
        snprintf(r->band.boot.text, sizeof(r->band.boot.text), "HOLD: MENU");
    }
}

void app_store(app_out_t *out, const char *key, const char *fmt, ...)
{
    app_action_t *a = app_out_push(out, APP_ACT_STORE);
    if (!a) {
        return;
    }
    snprintf(a->key, sizeof(a->key), "%s", key);
    va_list ap;
    va_start(ap, fmt);
    int n = vsnprintf(a->text, sizeof(a->text), fmt, ap);
    va_end(ap);
    a->len = n < 0 ? 0 : (size_t)n < sizeof(a->text) ? (size_t)n : sizeof(a->text) - 1;
}

void model_toggle_silent(app_state_t *s, app_ms_t now, app_out_t *out)
{
    s->set.silent = !s->set.silent;
    app_store(out, APP_KEY_SILENT, "%d", s->set.silent);
    model_toast(s, s->set.silent ? "SILENT ON" : "SILENT OFF", now);
}

#define TOAST_MS 2000
#define CARD_TIMEOUT_MS 60000 /* parent 7.6: no press for this long, and an open card goes back to the list */
/* A show, verdict or interrupt the bridge hasn't answered in this long: the link is dead, though no byte has
 * failed yet, as on a network the Mac has left (acceptance day 1, R3). The bridge's slowest answer is a
 * verdict's three herdr calls of up to 2 s each. */
#define ANSWER_MS 8000

void model_tick(app_state_t *s, app_ms_t now, app_out_t *out)
{
    if (s->card.open && now >= s->card.idle_from + CARD_TIMEOUT_MS) {
        s->card.open = false;
    }
    bool show_late = s->card.want_id != 0 && now >= s->card.want_at + ANSWER_MS;
    bool verdict_late = s->verdict.id != 0 && now >= s->verdict.sent_at + ANSWER_MS;
    if (show_late) {
        s->card.want_id = 0; /* given up: a card answering it now is dropped */
    }
    if (verdict_late) { /* discarded, as on a lost link (parent 8.7): never resent */
        s->verdict.id = 0;
        if (!s->verdict.interrupt) {
            s->card.open = false;
        }
    }
    if ((show_late || verdict_late) && s->net.link == APP_LINK_CONNECTED) {
        model_toast(s, verdict_late ? "X LINK LOST" : "X NO LINK", now);
        app_action_t *a = app_out_push(out, APP_ACT_LINK);
        if (a) {
            a->link_op = APP_LINK_OP_DISCONNECT; /* the link reports DOWN, and link.c redials after its backoff */
        }
    }
}

void model_toast(app_state_t *s, const char *text, app_ms_t now)
{
    snprintf(s->toast.text, sizeof(s->toast.text), "%s", text);
    s->toast.until = now + TOAST_MS;
    if (s->card.open) { /* the card is out of sight: shown again, guard and all, when the toast ends (parent 8.1) */
        s->card.shown_at = s->toast.until;
        router_card_shown(s);
    }
}

/* Makes r the screen, and asks for a render when it changed. */
static void show(app_state_t *s, const app_render_t *r, app_ms_t now, app_out_t *out)
{
    if (memcmp(r, &s->render, sizeof(*r)) != 0) {
        s->render = *r;
        s->disp.redraw = true;
    }
    if (s->disp.redraw && s->disp.awake) { /* asleep, rendering stops (parent 10.3) */
        if (app_out_push(out, APP_ACT_RENDER) != NULL) {
            s->disp.redraw = false;
        } else {
            app_out_deadline(out, now); /* a full batch: drawn by the next call, straight after this one */
        }
    }
}

void model_render(app_state_t *s, app_ms_t now, app_out_t *out)
{
    bool toast = now < s->toast.until;
    if (s->disp.awake) { /* asleep, nothing is drawn: no call to turn the clock over */
        app_out_deadline(out, next_change(s, now));
    }
    if (toast) {
        app_out_deadline(out, s->toast.until);
    }
    if (s->card.open) {
        app_out_deadline(out, s->card.idle_from + CARD_TIMEOUT_MS);
    }
    if (s->card.want_id != 0) {
        app_out_deadline(out, s->card.want_at + ANSWER_MS);
    }
    if (s->verdict.id != 0) {
        app_out_deadline(out, s->verdict.sent_at + ANSWER_MS);
    }
    static app_render_t r;
    if (pairing_screen(s, &r)) {
        /* Pairing mode: pairing.c's screens, and over them only the factory-reset chord's question (parent 2.2;
         * open-items O-28). */
        if (s->confirm.kind == APP_CONFIRM_RESET) {
            memset(&r, 0, sizeof(r));
            menu_render(s, &r);
            r.band.blank = true; /* the pairing screen's band, blank, under the question (O-32) */
        }
        show(s, &r, now, out);
        return;
    }
    memset(&r, 0, sizeof(r));
    r.screen = s->card.open ? APP_SCREEN_CARD : APP_SCREEN_LIST;
    build_band(s, now, &r.band);
    build_rows(s, now, &r);
    if (r.screen == APP_SCREEN_LIST) {
        if (s->net.link != APP_LINK_CONNECTED && s->model.have) { /* parent 16.4: say how old the list is */
            char age[6];
            format_age((int64_t)((now - s->model.snap_at) / 1000), age, sizeof(age));
            snprintf(r.title, sizeof(r.title), "LAST SEEN %s", age);
        }
        /* Only what the buttons do here: with no agent, a BOOT hold's global menu (the owner's decision,
         * 2026-10-27, open-items O-33), and with no link, PWR's device info too (O-77). */
        if (s->model.n == 0) {
            build_empty(s, &r);
            snprintf(r.band.pwr.text, sizeof(r.band.pwr.text), "%s", s->net.link != APP_LINK_CONNECTED ? "INFO" : "");
            snprintf(r.band.boot.text, sizeof(r.band.boot.text), "HOLD: MENU");
        } else {
            snprintf(r.band.pwr.text, sizeof(r.band.pwr.text), "OPEN");
            snprintf(r.band.boot.text, sizeof(r.band.boot.text), "%s", s->model.n > 1 ? "NEXT" : "");
        }
    } else {
        build_card(s, now, &r);
    }
    r.band.silent = s->set.silent;
    if (s->link_ctl.host_key_changed || s->link_ctl.refused) { /* parent 3.3, 3.5 */
        memset(r.lines, 0, sizeof(r.lines));
        r.banner[0] = '\0';
        r.screen = APP_SCREEN_LINK;
        snprintf(r.title, sizeof(r.title), "%s",
                 s->link_ctl.host_key_changed ? "HOST KEY CHANGED - RE-PAIR VIA USB" : "NOT AUTHORISED");
        snprintf(r.band.pwr.text, sizeof(r.band.pwr.text), "INFO");
        snprintf(r.band.boot.text, sizeof(r.band.boot.text), "HOLD: MENU");
    }
    menu_render(s, &r);
    if (toast) {
        r.screen = APP_SCREEN_TOAST;
        snprintf(r.title, sizeof(r.title), "%s", s->toast.text);
        r.band.pwr.text[0] = r.band.boot.text[0] = '\0';
    }
    show(s, &r, now, out);
}

#define BRIGHTNESS_MIN 20 /* the menu's lowest (menu.c); 0 would put the display to sleep */

/* Has the platform draw in the palette s->set.colour (APP_ACT_THEME) and, awake, light the backlight at
 * s->set.brightness: the stored settings, ahead of a render. */
static void apply_settings(const app_state_t *s, app_out_t *out)
{
    app_action_t *a = app_out_push(out, APP_ACT_THEME);
    if (a) {
        a->level = s->set.colour;
    }
    if (s->disp.awake && (a = app_out_push(out, APP_ACT_BACKLIGHT)) != NULL) {
        a->level = s->set.brightness;
    }
}

/* WIFI <ssid> FAILED (parent design §Error handling): three refused joins in a row on one network, which a wrong
 * password gives every time and an access point's brief refusals don't. It stays until a join goes through
 * (the owner's decision, 2026-10-27, open-items O-49). */
#define REFUSALS_FAILED 3

static void wifi_refusals(app_state_t *s, const app_event_t *ev)
{
    if (ev->wifi.connected) {
        s->net.refusals = 0;
        s->net.failed[0] = '\0';
    } else if (ev->wifi.fail == APP_WIFI_FAIL_REFUSED) {
        if (strcmp(ev->wifi.ssid, s->net.refused) != 0) {
            snprintf(s->net.refused, sizeof(s->net.refused), "%s", ev->wifi.ssid);
            s->net.refusals = 0;
        }
        if (s->net.refusals < UINT8_MAX && ++s->net.refusals >= REFUSALS_FAILED) {
            snprintf(s->net.failed, sizeof(s->net.failed), "%s", s->net.refused);
        }
    } else if (ev->wifi.fail == APP_WIFI_FAIL_OTHER) {
        s->net.refusals = 0;
    }
}

void model_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out)
{
    switch (ev->kind) {
    case APP_EV_BOOT:
        s->disp.awake = true;
        alerts_activity(s, now);
        s->set.volume = s->boot.volume;
        s->set.brightness = s->boot.brightness < BRIGHTNESS_MIN ? BRIGHTNESS_MIN : s->boot.brightness;
        s->set.silent = s->boot.silent;
        s->set.colour = s->boot.colour < APP_PALETTE_COUNT ? s->boot.colour : APP_PALETTE_ORANGE;
        s->set.done_tone = s->boot.done_tone;
        apply_settings(s, out); /* ahead of the first render, which ends this batch (open-items O-25) */
        if (s->boot.paired) {
            s->render.screen = APP_SCREEN_LIST;
        }
        if (s->boot.rtc_s > 0) {
            set_clock(s, s->boot.rtc_s, now);
        }
        break;
    case APP_EV_LINK:
        if (ev->link != APP_LINK_CONNECTED && s->verdict.id != 0) {
            /* parent 8.7: the session that would answer it is gone; never resent */
            s->verdict.id = 0;
            if (!s->verdict.interrupt) {
                s->card.open = false;
            }
            model_toast(s, "X LINK LOST", now);
        }
        if (ev->link == APP_LINK_CONNECTED) {
            s->net.dial_failed = false;
        } else if (ev->link == APP_LINK_DOWN && s->net.link == APP_LINK_CONNECTING) {
            s->net.dial_failed = true; /* a dial that never connected: NO BRIDGE ON THIS NETWORK (O-77) */
        }
        s->net.link = ev->link;
        if (ev->link != APP_LINK_CONNECTED) {
            s->card.want_id = 0; /* its answer went with the session: the next show has its own 8 s */
            alerts_link_lost(s);
        }
        break;
    case APP_EV_WIFI:
        if (ev->wifi.connected && !s->net.wifi) {
            s->net.dial_failed = false; /* a network joined: its first dial may well connect */
        }
        s->net.wifi = ev->wifi.connected;
        snprintf(s->net.ssid, sizeof(s->net.ssid), "%s", ev->wifi.ssid);
        snprintf(s->net.ip, sizeof(s->net.ip), "%s", ev->wifi.ip);
        wifi_refusals(s, ev);
        break;
    case APP_EV_POWER:
        s->net.power = ev->power;
        break;
    case APP_EV_STORE_FAILED:
        /* The device skipped the rest of the batch with the failed store: a setting's backlight or palette,
         * and the render. Both again; the new value holds until a restart (open-items O-26). */
        apply_settings(s, out);
        s->disp.redraw = true;
        break;
    default:
        break;
    }
}
