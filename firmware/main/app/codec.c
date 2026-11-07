/* The codec: bridge lines in and out (parent design §Protocol; the protocol
 * fixtures in bridge/testdata/protocol/ and their README, which says what a
 * reader checks). A line is read whole into a message, then handed to the
 * model; a line that isn't a valid message changes nothing. */
#include <stdarg.h>
#include <stdio.h>
#include <string.h>

#include "app_internal.h"
#include "app_json.h"

#define MAX_SAFE_INT 9007199254740991LL /* 2^53 - 1 */

/* One document at a time: the core is single-threaded, and the tokens are too big for a task's stack. */
static js_doc_t s_doc;

static bool read_text(const js_doc_t *d, int i, char *dst, size_t max)
{
    char buf[APP_HOST_NAME_MAX + 1]; /* the longest field */
    size_t len;
    if (max >= sizeof(buf) || !js_str(d, i, buf, max + 1, &len) || len > max) {
        return false;
    }
    app_filter_text(dst, max + 1, buf, len);
    return true;
}

static bool read_u64(const js_doc_t *d, int i, uint64_t *out)
{
    int64_t v;
    if (!js_int(d, i, 0, MAX_SAFE_INT, &v)) {
        return false;
    }
    *out = (uint64_t)v;
    return true;
}

static bool read_st(const js_doc_t *d, int i, uint8_t *st)
{
    static const char *const names[] = {
        [APP_ST_IDLE] = "idle",       [APP_ST_WORKING] = "working", [APP_ST_BLOCKED] = "blocked",
        [APP_ST_DONE] = "done",       [APP_ST_UNKNOWN] = "unknown",
    };
    for (uint8_t k = 0; k < sizeof(names) / sizeof(names[0]); k++) {
        if (js_str_is(d, i, names[k])) {
            *st = k;
            return true;
        }
    }
    return false;
}

static bool read_can(const js_doc_t *d, int i, uint8_t *can)
{
    static const struct {
        const char *name;
        uint8_t bit;
    } verbs[] = {
        {"show", APP_CAN_SHOW},           {"approve", APP_CAN_APPROVE}, {"deny", APP_CAN_DENY},
        {"interrupt", APP_CAN_INTERRUPT}, {"jump", APP_CAN_JUMP},
    };
    if (i < 0 || d->tok[i].type != JS_ARR) {
        return false;
    }
    *can = 0;
    for (int k = 0; k < d->tok[i].n; k++) {
        int e = js_elem(d, i, k);
        bool known = false;
        for (size_t v = 0; v < sizeof(verbs) / sizeof(verbs[0]); v++) {
            if (js_str_is(d, e, verbs[v].name)) {
                *can |= verbs[v].bit;
                known = true;
            }
        }
        if (!known) {
            return false;
        }
    }
    return true;
}

/* One id of a pane id: ASCII digits and letters, at least one. */
static bool pane_part(const char **p)
{
    const char *s = *p;
    while ((**p >= '0' && **p <= '9') || (**p >= 'A' && **p <= 'Z') || (**p >= 'a' && **p <= 'z')) {
        (*p)++;
    }
    return *p > s;
}

/* herdr's full pane id, w<id>:p<id>, each id digits and letters (w1:p3, w6:pE; the fixtures' README); it goes back into lines we send. */
static bool read_pane(const js_doc_t *d, int i, char *pane)
{
    if (!read_text(d, i, pane, APP_PANE_MAX)) {
        return false;
    }
    const char *p = pane;
    return *p++ == 'w' && pane_part(&p) && *p++ == ':' && *p++ == 'p' && pane_part(&p) && *p == '\0';
}

/* 64 lowercase hex digits; it goes back into the verdict we send. */
static bool read_hash(const js_doc_t *d, int i, char *hash)
{
    if (!read_text(d, i, hash, APP_HASH_LEN) || strlen(hash) != APP_HASH_LEN) {
        return false;
    }
    return strspn(hash, "0123456789abcdef") == APP_HASH_LEN;
}

/* `can` holds interrupt only for a working agent (R6.3). */
static bool can_fits_st(uint8_t can, uint8_t st)
{
    return !(can & APP_CAN_INTERRUPT) || st == APP_ST_WORKING;
}

static bool read_agent(const js_doc_t *d, int o, app_agent_t *a)
{
    memset(a, 0, sizeof(*a));
    return d->tok[o].type == JS_OBJ && js_unique_keys(d, o) && read_pane(d, js_get(d, o, "pane"), a->pane) &&
           read_text(d, js_get(d, o, "name"), a->name, APP_NAME_MAX) && a->name[0] != '\0' && /* 1 byte or more */
           read_text(d, js_get(d, o, "kind"), a->kind, APP_KIND_MAX) && a->kind[0] != '\0' &&
           (js_get(d, o, "at") < 0 || read_text(d, js_get(d, o, "at"), a->at, APP_AT_MAX)) && /* optional */
           read_st(d, js_get(d, o, "st"), &a->st) &&
           read_u64(d, js_get(d, o, "seq"), &a->seq) && read_u64(d, js_get(d, o, "dseq"), &a->dseq) &&
           js_int(d, js_get(d, o, "since"), 0, MAX_SAFE_INT, &a->since) &&
           read_can(d, js_get(d, o, "can"), &a->can) &&
           !(a->can & (APP_CAN_APPROVE | APP_CAN_DENY)) && /* only a card offers a verdict (parent 8.1, 8.6) */
           can_fits_st(a->can, a->st);
}

static void read_snap(app_state_t *s, const js_doc_t *d, app_ms_t now, app_out_t *out)
{
    static app_agent_t agents[APP_MAX_AGENTS];
    int64_t unix_now, more;
    int herdr = js_get(d, 0, "herdr");
    int list = js_get(d, 0, "agents");
    if (!js_int(d, js_get(d, 0, "now"), 0, MAX_SAFE_INT, &unix_now) ||
        !js_int(d, js_get(d, 0, "more"), 0, MAX_SAFE_INT, &more) || list < 0 || d->tok[list].type != JS_ARR ||
        d->tok[list].n > APP_MAX_AGENTS) {
        return;
    }
    bool offline = js_str_is(d, herdr, "offline");
    uint8_t n = (uint8_t)d->tok[list].n;
    if ((!offline && !js_str_is(d, herdr, "ok")) || (offline && (n > 0 || more > 0)) ||
        (more > 0 && n != APP_MAX_AGENTS)) {
        return;
    }
    for (int k = 0; k < n; k++) {
        if (!read_agent(d, js_elem(d, list, k), &agents[k])) {
            return;
        }
        for (int j = 0; j < k; j++) {
            if (strcmp(agents[j].pane, agents[k].pane) == 0) {
                return; /* no pane twice */
            }
        }
    }
    model_snapshot(s, agents, n, (uint32_t)more, offline, unix_now, now, out);
}

static void read_card(app_state_t *s, const js_doc_t *d, app_ms_t now, app_out_t *out)
{
    static app_card_t c;
    static const char *const kinds[] = {
        [APP_CARD_PERMISSION] = "permission", [APP_CARD_QUESTION] = "question", [APP_CARD_STATUS] = "status"};
    int64_t id;
    memset(&c, 0, sizeof(c));
    int lines = js_get(d, 0, "lines");
    int kind = js_get(d, 0, "kind");
    if (!js_int(d, js_get(d, 0, "id"), 0, UINT32_MAX, &id) || !read_pane(d, js_get(d, 0, "pane"), c.pane) ||
        !read_st(d, js_get(d, 0, "st"), &c.st) || !read_u64(d, js_get(d, 0, "seq"), &c.seq) ||
        !read_hash(d, js_get(d, 0, "hash"), c.hash) ||
        !read_text(d, js_get(d, 0, "summary"), c.summary, APP_TEXT_MAX) || !read_can(d, js_get(d, 0, "can"), &c.can) ||
        lines < 0 || d->tok[lines].type != JS_ARR || d->tok[lines].n > APP_CARD_LINES) {
        return;
    }
    c.id = (uint32_t)id;
    bool known = false;
    for (uint8_t k = 0; k < 3; k++) {
        if (js_str_is(d, kind, kinds[k])) {
            c.kind = k;
            known = true;
        }
    }
    if (!known) {
        return;
    }
    c.n_lines = (uint8_t)d->tok[lines].n;
    for (int k = 0; k < c.n_lines; k++) {
        if (!read_text(d, js_elem(d, lines, k), c.lines[k], APP_TEXT_MAX)) {
            return;
        }
    }
    int risk = js_get(d, 0, "risk"), rl = js_get(d, 0, "rl");
    if (risk != -1 || rl != -1) { /* together, or not at all */
        if (!js_str_is(d, risk, "danger") || !read_text(d, rl, c.rl, APP_LABEL_MAX) || c.rl[0] == '\0') {
            return;
        }
        c.danger = true;
    }
    /* approve and deny only on a permission card; a question card offers exactly jump, once: ["jump"] (R6.3,
     * parent 7.7; open-items O-40) */
    if (!can_fits_st(c.can, c.st) || (c.kind != APP_CARD_PERMISSION && (c.can & (APP_CAN_APPROVE | APP_CAN_DENY))) ||
        (c.kind == APP_CARD_QUESTION && (c.can != APP_CAN_JUMP || d->tok[js_get(d, 0, "can")].n != 1))) {
        return;
    }
    model_card(s, &c, now, out);
}

static void read_result(app_state_t *s, const js_doc_t *d, app_ms_t now, app_out_t *out)
{
    static const char *const errs[] = {"stale",         "not_allowed", "unknown_pane", "herdr_offline",
                                       "herdr_error",   "bad_request", "rate_limited"};
    int64_t id;
    int ok = js_get(d, 0, "ok");
    if (!js_int(d, js_get(d, 0, "id"), 0, UINT32_MAX, &id) || ok < 0 ||
        (d->tok[ok].type != JS_TRUE && d->tok[ok].type != JS_FALSE)) {
        return;
    }
    const char *err = NULL;
    if (d->tok[ok].type == JS_TRUE && js_get(d, 0, "err") != -1) {
        return; /* ok carries no err */
    }
    if (d->tok[ok].type == JS_FALSE) {
        int e = js_get(d, 0, "err");
        for (size_t k = 0; k < sizeof(errs) / sizeof(errs[0]); k++) {
            if (js_str_is(d, e, errs[k])) {
                err = errs[k];
            }
        }
        if (!err) {
            return;
        }
    }
    model_result(s, (uint32_t)id, err, now, out);
}

void codec_send(app_out_t *out, const char *fmt, ...)
{
    app_action_t *a = app_out_push(out, APP_ACT_SEND);
    if (!a) {
        return;
    }
    va_list ap;
    va_start(ap, fmt);
    int n = vsnprintf(a->text, sizeof(a->text), fmt, ap);
    va_end(ap);
    a->len = n < 0 ? 0 : (size_t)n < sizeof(a->text) ? (size_t)n : sizeof(a->text) - 1;
}

uint32_t codec_next_id(app_state_t *s)
{
    if (++s->next_id == 0) { /* ids run 1 to 2^32 - 1, then wrap */
        s->next_id = 1;
    }
    return s->next_id;
}

void codec_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out)
{
    const js_doc_t *d = &s_doc;
    if (ev->line.len > APP_LINE_MAX) {
        /* parent 13.5: dropped; what it said is lost, so ask for the whole picture again */
        codec_send(out, "{\"t\":\"list\",\"id\":%u}", (unsigned)codec_next_id(s));
        return;
    }
    if (!js_parse(&s_doc, ev->line.data, ev->line.len) || d->tok[0].type != JS_OBJ || !js_unique_keys(d, 0)) {
        return; /* malformed, or not one object, or a key twice: ignored */
    }
    int t = js_get(d, 0, "t");
    if (js_str_is(d, t, "snap")) {
        read_snap(s, d, now, out);
    } else if (js_str_is(d, t, "card")) {
        read_card(s, d, now, out);
    } else if (js_str_is(d, t, "result")) {
        read_result(s, d, now, out);
    }
}
