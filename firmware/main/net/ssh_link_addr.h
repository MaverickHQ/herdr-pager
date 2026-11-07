/* The SSH link's TCP connection (net/ssh_link.c's; no SSH library here, so the
 * host suite builds it: firmware/test_host/test_isolation_net.c).
 *
 * The host goes to the resolver exactly as provisioned, so `.local` names
 * resolve: on the board through lwip's one-shot mDNS query
 * (CONFIG_LWIP_DNS_SUPPORT_MDNS_QUERIES), in the emulator through the Mac's
 * resolver. An IP address works the same way.
 */
#pragma once

#include <netdb.h>
#include <stdint.h>

/** getaddrinfo's signature: the resolver ssh_link_tcp_connect uses. */
typedef int (*ssh_link_gai_t)(const char *node, const char *service, const struct addrinfo *hints,
                              struct addrinfo **res);

/**
 * Resolves host with gai (getaddrinfo, outside tests) and connects to it on
 * port, within timeout_s. Returns the connected socket, or -1 with *why set to
 * a short reason (static text, never the host).
 */
int ssh_link_tcp_connect(const char *host, uint16_t port, int timeout_s, ssh_link_gai_t gai, const char **why);
