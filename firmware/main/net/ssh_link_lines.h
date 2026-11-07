/* The SSH link's line assembler (net/ssh_link.c's; no SSH library here, so the
 * host suite builds it: firmware/test_host/test_isolation_net.c).
 *
 * Bytes from the bridge in, whole lines out, without their \n. A line longer
 * than APP_LINE_MAX goes out cut to APP_LINE_MAX + 1 bytes, so the core sees
 * it's too long (ssh_link.h), and the rest of it, up to its \n, is skipped.
 */
#pragma once

#include <stdbool.h>
#include <stddef.h>

#include "app.h"

typedef struct {
    char buf[APP_LINE_MAX + 1];
    size_t have;
    bool cut; /* the rest of an over-long line is being skipped */
} ssh_link_lines_t;

/** ssh_link_cbs_t's on_line: one line, not NUL-terminated. */
typedef void (*ssh_link_line_fn)(const char *line, size_t len, void *ctx);

/** Forgets any partial line: each session starts clean. */
void ssh_link_lines_reset(ssh_link_lines_t *l);

/** Takes n bytes, handing each line they end to on_line (with ctx), if there is one. */
void ssh_link_lines_take(ssh_link_lines_t *l, const char *b, size_t n, ssh_link_line_fn on_line, void *ctx);
