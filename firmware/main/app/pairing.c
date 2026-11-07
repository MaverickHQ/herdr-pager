/* Pairing: pairing mode and the @P lines (parent 2; parent design §Provisioning; ADR 0011). The contract is
 * bridge/testdata/provisioning/ and its README: what each line holds, the limits, the timings and the errors, and
 * what each line and button does in each state (§The device's states).
 *
 * Pairing mode (an unpaired boot, or Pair again) shows PAIR VIA USB and writes a prov_hello at once and every
 * 2 s. A valid prov_offer (the name, address, port, host key and the Mac's name: nothing secret) shows both full
 * fingerprints and the host name for 30 s: PWR or the 30 s discard it, and BOOT confirms it. Confirmed, the pager
 * says so in a prov_hello at once, and waits 30 s for the prov_set: the networks and their passwords, with the
 * offer's host key. It takes that without a second press, stores it all and restarts paired; any other prov_set,
 * PWR or the 30 s discard everything. The offer is never stored. The device's private key never reaches the
 * core: a prov_hello carries boot.pubkey, and only when that is an ed25519 public key. */
#include <stdarg.h>
#include <stdio.h>
#include <string.h>

#include "app_internal.h"
#include "app_json.h"
#include "mbedtls/base64.h"

#define HELLO_MS 2000
#define CONFIRM_MS 30000 /* for BOOT or PWR on an offer, and then for the prov_set */
#define NETS_MAX 4
#define WIFI_PASS_MAX 64 /* 64 hex digits */
#define HOST_MAX 64

typedef enum {
    WAITING,    /* for a prov_offer */
    CONFIRMING, /* an offer is shown, waiting for BOOT or PWR */
    CONFIRMED,  /* BOOT confirmed it: waiting for its prov_set */
} pairing_state_t;

typedef struct {
    bool active;     /* in pairing mode */
    bool committing; /* the prov_set is being stored: a failed store answers storage */
    bool no_key;     /* boot.pubkey isn't an ed25519 public key: nothing is sent */
    pairing_state_t state;
    app_ms_t hello_at;
    app_ms_t until; /* confirming: BOOT or PWR by then; confirmed: the prov_set by then */
    /* The offer being confirmed, in memory only. */
    char name[APP_NAME_MAX + 1];
    char host[HOST_MAX + 1];
    uint16_t port;
    char host_key[APP_KEY_MAX + 1];
    char host_name[APP_HOST_NAME_MAX + 1];
    /* The prov_set's networks, between reading it and storing it. */
    uint8_t n_nets;
    struct {
        char ssid[APP_SSID_MAX + 1];
        char pass[WIFI_PASS_MAX + 1];
    } nets[NETS_MAX];
} pairing_t;

/* Pairing's state lives here because app_state_t (app.h) has no place for it: one core per device, so one
 * copy, tied to the state it belongs to; a different state starts from nothing. */
static struct {
    const app_state_t *owner;
    pairing_t p;
} s_pairing;

static pairing_t *pairing_of(const app_state_t *s)
{
    if (s_pairing.owner != s) {
        memset(&s_pairing, 0, sizeof(s_pairing));
        s_pairing.owner = s;
    }
    return &s_pairing.p;
}

static js_doc_t s_doc;

/* ---- Checks (the README's rules) ---- */

/* An OpenSSH ed25519 public key and nothing else: "ssh-ed25519 <base64>", the blob being the type and a
 * 32-byte key (design D4). */
static bool ed25519_public(const char *key)
{
    static const char prefix[] = "ssh-ed25519 ";
    if (strncmp(key, prefix, sizeof(prefix) - 1) != 0) {
        return false;
    }
    const char *b64 = key + sizeof(prefix) - 1;
    size_t len = strlen(b64);
    if (len == 0 || strspn(b64, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=") != len) {
        return false;
    }
    unsigned char blob[64];
    size_t n;
    if (mbedtls_base64_decode(blob, sizeof(blob), &n, (const unsigned char *)b64, len) != 0) {
        return false;
    }
    static const unsigned char want[] = {0, 0, 0, 11, 's', 's', 'h', '-', 'e', 'd', '2', '5', '5', '1', '9', 0, 0, 0, 32};
    return n == sizeof(want) + 32 && memcmp(blob, want, sizeof(want)) == 0;
}

static bool all_of(const char *s, size_t len, const char *set)
{
    for (size_t i = 0; i < len; i++) {
        if (!s[i] || !strchr(set, s[i])) {
            return false;
        }
    }
    return true;
}

#define ALNUM "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

/* Reads string member key of obj into buf: min-max bytes, no NUL byte. */
static bool get_str(int obj, const char *key, char *buf, size_t max, size_t min, size_t *len)
{
    int i = js_get(&s_doc, obj, key);
    return i >= 0 && js_str(&s_doc, i, buf, max + 1, len) && *len >= min && *len <= max && strlen(buf) == *len;
}

static bool valid_pass(const char *p, size_t len)
{
    if (len == 0) {
        return true; /* an open network */
    }
    if (len == 64) {
        return all_of(p, len, "0123456789abcdefABCDEF");
    }
    if (len < 8 || len > 63) {
        return false;
    }
    for (size_t i = 0; i < len; i++) {
        if (p[i] < 0x20 || p[i] > 0x7e) {
            return false;
        }
    }
    return true;
}

typedef enum { LINE_OTHER, LINE_OFFER, LINE_SET } line_kind_t;

/* Parses an @P line into s_doc, and says which message it is: LINE_OTHER for a line over 512 bytes, one that
 * isn't one JSON object with no key twice, or one whose t is neither prov_offer nor prov_set. */
static line_kind_t parse_line(const char *data, size_t len)
{
    if (len > APP_PROV_LINE_MAX || len < 3 || memcmp(data, "@P ", 3) != 0) {
        return LINE_OTHER;
    }
    if (!js_parse(&s_doc, data + 3, len - 3) || s_doc.tok[0].type != JS_OBJ || !js_unique_keys(&s_doc, 0)) {
        return LINE_OTHER;
    }
    int t = js_get(&s_doc, 0, "t");
    if (t >= 0 && js_str_is(&s_doc, t, "prov_offer")) {
        return LINE_OFFER;
    }
    if (t >= 0 && js_str_is(&s_doc, t, "prov_set")) {
        return LINE_SET;
    }
    return LINE_OTHER;
}

/* Reads the parsed prov_offer into p; false when it breaks any rule. */
static bool read_offer(pairing_t *p)
{
    size_t n;
    if (!get_str(0, "name", p->name, APP_NAME_MAX, 1, &n) || !all_of(p->name, n, ALNUM "._-") ||
        !strchr(ALNUM, p->name[0])) {
        return false;
    }
    if (!get_str(0, "host", p->host, HOST_MAX, 1, &n) || !all_of(p->host, n, ALNUM ".:-")) {
        return false;
    }
    int64_t port;
    int i = js_get(&s_doc, 0, "port");
    if (i < 0 || !js_int(&s_doc, i, 1, 65535, &port)) {
        return false;
    }
    p->port = (uint16_t)port;
    if (!get_str(0, "host_key", p->host_key, APP_KEY_MAX, 1, &n) || !ed25519_public(p->host_key)) {
        return false;
    }
    char host_name[APP_HOST_NAME_MAX + 1];
    i = js_get(&s_doc, 0, "host_name");
    if (i < 0 || !js_str(&s_doc, i, host_name, sizeof(host_name), &n) || n < 1 || n > APP_HOST_NAME_MAX) {
        return false;
    }
    app_filter_text(p->host_name, sizeof(p->host_name), host_name, n); /* display text: shown as ASCII */
    return true;
}

/* Reads the parsed prov_set's networks into p; false when it breaks any rule, or its host_key isn't exactly the
 * confirmed offer's. */
static bool read_set(pairing_t *p)
{
    char host_key[APP_KEY_MAX + 1];
    size_t n;
    if (!get_str(0, "host_key", host_key, APP_KEY_MAX, 1, &n) || !ed25519_public(host_key) ||
        strcmp(host_key, p->host_key) != 0) {
        return false;
    }
    int wifi = js_get(&s_doc, 0, "wifi");
    if (wifi < 0 || s_doc.tok[wifi].type != JS_ARR || s_doc.tok[wifi].n < 1 || s_doc.tok[wifi].n > NETS_MAX) {
        return false;
    }
    p->n_nets = (uint8_t)s_doc.tok[wifi].n;
    for (int k = 0; k < p->n_nets; k++) {
        int net = js_elem(&s_doc, wifi, k);
        if (s_doc.tok[net].type != JS_OBJ || !js_unique_keys(&s_doc, net) ||
            !get_str(net, "ssid", p->nets[k].ssid, APP_SSID_MAX, 1, &n) ||
            !get_str(net, "pass", p->nets[k].pass, WIFI_PASS_MAX, 0, &n) || !valid_pass(p->nets[k].pass, n)) {
            return false;
        }
    }
    return true;
}

/* ---- Lines and screens ---- */

static void serial(app_out_t *out, const char *fmt, ...) __attribute__((format(printf, 2, 3)));

static void serial(app_out_t *out, const char *fmt, ...)
{
    app_action_t *a = app_out_push(out, APP_ACT_SERIAL);
    if (!a) {
        return;
    }
    va_list ap;
    va_start(ap, fmt);
    int n = vsnprintf(a->text, sizeof(a->text), fmt, ap);
    va_end(ap);
    a->len = n < 0 ? 0 : (size_t)n < sizeof(a->text) ? (size_t)n : sizeof(a->text) - 1;
}

static void hello(app_state_t *s, pairing_t *p, app_ms_t now, app_out_t *out)
{
    static const char *const states[] = {[WAITING] = "waiting", [CONFIRMING] = "confirming", [CONFIRMED] = "confirmed"};
    p->hello_at = now + HELLO_MS;
    if (p->no_key) {
        return;
    }
    serial(out, "@P {\"t\":\"prov_hello\",\"fw\":\"%s\",\"pubkey\":\"%s\",\"state\":\"%s\"}", s->boot.fw,
           s->boot.pubkey, states[p->state]);
}

static void result(app_out_t *out, const char *err)
{
    if (err) {
        serial(out, "@P {\"t\":\"prov_result\",\"ok\":false,\"err\":\"%s\"}", err);
    } else {
        serial(out, "@P {\"t\":\"prov_result\",\"ok\":true}");
    }
}

/* Pairing mode's screen, into r: model_render draws it, with the factory-reset confirmation over it when the
 * chord asks (open-items O-28). */
static void render(const app_state_t *s, const pairing_t *p, app_render_t *r)
{
    memset(r, 0, sizeof(*r));
    r->band.blank = true; /* before pairing, none of the status applies (O-32) */
    if (p->state != WAITING) {
        char fp[APP_FP_MAX + 1];
        r->screen = APP_SCREEN_PAIRING;
        /* The longest name is cut to fit, keeping the "?". Confirmed, the same two fingerprints stay up, for the
         * owner to compare with the page or the terminal, which ask next. */
        if (p->state == CONFIRMING) {
            snprintf(r->title, sizeof(r->title), "PAIR WITH %.*s?", APP_BODY_LINE_MAX - 11, p->host_name);
            snprintf(r->band.boot.text, sizeof(r->band.boot.text), "PAIR");
        } else {
            snprintf(r->title, sizeof(r->title), "NOW CONFIRM ON %.*s", APP_BODY_LINE_MAX - 15, p->host_name);
        }
        snprintf(r->lines[0], sizeof(r->lines[0]), "HOST %s", app_fingerprint(p->host_key, fp, sizeof(fp)) ? fp : "-");
        snprintf(r->lines[1], sizeof(r->lines[1]), "DEV %s", app_fingerprint(s->boot.pubkey, fp, sizeof(fp)) ? fp : "-");
        snprintf(r->band.pwr.text, sizeof(r->band.pwr.text), "CANCEL");
    } else if (p->no_key) {
        /* provision can't help: a pager with no key never answers it. The chord is the way out (O-07). */
        r->screen = APP_SCREEN_BOOT;
        snprintf(r->title, sizeof(r->title), "NO DEVICE KEY");
        snprintf(r->lines[0], sizeof(r->lines[0]), "Hold BOOT and PWR 10 s to reset");
        snprintf(r->lines[1], sizeof(r->lines[1]), "then pair it again from the Mac");
    } else {
        r->screen = APP_SCREEN_BOOT;
        snprintf(r->title, sizeof(r->title), "PAIR VIA USB");
        snprintf(r->lines[0], sizeof(r->lines[0]), "Run pager-bridge provision on the Mac");
    }
}

/* Forgets the offer and any prov_set, passwords and all, answers err, and waits for the next offer. */
static void discard(app_state_t *s, pairing_t *p, const char *err, app_out_t *out)
{
    bool active = p->active, no_key = p->no_key;
    app_ms_t hello_at = p->hello_at;
    memset(p, 0, sizeof(*p));
    p->active = active;
    p->no_key = no_key;
    p->hello_at = hello_at;
    result(out, err); /* and back to PAIR VIA USB, which model_render draws */
}

/* commit's batch fits one app_out_t: app_out_push drops what doesn't fit, and the restart is last. The pin's
 * erase, five stores, two stores or erases per network slot, the pin, three results and the restart. */
_Static_assert(1 + 5 + 2 * NETS_MAX + 1 + 3 + 1 <= APP_MAX_ACTIONS, "pairing's commit doesn't fit one app_out_t");

/* Stores the confirmed offer and its prov_set, and restarts paired. The pinned key goes first (erased) and last
 * (stored): it is what makes the device paired, so a batch cut short leaves it unpaired, never paired to half a
 * configuration. */
static void commit(app_state_t *s, pairing_t *p, app_out_t *out)
{
    p->committing = true;
    app_action_t *a = app_out_push(out, APP_ACT_ERASE);
    if (a) {
        snprintf(a->key, sizeof(a->key), "%s", APP_KEY_HOST_KEY);
    }
    app_store(out, APP_KEY_NAME, "%s", p->name);
    app_store(out, APP_KEY_HOST, "%s", p->host);
    app_store(out, APP_KEY_PORT, "%u", (unsigned)p->port);
    app_store(out, APP_KEY_HOST_NAME, "%s", p->host_name);
    app_store(out, APP_KEY_WIFI_N, "%u", (unsigned)p->n_nets);
    for (uint8_t i = 0; i < p->n_nets; i++) {
        char key[APP_STORE_KEY_MAX + 1];
        snprintf(key, sizeof(key), "wifi%u_ssid", (unsigned)i);
        app_store(out, key, "%s", p->nets[i].ssid);
        snprintf(key, sizeof(key), "wifi%u_pass", (unsigned)i);
        app_store(out, key, "%s", p->nets[i].pass);
    }
    /* A pairing with fewer networks than the last leaves none of the others' names or passwords behind
     * (open-items O-30). Erasing a key that isn't there is OK (lcd349_kv_erase). */
    _Static_assert(NETS_MAX <= 10, "a network slot is one digit");
    for (uint8_t i = p->n_nets; i < NETS_MAX; i++) {
        char slot = (char)('0' + i); /* one character, so GCC can bound the key's length */
        app_action_t *a = app_out_push(out, APP_ACT_ERASE);
        if (a) {
            snprintf(a->key, sizeof(a->key), "wifi%c_ssid", slot);
        }
        a = app_out_push(out, APP_ACT_ERASE);
        if (a) {
            snprintf(a->key, sizeof(a->key), "wifi%c_pass", slot);
        }
    }
    app_store(out, APP_KEY_HOST_KEY, "%s", p->host_key);
    /* Three times: a USB console the Mac isn't reading can cut a line short, and a lost ok would leave this
     * pager paired and the bridge not (the desk checkpoint, 2026-10-24). provision takes the first whole one. */
    for (int i = 0; i < 3; i++) {
        result(out, NULL);
    }
    app_out_push(out, APP_ACT_REBOOT);
}

static void enter_pairing_mode(app_state_t *s, app_ms_t now, app_out_t *out)
{
    pairing_t *p = pairing_of(s);
    memset(p, 0, sizeof(*p));
    p->active = true;
    p->no_key = !ed25519_public(s->boot.pubkey);
    /* The stored pairing is being replaced: the link isn't dialled again, so nothing pages behind this screen,
     * until the device restarts. */
    s->boot.paired = false;
    hello(s, p, now, out);
}

/* What a line does in each state: the README's §The device's states. */
static void on_line(app_state_t *s, pairing_t *p, const app_event_t *ev, app_ms_t now, app_out_t *out)
{
    if (s->confirm.kind != APP_CONFIRM_NONE) { /* FACTORY RESET? is asked (O-28) */
        result(out, "busy");
        return;
    }
    /* With no key to show, there's nothing for the owner to compare, and paired, the next boot would make a key
     * the bridge never saw (open-items O-29). provision never sends one unasked: this pager sends no hello. */
    if (p->no_key) {
        discard(s, p, "invalid", out);
        return;
    }
    line_kind_t kind = parse_line(ev->line.data, ev->line.len);
    switch (p->state) {
    case WAITING:
        /* Only an offer: a prov_set here has no confirmed offer (an older bridge sends one alone). */
        if (kind != LINE_OFFER || !read_offer(p)) {
            discard(s, p, "invalid", out);
            return;
        }
        p->state = CONFIRMING;
        p->until = now + CONFIRM_MS;
        router_card_shown(s); /* a press already down when the question appears doesn't answer it */
        /* Lit for the whole 30 s, and past it, so the screen it goes back to is seen. */
        alerts_wake(s, now, out);
        alerts_activity(s, p->until);
        break;
    case CONFIRMING:
        /* A prov_set before BOOT has no confirmed offer, and ends this one; anything else waits its turn. */
        if (kind == LINE_SET) {
            discard(s, p, "invalid", out);
        } else {
            result(out, "busy");
        }
        break;
    case CONFIRMED:
        if (kind == LINE_OFFER) {
            result(out, "busy");
        } else if (kind == LINE_SET && read_set(p)) {
            commit(s, p, out);
        } else {
            discard(s, p, "invalid", out); /* another key, a broken prov_set, or any other line */
        }
        break;
    }
}

/* ---- The area's entry points (app_internal.h) ---- */

void pairing_enter(app_state_t *s, app_ms_t now, app_out_t *out)
{
    enter_pairing_mode(s, now, out);
}

bool pairing_screen(const app_state_t *s, app_render_t *r)
{
    const pairing_t *p = pairing_of(s);
    if (!p->active) {
        return false;
    }
    render(s, p, r);
    return true;
}

void pairing_reset_asked(app_state_t *s, app_out_t *out)
{
    pairing_t *p = pairing_of(s);
    if (p->state != WAITING && !p->committing) { /* the chord answers the offer on screen: cancelled, as PWR would */
        discard(s, p, "cancelled", out);
    }
}

void pairing_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out)
{
    pairing_t *p = pairing_of(s);
    switch (ev->kind) {
    case APP_EV_BOOT:
        memset(p, 0, sizeof(*p));
        /* Paired or not, a pager with no usable device key shows NO DEVICE KEY: pairing mode, which with no key
         * sends no hello, takes no offer and dials nothing. Its stored pairing is kept; the way out is the
         * factory-reset chord (the owner's decision, 2026-10-27, open-items O-07). */
        if (!s->boot.paired || !ed25519_public(s->boot.pubkey)) {
            enter_pairing_mode(s, now, out);
        }
        break;
    case APP_EV_PROV_LINE:
        if (p->active && !p->committing) { /* outside pairing mode, @P lines are ignored */
            on_line(s, p, ev, now, out);
        }
        break;
    case APP_EV_STORE_FAILED:
        if (p->committing) {
            discard(s, p, "storage", out);
        }
        break;
    case APP_EV_DEADLINE:
        if (p->state != WAITING && !p->committing && now >= p->until) {
            discard(s, p, "timeout", out);
        }
        if (p->active && !p->committing && now >= p->hello_at) {
            hello(s, p, now, out);
        }
        break;
    default:
        break;
    }
}

void pairing_press(app_state_t *s, app_btn_t btn, app_ms_t now, app_out_t *out)
{
    pairing_t *p = pairing_of(s);
    if (p->state == WAITING || p->committing) {
        return; /* waiting: the buttons do nothing (parent design, PROVISION_WAIT) */
    }
    if (now >= p->until) { /* a press after the 30 s, handed over before the deadline call (parent 2.7) */
        discard(s, p, "timeout", out);
    } else if (btn != APP_BTN_BOOT) {
        discard(s, p, "cancelled", out);
    } else if (p->state == CONFIRMING) {
        /* Confirmed: it says so at once, and waits 30 s for the prov_set, which it then takes without a press. */
        p->state = CONFIRMED;
        p->until = now + CONFIRM_MS;
        alerts_activity(s, p->until);
        hello(s, p, now, out);
    }
}

app_ms_t pairing_deadline(const app_state_t *s)
{
    const pairing_t *p = pairing_of(s);
    if (!p->active || p->committing) {
        return APP_NEVER;
    }
    if (p->state != WAITING && p->until < p->hello_at) {
        return p->until;
    }
    return p->hello_at;
}
