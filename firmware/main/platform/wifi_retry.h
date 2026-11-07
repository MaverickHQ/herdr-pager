/* When the pump tries a failed Wi-Fi join again (platform/pump.c). The kit alone tries again only at its next
 * scan, a minute on; this is the pump's policy, kept free of FreeRTOS and the kit so the host suite builds it
 * (test_host/test_isolation_wifi_retry.c). */
#pragma once

#include <stdbool.h>
#include <stdint.h>

typedef struct {
    uint8_t tries;   /* backoff steps taken since the last join */
    uint8_t refused; /* joins the access point refused in a row */
} wifi_retry_t;

/* wifi_retry_failed's answer when a retry is already due: it stays as it is. */
#define WIFI_RETRY_KEEP UINT32_MAX

/* A join went through: the next failure starts over. */
void wifi_retry_joined(wifi_retry_t *r);

/* A join failed, not because the station was stopped: refused, the access point turned it away (the kit's
 * LCD349_WIFI_REASON_AUTH); pending, a retry is already due. Returns how many ms to wait before the next
 * try, 0 for at once, or WIFI_RETRY_KEEP. */
uint32_t wifi_retry_failed(wifi_retry_t *r, bool refused, bool pending);
