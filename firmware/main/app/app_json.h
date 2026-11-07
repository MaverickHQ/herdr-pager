/* A strict JSON reader for the bridge's lines (RFC 8259), inside the codec.
 *
 * Not cJSON: the protocol needs what cJSON drops (bridge/testdata/protocol/README.md): a decoded string keeps
 * its length, so a \u0000 can't cut a field short; an integer is read from its own digits, so 41.0 isn't 41;
 * and a reader can see a duplicate key. It allocates nothing: a document is a flat array of tokens over the
 * line's bytes, each container followed by its children. */
#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef enum {
    JS_NULL,
    JS_FALSE,
    JS_TRUE,
    JS_NUM,
    JS_STR,
    JS_ARR,
    JS_OBJ,
} js_type_t;

typedef struct {
    uint8_t type;
    uint16_t n;     /**< an array's elements, an object's members */
    uint16_t next;  /**< the token after this one's children */
    uint32_t start; /**< the raw text; a string's without its quotes */
    uint32_t len;
} js_tok_t;

#define JS_MAX_TOK 1200
#define JS_MAX_DEPTH 8

typedef struct {
    const char *src;
    uint16_t n;
    js_tok_t tok[JS_MAX_TOK];
} js_doc_t;

/** Parses src as exactly one JSON value with only whitespace around it. Token 0 is that value. */
bool js_parse(js_doc_t *d, const char *src, size_t len);

/** The value of obj's member key: its token, -1 when missing, -2 when the key appears twice. */
int js_get(const js_doc_t *d, int obj, const char *key);

/** True when every key of obj appears once. */
bool js_unique_keys(const js_doc_t *d, int obj);

/** The k-th element of arr (k < n), by token. */
int js_elem(const js_doc_t *d, int arr, int k);

/** Reads an integer written as plain digits (a leading '-' only for negatives) within [min, max]. */
bool js_int(const js_doc_t *d, int i, int64_t min, int64_t max, int64_t *out);

/** Decodes string i into buf (cap bytes), keeping NUL bytes; *len is the decoded length. False when it isn't a
 *  string or won't fit. buf is NUL-terminated too when it fits (cap > *len). */
bool js_str(const js_doc_t *d, int i, char *buf, size_t cap, size_t *len);

/** True when string i decodes to exactly s. */
bool js_str_is(const js_doc_t *d, int i, const char *s);
