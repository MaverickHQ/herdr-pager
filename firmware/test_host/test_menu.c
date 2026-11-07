/* Menus and confirms: interrupt, jump, navigation, settings, device info, the factory-reset chord; and the
 * link: reconnecting with backoff, the refused and changed-key states. */
#include <stdio.h>
#include <string.h>

#include "app.h"
#include "test_registry.h"
#include "test_rig.h"

#define SHORT 100

static const char *snap_tests(int64_t now, const char *st, uint64_t seq, uint64_t dseq);

/* A BOOT hold of ms from t. */
static void hold_boot(app_ms_t t, app_ms_t ms)
{
    rig_btn(t, APP_BTN_BOOT, true);
    rig_at(t + ms);
    rig_btn(t + ms, APP_BTN_BOOT, false);
}

/* The index of the menu item named item, or -1. */
static int item_index(const char *item)
{
    const app_render_t *r = rig_render();
    for (int i = 0; i < r->n_items; i++) {
        if (strcmp(r->items[i], item) == 0) {
            return i;
        }
    }
    return -1;
}

/* With a menu open, moves to item with BOOT presses from t and selects it with PWR. Returns a later time. */
static app_ms_t pick(app_ms_t t, const char *item)
{
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    int want = item_index(item);
    TEST_ASSERT_GREATER_OR_EQUAL_INT_MESSAGE(0, want, item);
    while (rig_render()->selected_item != want) {
        rig_press(APP_BTN_BOOT, t, SHORT);
        t += 300;
    }
    rig_press(APP_BTN_PWR, t, SHORT);
    return t + 300;
}

static void test_interrupt_asks_first(void)
{
    rig_boot(true);
    rig_snap_quiet(1000); /* tests, working, is selected */

    /* Interrupt is in the working agent's menu, and asks first: each meaning in its button's cell. */
    hold_boot(2000, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    app_ms_t t = pick(4000, "INTERRUPT");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, r->screen);
    TEST_ASSERT_EQUAL_STRING("INTERRUPT tests?", r->title);
    TEST_ASSERT_EQUAL_STRING("YES", r->band.pwr.text);
    TEST_ASSERT_EQUAL_STRING("NO", r->band.boot.text);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());

    /* BOOT is no: nothing is sent. */
    rig_press(APP_BTN_BOOT, t, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());

    /* PWR is yes: interrupt goes, with the agent's pane and seq. */
    hold_boot(t + 1000, 1600);
    t = pick(t + 3000, "INTERRUPT");
    rig_press(APP_BTN_PWR, t, SHORT);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    /* The boot's first request: id 1, pinned (open-items O-38). */
    TEST_ASSERT_EQUAL_STRING("{\"t\":\"interrupt\",\"id\":1,\"pane\":\"w3:p1\",\"seq\":50}", rig_sent(0));

    /* An idle agent's menu doesn't offer it. */
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_press(APP_BTN_BOOT, 1500, SHORT);
    rig_press(APP_BTN_BOOT, 2000, SHORT); /* spike-ble */
    TEST_ASSERT_EQUAL_STRING("spike-ble", rig_render()->rows[rig_render()->selected].name);
    hold_boot(3000, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    TEST_ASSERT_EQUAL_INT(-1, item_index("INTERRUPT"));
}

static void test_unanswered_interrupt_drops_the_link(void)
{
    /* An interrupt the bridge hasn't answered in 8 s: the link is dropped, as for a verdict (test_router.c). */
    rig_boot(true);
    rig_snap_quiet(1000); /* tests, working, is selected */
    hold_boot(2000, 1600);
    app_ms_t t = pick(4000, "INTERRUPT");
    rig_clear_log();
    rig_press(APP_BTN_PWR, t, SHORT); /* yes: the interrupt goes at the release */
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    rig_at(t + SHORT + 7999);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_LINK));
    rig_at(t + SHORT + 8000);
    TEST_ASSERT_EQUAL(APP_LINK_OP_DISCONNECT, rig_last(APP_ACT_LINK)->link_op);
    rig_link(t + SHORT + 8050, APP_LINK_DOWN);
    TEST_ASSERT_EQUAL_STRING("X LINK LOST", rig_render()->title);
}

static void test_jump_sends_jump(void)
{
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_press(APP_BTN_BOOT, 1500, SHORT); /* migrations, w3:p2 */
    hold_boot(2000, 1600);
    pick(4000, "JUMP");
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    TEST_ASSERT_EQUAL_STRING("{\"t\":\"jump\",\"id\":1,\"pane\":\"w3:p2\"}", rig_sent(0)); /* id pinned (O-38) */
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
}

/* JUMP says what happened (the owner's decision, 2026-11-01, open-items O-83): JUMPED when the bridge focused the
 * agent's pane, and X with the reason when it couldn't. Before, the pager said nothing either way. */
static void test_jump_says_what_happened(void)
{
    char line[96];
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_press(APP_BTN_BOOT, 1500, SHORT); /* migrations, w3:p2 */
    hold_boot(2000, 1600);
    pick(4000, "JUMP");
    snprintf(line, sizeof(line), "{\"t\":\"result\",\"id\":%u,\"ok\":true}", (unsigned)rig_sent_id(rig_sent(0)));
    rig_line(4500, line);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("JUMPED", rig_render()->title);

    rig_at(7000); /* the toast's 2 s are up */
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    hold_boot(7000, 1600);
    pick(9000, "JUMP");
    TEST_ASSERT_EQUAL_size_t(2, rig_n_sent());
    snprintf(line, sizeof(line), "{\"t\":\"result\",\"id\":%u,\"ok\":false,\"err\":\"unknown_pane\"}",
             (unsigned)rig_sent_id(rig_sent(1)));
    rig_line(9500, line);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("X GONE", rig_render()->title);

    rig_at(12000); /* the same result again, or one for no request: nothing */
    rig_line(12000, line);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
}

/* The list's cells say only what the buttons do there: OPEN and NEXT with several agents; with one, OPEN and a
 * blank BOOT cell (NEXT would go nowhere); with none, a blank PWR cell and HOLD: MENU under BOOT, as on the
 * link's error screens (the owner's decision, 2026-10-27, open-items O-33). */
static void test_list_cells_say_what_the_buttons_do(void)
{
    rig_boot(true);
    rig_snap_quiet(1000);
    TEST_ASSERT_EQUAL_STRING("OPEN", rig_render()->band.pwr.text);
    TEST_ASSERT_EQUAL_STRING("NEXT", rig_render()->band.boot.text);

    rig_line(2000, snap_tests(1760220002, "working", 50, 0));
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->n_rows);
    TEST_ASSERT_EQUAL_STRING("OPEN", rig_render()->band.pwr.text);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.boot.text);

    rig_fixture(3000, "to-device/valid/snap-empty.ndjson");
    TEST_ASSERT_EQUAL_UINT8(0, rig_render()->n_rows);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.pwr.text);
    TEST_ASSERT_EQUAL_STRING("HOLD: MENU", rig_render()->band.boot.text);
}

static void test_menu_navigation(void)
{
    rig_boot(true);
    rig_snap_quiet(1000); /* tests: working */

    /* The agent menu opens at 1.5 s of BOOT, while it's held: on the deadline, 30 ms after (O-22). */
    rig_btn(2000, APP_BTN_BOOT, true);
    rig_at(3529);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    rig_at(3530);
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    rig_btn(3700, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL_STRING("JUMP", rig_render()->items[0]);
    TEST_ASSERT_EQUAL_STRING("INTERRUPT", rig_render()->items[1]);
    TEST_ASSERT_EQUAL_STRING("SETTINGS", rig_render()->items[2]);
    TEST_ASSERT_EQUAL_UINT8(3, rig_render()->n_items);
    TEST_ASSERT_NOT_NULL(strstr(rig_render()->band.boot.text, "HOLD: EXIT"));

    /* BOOT steps through the items and wraps. */
    for (int i = 1; i <= 4; i++) {
        rig_press(APP_BTN_BOOT, 4000 + 300 * (app_ms_t)i, SHORT);
        TEST_ASSERT_EQUAL_UINT8(i % 3, rig_render()->selected_item);
    }
    /* A 1.5 s hold closes it; nothing was sent. */
    hold_boot(6000, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());

    /* SETTINGS opens the global menu: no snooze, quiet hours or power off in this phase. */
    hold_boot(8000, 1600);
    app_ms_t t = pick(10000, "SETTINGS");
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, item_index("SILENT"));
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, item_index("VOLUME"));
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, item_index("BRIGHTNESS"));
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, item_index("COLOUR"));
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, item_index("DEVICE INFO"));
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, item_index("PAIR AGAIN"));
    for (int i = 0; i < rig_render()->n_items; i++) {
        TEST_ASSERT_NULL(strstr(rig_render()->items[i], "SNOOZE"));
        TEST_ASSERT_NULL(strstr(rig_render()->items[i], "QUIET"));
        TEST_ASSERT_NULL(strstr(rig_render()->items[i], "POWER"));
    }
    hold_boot(t, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);

    /* With no agent to select, the hold opens the global menu itself. */
    rig_fixture(t + 2000, "to-device/valid/snap-empty.ndjson");
    hold_boot(t + 3000, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, item_index("PAIR AGAIN"));
    TEST_ASSERT_EQUAL_INT(-1, item_index("JUMP"));

    /* On a status card the hold opens the card's agent's menu; closing it goes back to the card. */
    rig_boot(true);
    rig_snap_quiet(1000);
    t = rig_open_card(2000, "tests", "to-device/valid/card-status.ndjson");
    hold_boot(t + 1000, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, item_index("INTERRUPT"));
    hold_boot(t + 4000, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
}

/* tests (w3:p1) working at seq, interruptible. */
static const char *snap_tests_working(int64_t now, uint64_t seq)
{
    static char buf[512];
    snprintf(buf, sizeof(buf),
             "{\"t\":\"snap\",\"now\":%lld,\"herdr\":\"ok\",\"more\":0,\"agents\":[{\"pane\":\"w3:p1\",\"name\":\"tests\","
             "\"kind\":\"claude\",\"st\":\"working\",\"seq\":%llu,\"dseq\":4,\"since\":%lld,"
             "\"can\":[\"show\",\"jump\",\"interrupt\"]}]}",
             (long long)now, (unsigned long long)seq, (long long)now);
    return buf;
}

/* The agent menu's items are fixed when it opens. Interrupt checks again, at its select and at YES, that the
 * agent is still working, can be interrupted and has the same seq; otherwise it closes and shows SCREEN CHANGED,
 * and nothing is sent (parent 9.5; the owner's decision, 2026-10-27, open-items O-27). */
static void test_interrupt_checks_again(void)
{
    /* tests turns blocked (seq 51) while its menu is open: INTERRUPT asks nothing. Within 3 s: nothing pages. */
    rig_boot(true);
    rig_snap_quiet(1000); /* tests, working at seq 50, is selected */
    hold_boot(2000, 1600);
    rig_line(4000, snap_tests(1760220004, "blocked", 51, 4));
    app_ms_t t = pick(4100, "INTERRUPT");
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("SCREEN CHANGED", rig_render()->title);
    TEST_ASSERT_EQUAL(0, rig.s.confirm.kind);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());

    /* With INTERRUPT tests? already up, it turns blocked: YES sends nothing. */
    rig_boot(true);
    rig_snap_quiet(1000);
    hold_boot(2000, 1600);
    t = pick(4000, "INTERRUPT");
    TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, rig_render()->screen);
    rig_line(t, snap_tests(1760220005, "blocked", 51, 4));
    rig_press(APP_BTN_PWR, t + 100, SHORT); /* YES */
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    TEST_ASSERT_EQUAL_STRING("SCREEN CHANGED", rig_render()->title);

    /* Still working, but at another seq (it was blocked and back): YES sends nothing either. */
    rig_boot(true);
    rig_snap_quiet(1000);
    hold_boot(2000, 1600);
    t = pick(4000, "INTERRUPT");
    rig_line(t, snap_tests_working(1760220005, 52));
    rig_press(APP_BTN_PWR, t + 100, SHORT);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    TEST_ASSERT_EQUAL_STRING("SCREEN CHANGED", rig_render()->title);

    /* Unchanged, YES interrupts. */
    rig_boot(true);
    rig_snap_quiet(1000);
    hold_boot(2000, 1600);
    t = pick(4000, "INTERRUPT");
    rig_line(t, snap_tests_working(1760220005, 50));
    rig_press(APP_BTN_PWR, t + 100, SHORT);
    TEST_ASSERT_EQUAL_STRING("{\"t\":\"interrupt\",\"id\":1,\"pane\":\"w3:p1\",\"seq\":50}", rig_sent(0));
}

/* From the list, opens the global menu through the agent menu's SETTINGS. */
static app_ms_t open_settings(app_ms_t t)
{
    hold_boot(t, 1600);
    return pick(t + 2000, "SETTINGS");
}

static void test_global_menu_settings(void)
{
    rig_boot(true); /* volume 60, brightness 80 */
    rig_wifi_ip(500, true, "home", "192.168.1.20");
    rig_snap_quiet(1000);

    /* Volume and brightness step on each select and are stored; the new brightness is applied. */
    app_ms_t t = pick(open_settings(2000), "VOLUME");
    TEST_ASSERT_EQUAL_STRING("80", rig_last_store(APP_KEY_VOLUME)->text);
    TEST_ASSERT_EQUAL_STRING("VOLUME 80", rig_render()->title);
    rig_at(t + 2000); /* the toast has gone: the menu is still open */
    t = pick(t + 2000, "BRIGHTNESS");
    TEST_ASSERT_EQUAL_STRING("100", rig_last_store(APP_KEY_BRIGHTNESS)->text);
    TEST_ASSERT_EQUAL_UINT8(100, rig_last(APP_ACT_BACKLIGHT)->level);
    rig_at(t + 2000);
    t = pick(t + 2000, "BRIGHTNESS"); /* it wraps to its lowest step, never to dark */
    TEST_ASSERT_EQUAL_STRING("20", rig_last_store(APP_KEY_BRIGHTNESS)->text);
    TEST_ASSERT_EQUAL_UINT8(20, rig_last(APP_ACT_BACKLIGHT)->level);

    /* Device info: the name, the firmware version and why it started, both full fingerprints, the IP and the
     * bridge's address. */
    rig_at(t + 2000);
    t = pick(t + 2000, "DEVICE INFO");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_INFO, r->screen);
    TEST_ASSERT_EQUAL_STRING("pager-01", r->title);
    TEST_ASSERT_EQUAL_STRING("FW 0.1.0  STARTED: POWER ON", r->lines[0]);
    TEST_ASSERT_EQUAL_STRING("DEV " RIG_DEVICE_FP, r->lines[1]);
    TEST_ASSERT_EQUAL_STRING("HOST " RIG_HOST_FP, r->lines[2]);
    TEST_ASSERT_EQUAL_STRING("IP 192.168.1.20  BRIDGE studio-mac.local:2277", r->lines[3]);
    rig_press(APP_BTN_PWR, t, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);

    /* Pair again asks first (BOOT yes, PWR no): no keeps everything. */
    t = pick(open_settings(t + 1000), "PAIR AGAIN");
    r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, r->screen);
    TEST_ASSERT_EQUAL_STRING("PAIR AGAIN?", r->title);
    TEST_ASSERT_EQUAL_STRING("YES", r->band.boot.text);
    TEST_ASSERT_EQUAL_STRING("NO", r->band.pwr.text);
    rig_clear_log();
    rig_press(APP_BTN_PWR, t, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_LINK));

    /* Yes: pairing mode, with the link dropped and the key pair kept (nothing erased). */
    t = pick(open_settings(t + 1000), "PAIR AGAIN");
    rig_clear_log();
    rig_press(APP_BTN_BOOT, t, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    TEST_ASSERT_EQUAL(APP_LINK_OP_DISCONNECT, rig_last(APP_ACT_LINK)->link_op);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_ERASE));
}

static void test_global_menu_colour(void)
{
    rig_boot(true); /* ORANGE: no colour stored */
    rig_snap_quiet(1000);

    /* COLOUR comes after BRIGHTNESS. Each select turns the palette over, stores it, applies it and says so. */
    app_ms_t t = open_settings(2000);
    TEST_ASSERT_EQUAL_INT(item_index("BRIGHTNESS") + 1, item_index("COLOUR"));
    t = pick(t, "COLOUR");
    TEST_ASSERT_EQUAL_STRING("1", rig_last_store(APP_KEY_COLOUR)->text);
    TEST_ASSERT_EQUAL_UINT8(APP_PALETTE_GREEN, rig_last(APP_ACT_THEME)->level);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("COLOUR GREEN", rig_render()->title);
    rig_at(t + 2000); /* the toast has gone: the menu is still open */
    t = pick(t + 2000, "COLOUR");
    TEST_ASSERT_EQUAL_STRING("0", rig_last_store(APP_KEY_COLOUR)->text);
    TEST_ASSERT_EQUAL_UINT8(APP_PALETTE_ORANGE, rig_last(APP_ACT_THEME)->level);
    TEST_ASSERT_EQUAL_STRING("COLOUR ORANGE", rig_render()->title);
    TEST_ASSERT_EQUAL_size_t(2, rig_count(APP_ACT_THEME));
}

/* One agent, tests (w3:p1), with its status, seq and dseq. */
static const char *snap_tests(int64_t now, const char *st, uint64_t seq, uint64_t dseq)
{
    static char buf[512];
    snprintf(buf, sizeof(buf),
             "{\"t\":\"snap\",\"now\":%lld,\"herdr\":\"ok\",\"more\":0,\"agents\":[{\"pane\":\"w3:p1\",\"name\":\"tests\","
             "\"kind\":\"claude\",\"st\":\"%s\",\"seq\":%llu,\"dseq\":%llu,\"since\":%lld,\"can\":[\"show\",\"jump\"]}]}",
             (long long)now, st, (unsigned long long)seq, (unsigned long long)dseq, (long long)now);
    return buf;
}

static size_t count_done_tones(void)
{
    size_t n = 0;
    for (size_t i = 0; i < rig.n_log; i++) {
        n += rig.log[i].kind == APP_ACT_TONE && rig.log[i].tone == APP_TONE_DONE;
    }
    return n;
}

/* DONE TONE (the acceptance day, 2026-10-25) comes after VOLUME. Each select turns it over, stores it and says
 * so. While it's off, a finished turn makes no sound; the screen still lights. */
static void test_global_menu_done_tone(void)
{
    rig_boot(true); /* on: nothing stored */
    rig_line(1000, snap_tests(1760220001, "working", 50, 0));
    app_ms_t t = open_settings(2000);
    TEST_ASSERT_EQUAL_INT(item_index("VOLUME") + 1, item_index("DONE TONE"));
    t = pick(t, "DONE TONE");
    TEST_ASSERT_EQUAL_STRING("0", rig_last_store(APP_KEY_DONE_TONE)->text);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("DONE TONE OFF", rig_render()->title);

    rig_clear_log();
    rig_line(t, snap_tests(1760220010, "done", 51, 3));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_TONE));
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));

    rig_at(t + 2000); /* the toast has gone: the menu is still open */
    t = pick(t + 2000, "DONE TONE");
    TEST_ASSERT_EQUAL_STRING("1", rig_last_store(APP_KEY_DONE_TONE)->text);
    TEST_ASSERT_EQUAL_STRING("DONE TONE ON", rig_render()->title);
    rig_line(t, snap_tests(1760220020, "working", 52, 3));
    rig_line(t + 1000, snap_tests(1760220021, "done", 53, 4));
    TEST_ASSERT_EQUAL_size_t(1, count_done_tones());
}

/* A stored 0 turns the done tone off from boot; nothing stored is on (rig_boot). */
static void test_boot_applies_stored_done_tone(void)
{
    rig_boot(true);
    rig.boot.done_tone = false;
    app_init(&rig.s);
    app_event_t ev = {.kind = APP_EV_BOOT, .boot = &rig.boot};
    rig_event(0, &ev);
    rig_wifi(0, true, "home");
    rig_link(0, APP_LINK_CONNECTED);
    rig_clear_log();

    rig_line(1000, snap_tests(1760220001, "working", 50, 0));
    rig_line(2000, snap_tests(1760220002, "done", 51, 3));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_TONE));
    pick(open_settings(3000), "DONE TONE");
    TEST_ASSERT_EQUAL_STRING("1", rig_last_store(APP_KEY_DONE_TONE)->text);
}

/* The log's first action of kind, or -1. */
static int first_index(app_action_kind_t kind)
{
    for (size_t i = 0; i < rig.n_log; i++) {
        if (rig.log[i].kind == kind) {
            return (int)i;
        }
    }
    return -1;
}

/* Boots again, paired or not, with colour as the stored palette; the log starts with the boot's actions. As
 * with rig_boot, a paired one then has Wi-Fi and the link up. */
static void boot_colour(bool paired, uint8_t colour)
{
    rig_boot(paired);
    rig.boot.colour = colour;
    app_init(&rig.s);
    rig_clear_log();
    app_event_t ev = {.kind = APP_EV_BOOT, .boot = &rig.boot};
    rig_event(0, &ev);
    if (paired) {
        rig_wifi(0, true, "home");
        rig_link(0, APP_LINK_CONNECTED);
    }
}

static void test_boot_applies_stored_colour(void)
{
    /* GREEN stored: the palette is applied before the first render, paired or not. */
    for (int paired = 0; paired <= 1; paired++) {
        boot_colour(paired, APP_PALETTE_GREEN);
        TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_THEME));
        TEST_ASSERT_EQUAL_UINT8(APP_PALETTE_GREEN, rig_last(APP_ACT_THEME)->level);
        TEST_ASSERT_GREATER_OR_EQUAL_INT(0, first_index(APP_ACT_RENDER));
        TEST_ASSERT_LESS_THAN_INT(first_index(APP_ACT_RENDER), first_index(APP_ACT_THEME));
    }
    /* The menu's next select goes on from it. */
    rig_snap_quiet(1000);
    pick(open_settings(2000), "COLOUR");
    TEST_ASSERT_EQUAL_STRING("0", rig_last_store(APP_KEY_COLOUR)->text);

    /* Nothing stored, or a value that's no palette: ORANGE. */
    boot_colour(true, APP_PALETTE_ORANGE);
    TEST_ASSERT_EQUAL_UINT8(APP_PALETTE_ORANGE, rig_last(APP_ACT_THEME)->level);
    boot_colour(true, APP_PALETTE_COUNT);
    TEST_ASSERT_EQUAL_UINT8(APP_PALETTE_ORANGE, rig_last(APP_ACT_THEME)->level);
    boot_colour(true, 255);
    TEST_ASSERT_EQUAL_UINT8(APP_PALETTE_ORANGE, rig_last(APP_ACT_THEME)->level);
    rig_snap_quiet(1000);
    pick(open_settings(2000), "COLOUR");
    TEST_ASSERT_EQUAL_STRING("1", rig_last_store(APP_KEY_COLOUR)->text);
}

static void boot_brightness(bool paired, uint8_t brightness)
{
    rig_boot(paired);
    rig.boot.brightness = brightness;
    app_init(&rig.s);
    rig_clear_log();
    app_event_t ev = {.kind = APP_EV_BOOT, .boot = &rig.boot};
    rig_event(0, &ev);
    if (paired) {
        rig_wifi(0, true, "home");
        rig_link(0, APP_LINK_CONNECTED);
    }
}

static void test_boot_applies_stored_brightness(void)
{
    /* 40 stored: the backlight is set to it before the first render, paired or not (open-items O-25: it stayed
     * at the kit's default until the first wake, page or change). */
    for (int paired = 0; paired <= 1; paired++) {
        boot_brightness(paired, 40);
        TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));
        TEST_ASSERT_EQUAL_UINT8(40, rig_last(APP_ACT_BACKLIGHT)->level);
        TEST_ASSERT_GREATER_OR_EQUAL_INT(0, first_index(APP_ACT_RENDER));
        TEST_ASSERT_LESS_THAN_INT(first_index(APP_ACT_RENDER), first_index(APP_ACT_BACKLIGHT));
    }
    /* A stored 0 would put the display to sleep: the menu's lowest, 20, and the next select goes on from it. */
    boot_brightness(true, 0);
    TEST_ASSERT_EQUAL_UINT8(20, rig_last(APP_ACT_BACKLIGHT)->level);
    rig_snap_quiet(1000);
    pick(open_settings(2000), "BRIGHTNESS");
    TEST_ASSERT_EQUAL_STRING("40", rig_last_store(APP_KEY_BRIGHTNESS)->text);
}

/* A store that fails stops the rest of its batch on the device (executor.c): the render, and the backlight or
 * palette its setting changed. The core answers APP_EV_STORE_FAILED by drawing again and applying the settings
 * again, keeping the new value for this boot (open-items O-26). */
static void test_store_failed_still_applies(void)
{
    app_event_t failed = {.kind = APP_EV_STORE_FAILED};

    rig_boot(true); /* brightness 80 */
    rig_snap_quiet(1000);
    app_ms_t t = pick(open_settings(2000), "BRIGHTNESS");
    TEST_ASSERT_EQUAL_STRING("100", rig_last_store(APP_KEY_BRIGHTNESS)->text);
    rig_clear_log();
    rig_event(t, &failed);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_RENDER));
    TEST_ASSERT_EQUAL_UINT8(100, rig_last(APP_ACT_BACKLIGHT)->level);
    TEST_ASSERT_EQUAL_STRING("BRIGHTNESS 100", rig_render()->title);

    rig_boot(true);
    rig_snap_quiet(1000);
    t = pick(open_settings(2000), "COLOUR");
    uint8_t colour = rig_last(APP_ACT_THEME)->level;
    rig_clear_log();
    rig_event(t, &failed);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_RENDER));
    TEST_ASSERT_EQUAL_UINT8(colour, rig_last(APP_ACT_THEME)->level);

    /* Silent mode: its store, then the band's render. */
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_press(APP_BTN_PWR, 2000, 2100);
    TEST_ASSERT_TRUE(rig_render()->band.silent);
    rig_clear_log();
    rig_event(4500, &failed);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_RENDER));
    TEST_ASSERT_TRUE(rig_render()->band.silent);
}

static void test_chord_only_when_both_go_down_together(void)
{
    /* PWR down 500 ms after BOOT, before BOOT's first hold threshold (1 s): the chord. Neither button's own
     * hold acts (no menu at 1.5 s, no silent toggle at 2 s); at 10 s with both down, the confirmation. */
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_btn(2000, APP_BTN_BOOT, true);
    rig_btn(2500, APP_BTN_PWR, true);
    rig_at(12529); /* a deadline fires a threshold 30 ms after it (O-22) */
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_FALSE(rig_render()->band.silent);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_STORE));
    rig_at(12530);
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, r->screen);
    TEST_ASSERT_EQUAL_STRING("FACTORY RESET?", r->title);
    TEST_ASSERT_FALSE(r->band.blank); /* paired, the band keeps its status under the question (O-32) */
    TEST_ASSERT_EQUAL_STRING("RESET", r->band.boot.text);
    TEST_ASSERT_EQUAL_STRING("CANCEL", r->band.pwr.text);
    /* Letting go of both answers nothing; PWR then cancels. */
    rig_btn(13000, APP_BTN_BOOT, false);
    rig_btn(13100, APP_BTN_PWR, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, rig_render()->screen);
    rig_press(APP_BTN_PWR, 14000, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_ERASE));

    /* Again, and BOOT resets: everything is erased, the key pair too, and the device restarts unpaired. */
    rig_btn(15000, APP_BTN_PWR, true);
    rig_btn(15900, APP_BTN_BOOT, true);
    rig_at(25900);
    rig_btn(26000, APP_BTN_PWR, false);
    rig_btn(26000, APP_BTN_BOOT, false);
    rig_press(APP_BTN_BOOT, 27000, SHORT);
    TEST_ASSERT_EQUAL_STRING("", rig_last(APP_ACT_ERASE)->key);
    TEST_ASSERT_NOT_NULL(rig_last(APP_ACT_REBOOT));

    /* Let go before 10 s: nothing at all. */
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_btn(2000, APP_BTN_BOOT, true);
    rig_btn(2200, APP_BTN_PWR, true);
    rig_at(7000);
    rig_btn(7000, APP_BTN_PWR, false);
    rig_at(20000);
    rig_btn(20000, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_STORE));
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());

    /* PWR down only after BOOT passed a threshold: BOOT's gesture stands (its menu at 1.5 s), and the PWR
     * press is consumed: no select at its release, no silent toggle at its 2 s. */
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_btn(2000, APP_BTN_BOOT, true);
    rig_at(3530); /* 1.5 s, and the 30 ms a deadline waits for a late edge (O-22) */
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    rig_btn(3600, APP_BTN_PWR, true);
    rig_at(6000);
    rig_btn(6000, APP_BTN_PWR, false);
    rig_btn(6100, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    TEST_ASSERT_FALSE(rig_render()->band.silent);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    rig_at(20000);
    TEST_ASSERT_NOT_EQUAL(APP_SCREEN_CONFIRM, rig_render()->screen);
}

/* The time of the first link-connect action in the log, or APP_NEVER. */
static app_ms_t first_connect(void)
{
    for (size_t i = 0; i < rig.n_log; i++) {
        if (rig.log[i].kind == APP_ACT_LINK && rig.log[i].link_op == APP_LINK_OP_CONNECT) {
            return rig.at[i];
        }
    }
    return APP_NEVER;
}

/* From a paired boot with the given seed: the link drops at 1 s, and each attempt fails 500 ms after it
 * starts. Fills delays[n] with the time from each drop to the next attempt. */
static void backoff_delays(uint32_t seed, app_ms_t *delays, int n)
{
    rig_boot_seed(true, seed);
    app_ms_t down = 1000;
    rig_link(down, APP_LINK_DOWN);
    TEST_ASSERT_EQUAL(APP_LINK_DOWN, rig_render()->band.link);
    for (int i = 0; i < n; i++) {
        rig_clear_log();
        /* Deadline by deadline, so the clock stops at the dial (the attempt's edges come at its time). */
        while (first_connect() == APP_NEVER && rig.out.deadline != APP_NEVER && rig.out.deadline <= down + 40000) {
            rig_at(rig.out.deadline);
        }
        app_ms_t at = first_connect();
        TEST_ASSERT_NOT_EQUAL_MESSAGE(APP_NEVER, at, "no reconnect");
        delays[i] = at - down;
        rig_link(at, APP_LINK_CONNECTING);
        TEST_ASSERT_EQUAL(APP_LINK_CONNECTING, rig_render()->band.link);
        down = at + 500;
        rig_link(down, APP_LINK_DOWN);
    }
}

static void test_reconnect_backoff_with_jitter(void)
{
    /* A paired boot dials once Wi-Fi is up, and says hello when the link connects. */
    rig_boot(true);
    rig_wifi(1000, false, "");
    rig_link(1000, APP_LINK_DOWN); /* the session goes with the network */
    rig_clear_log();
    rig_wifi(2000, true, "home");
    TEST_ASSERT_EQUAL(APP_LINK_OP_CONNECT, rig_last(APP_ACT_LINK)->link_op);
    rig_link(2100, APP_LINK_CONNECTING);
    rig_link(2500, APP_LINK_CONNECTED);
    TEST_ASSERT_EQUAL(APP_LINK_CONNECTED, rig_render()->band.link);
    TEST_ASSERT_EQUAL_STRING("{\"t\":\"hello\",\"v\":1,\"fw\":\"0.1.0\",\"name\":\"pager-01\",\"lines\":4}", rig_sent(0));

    /* 1, 2, 4, 8, 16, then 30 s, each within 20 %. */
    static const app_ms_t base[] = {1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000};
    enum { N = sizeof(base) / sizeof(base[0]) };
    app_ms_t a[N], b[N], c[N];
    backoff_delays(1, a, N);
    bool jittered = false;
    for (int i = 0; i < N; i++) {
        TEST_ASSERT_UINT64_WITHIN_MESSAGE(base[i] / 5, base[i], a[i], "a delay outside its 20 %");
        jittered |= a[i] != base[i];
    }
    TEST_ASSERT_TRUE_MESSAGE(jittered, "no jitter");
    /* The jitter comes from the seed: the same seed, the same delays; another seed, others. */
    backoff_delays(1, b, N);
    TEST_ASSERT_EQUAL_MEMORY(a, b, sizeof(a));
    backoff_delays(2, c, N);
    TEST_ASSERT_TRUE(memcmp(a, c, sizeof(a)) != 0);
    /* At the 30 s cap the jitter only shortens the wait (24-30 s), so a long outage never waits past 30 s
     * and N3's 35 s holds (the owner's decision, 2026-10-27, open-items O-48). */
    for (uint32_t seed = 1; seed <= 40; seed++) {
        app_ms_t d[N];
        backoff_delays(seed, d, N);
        for (int i = 5; i < N; i++) {
            TEST_ASSERT_TRUE_MESSAGE(d[i] <= 30000 && d[i] >= 24000, "a delay at the cap outside 24-30 s");
        }
    }

    /* Connected again, the backoff starts over at 1 s. */
    rig_link(rig.now + 100, APP_LINK_CONNECTED);
    app_ms_t down = rig.now + 1000;
    rig_link(down, APP_LINK_DOWN);
    rig_clear_log();
    rig_at(down + 2000);
    TEST_ASSERT_UINT64_WITHIN(200, 1000, first_connect() - down);

    /* No Wi-Fi: no attempts until it's back. */
    rig_wifi(rig.now, false, "");
    rig_link(rig.now, APP_LINK_DOWN);
    rig_clear_log();
    rig_at(rig.now + 120000);
    TEST_ASSERT_EQUAL_UINT64(APP_NEVER, first_connect());
    rig_wifi(rig.now, true, "home");
    TEST_ASSERT_EQUAL_UINT64(rig.now, first_connect());
}

/* Walks the fake clock deadline by deadline up to until, and returns the first dial's time, or APP_NEVER. */
static app_ms_t next_dial(app_ms_t until)
{
    rig_clear_log();
    while (first_connect() == APP_NEVER && rig.out.deadline != APP_NEVER && rig.out.deadline <= until) {
        rig_at(rig.out.deadline);
    }
    app_ms_t at = first_connect();
    if (at == APP_NEVER) {
        rig_at(until);
    }
    return at;
}

static void test_auth_refused_retry_limit(void)
{
    rig_boot(true);
    rig_snap_quiet(500);
    rig_link(1000, APP_LINK_CONNECTING);
    rig_link(1200, APP_LINK_AUTH_REFUSED);
    TEST_ASSERT_EQUAL(APP_SCREEN_LINK, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("NOT AUTHORISED", rig_render()->title);
    TEST_ASSERT_EQUAL(APP_LINK_AUTH_REFUSED, rig_render()->band.link);

    /* The session ends, and Wi-Fi even comes back: no attempt for 5 minutes. */
    rig_link(1300, APP_LINK_DOWN);
    rig_wifi(60000, false, "");
    rig_wifi(61000, true, "home");
    rig_link(61000, APP_LINK_DOWN);
    TEST_ASSERT_EQUAL(APP_LINK_AUTH_REFUSED, rig_render()->band.link); /* the band keeps the refusal */
    TEST_ASSERT_EQUAL_UINT64(301200, next_dial(400000));

    /* Refused again: 5 minutes again. */
    rig_link(301300, APP_LINK_CONNECTING);
    TEST_ASSERT_EQUAL(APP_LINK_AUTH_REFUSED, rig_render()->band.link); /* not the redial's ~ */
    rig_link(301500, APP_LINK_AUTH_REFUSED);
    rig_link(301600, APP_LINK_DOWN);
    TEST_ASSERT_EQUAL_UINT64(601500, next_dial(700000));

    /* Accepted at last: the list is back, and drops use the backoff again. */
    rig_link(601600, APP_LINK_CONNECTING);
    rig_link(601800, APP_LINK_CONNECTED);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    rig_link(602000, APP_LINK_DOWN);
    TEST_ASSERT_UINT64_WITHIN(200, 603000, next_dial(700000));
}

/* HOST KEY CHANGED keeps its screen, and the link never authenticates to that key (ssh_link aborts first), but it
 * dials again every 5 minutes, so one spoofed reply can't stop the pager for days. When the bridge answers with
 * the pinned key, the state clears (the security pass, 2026-11-01, amending parent 3.3). */
static void test_host_key_changed_retries_every_five_minutes(void)
{
    rig_boot(true);
    rig_snap_quiet(500);
    rig_link(1000, APP_LINK_CONNECTING);
    rig_link(1200, APP_LINK_HOST_KEY_CHANGED);
    TEST_ASSERT_EQUAL(APP_SCREEN_LINK, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("HOST KEY CHANGED - RE-PAIR VIA USB", rig_render()->title);
    TEST_ASSERT_EQUAL(APP_LINK_HOST_KEY_CHANGED, rig_render()->band.link);

    /* The session ends, and Wi-Fi even comes back: no dial for 5 minutes. */
    rig_link(1300, APP_LINK_DOWN);
    TEST_ASSERT_EQUAL(APP_LINK_HOST_KEY_CHANGED, rig_render()->band.link);
    rig_wifi(5000, false, "");
    rig_wifi(6000, true, "home");
    TEST_ASSERT_EQUAL_UINT64(301200, next_dial(400000));
    TEST_ASSERT_EQUAL(APP_SCREEN_LINK, rig_render()->screen);

    /* The changed key again: 5 minutes again, the screen and the band as they were through the dial. */
    rig_link(301300, APP_LINK_CONNECTING);
    TEST_ASSERT_EQUAL(APP_LINK_HOST_KEY_CHANGED, rig_render()->band.link); /* not the redial's ~ */
    rig_link(301500, APP_LINK_HOST_KEY_CHANGED);
    TEST_ASSERT_EQUAL_UINT64(601500, next_dial(700000));

    /* A dial that reaches no bridge at all: 5 minutes again, never at once. */
    rig_link(601600, APP_LINK_CONNECTING);
    rig_link(611600, APP_LINK_DOWN);
    TEST_ASSERT_EQUAL_UINT64(911600, next_dial(1000000));
    TEST_ASSERT_EQUAL_STRING("HOST KEY CHANGED - RE-PAIR VIA USB", rig_render()->title);

    /* The bridge answers with the pinned key: the state clears, the list is back, and drops use the backoff. */
    rig_link(911700, APP_LINK_CONNECTING);
    rig_link(911900, APP_LINK_CONNECTED);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL(APP_LINK_CONNECTED, rig_render()->band.link);
    rig_link(912000, APP_LINK_DOWN);
    TEST_ASSERT_UINT64_WITHIN(200, 913000, next_dial(1000000));

    /* Changed again, then the pinned key answers but refuses the device: NOT AUTHORISED, no longer HOST KEY
     * CHANGED. */
    rig_link(913100, APP_LINK_CONNECTING);
    rig_link(913300, APP_LINK_HOST_KEY_CHANGED);
    TEST_ASSERT_EQUAL_UINT64(1213300, next_dial(1300000));
    rig_link(1213400, APP_LINK_CONNECTING);
    rig_link(1213600, APP_LINK_AUTH_REFUSED);
    TEST_ASSERT_EQUAL_STRING("NOT AUTHORISED", rig_render()->title);
    TEST_ASSERT_EQUAL(APP_LINK_AUTH_REFUSED, rig_render()->band.link);
}

/* PWR held 2.1 s from t: true when it changed nothing (no silent toggle, no store) and the screen is the same. */
static bool pwr_hold_does_nothing(app_ms_t t)
{
    app_screen_t screen = rig_render()->screen;
    bool silent = rig_render()->band.silent;
    rig_clear_log();
    rig_btn(t, APP_BTN_PWR, true);
    rig_at(t + 2100);
    rig_btn(t + 2100, APP_BTN_PWR, false);
    return rig_render()->screen == screen && rig_render()->band.silent == silent && rig_count(APP_ACT_STORE) == 0;
}

/* PWR held 2 s toggles silent only where the parent's button table has it: the list and the cards. In menus,
 * device info, confirmations and the link's error screens it does nothing (the owner's decision, 2026-10-27,
 * open-items O-23). */
static void test_silent_hold_only_on_the_list_and_cards(void)
{
    rig_boot(true);
    rig_snap_quiet(1000); /* tests, working, is selected */
    hold_boot(2000, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    TEST_ASSERT_TRUE_MESSAGE(pwr_hold_does_nothing(4000), "agent menu");

    app_ms_t t = pick(7000, "INTERRUPT");
    TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, rig_render()->screen);
    TEST_ASSERT_TRUE_MESSAGE(pwr_hold_does_nothing(t), "INTERRUPT tests?");
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent()); /* and the held PWR wasn't YES */

    rig_boot(true);
    rig_snap_quiet(1000);
    t = open_settings(2000);
    TEST_ASSERT_TRUE_MESSAGE(pwr_hold_does_nothing(t), "global menu");
    t = pick(t + 3000, "DEVICE INFO");
    TEST_ASSERT_EQUAL(APP_SCREEN_INFO, rig_render()->screen);
    TEST_ASSERT_TRUE_MESSAGE(pwr_hold_does_nothing(t), "device info");

    rig_boot(true);
    rig_snap_quiet(1000);
    t = pick(open_settings(2000), "PAIR AGAIN");
    TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, rig_render()->screen);
    TEST_ASSERT_TRUE_MESSAGE(pwr_hold_does_nothing(t), "PAIR AGAIN?");

    rig_boot(true);
    rig_snap_quiet(500);
    rig_link(1000, APP_LINK_CONNECTING);
    rig_link(1200, APP_LINK_AUTH_REFUSED);
    TEST_ASSERT_TRUE_MESSAGE(pwr_hold_does_nothing(2000), "NOT AUTHORISED");

    /* On the list it still toggles. */
    rig_boot(true);
    rig_snap_quiet(1000);
    TEST_ASSERT_FALSE(pwr_hold_does_nothing(2000));
    TEST_ASSERT_TRUE(rig.s.set.silent);
}

/* The link's error screens' buttons (parent design §Input router; open-items O-16): BOOT short does nothing, PWR
 * shows device info and PWR again goes back, a BOOT hold opens the global menu for PAIR AGAIN, and another closes
 * it. Nothing is sent from either screen. */
static void test_error_screens_buttons(void)
{
    static const struct {
        app_link_t link;
        const char *title;
    } errors[] = {
        {APP_LINK_AUTH_REFUSED, "NOT AUTHORISED"},
        {APP_LINK_HOST_KEY_CHANGED, "HOST KEY CHANGED - RE-PAIR VIA USB"},
    };
    for (size_t k = 0; k < sizeof(errors) / sizeof(errors[0]); k++) {
        rig_boot(true);
        rig_snap_quiet(500);
        rig_link(1000, APP_LINK_CONNECTING);
        rig_link(1200, errors[k].link);
        rig_link(1300, APP_LINK_DOWN);
        rig_clear_log();
        TEST_ASSERT_EQUAL(APP_SCREEN_LINK, rig_render()->screen);
        TEST_ASSERT_EQUAL_STRING("INFO", rig_render()->band.pwr.text);
        TEST_ASSERT_EQUAL_STRING("HOLD: MENU", rig_render()->band.boot.text);

        rig_press(APP_BTN_BOOT, 2000, SHORT);
        TEST_ASSERT_EQUAL_MESSAGE(APP_SCREEN_LINK, rig_render()->screen, errors[k].title);
        TEST_ASSERT_EQUAL_STRING(errors[k].title, rig_render()->title);

        rig_press(APP_BTN_PWR, 3000, SHORT);
        TEST_ASSERT_EQUAL_MESSAGE(APP_SCREEN_INFO, rig_render()->screen, errors[k].title);
        rig_press(APP_BTN_PWR, 4000, SHORT);
        TEST_ASSERT_EQUAL_MESSAGE(APP_SCREEN_LINK, rig_render()->screen, errors[k].title);

        hold_boot(5000, 1600);
        TEST_ASSERT_EQUAL_MESSAGE(APP_SCREEN_MENU, rig_render()->screen, errors[k].title);
        TEST_ASSERT_GREATER_OR_EQUAL_INT_MESSAGE(0, item_index("PAIR AGAIN"), errors[k].title);
        hold_boot(8000, 1600);
        TEST_ASSERT_EQUAL_MESSAGE(APP_SCREEN_LINK, rig_render()->screen, errors[k].title);

        TEST_ASSERT_EQUAL_size_t_MESSAGE(0, rig_n_sent(), errors[k].title);
    }
}

/* NO SOUND (parent design §Error handling): audio that failed at start-up leaves the pager silent, and device
 * info says so after the version (the owner's decision, 2026-10-27, open-items O-49). */
static void test_device_info_says_no_sound(void)
{
    for (int failed = 0; failed <= 1; failed++) {
        rig_boot(true);
        rig.boot.no_sound = failed;
        app_init(&rig.s);
        rig_clear_log();
        app_event_t ev = {.kind = APP_EV_BOOT, .boot = &rig.boot};
        rig_event(0, &ev);
        rig_wifi(0, true, "home");
        rig_link(0, APP_LINK_CONNECTED);
        rig_snap_quiet(1000);
        pick(open_settings(2000), "DEVICE INFO");
        TEST_ASSERT_EQUAL(APP_SCREEN_INFO, rig_render()->screen);
        TEST_ASSERT_EQUAL_STRING(failed ? "FW 0.1.0  NO SOUND  STARTED: POWER ON" : "FW 0.1.0  STARTED: POWER ON",
                                 rig_render()->lines[0]);
    }
}

/* WIFI <ssid> FAILED (parent design §Error handling), in NO WIFI's place: after three refused joins in a row on
 * one network, which a wrong password gives every time and an access point's brief refusals don't; it stays
 * until a join goes through (the owner's decision, 2026-10-27, open-items O-49). */
static void test_wifi_failed_after_three_refusals(void)
{
    rig_boot(true);
    rig_wifi(500, false, "");
    rig_wifi_fail(1000, APP_WIFI_FAIL_REFUSED, "home");
    rig_wifi_fail(2000, APP_WIFI_FAIL_REFUSED, "home");
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.failed); /* two: a brief refusal */
    rig_wifi_fail(2500, APP_WIFI_FAIL_OTHER, "");             /* out of range: the row is broken */
    rig_wifi_fail(3000, APP_WIFI_FAIL_REFUSED, "home");
    rig_wifi_fail(4000, APP_WIFI_FAIL_REFUSED, "home");
    rig_wifi_fail(4500, APP_WIFI_FAIL_REFUSED, "cafe"); /* another network: a row of its own */
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.failed);
    rig_wifi_fail(5000, APP_WIFI_FAIL_REFUSED, "home");
    rig_wifi_fail(6000, APP_WIFI_FAIL_REFUSED, "home");
    rig_wifi_fail(7000, APP_WIFI_FAIL_REFUSED, "home");
    TEST_ASSERT_FALSE(rig_render()->band.wifi);
    TEST_ASSERT_EQUAL_STRING("home", rig_render()->band.failed);

    /* Scanning and connecting again change nothing; another failure doesn't clear it either. */
    rig_wifi(7500, false, "");
    rig_wifi_fail(8000, APP_WIFI_FAIL_OTHER, "");
    TEST_ASSERT_EQUAL_STRING("home", rig_render()->band.failed);

    /* A join goes through: the network's name, and a later failure starts from nothing. */
    rig_wifi(9000, true, "home");
    TEST_ASSERT_TRUE(rig_render()->band.wifi);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.failed);
    rig_wifi(10000, false, "");
    rig_wifi_fail(10500, APP_WIFI_FAIL_REFUSED, "home");
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.failed);
}

/* Device info for a pager away from home (the owner's decision, 2026-10-27, open-items O-77): the FW line ends
 * with why the pager last started, and the IP line with the address it dials. */
static void test_device_info_says_why_it_started_and_where_it_dials(void)
{
    static const struct {
        app_start_t start;
        const char *line;
    } starts[] = {
        {APP_START_POWER_ON, "FW 0.1.0  STARTED: POWER ON"}, {APP_START_RESTART, "FW 0.1.0  STARTED: RESTART"},
        {APP_START_CRASH, "FW 0.1.0  STARTED: CRASH"},       {APP_START_BROWNOUT, "FW 0.1.0  STARTED: BROWNOUT"},
        {APP_START_RESET, "FW 0.1.0  STARTED: RESET"},
    };
    for (size_t k = 0; k < sizeof(starts) / sizeof(starts[0]); k++) {
        rig_boot(true);
        rig.boot.start = starts[k].start;
        app_init(&rig.s);
        app_event_t ev = {.kind = APP_EV_BOOT, .boot = &rig.boot};
        rig_event(0, &ev);
        rig_wifi_ip(0, true, "home", "192.168.1.20");
        rig_link(0, APP_LINK_CONNECTED);
        rig_snap_quiet(1000);
        pick(open_settings(2000), "DEVICE INFO");
        TEST_ASSERT_EQUAL(APP_SCREEN_INFO, rig_render()->screen);
        TEST_ASSERT_EQUAL_STRING(starts[k].line, rig_render()->lines[0]);
        TEST_ASSERT_EQUAL_STRING("IP 192.168.1.20  BRIDGE studio-mac.local:2277", rig_render()->lines[3]);
    }
}

/* The empty list's two lines, the hint cells, and that the band is the list's. */
static void assert_empty(const char *title, const char *line, const char *pwr)
{
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, r->screen);
    TEST_ASSERT_EQUAL_UINT8(0, r->n_rows);
    TEST_ASSERT_EQUAL_STRING(title, r->lines[0]);
    TEST_ASSERT_EQUAL_STRING(line, r->lines[1]);
    TEST_ASSERT_EQUAL_STRING(pwr, r->band.pwr.text);
    TEST_ASSERT_EQUAL_STRING("HOLD: MENU", r->band.boot.text);
}

/* With no agent to show, the list says why (the owner's decision, 2026-10-27, open-items O-77; parent 16.4): on
 * a phone's hotspot without the Mac, a pager that had restarted showed only the band, "~" and the network, over
 * an empty body. On a live link, WAITING FOR HERDR until the first snapshot, HERDR OFFLINE while herdr is down,
 * and NO AGENTS only when herdr's list is empty (O-84, 2026-11-01); NO WIFI without a network; CONNECTING to the
 * bridge's address until a dial on this network fails; then NO BRIDGE ON THIS NETWORK, asking whether the Mac is
 * on it. Joining a network starts again from CONNECTING. */
/* The owner's recheck of 7.2 (B): a saved network in range, refusing a wrong password, showed
 * NO WIFI, "Looking for a saved network". Once it has refused three times (the band's failed row,
 * O-49), the empty list says so, and how to fix it. */
static void test_empty_list_says_a_network_refused_the_password(void)
{
    rig_boot(true);                 /* linked: WAITING FOR HERDR until its link drops (O-84) */
    rig_wifi(1000, false, "");      /* the Wi-Fi goes, as in test_empty_list_says_why, */
    rig_link(1500, APP_LINK_DOWN);  /* and the link with it */
    assert_empty("NO WIFI", "Looking for a saved network", "INFO");
    rig_wifi_fail(2000, APP_WIFI_FAIL_REFUSED, "home");
    rig_wifi_fail(3000, APP_WIFI_FAIL_REFUSED, "home");
    assert_empty("NO WIFI", "Looking for a saved network", "INFO"); /* two: a brief refusal */
    rig_wifi_fail(4000, APP_WIFI_FAIL_REFUSED, "home");
    assert_empty("WRONG WIFI PASSWORD", "home refused it: pair again in SETTINGS", "INFO");
    rig_wifi(5000, true, "home");
    assert_empty("CONNECTING", "to the bridge at studio-mac.local", "INFO");
}

static void test_empty_list_says_why(void)
{
    rig_boot(true); /* linked, and no snapshot yet: not NO AGENTS, which it can't know yet (O-84) */
    assert_empty("WAITING FOR HERDR", "", "");
    rig_link(1000, APP_LINK_DOWN); /* a drop: the next dial may well connect */
    assert_empty("CONNECTING", "to the bridge at studio-mac.local", "INFO");
    rig_link(2000, APP_LINK_CONNECTING);
    assert_empty("CONNECTING", "to the bridge at studio-mac.local", "INFO");
    rig_link(12000, APP_LINK_DOWN); /* that dial failed */
    assert_empty("NO BRIDGE ON THIS NETWORK", "Is the Mac on home too?", "INFO");
    rig_link(14000, APP_LINK_CONNECTING); /* the redials keep it */
    assert_empty("NO BRIDGE ON THIS NETWORK", "Is the Mac on home too?", "INFO");

    rig_wifi(20000, false, "");
    assert_empty("NO WIFI", "Looking for a saved network", "INFO");
    rig_link(21000, APP_LINK_DOWN);
    assert_empty("NO WIFI", "Looking for a saved network", "INFO");
    rig_wifi(30000, true, "iPhone");
    assert_empty("CONNECTING", "to the bridge at studio-mac.local", "INFO");
    rig_link(30100, APP_LINK_CONNECTING);
    rig_link(40100, APP_LINK_DOWN);
    assert_empty("NO BRIDGE ON THIS NETWORK", "Is the Mac on iPhone too?", "INFO");

    /* The list: nothing in the body but its rows. */
    rig_link(70000, APP_LINK_CONNECTING);
    rig_link(70500, APP_LINK_CONNECTED);
    assert_empty("WAITING FOR HERDR", "", "");
    rig_fixture(70600, "to-device/valid/snap-offline.ndjson"); /* herdr is down from the first snapshot (O-84) */
    assert_empty("HERDR OFFLINE", "Is herdr running on the Mac?", "");
    rig_fixture(70700, "to-device/valid/snap-empty.ndjson");
    assert_empty("NO AGENTS", "", "");
    rig_fixture(70800, "to-device/valid/snap-offline.ndjson"); /* and down again, with no agent to freeze */
    assert_empty("HERDR OFFLINE", "Is herdr running on the Mac?", "");
    rig_snap_quiet(71000);
    TEST_ASSERT_EQUAL_UINT8(3, rig_render()->n_rows);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->lines[0]);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->lines[1]);

    /* A list that was there stays, LAST SEEN in the band, through a failed dial. */
    rig_link(72000, APP_LINK_DOWN);
    rig_link(73000, APP_LINK_CONNECTING);
    rig_link(83000, APP_LINK_DOWN);
    TEST_ASSERT_EQUAL_UINT8(3, rig_render()->n_rows);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->lines[0]);
    TEST_ASSERT_EQUAL_STRING("LAST SEEN 0m", rig_render()->title);
}

/* The empty list's buttons while the link is down (O-77): PWR shows device info, and PWR again comes back; BOOT
 * short does nothing; a BOOT hold opens the global menu; PWR held 2 s still turns silent over, as on the list.
 * Nothing is sent. */
static void test_empty_unlinked_list_buttons(void)
{
    rig_boot(true);
    rig_link(1000, APP_LINK_DOWN);
    rig_link(2000, APP_LINK_CONNECTING);
    rig_link(12000, APP_LINK_DOWN);
    rig_clear_log();

    rig_press(APP_BTN_BOOT, 13000, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("NO BRIDGE ON THIS NETWORK", rig_render()->lines[0]);

    rig_press(APP_BTN_PWR, 14000, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_INFO, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("IP -  BRIDGE studio-mac.local:2277", rig_render()->lines[3]);
    rig_press(APP_BTN_PWR, 15000, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);

    hold_boot(16000, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, item_index("DEVICE INFO"));
    hold_boot(19000, 1600);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);

    TEST_ASSERT_FALSE(pwr_hold_does_nothing(22000));
    TEST_ASSERT_TRUE(rig.s.set.silent);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
}

/* OPEN on a list kept from before the link went (LAST SEEN): the card can't come, so X NO LINK at once, as a
 * verdict says (parent 8.7), and nothing is sent; before, the show was dropped without a word (O-77). */
static void test_open_without_a_link_says_no_link(void)
{
    rig_boot(true);
    rig_snap_quiet(500);
    rig_link(1000, APP_LINK_DOWN);
    rig_clear_log();
    rig_press(APP_BTN_PWR, 2000, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("X NO LINK", rig_render()->title);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    rig_at(12000);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_UINT8(3, rig_render()->n_rows);
}

TEST_AREA(menu)
{
    RUN_TEST(test_device_info_says_why_it_started_and_where_it_dials);
    RUN_TEST(test_empty_list_says_why);
    RUN_TEST(test_empty_list_says_a_network_refused_the_password);
    RUN_TEST(test_empty_unlinked_list_buttons);
    RUN_TEST(test_open_without_a_link_says_no_link);
    RUN_TEST(test_device_info_says_no_sound);
    RUN_TEST(test_wifi_failed_after_three_refusals);
    RUN_TEST(test_error_screens_buttons);
    RUN_TEST(test_silent_hold_only_on_the_list_and_cards);
    RUN_TEST(test_auth_refused_retry_limit);
    RUN_TEST(test_host_key_changed_retries_every_five_minutes);
    RUN_TEST(test_reconnect_backoff_with_jitter);
    RUN_TEST(test_chord_only_when_both_go_down_together);
    RUN_TEST(test_global_menu_settings);
    RUN_TEST(test_global_menu_colour);
    RUN_TEST(test_boot_applies_stored_colour);
    RUN_TEST(test_boot_applies_stored_brightness);
    RUN_TEST(test_store_failed_still_applies);
    RUN_TEST(test_global_menu_done_tone);
    RUN_TEST(test_boot_applies_stored_done_tone);
    RUN_TEST(test_menu_navigation);
    RUN_TEST(test_jump_sends_jump);
    RUN_TEST(test_jump_says_what_happened);
    RUN_TEST(test_interrupt_asks_first);
    RUN_TEST(test_interrupt_checks_again);
    RUN_TEST(test_list_cells_say_what_the_buttons_do);
    RUN_TEST(test_unanswered_interrupt_drops_the_link);
}
