/* The strict JSON reader (app_json.h). */
#include "app_json.h"

#include <string.h>

typedef struct {
    js_doc_t *d;
    const char *s;
    size_t len, pos;
} parser_t;

static void skip_ws(parser_t *p)
{
    while (p->pos < p->len) {
        char c = p->s[p->pos];
        if (c != ' ' && c != '\t' && c != '\n' && c != '\r') {
            break;
        }
        p->pos++;
    }
}

static int new_tok(parser_t *p, js_type_t type, size_t start)
{
    if (p->d->n >= JS_MAX_TOK) {
        return -1;
    }
    int i = p->d->n++;
    js_tok_t *t = &p->d->tok[i];
    memset(t, 0, sizeof(*t));
    t->type = (uint8_t)type;
    t->start = (uint32_t)start;
    return i;
}

static bool is_digit(char c)
{
    return c >= '0' && c <= '9';
}

static int hex_val(char c)
{
    if (c >= '0' && c <= '9') {
        return c - '0';
    }
    if (c >= 'a' && c <= 'f') {
        return c - 'a' + 10;
    }
    if (c >= 'A' && c <= 'F') {
        return c - 'A' + 10;
    }
    return -1;
}

static bool parse_value(parser_t *p, int depth);

static bool parse_string(parser_t *p)
{
    size_t start = ++p->pos; /* past the quote */
    while (p->pos < p->len) {
        unsigned char c = (unsigned char)p->s[p->pos];
        if (c == '"') {
            int i = new_tok(p, JS_STR, start);
            if (i < 0) {
                return false;
            }
            p->d->tok[i].len = (uint32_t)(p->pos - start);
            p->d->tok[i].next = (uint16_t)p->d->n;
            p->pos++;
            return true;
        }
        if (c < 0x20) {
            return false;
        }
        if (c == '\\') {
            if (++p->pos >= p->len) {
                return false;
            }
            char e = p->s[p->pos];
            if (e == 'u') {
                if (p->pos + 4 >= p->len) {
                    return false;
                }
                for (int k = 1; k <= 4; k++) {
                    if (hex_val(p->s[p->pos + k]) < 0) {
                        return false;
                    }
                }
                p->pos += 4;
            } else if (!strchr("\"\\/bfnrt", e) || e == '\0') {
                return false;
            }
        }
        p->pos++;
    }
    return false;
}

static bool parse_number(parser_t *p)
{
    size_t start = p->pos;
    if (p->s[p->pos] == '-') {
        p->pos++;
    }
    if (p->pos >= p->len || !is_digit(p->s[p->pos])) {
        return false;
    }
    if (p->s[p->pos] == '0') {
        p->pos++;
    } else {
        while (p->pos < p->len && is_digit(p->s[p->pos])) {
            p->pos++;
        }
    }
    if (p->pos < p->len && p->s[p->pos] == '.') {
        p->pos++;
        if (p->pos >= p->len || !is_digit(p->s[p->pos])) {
            return false;
        }
        while (p->pos < p->len && is_digit(p->s[p->pos])) {
            p->pos++;
        }
    }
    if (p->pos < p->len && (p->s[p->pos] == 'e' || p->s[p->pos] == 'E')) {
        p->pos++;
        if (p->pos < p->len && (p->s[p->pos] == '+' || p->s[p->pos] == '-')) {
            p->pos++;
        }
        if (p->pos >= p->len || !is_digit(p->s[p->pos])) {
            return false;
        }
        while (p->pos < p->len && is_digit(p->s[p->pos])) {
            p->pos++;
        }
    }
    int i = new_tok(p, JS_NUM, start);
    if (i < 0) {
        return false;
    }
    p->d->tok[i].len = (uint32_t)(p->pos - start);
    p->d->tok[i].next = (uint16_t)p->d->n;
    return true;
}

static bool parse_literal(parser_t *p, const char *word, js_type_t type)
{
    size_t n = strlen(word);
    if (p->len - p->pos < n || memcmp(p->s + p->pos, word, n) != 0) {
        return false;
    }
    int i = new_tok(p, type, p->pos);
    if (i < 0) {
        return false;
    }
    p->d->tok[i].len = (uint32_t)n;
    p->d->tok[i].next = (uint16_t)p->d->n;
    p->pos += n;
    return true;
}

/* An array or an object: obj says which. */
static bool parse_container(parser_t *p, bool obj, int depth)
{
    if (depth >= JS_MAX_DEPTH) {
        return false;
    }
    int i = new_tok(p, obj ? JS_OBJ : JS_ARR, p->pos);
    if (i < 0) {
        return false;
    }
    char close = obj ? '}' : ']';
    p->pos++;
    skip_ws(p);
    uint16_t n = 0;
    if (p->pos < p->len && p->s[p->pos] == close) {
        p->pos++;
    } else {
        for (;;) {
            if (obj) {
                if (p->pos >= p->len || p->s[p->pos] != '"' || !parse_string(p)) {
                    return false;
                }
                skip_ws(p);
                if (p->pos >= p->len || p->s[p->pos] != ':') {
                    return false;
                }
                p->pos++;
            }
            if (!parse_value(p, depth + 1)) {
                return false;
            }
            n++;
            skip_ws(p);
            if (p->pos >= p->len) {
                return false;
            }
            if (p->s[p->pos] == ',') {
                p->pos++;
                skip_ws(p);
                continue;
            }
            if (p->s[p->pos] != close) {
                return false;
            }
            p->pos++;
            break;
        }
    }
    js_tok_t *t = &p->d->tok[i];
    t->n = n;
    t->len = (uint32_t)(p->pos - t->start);
    t->next = (uint16_t)p->d->n;
    return true;
}

static bool parse_value(parser_t *p, int depth)
{
    skip_ws(p);
    if (p->pos >= p->len) {
        return false;
    }
    switch (p->s[p->pos]) {
    case '{':
        return parse_container(p, true, depth);
    case '[':
        return parse_container(p, false, depth);
    case '"':
        return parse_string(p);
    case 't':
        return parse_literal(p, "true", JS_TRUE);
    case 'f':
        return parse_literal(p, "false", JS_FALSE);
    case 'n':
        return parse_literal(p, "null", JS_NULL);
    default:
        return parse_number(p);
    }
}

bool js_parse(js_doc_t *d, const char *src, size_t len)
{
    parser_t p = {.d = d, .s = src, .len = len};
    d->src = src;
    d->n = 0;
    if (!parse_value(&p, 0)) {
        return false;
    }
    skip_ws(&p);
    return p.pos == len;
}

int js_get(const js_doc_t *d, int obj, const char *key)
{
    if (obj < 0 || d->tok[obj].type != JS_OBJ) {
        return -1;
    }
    int found = -1;
    int k = obj + 1;
    for (int m = 0; m < d->tok[obj].n; m++) {
        int v = k + 1;
        if (js_str_is(d, k, key)) {
            if (found >= 0) {
                return -2;
            }
            found = v;
        }
        k = d->tok[v].next;
    }
    return found;
}

/* Compares two string tokens by their decoded bytes. */
static bool str_eq(const js_doc_t *d, int a, int b)
{
    char x[256], y[256];
    size_t lx, ly;
    if (!js_str(d, a, x, sizeof(x), &lx) || !js_str(d, b, y, sizeof(y), &ly)) {
        /* A key this long is none the protocol has: compare the raw text. */
        return d->tok[a].len == d->tok[b].len &&
               memcmp(d->src + d->tok[a].start, d->src + d->tok[b].start, d->tok[a].len) == 0;
    }
    return lx == ly && memcmp(x, y, lx) == 0;
}

bool js_unique_keys(const js_doc_t *d, int obj)
{
    int k = obj + 1;
    for (int m = 0; m < d->tok[obj].n; m++) {
        int j = d->tok[k + 1].next;
        for (int o = m + 1; o < d->tok[obj].n; o++) {
            if (str_eq(d, k, j)) {
                return false;
            }
            j = d->tok[j + 1].next;
        }
        k = d->tok[k + 1].next;
    }
    return true;
}

int js_elem(const js_doc_t *d, int arr, int k)
{
    int i = arr + 1;
    while (k-- > 0) {
        i = d->tok[i].next;
    }
    return i;
}

bool js_int(const js_doc_t *d, int i, int64_t min, int64_t max, int64_t *out)
{
    if (i < 0 || d->tok[i].type != JS_NUM) {
        return false;
    }
    const char *s = d->src + d->tok[i].start;
    size_t n = d->tok[i].len;
    bool neg = s[0] == '-';
    size_t k = neg ? 1 : 0;
    if (k == n || n - k > 18) {
        return false;
    }
    int64_t v = 0;
    for (; k < n; k++) {
        if (!is_digit(s[k])) {
            return false;
        }
        v = v * 10 + (s[k] - '0');
    }
    if (neg) {
        if (v == 0) { /* a leading - only on a negative value, as the bridge reads it (open-items O-40) */
            return false;
        }
        v = -v;
    }
    if (v < min || v > max) {
        return false;
    }
    *out = v;
    return true;
}

/* Appends cp as UTF-8. */
static bool put_utf8(char *buf, size_t cap, size_t *o, uint32_t cp)
{
    unsigned char b[4];
    size_t n;
    if (cp < 0x80) {
        b[0] = (unsigned char)cp;
        n = 1;
    } else if (cp < 0x800) {
        b[0] = (unsigned char)(0xC0 | (cp >> 6));
        b[1] = (unsigned char)(0x80 | (cp & 0x3F));
        n = 2;
    } else if (cp < 0x10000) {
        b[0] = (unsigned char)(0xE0 | (cp >> 12));
        b[1] = (unsigned char)(0x80 | ((cp >> 6) & 0x3F));
        b[2] = (unsigned char)(0x80 | (cp & 0x3F));
        n = 3;
    } else {
        b[0] = (unsigned char)(0xF0 | (cp >> 18));
        b[1] = (unsigned char)(0x80 | ((cp >> 12) & 0x3F));
        b[2] = (unsigned char)(0x80 | ((cp >> 6) & 0x3F));
        b[3] = (unsigned char)(0x80 | (cp & 0x3F));
        n = 4;
    }
    if (*o + n > cap) {
        return false;
    }
    memcpy(buf + *o, b, n);
    *o += n;
    return true;
}

static uint32_t hex4(const char *s)
{
    return (uint32_t)(hex_val(s[0]) << 12 | hex_val(s[1]) << 8 | hex_val(s[2]) << 4 | hex_val(s[3]));
}

bool js_str(const js_doc_t *d, int i, char *buf, size_t cap, size_t *len)
{
    if (i < 0 || d->tok[i].type != JS_STR) {
        return false;
    }
    const char *s = d->src + d->tok[i].start;
    size_t n = d->tok[i].len, o = 0;
    for (size_t k = 0; k < n; k++) {
        char c = s[k];
        if (c != '\\') {
            if (o + 1 > cap) {
                return false;
            }
            buf[o++] = c;
            continue;
        }
        c = s[++k];
        if (c == 'u') {
            uint32_t cp = hex4(s + k + 1);
            k += 4;
            if (cp >= 0xD800 && cp < 0xDC00 && k + 6 < n + 0 && s[k + 1] == '\\' && s[k + 2] == 'u') {
                uint32_t lo = hex4(s + k + 3);
                if (lo >= 0xDC00 && lo < 0xE000) {
                    cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
                    k += 6;
                }
            }
            if (!put_utf8(buf, cap, &o, cp)) {
                return false;
            }
            continue;
        }
        static const char from[] = "\"\\/bfnrt", to[] = "\"\\/\b\f\n\r\t";
        const char *at = strchr(from, c);
        if (o + 1 > cap) {
            return false;
        }
        buf[o++] = to[at - from];
    }
    *len = o;
    if (o < cap) {
        buf[o] = '\0';
    }
    return true;
}

bool js_str_is(const js_doc_t *d, int i, const char *s)
{
    char buf[64];
    size_t len;
    size_t n = strlen(s);
    return n < sizeof(buf) && js_str(d, i, buf, sizeof(buf), &len) && len == n && memcmp(buf, s, n) == 0;
}
