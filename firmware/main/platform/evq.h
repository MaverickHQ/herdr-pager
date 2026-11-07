/* What goes into the pump's event queue (platform/pump.c). The queue is FreeRTOS's; this is its policy, kept
 * free of FreeRTOS and the kit so the host suite builds it (test_host/test_evq.c). */
#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

/* The last touch sample the pump posted. */
typedef struct {
    bool have, pressed;
    uint16_t x, y;
} evq_touch_t;

/* Whether a touch sample is posted; if it is, it's recorded in *last. */
bool evq_touch_news(evq_touch_t *last, bool pressed, uint16_t x, uint16_t y);

/* Whether an event may go into a queue of cap slots, waiting of them in use. */
bool evq_admit(size_t waiting, size_t cap, bool button);
