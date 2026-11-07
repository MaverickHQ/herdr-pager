/* The alert engine: pages, reminders, auto-clear and the backlight's limits
 * (parent 6; parent design §Firmware components, Alert engine). Its timers
 * are derived from the state: alerts_tick() runs what is due, first on every
 * event, and alerts_deadline() says when the next one is. */
#include <stdio.h>
#include <string.h>

#include "app_internal.h"

#define DEBOUNCE_MS 3000
#define REMIND_MS (5 * 60 * 1000)

#define PAGE_LIGHT_MS 60000 /* an unacknowledged page keeps the backlight on this long, at most */
#define IDLE_MS 30000       /* the backlight goes off this long after the last press */

void alerts_activity(app_state_t *s, app_ms_t t)
{
    if (s->disp.off_at < t + IDLE_MS) {
        s->disp.off_at = t + IDLE_MS;
    }
}

static void backlight(uint8_t level, app_out_t *out)
{
    app_action_t *a = app_out_push(out, APP_ACT_BACKLIGHT);
    if (a) {
        a->level = level;
    }
}

static void tone(app_state_t *s, app_tone_t t, app_out_t *out)
{
    if (s->set.silent) {
        return;
    }
    app_action_t *a = app_out_push(out, APP_ACT_TONE);
    if (a) {
        a->tone = t;
        a->level = s->set.volume;
    }
}

/* Whether the list's selection is on a blocked agent. */
static bool selected_blocked(const app_state_t *s)
{
    for (uint8_t i = 0; i < s->model.n; i++) {
        if (strcmp(s->model.agents[i].pane, s->sel.pane) == 0) {
            return s->model.agents[i].st == APP_ST_BLOCKED;
        }
    }
    return false;
}

/* Pages watch i. Its tone, flash and backlight are alerts_tick's: one of each a tick, however many page in it. */
static void page(app_state_t *s, int i, app_ms_t now)
{
    app_watch_t *w = &s->alerts.watch[i];
    w->paged = true;
    w->paged_at = now;
    w->last_tone = now;
    marks_add(&s->alerts.paged, w->pane, w->seq);
    /* The list's selection moves to the paged agent, so one PWR opens its card (the desk checkpoint,
     * 2026-10-24), unless it's on a blocked agent already: of several pages at once the first stays, the
     * most urgent, and neither a later page nor one in the same tick pulls it off a prompt the owner chose.
     * An open card or menu keeps its own pane. */
    if (!selected_blocked(s)) {
        snprintf(s->sel.pane, sizeof(s->sel.pane), "%s", w->pane);
    }
    s->disp.awake = true;
    if (s->disp.off_at < now + PAGE_LIGHT_MS) {
        s->disp.off_at = now + PAGE_LIGHT_MS;
    }
}

static bool acked(const app_state_t *s, const app_watch_t *w)
{
    return marks_has(&s->alerts.acked, w->pane, w->seq);
}

/* When watch w's next timer is due: its page, or its next reminder, until it's acknowledged. A card shown in
 * the debounce acknowledges the page before it fires (the owner's decision, 2026-10-27, open-items O-21). */
static app_ms_t due(const app_state_t *s, const app_watch_t *w)
{
    if (acked(s, w)) {
        return APP_NEVER;
    }
    return w->paged ? w->last_tone + REMIND_MS : w->first + DEBOUNCE_MS;
}

void alerts_ack(app_state_t *s, const char *pane, uint64_t seq)
{
    marks_add(&s->alerts.acked, pane, seq);
}

/* The list is stale while the link is down or herdr is offline: nothing pages from it (parent 16.4). */
static bool stale(const app_state_t *s)
{
    return s->net.link != APP_LINK_CONNECTED || s->model.herdr_offline;
}

void alerts_link_lost(app_state_t *s)
{
    uint8_t n = 0;
    for (uint8_t i = 0; i < s->alerts.n; i++) {
        if (s->alerts.watch[i].paged) { /* a debounce restarts from the next fresh snapshot */
            s->alerts.watch[n++] = s->alerts.watch[i];
        }
    }
    s->alerts.n = n;
}

void alerts_tick(app_state_t *s, app_ms_t now, app_out_t *out)
{
    if (s->disp.awake && s->disp.off_at != 0 && now >= s->disp.off_at) {
        s->disp.awake = false;
        backlight(0, out);
        /* Out of sight, a card closes: from asleep, a verdict takes a wake, an open and a hold (parent design
         * §Input router, T14). */
        s->card.open = false;
    }
    /* Pages and reminders due together sound once: three actions a page would overrun a batch of
     * APP_MAX_ACTIONS with seven pages, and lose its render (open-items O-20). */
    bool paged = false;
    bool due_now = false;
    for (uint8_t i = 0; i < s->alerts.n && !stale(s); i++) {
        app_watch_t *w = &s->alerts.watch[i];
        if (now < due(s, w)) {
            continue;
        }
        due_now = true;
        if (!w->paged) {
            page(s, i, now);
            paged = true;
        } else {
            w->last_tone = now;
        }
    }
    if (due_now) {
        tone(s, APP_TONE_BLOCKED, out);
    }
    if (paged) {
        if (!s->set.silent) {
            app_out_push(out, APP_ACT_FLASH);
        }
        backlight(s->set.brightness, out);
    }
}

app_ms_t alerts_deadline(const app_state_t *s)
{
    app_ms_t t = s->disp.awake && s->disp.off_at != 0 ? s->disp.off_at : APP_NEVER;
    for (uint8_t i = 0; i < s->alerts.n && !stale(s); i++) {
        if (due(s, &s->alerts.watch[i]) < t) {
            t = due(s, &s->alerts.watch[i]);
        }
    }
    return t;
}

void alerts_snapshot(app_state_t *s, app_ms_t now)
{
    app_watch_t next[APP_MAX_AGENTS];
    uint8_t n = 0;
    for (uint8_t k = 0; k < s->model.n; k++) {
        const app_agent_t *a = &s->model.agents[k];
        if (a->st != APP_ST_BLOCKED) {
            continue;
        }
        bool kept = false;
        for (uint8_t i = 0; i < s->alerts.n; i++) {
            if (s->alerts.watch[i].seq == a->seq && strcmp(s->alerts.watch[i].pane, a->pane) == 0) {
                next[n] = s->alerts.watch[i];
                kept = true;
            }
        }
        if (!kept) {
            memset(&next[n], 0, sizeof(next[n]));
            snprintf(next[n].pane, sizeof(next[n].pane), "%s", a->pane);
            next[n].seq = a->seq;
            next[n].first = now;
            next[n].paged = marks_has(&s->alerts.paged, a->pane, a->seq);
            next[n].paged_at = now;
            next[n].last_tone = now;
        }
        n++;
    }
    memcpy(s->alerts.watch, next, n * sizeof(next[0]));
    s->alerts.n = n;
}

void alerts_wake(app_state_t *s, app_ms_t t, app_out_t *out)
{
    s->disp.awake = true;
    s->disp.redraw = true;
    s->disp.off_at = 0;
    alerts_activity(s, t);
    backlight(s->set.brightness, out);
}

int64_t alerts_since_page(const app_state_t *s, const char *pane, app_ms_t now)
{
    for (uint8_t i = 0; i < s->alerts.n; i++) {
        if (s->alerts.watch[i].paged && strcmp(s->alerts.watch[i].pane, pane) == 0) {
            return (int64_t)(now - s->alerts.watch[i].paged_at);
        }
    }
    return -1;
}

/* The done tone (parent 6.2; pulled into the desk pager on the acceptance day, 2026-10-25): herdr calls every
 * finished turn done, one that ends asking for the owner too. Each (pane, dseq) done that hasn't chimed lights
 * the screen, selects its row as a page does (unless the selection is on a blocked agent) and plays the done
 * tone, once for the snapshot, unless it's off or silent. No flash and no reminders: those are for blocked
 * pages. The first snapshot after boot only notes the agents done already. */
void alerts_done(app_state_t *s, bool first, app_ms_t now, app_out_t *out)
{
    bool chime = false;
    for (uint8_t i = 0; i < s->model.n; i++) {
        const app_agent_t *a = &s->model.agents[i];
        if (a->st != APP_ST_DONE || marks_has(&s->alerts.chimed, a->pane, a->dseq)) {
            continue;
        }
        marks_add(&s->alerts.chimed, a->pane, a->dseq);
        if (!first && !chime && !selected_blocked(s)) {
            snprintf(s->sel.pane, sizeof(s->sel.pane), "%s", a->pane);
        }
        chime = !first;
    }
    if (!chime) {
        return;
    }
    if (s->set.done_tone) {
        tone(s, APP_TONE_DONE, out);
    }
    backlight(s->set.brightness, out);
    s->disp.awake = true;
    alerts_activity(s, now);
}

bool alerts_page_active(const app_state_t *s, const char *pane)
{
    for (uint8_t i = 0; i < s->alerts.n; i++) {
        const app_watch_t *w = &s->alerts.watch[i];
        if (w->paged && !acked(s, w) && strcmp(w->pane, pane) == 0) {
            return true;
        }
    }
    return false;
}
