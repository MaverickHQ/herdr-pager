/* The app core's entry points, and its dispatch to one file per area. */
#include <string.h>

#include "app.h"
#include "app_internal.h"

void app_init(app_state_t *s)
{
    memset(s, 0, sizeof(*s));
    s->render.screen = APP_SCREEN_BOOT;
}

const app_render_t *app_render(const app_state_t *s)
{
    return &s->render;
}

app_action_t *app_out_push(app_out_t *out, app_action_kind_t k)
{
    if (out->n >= APP_MAX_ACTIONS) {
        return NULL;
    }
    app_action_t *a = &out->a[out->n++];
    memset(a, 0, sizeof(*a));
    a->kind = k;
    return a;
}

void app_out_deadline(app_out_t *out, app_ms_t t)
{
    if (t < out->deadline) {
        out->deadline = t;
    }
}

void app_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out)
{
    out->n = 0;
    /* The platform keeps only the latest deadline, so every call restates the earliest one any area has
     * pending, whichever areas the event reached: a page's 3 s debounce mustn't be lost to a button edge.
     * Timers are derived from the state: what is due by now runs first. */
    out->deadline = APP_NEVER;
    if (s->booted) {
        alerts_tick(s, now, out);
        model_tick(s, now, out);
        link_tick(s, now, out);
    }

    switch (ev->kind) {
    case APP_EV_BOOT:
        s->boot = *ev->boot;
        s->booted = true;
        model_handle(s, ev, now, out);
        link_handle(s, ev, now, out);
        pairing_handle(s, ev, now, out);
        break;
    case APP_EV_LINE:
        codec_handle(s, ev, now, out);
        break;
    case APP_EV_LINK:
    case APP_EV_WIFI:
        model_handle(s, ev, now, out); /* first: link.c reads the network's new state */
        link_handle(s, ev, now, out);
        break;
    case APP_EV_POWER:
        model_handle(s, ev, now, out);
        break;
    case APP_EV_BUTTON:
    case APP_EV_TOUCH:
        router_handle(s, ev, now, out);
        break;
    case APP_EV_PROV_LINE:
        pairing_handle(s, ev, now, out);
        break;
    case APP_EV_STORE_FAILED:
        pairing_handle(s, ev, now, out);
        model_handle(s, ev, now, out); /* the rest of the failed batch: the render and the settings */
        break;
    case APP_EV_DEADLINE: /* the alerts', model's and link's timers ran above */
        router_handle(s, ev, now, out);
        pairing_handle(s, ev, now, out);
        break;
    }
    model_render(s, now, out);
    app_out_deadline(out, alerts_deadline(s));
    app_out_deadline(out, router_deadline(s, now));
    app_out_deadline(out, link_deadline(s));
    app_out_deadline(out, pairing_deadline(s));
}
