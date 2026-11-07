#include "wifi_retry.h"

/* Parent design §Error handling: the network backoff is 1, 2, 4, 8, 16, then 30 s. An access point can refuse
 * the first join after a reset (task 10.6). */
static const uint16_t RETRY_S[] = {1, 2, 4, 8, 16, 30};

void wifi_retry_joined(wifi_retry_t *r)
{
    r->tries = 0;
    r->refused = 0;
}

/* Refusals in a row tried again at once. An access point can refuse a join twice and take the third (the gate's
 * and §B's board runs); at once, two refusals cost two attempts, not the backoff's waits as well. N7 covers
 * them (the owner's decision, 2026-10-27, open-items O-45). A wrong password is refused every time: after
 * these, the backoff. */
#define REFUSED_AT_ONCE 3

uint32_t wifi_retry_failed(wifi_retry_t *r, bool refused, bool pending)
{
    if (!refused) {
        r->refused = 0; /* the row is broken */
    } else if (r->refused < REFUSED_AT_ONCE) {
        r->refused++;
        return 0;
    }
    if (pending) {
        return WIFI_RETRY_KEEP;
    }
    const uint8_t n = sizeof(RETRY_S) / sizeof(RETRY_S[0]);
    uint16_t s = RETRY_S[r->tries < n ? r->tries++ : n - 1];
    return (uint32_t)s * 1000u;
}
