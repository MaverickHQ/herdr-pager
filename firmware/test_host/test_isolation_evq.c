/* The pump's event queue policy (platform/evq.c), which needs no FreeRTOS, so the host suite builds it here.
 * The queue is driven as platform/pump.c drives its FreeRTOS queue of 32: the kit's touch task offers a sample
 * every 30 ms while a finger is down, its input task offers button edges, and the app task, stalled, takes
 * nothing. A lost BOOT up edge on a permission card would let the 1 s hold's deadline approve (open-items O-06). */
#include <stdbool.h>
#include <string.h>

#include "../main/platform/evq.c"
#include "test_registry.h"

#define CAP 32 /* pump.c's QUEUE_LEN */

typedef struct {
    bool button, down;
} queued_t;

static struct {
    queued_t q[CAP];
    size_t n;
    evq_touch_t touch;
} s_evq;

static void evq_reset(void)
{
    memset(&s_evq, 0, sizeof(s_evq));
}

static void offer_touch(bool pressed, uint16_t x, uint16_t y)
{
    if (evq_touch_news(&s_evq.touch, pressed, x, y) && evq_admit(s_evq.n, CAP, false)) {
        s_evq.q[s_evq.n++] = (queued_t){.button = false, .down = pressed};
    }
}

static void offer_button(bool down)
{
    if (evq_admit(s_evq.n, CAP, true)) {
        s_evq.q[s_evq.n++] = (queued_t){.button = true, .down = down};
    }
}

static size_t count(bool button)
{
    size_t k = 0;
    for (size_t i = 0; i < s_evq.n; i++) {
        k += s_evq.q[i].button == button;
    }
    return k;
}

/* A drag fills the queue, each sample at a new point; BOOT goes down and up in the middle of it. */
static void test_button_edges_survive_a_full_queue(void)
{
    evq_reset();
    for (uint16_t i = 0; i < 40; i++) {
        offer_touch(true, (uint16_t)(100 + i), 50);
    }
    offer_button(true);
    for (uint16_t i = 0; i < 40; i++) {
        offer_touch(true, (uint16_t)(140 + i), 50);
    }
    offer_button(false);
    TEST_ASSERT_EQUAL_size_t(2, count(true));
    size_t down = CAP, up = CAP;
    for (size_t i = 0; i < s_evq.n; i++) {
        if (s_evq.q[i].button) {
            *(s_evq.q[i].down ? &down : &up) = i;
        }
    }
    TEST_ASSERT_TRUE_MESSAGE(down < up, "BOOT down, then up");
}

/* A finger held still is one sample, its lift another. */
static void test_touch_posted_only_when_it_changes(void)
{
    evq_reset();
    for (int i = 0; i < 40; i++) {
        offer_touch(true, 200, 80);
    }
    offer_touch(false, 200, 80);
    offer_touch(false, 200, 80);
    TEST_ASSERT_EQUAL_size_t(2, count(false));
    TEST_ASSERT_TRUE(s_evq.q[0].down);
    TEST_ASSERT_FALSE(s_evq.q[1].down);
}

TEST_AREA(evq)
{
    RUN_TEST(test_button_edges_survive_a_full_queue);
    RUN_TEST(test_touch_posted_only_when_it_changes);
}
