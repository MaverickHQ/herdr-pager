/* The SSH link's line assembler (ssh_link_lines.h). */
#include "ssh_link_lines.h"

void ssh_link_lines_reset(ssh_link_lines_t *l)
{
    l->have = 0;
    l->cut = false;
}

void ssh_link_lines_take(ssh_link_lines_t *l, const char *b, size_t n, ssh_link_line_fn on_line, void *ctx)
{
    for (size_t i = 0; i < n; i++) {
        if (b[i] == '\n') {
            if (!l->cut && on_line) {
                on_line(l->buf, l->have, ctx);
            }
            ssh_link_lines_reset(l);
        } else if (!l->cut) {
            l->buf[l->have++] = b[i];
            if (l->have == sizeof(l->buf)) { /* APP_LINE_MAX + 1 bytes: too long, as the core will see */
                if (on_line) {
                    on_line(l->buf, l->have, ctx);
                }
                l->have = 0;
                l->cut = true;
            }
        }
    }
}
