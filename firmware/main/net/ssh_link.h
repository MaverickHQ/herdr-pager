/* The SSH link to pager-bridge (parent design §SSH link, design D4).
 *
 * ssh_link.c is the only file in the firmware that includes an SSH library's
 * headers: david-cermak/libssh 0.12.2 on the board, the Mac's libssh 0.12.2 in
 * the emulator (R2.2). Everything else sees this interface.
 *
 * The link dials out; the device never listens. It reads the stored host,
 * port, pinned host key and device key itself (namespace "pager"), reports its
 * states and the bridge's lines through the callbacks, and never retries on
 * its own: the app core decides when to connect (its backoff). It sends the
 * protocol's ping every 60 s, and drops a link that has been silent for 150 s.
 * Callbacks run on the link's own task.
 */
#pragma once

#include <stddef.h>

#include "app.h"

/** The NVS key (namespace "pager") of the device's private key: an OpenSSH private key, never printed or sent. */
#define SSH_LINK_KEY_DEV "dev_key"

/** The longest stored private key, NUL included. */
#define SSH_LINK_PRIV_MAX 1024

typedef struct {
    /** The link's new state. */
    void (*on_state)(app_link_t state, void *ctx);
    /** One line from the bridge, without its \n. A line longer than APP_LINE_MAX
     *  is delivered cut to APP_LINE_MAX + 1 bytes, so the core sees it's too long. */
    void (*on_line)(const char *line, size_t len, void *ctx);
    void *ctx;
} ssh_link_cbs_t;

/** Sets the callbacks and starts the link's task. Call once, before connecting. Returns 0 on success. */
int ssh_link_init(const ssh_link_cbs_t *cbs);

/** Dials the stored host and returns at once; states follow through on_state. Returns 0 if started. */
int ssh_link_connect(void);

/** Closes the link, if open. on_state reports APP_LINK_DOWN. */
void ssh_link_disconnect(void);

/** Sends one line (no \n; the link adds it). Returns 0 if queued, -1 if the link isn't connected. */
int ssh_link_send(const char *line, size_t len);

/** The SSH library's version, for the boot log. */
const char *ssh_link_library_version(void);

/**
 * Makes a new ed25519 key pair: the private key, as an OpenSSH private key
 * (text), into priv; the public key, "ssh-ed25519 <base64>", into pub.
 * The RNG must be truly random by then (parent design §Provisioning): the
 * radio on. It reseeds the SSH library's generator from the RNG first.
 * Returns 0 on success.
 */
int ssh_link_keygen(char *priv, size_t priv_size, char *pub, size_t pub_size);

/** The public key, "ssh-ed25519 <base64>", of the private key priv (as ssh_link_keygen made it). Returns 0 on success. */
int ssh_link_pubkey(const char *priv, char *pub, size_t pub_size);
