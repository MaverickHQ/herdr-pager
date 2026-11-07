/* The device's identity: its ed25519 key pair (parent design §Provisioning).
 *
 * The private key is created once, stored as an NVS blob (namespace "pager",
 * key dev_key), and never printed or sent. Key generation goes through
 * net/ssh_link (the only file that reaches the SSH library), only after the
 * RNG is truly random. The pairing flow itself is in the app core
 * (app/pairing.c).
 */
#pragma once

#include <stddef.h>

/**
 * Writes the device's public key, "ssh-ed25519 <base64>", into out, creating
 * the key pair when none is stored. A stored key that can't be read is kept,
 * and no key is made. Returns 0 on success; on failure out is "".
 */
int prov_identity(char *out, size_t size);
