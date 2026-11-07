#include "evq.h"

/* Slots only a button edge may take, so a queue filled by everything else never drops one (open-items O-06): a
 * lost BOOT up edge on a permission card would let the hold's deadline approve. Eight is four presses, made
 * while the app task is stalled. */
#define BUTTON_SLOTS 8

bool evq_touch_news(evq_touch_t *last, bool pressed, uint16_t x, uint16_t y)
{
    /* The kit samples a touch every 30 ms while it's down, and touch never acts (R1.3): a finger held
     * still is one sample, its lift another. */
    if (last->have && last->pressed == pressed && last->x == x && last->y == y) {
        return false;
    }
    last->have = true;
    last->pressed = pressed;
    last->x = x;
    last->y = y;
    return true;
}

bool evq_admit(size_t waiting, size_t cap, bool button)
{
    size_t room = button || cap <= BUTTON_SLOTS ? cap : cap - BUTTON_SLOTS;
    return waiting < room;
}
