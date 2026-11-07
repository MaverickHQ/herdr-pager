/* The link's control: when to dial the bridge (parent 3.1, 3.4; parent design §Firmware components, net).
 * The platform's ssh_link reports its states as APP_EV_LINK; the core decides when it dials (APP_ACT_LINK):
 * at once when Wi-Fi comes up, then after each drop with backoff, never without Wi-Fi (the emulator's
 * sockets ignore its simulated Wi-Fi, so this is where NO WIFI stops the link). */
#include "app_internal.h"

#define BACKOFF_MAX_MS 30000
#define JITTER_PERMILLE 200 /* each delay within 20 % */

static uint32_t next_random(app_state_t *s)
{
    uint32_t x = s->link_ctl.rng;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    s->link_ctl.rng = x;
    return x;
}

/* NOT AUTHORISED or HOST KEY CHANGED: dials wait for refused_until. */
static bool held(const app_state_t *s)
{
    return s->link_ctl.refused || s->link_ctl.host_key_changed;
}

static void dial(app_state_t *s, app_out_t *out)
{
    s->link_ctl.next_at = 0;
    s->link_ctl.held_dialled = held(s);
    app_action_t *a = app_out_push(out, APP_ACT_LINK);
    if (a) {
        a->link_op = APP_LINK_OP_CONNECT;
    }
}

#define REFUSED_RETRY_MS (5 * 60 * 1000) /* parent 3.5: NOT AUTHORISED retries no more often than this */
/* HOST KEY CHANGED too: the link never authenticates to the changed key (ssh_link aborts before), but one spoofed
 * reply mustn't stop the pager for good (the security pass, 2026-11-01, amending parent 3.3). */
#define HOST_KEY_RETRY_MS (5 * 60 * 1000)

static bool may_dial(const app_state_t *s)
{
    return s->boot.paired && s->net.wifi;
}

/* Dials now, or once a refusal's or a changed key's 5 minutes are up. A held dial that ended without either
 * (it reached no bridge) waits 5 minutes more: never at once, so it can't loop. */
static void dial_when_allowed(app_state_t *s, app_ms_t now, app_out_t *out)
{
    if (held(s) && now >= s->link_ctl.refused_until && s->link_ctl.held_dialled) {
        s->link_ctl.refused_until = now + (s->link_ctl.host_key_changed ? HOST_KEY_RETRY_MS : REFUSED_RETRY_MS);
    }
    if (held(s) && now < s->link_ctl.refused_until) {
        s->link_ctl.next_at = s->link_ctl.refused_until;
    } else {
        dial(s, out);
    }
}

/* The link was refused (or met a changed key) at now: no dial for ms. */
static void hold(app_state_t *s, app_ms_t now, app_ms_t ms)
{
    s->link_ctl.refused_until = now + ms;
    s->link_ctl.held_dialled = false;
    s->link_ctl.next_at = may_dial(s) ? s->link_ctl.refused_until : 0;
}

/* 1, 2, 4, 8, 16, then 30 s, each with up to 20 % of jitter either way; at the 30 s cap the jitter only
 * shortens it (24-30 s), so a long outage never waits past 30 s and N3's 35 s holds (the owner's decision,
 * 2026-10-27, open-items O-48). */
static app_ms_t backoff(app_state_t *s)
{
    uint8_t n = s->link_ctl.attempt < 5 ? s->link_ctl.attempt : 5;
    app_ms_t base = (app_ms_t)1000 << n;
    bool capped = base >= BACKOFF_MAX_MS;
    if (capped) {
        base = BACKOFF_MAX_MS;
    }
    if (s->link_ctl.attempt < 255) {
        s->link_ctl.attempt++;
    }
    int32_t off = capped ? -(int32_t)(next_random(s) % (JITTER_PERMILLE + 1))
                         : (int32_t)(next_random(s) % (2 * JITTER_PERMILLE + 1)) - JITTER_PERMILLE;
    return (app_ms_t)((int64_t)base * (1000 + off) / 1000);
}

void link_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out)
{
    switch (ev->kind) {
    case APP_EV_BOOT:
        s->link_ctl.rng = s->boot.seed ? s->boot.seed : 1;
        break;
    case APP_EV_WIFI:
        if (!ev->wifi.connected) {
            s->link_ctl.next_at = 0;
        } else if (may_dial(s) && s->net.link != APP_LINK_CONNECTED && s->net.link != APP_LINK_CONNECTING) {
            dial_when_allowed(s, now, out);
        }
        break;
    case APP_EV_LINK:
        if (ev->link == APP_LINK_CONNECTED) { /* the pinned key answered, and took the device's */
            s->link_ctl.attempt = 0;
            s->link_ctl.next_at = 0;
            s->link_ctl.refused = false;
            s->link_ctl.host_key_changed = false;
            s->link_ctl.held_dialled = false;
            /* lines: the most lines of a card it draws, so a bridge sends it the 4 an MCP card may need (O-90's
             * second review); a bridge from before ignores the key, and sends no card of more than 3. */
            codec_send(out, "{\"t\":\"hello\",\"v\":1,\"fw\":\"%s\",\"name\":\"%s\",\"lines\":%d}", s->boot.fw, s->boot.name,
                       APP_CARD_LINES);
        } else if (ev->link == APP_LINK_AUTH_REFUSED) { /* the pinned key answered: the host key is fine */
            s->link_ctl.refused = true;
            s->link_ctl.host_key_changed = false;
            hold(s, now, REFUSED_RETRY_MS);
        } else if (ev->link == APP_LINK_HOST_KEY_CHANGED) {
            s->link_ctl.host_key_changed = true;
            s->link_ctl.refused = false;
            hold(s, now, HOST_KEY_RETRY_MS);
        } else if (ev->link == APP_LINK_DOWN && may_dial(s)) {
            if (held(s)) {
                dial_when_allowed(s, now, out);
            } else {
                s->link_ctl.next_at = now + backoff(s);
            }
        }
        break;
    default:
        break;
    }
}

void link_tick(app_state_t *s, app_ms_t now, app_out_t *out)
{
    if (s->link_ctl.next_at != 0 && now >= s->link_ctl.next_at) {
        if (may_dial(s)) {
            dial(s, out);
        } else {
            s->link_ctl.next_at = 0;
        }
    }
}

app_ms_t link_deadline(const app_state_t *s)
{
    return s->link_ctl.next_at != 0 ? s->link_ctl.next_at : APP_NEVER;
}
