/* The app core (phase 2 design D2-1, D2-9; seam S2-6).
 *
 * Every paging, safety and navigation rule of the pager sits behind one call:
 *
 *     app_handle(&state, &event, now, &out);
 *
 * It is pure C: no kit, no LVGL, no FreeRTOS and no clock of its own. Time is
 * the `now` argument, and the core asks for its next call with out.deadline,
 * so the host tests (firmware/test_host, Unity) drive it on a fake clock.
 *
 * Events in: raw lines from the bridge (the codec is inside), link states,
 * Wi-Fi, the power source, raw button edges with timestamps, touch (always
 * ignored), @P provisioning lines, the deadline, and boot (stored settings).
 * Actions out, run in order by the platform (main/platform/executor.c): the
 * render model, tone, flash, backlight, send line, serial reply, storage write
 * and erase, RTC write, reboot, link control and the palette; then the next
 * deadline.
 *
 * The codec, model, alert engine, router and pairing (app_internal.h) are the
 * core's implementation, not separate seams: tests go through app_handle.
 */
#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ---- Limits (parent design §Protocol, §Provisioning; the protocol fixtures' README) ---- */

#define APP_LINE_MAX 8192      /**< longest bridge line, not counting its \n */
#define APP_SEND_MAX 512       /**< longest line to the bridge, not counting its \n */
#define APP_PROV_LINE_MAX 512  /**< longest @P line, its "@P " included */
#define APP_MAX_AGENTS 16
#define APP_PANE_MAX 32
#define APP_NAME_MAX 24
#define APP_KIND_MAX 10
#define APP_AT_MAX 16 /**< where an agent works, its folder's name (a snapshot entry's optional `at`) */
#define APP_TEXT_MAX 40        /**< a card's summary or line; one 40-column row */
#define APP_CARD_LINES 4 /**< a card's lines: an MCP card's tool and 3 arguments (O-90); the body shows 4 */
#define APP_LABEL_MAX 32       /**< a danger card's label (`rl`) */
#define APP_HASH_LEN 64
#define APP_FW_MAX 32
#define APP_HOST_NAME_MAX 64
#define APP_SSID_MAX 32
#define APP_KEY_MAX 95         /**< "ssh-ed25519 <base64>" is 80 bytes */
#define APP_STORE_KEY_MAX 15   /**< a storage key (NVS's limit) */

/* ---- Time ---- */

/** Milliseconds on the device's monotonic clock. */
typedef uint64_t app_ms_t;

/** out.deadline when the core needs no call before the next event. */
#define APP_NEVER UINT64_MAX

/* ---- Storage keys (namespace "pager", parent design §Data models) ----
 * The core writes them with APP_ACT_STORE; the platform reads them at boot. */

#define APP_NVS_NS "pager"
#define APP_KEY_NAME "name"
#define APP_KEY_HOST "host"
#define APP_KEY_PORT "port"
#define APP_KEY_HOST_KEY "host_key"
#define APP_KEY_HOST_NAME "host_name"
#define APP_KEY_WIFI_N "wifi_n"      /**< then wifi0_ssid, wifi0_pass ... wifi3_pass */
#define APP_KEY_VOLUME "volume"
#define APP_KEY_BRIGHTNESS "brightness"
#define APP_KEY_SILENT "silent"
#define APP_KEY_COLOUR "colour"      /**< the palette's number (app_palette_t) */
#define APP_KEY_DONE_TONE "done_tone" /**< 1 or 0: the done tone plays (parent 6.2); 1 when not stored */

/** The screen's colour palettes (their colours are platform/theme.h's). */
typedef enum {
    APP_PALETTE_ORANGE, /**< the default, and what a missing or bad stored value means */
    APP_PALETTE_GREEN,
    APP_PALETTE_COUNT,
} app_palette_t;

/* ---- Events ---- */

typedef enum {
    APP_BTN_BOOT = 0,
    APP_BTN_PWR = 1,
} app_btn_t;

typedef enum {
    APP_LINK_DOWN = 0,
    APP_LINK_CONNECTING,
    APP_LINK_CONNECTED,
    APP_LINK_AUTH_REFUSED,     /**< the bridge refused the device's key: NOT AUTHORISED */
    APP_LINK_HOST_KEY_CHANGED, /**< the bridge's key isn't the pinned one: terminal */
} app_link_t;

typedef enum {
    APP_POWER_UNKNOWN = 0,
    APP_POWER_USB,
    APP_POWER_BATTERY,
} app_power_t;

/** What a Wi-Fi status says of a join. */
typedef enum {
    APP_WIFI_FAIL_NONE = 0, /**< no failed join: connected, scanning, connecting, or stopped */
    APP_WIFI_FAIL_REFUSED,  /**< the access point refused the join: a wrong password, or a brief refusal */
    APP_WIFI_FAIL_OTHER,    /**< no saved network in range, or the joined one lost */
} app_wifi_fail_t;

/** Why the pager started this time (device info's STARTED; the kit's boot reason). */
typedef enum {
    APP_START_POWER_ON = 0, /**< switched on */
    APP_START_RESTART,      /**< the firmware restarted itself: after pairing, or a factory reset */
    APP_START_CRASH,        /**< a panic or a watchdog */
    APP_START_BROWNOUT,     /**< the supply dipped too low: a flat battery, most likely */
    APP_START_RESET,        /**< anything else: a reset over USB, say */
} app_start_t;

/** What the device has stored, given once at boot. */
typedef struct {
    bool paired;                                /**< a host and its key are stored */
    char name[APP_NAME_MAX + 1];                /**< the device's name ("" when unpaired) */
    char fw[APP_FW_MAX + 1];                    /**< the firmware's version */
    char pubkey[APP_KEY_MAX + 1];               /**< the device's public key, "ssh-ed25519 <base64>" */
    char host_key[APP_KEY_MAX + 1];             /**< the bridge's pinned key ("" when unpaired) */
    char host_name[APP_HOST_NAME_MAX + 1];      /**< the Mac's name, for device info */
    char host[APP_HOST_NAME_MAX + 1];           /**< the address the pager dials ("" when unpaired) */
    uint16_t port;                              /**< ... and its port */
    uint8_t volume;                             /**< 0-100 */
    uint8_t brightness;                         /**< 0-100 */
    bool silent;
    uint8_t colour;                             /**< app_palette_t, as stored; anything else is ORANGE */
    bool done_tone;                             /**< the done tone plays */
    int64_t rtc_s;                              /**< the RTC at boot, Unix seconds; 0 when unknown */
    uint32_t seed;                              /**< seeds the reconnect jitter */
    bool no_sound;                              /**< the speaker's audio failed at start-up: device info says
                                                     NO SOUND */
    app_start_t start;                          /**< why the pager started */
} app_boot_t;

typedef enum {
    APP_EV_BOOT,         /**< .boot: once, first */
    APP_EV_LINE,         /**< .line: a line from the bridge */
    APP_EV_LINK,         /**< .link: the link's new state */
    APP_EV_WIFI,         /**< .wifi */
    APP_EV_POWER,        /**< .power: the power source */
    APP_EV_BUTTON,       /**< .button: a raw, debounced edge */
    APP_EV_TOUCH,        /**< .touch: a sample, which never acts (R1.3) */
    APP_EV_PROV_LINE,    /**< .line: a console line starting "@P " */
    APP_EV_DEADLINE,     /**< the deadline the core asked for has come */
    APP_EV_STORE_FAILED, /**< an APP_ACT_STORE or APP_ACT_ERASE failed; the rest of its batch didn't run */
} app_event_kind_t;

typedef struct {
    app_event_kind_t kind;
    union {
        const app_boot_t *boot;
        struct {
            const char *data; /**< not NUL-terminated, and may hold NUL bytes */
            size_t len;       /**< without the \n. A bridge line longer than APP_LINE_MAX
                                   arrives cut to APP_LINE_MAX + 1 bytes. */
        } line;
        app_link_t link;
        struct {
            bool connected;              /**< associated, with an address */
            char ssid[APP_SSID_MAX + 1]; /**< the network in use, the one that refused a join, or "" */
            char ip[16];                 /**< its IPv4 address, dotted, or "" (device info) */
            app_wifi_fail_t fail;        /**< a join that just failed, and how */
        } wifi;
        app_power_t power;
        struct {
            app_btn_t btn;
            bool down;
            app_ms_t t; /**< when the edge happened, on the same clock as now */
        } button;
        struct {
            uint16_t x, y;
            bool pressed;
        } touch;
    };
} app_event_t;

/* ---- Actions ---- */

typedef enum {
    APP_ACT_RENDER,    /**< draw app_render(state) */
    APP_ACT_TONE,      /**< play .tone at volume .level */
    APP_ACT_FLASH,     /**< flash the screen: invert it 3 times at 150 ms */
    APP_ACT_BACKLIGHT, /**< .level 0-100; 0 turns the display off (it sleeps) */
    APP_ACT_SEND,      /**< send .text (one line, no \n) to the bridge */
    APP_ACT_SERIAL,    /**< write .text and \n to the serial console (an @P reply) */
    APP_ACT_STORE,     /**< store the string .text under .key; never logged */
    APP_ACT_ERASE,     /**< erase .key, or every key when .key is "" (factory reset) */
    APP_ACT_RTC,       /**< set the RTC to .unix_s */
    APP_ACT_REBOOT,
    APP_ACT_LINK,      /**< .link_op */
    APP_ACT_THEME,     /**< recolour the screen in palette .level (app_palette_t) */
} app_action_kind_t;

/** Tone patterns (parent design §Firmware components). */
typedef enum {
    APP_TONE_BLOCKED, /**< "knock-knock" */
    APP_TONE_DONE,    /**< "ta-da" */
    APP_TONE_DANGER,  /**< when a danger card opens */
    APP_TONE_CLICK,
} app_tone_t;

typedef enum {
    APP_LINK_OP_CONNECT,    /**< dial the stored host now; states follow as APP_EV_LINK */
    APP_LINK_OP_DISCONNECT,
} app_link_op_t;

#define APP_MAX_ACTIONS 20

typedef struct {
    app_action_kind_t kind;
    app_tone_t tone;
    uint8_t level;
    app_link_op_t link_op;
    int64_t unix_s;
    char key[APP_STORE_KEY_MAX + 1];
    size_t len;                     /**< of .text */
    char text[APP_SEND_MAX + 1];    /**< NUL-terminated as well */
} app_action_t;

typedef struct {
    size_t n;
    app_action_t a[APP_MAX_ACTIONS];
    app_ms_t deadline; /**< the core's next deadline, replacing any earlier one: call app_handle
                            with APP_EV_DEADLINE at this time unless another event comes first.
                            APP_NEVER for none. */
} app_out_t;

/* ---- The render model: what the screen shows ----
 * Every screen has two bands (design D2-10): the status band on top, with the
 * status on the left and a hint cell under each button on the right (PWR,
 * then BOOT), and a 144 px body. `band` is the status band, on every screen
 * (ui/screen_status.c draws it); `screen` picks the body. Text is printable
 * ASCII only. */

typedef enum {
    APP_SCREEN_BOOT,    /**< starting, or unpaired: PAIR VIA USB */
    APP_SCREEN_LIST,
    APP_SCREEN_CARD,
    APP_SCREEN_MENU,
    APP_SCREEN_CONFIRM,
    APP_SCREEN_TOAST,
    APP_SCREEN_INFO,
    APP_SCREEN_PAIRING,
    APP_SCREEN_LINK,    /**< NOT AUTHORISED, HOST KEY CHANGED (NO WIFI is the band's, and the empty list's) */
    APP_SCREEN_COUNT,
} app_screen_t;

#define APP_HINT_MAX 31      /**< a hint cell: up to two lines, '\n' between */
#define APP_BODY_LINES 4
#define APP_BODY_LINE_MAX 63
#define APP_MENU_MAX 8
#define APP_ITEM_MAX 20

typedef struct {
    char text[APP_HINT_MAX + 1];
    uint16_t fill_permille; /**< a hold's progress, filling the cell; 0 = none */
} app_hint_t;

typedef struct {
    uint8_t blocked, done, working;  /**< the counts */
    bool all_clear;                  /**< on a live list, nothing blocked or done unseen: ALL CLEAR (O-85) */
    char agent[APP_NAME_MAX + 1];    /**< an open card's agent's name, shown in the counts' place; "" for the counts */
    char clock[6];                   /**< "HH:MM", or "" while the time is unknown */
    app_link_t link;
    bool wifi;                       /**< false: NO WIFI */
    char ssid[APP_SSID_MAX + 1];
    char failed[APP_SSID_MAX + 1];   /**< while there's no Wi-Fi: the network a wrong password keeps refusing,
                                          shown as WIFI <ssid> FAILED in NO WIFI's place; "" for NO WIFI */
    bool herdr_offline;
    bool on_battery;                 /**< ON BATTERY (R1.1) */
    bool silent;
    bool blank;                      /**< no status at all, only the hint cells: the pairing screens and the
                                          factory-reset question over them (open-items O-32) */
    app_hint_t pwr, boot;            /**< the hint cells */
} app_band_t;

typedef struct {
    char glyph;                      /**< ! blocked, * done unseen, ? unknown, > working, . idle */
    char name[APP_NAME_MAX + 1];
    char kind[APP_AT_MAX + 1];       /**< where it works (at), else its kind */
    char age[6];                     /**< "3m", "2h" */
    bool dim;                        /**< a seen done agent */
    bool paged;                      /**< highlighted by a page */
} app_row_t;

typedef struct {
    app_screen_t screen;
    app_band_t band;
    /* The body: each screen uses the fields it needs. */
    char banner[APP_TEXT_MAX + 1];                        /**< a danger card's banner, the body's first row */
    char title[APP_BODY_LINE_MAX + 1];
    char lines[APP_BODY_LINES][APP_BODY_LINE_MAX + 1];
    app_row_t rows[APP_MAX_AGENTS];
    uint8_t n_rows, first_row, selected;
    uint32_t more;                                        /**< agents beyond the 16 */
    char items[APP_MENU_MAX][APP_ITEM_MAX + 1];
    uint8_t n_items, selected_item;
} app_render_t;

/* ---- The core ---- */

/* The core's own state. Callers allocate it (statically) and read only app_render(). */

typedef enum {
    APP_ST_IDLE,
    APP_ST_WORKING,
    APP_ST_BLOCKED,
    APP_ST_DONE,
    APP_ST_UNKNOWN,
} app_st_t;

/* The verbs a snapshot entry or a card offers (`can`). */
#define APP_CAN_SHOW 0x01
#define APP_CAN_APPROVE 0x02
#define APP_CAN_DENY 0x04
#define APP_CAN_INTERRUPT 0x08
#define APP_CAN_JUMP 0x10

/** A snapshot entry; its text already filtered to printable ASCII. */
typedef struct {
    char pane[APP_PANE_MAX + 1];
    char name[APP_NAME_MAX + 1];
    char kind[APP_KIND_MAX + 1];
    char at[APP_AT_MAX + 1]; /**< where it works, its folder's name, or "" */
    uint8_t st;    /**< app_st_t */
    uint8_t can;   /**< APP_CAN_* */
    uint64_t seq;  /**< state_change_seq */
    uint64_t dseq; /**< completion_seq, or 0 */
    int64_t since; /**< Unix s, when the bridge first saw seq */
} app_agent_t;

typedef enum {
    APP_CARD_PERMISSION,
    APP_CARD_QUESTION,
    APP_CARD_STATUS,
} app_card_kind_t;

/** A card from the bridge; its text already filtered. */
typedef struct {
    uint32_t id;
    char pane[APP_PANE_MAX + 1];
    uint8_t st;   /**< app_st_t */
    uint8_t kind; /**< app_card_kind_t */
    uint8_t can;
    bool danger;
    uint64_t seq;
    char hash[APP_HASH_LEN + 1];
    char rl[APP_LABEL_MAX + 1];
    char summary[APP_TEXT_MAX + 1];
    uint8_t n_lines;
    char lines[APP_CARD_LINES][APP_TEXT_MAX + 1];
} app_card_t;

/** A (pane, seq) pair, for the seen, acknowledged and paged sets. */
typedef struct {
    char pane[APP_PANE_MAX + 1];
    uint64_t seq;
} app_mark_t;

#define APP_MARKS 64

/** A set of marks, as a ring: the oldest is forgotten first. */
typedef struct {
    app_mark_t m[APP_MARKS];
    uint8_t n, next;
} app_marks_t;

/** A blocked agent's (pane, seq), as the alert engine watches it. */
typedef struct {
    char pane[APP_PANE_MAX + 1];
    uint64_t seq;
    app_ms_t first;     /**< first seen blocked with this seq */
    bool paged;         /**< its page has fired */
    app_ms_t paged_at;  /**< when (or when seen again, after a reconnect) */
    app_ms_t last_tone; /**< its page or last reminder */
} app_watch_t;

/** A button's press in progress. */
typedef struct {
    bool down;
    bool consumed;   /**< the rest of this press does nothing */
    uint8_t reached; /**< how many of its hold thresholds have passed */
    app_ms_t t_down;
} app_press_t;

typedef struct {
    app_render_t render;
    app_boot_t boot;
    bool booted;

    /* model.c: the latest snapshot, in the bridge's order, and the device's own marks. */
    struct {
        app_agent_t agents[APP_MAX_AGENTS];
        uint8_t n;
        uint32_t more;
        bool herdr_offline;
        bool have;        /**< a snapshot has come */
        app_ms_t snap_at; /**< when the latest came: how old the list is while the link is down */
        app_marks_t seen; /**< (pane, dseq) of done agents whose card was shown */
    } model;

    /* The open card, if any, and the request a card must answer to be shown. */
    struct {
        bool open;
        app_card_t c;
        app_ms_t shown_at;
        app_ms_t idle_from; /**< shown, or the last button edge since: it closes 60 s after */
        uint32_t want_id; /**< the latest show's id; 0 for none */
        app_ms_t want_at; /**< when the first show still unanswered went: asking again keeps it */
    } card;

    /* codec.c: the id of the next request to the bridge (from 1). */
    uint32_t next_id;

    /* The verdict sent from the open card, awaiting its result. */
    struct {
        uint32_t id;    /**< 0: none */
        bool deny;      /**< it was a deny, not an approve */
        bool interrupt; /**< it was an interrupt, from the menu */
        app_ms_t sent_at;
    } verdict;

    /* menu.c: the open menu: its items (menu.c's item ids), and the agent it's for. */
    struct {
        bool open;
        bool info; /**< device info is shown */
        uint8_t n, selected;
        uint8_t items[APP_MENU_MAX];
        char pane[APP_PANE_MAX + 1];
        uint64_t seq; /**< the agent's seq when its menu opened: INTERRUPT checks it (open-items O-27) */
        uint32_t jump_id; /**< a JUMP's request until its result, which a toast reports (open-items O-83); 0: none */
    } menu;

    /* menu.c: the open confirm, and what it would do. */
    struct {
        uint8_t kind; /**< menu.c's confirm kinds; 0: none */
        char pane[APP_PANE_MAX + 1];
        char name[APP_NAME_MAX + 1];
        uint64_t seq;
    } confirm;

    /* The link, the network and the power source, as last reported. */
    struct {
        app_link_t link;
        bool wifi;
        char ssid[APP_SSID_MAX + 1];
        char ip[16];
        app_power_t power;
        char refused[APP_SSID_MAX + 1]; /**< the network refusing joins in a row ... */
        uint8_t refusals;               /**< ... and how many times */
        char failed[APP_SSID_MAX + 1];  /**< a network that refused three in a row, until a join goes through */
        bool dial_failed; /**< a dial since Wi-Fi joined, or since the link last connected, didn't connect */
    } net;

    /* Settings, from boot and the menu. */
    struct {
        uint8_t volume, brightness;
        bool silent;
        uint8_t colour; /**< app_palette_t */
        bool done_tone;
    } set;

    /* alerts.c: each blocked agent's (pane, seq), watched from when it was first seen. */
    struct {
        app_watch_t watch[APP_MAX_AGENTS];
        uint8_t n;
        app_marks_t paged; /**< every (pane, seq) paged: one page each, across reconnects */
        app_marks_t acked; /**< every (pane, seq) whose card was shown: no more reminders */
        app_marks_t chimed; /**< every (pane, dseq) done that chimed, or was done at boot: one tone each */
    } alerts;

    /* alerts.c: the backlight. off_at is when it goes off, 0 for no limit yet. */
    struct {
        bool awake;
        bool redraw; /**< the render model changed while asleep */
        app_ms_t off_at;
    } disp;

    /* router.c: each button's press in progress. */
    struct {
        app_press_t btn[2];
        bool locked; /**< a press woke the screen: every press is consumed until both buttons are up */
        bool chord;  /**< both went down together: the factory-reset chord is being held */
        app_ms_t chord_at;
    } router;

    /* The list's selection, kept on its agent across snapshots. */
    struct {
        uint8_t selected, first_row;
        char pane[APP_PANE_MAX + 1];
    } sel;

    /* link.c: when to dial the bridge again. */
    struct {
        uint8_t attempt;         /**< drops since the link last connected */
        app_ms_t next_at;        /**< the next dial; 0 for none pending */
        uint32_t rng;            /**< the jitter's state, from boot's seed */
        bool refused;            /**< NOT AUTHORISED, until the link connects */
        app_ms_t refused_until;  /**< no dial before this (5 minutes after a refusal or a changed host key) */
        bool host_key_changed;   /**< HOST KEY CHANGED, until the pinned key answers (dials every 5 minutes) */
        bool held_dialled;       /**< a dial went at refused_until: a DOWN after it waits 5 minutes more */
    } link_ctl;

    /* A confirmation over the screen, until its time. */
    struct {
        char text[APP_BODY_LINE_MAX + 1];
        app_ms_t until;
    } toast;

    /* The wall clock: Unix time unix_s at the core's time at_ms. */
    struct {
        bool known;
        int64_t unix_s;
        app_ms_t at_ms;
    } clock;
} app_state_t;

/** Puts s in its power-on state. Then send APP_EV_BOOT. */
void app_init(app_state_t *s);

/** Handles one event at time now: fills out with the actions to run, in order, and the next deadline. */
void app_handle(app_state_t *s, const app_event_t *ev, app_ms_t now, app_out_t *out);

/** What the screen shows now. */
const app_render_t *app_render(const app_state_t *s);

#ifdef __cplusplus
}
#endif
