/* The input router: gestures from raw edges, the card guard, holds, the pocket fuzz, touch. */
#include <stdio.h>
#include <string.h>

#include "app.h"
#include "test_registry.h"
#include "test_rig.h"

#define SHORT 100 /* ms: a short press */

static void test_boot_short_moves_selection_and_wraps(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL_UINT8(0, r->selected);
    TEST_ASSERT_EQUAL_UINT8(0, r->first_row);
    TEST_ASSERT_NOT_NULL(strstr(r->band.boot.text, "NEXT"));

    /* Four rows fit the body: the view scrolls one row at a time to keep the selection in it. */
    static const struct {
        uint8_t selected, first_row;
    } want[] = {{1, 0}, {2, 0}, {3, 0}, {4, 1},   {5, 2},   {6, 3},   {7, 4},   {8, 5},
                {9, 6}, {10, 7}, {11, 8}, {12, 9}, {13, 10}, {14, 11}, {15, 12}, {0, 0}};
    app_ms_t t = 2000;
    for (int i = 0; i < 16; i++, t += 1000) {
        rig_press(APP_BTN_BOOT, t, SHORT);
        TEST_ASSERT_EQUAL_UINT8_MESSAGE(want[i].selected, rig_render()->selected, "selected");
        TEST_ASSERT_EQUAL_UINT8_MESSAGE(want[i].first_row, rig_render()->first_row, "first_row");
    }

    /* The selection stays on its agent when a snapshot re-sorts the list. */
    rig_press(APP_BTN_BOOT, t, SHORT); /* api-refactor -> release */
    TEST_ASSERT_EQUAL_STRING("release", rig_render()->rows[rig_render()->selected].name);
    rig_line(t + 1000, "{\"t\":\"snap\",\"now\":1760220010,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                       "{\"pane\":\"w1:p5\",\"name\":\"release\",\"kind\":\"claude\",\"st\":\"blocked\",\"seq\":9,"
                       "\"dseq\":0,\"since\":1760219950,\"can\":[\"show\",\"jump\"]},"
                       "{\"pane\":\"w9:p1\",\"name\":\"newer\",\"kind\":\"claude\",\"st\":\"blocked\",\"seq\":60,"
                       "\"dseq\":0,\"since\":1760210000,\"can\":[\"show\",\"jump\"]}]}");
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->selected);
    TEST_ASSERT_EQUAL_STRING("release", rig_render()->rows[1].name);

    /* When the selected agent goes, the selection stays in the list. */
    rig_line(t + 2000, "{\"t\":\"snap\",\"now\":1760220011,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                       "{\"pane\":\"w9:p1\",\"name\":\"newer\",\"kind\":\"claude\",\"st\":\"blocked\",\"seq\":60,"
                       "\"dseq\":0,\"since\":1760210000,\"can\":[\"show\",\"jump\"]}]}");
    TEST_ASSERT_EQUAL_UINT8(0, rig_render()->selected);
}

static void test_viewing_list_marks_nothing(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson");
    /* Through the whole list twice, resting on each done agent, with snapshots arriving. */
    app_ms_t t = 2000;
    for (int i = 0; i < 40; i++, t += 700) {
        rig_press(APP_BTN_BOOT, t, SHORT);
        if (i % 5 == 0) {
            rig_fixture(t + 300, "to-device/valid/snap-16-agents-more.ndjson");
        }
    }
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL_UINT8(2, r->band.done);
    TEST_ASSERT_EQUAL_CHAR('*', r->rows[3].glyph);
    TEST_ASSERT_EQUAL_CHAR('*', r->rows[4].glyph);
    TEST_ASSERT_FALSE(r->rows[3].dim);
    TEST_ASSERT_FALSE(r->rows[4].dim);
    TEST_ASSERT_EQUAL_STRING("docs-pass", r->rows[3].name);
    /* Nothing went to the bridge: no card was asked for, so nothing was acknowledged either. */
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
}

static void test_wake_press_consumed_whole(void)
{
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_at(40000); /* asleep */
    TEST_ASSERT_EQUAL_UINT8(0, rig_last(APP_ACT_BACKLIGHT)->level);

    /* BOOT wakes the screen, and its press, held through 1 s, 1.5 s and 2 s, then released, does nothing else. */
    rig_clear_log();
    rig_btn(50000, APP_BTN_BOOT, true);
    TEST_ASSERT_EQUAL_UINT8(80, rig_last(APP_ACT_BACKLIGHT)->level);
    TEST_ASSERT_NOT_NULL(rig_last(APP_ACT_RENDER));
    rig_at(52500);
    rig_btn(52500, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_UINT8(0, rig_render()->selected);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_STORE));
    /* The next press acts. */
    rig_press(APP_BTN_BOOT, 53000, 100);
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->selected);

    /* PWR wakes it: held past 2 s it doesn't toggle silent, and a BOOT press made while it's held does nothing
     * either. Nothing acts until both buttons are up again. */
    rig_at(100000);
    rig_clear_log();
    rig_btn(100000, APP_BTN_PWR, true);
    TEST_ASSERT_EQUAL_UINT8(80, rig_last(APP_ACT_BACKLIGHT)->level);
    rig_press(APP_BTN_BOOT, 100500, 100);
    rig_at(103000);
    TEST_ASSERT_FALSE(rig_render()->band.silent);
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->selected);
    rig_btn(103000, APP_BTN_BOOT, true); /* still while PWR is down */
    rig_btn(103100, APP_BTN_PWR, false);
    rig_btn(103200, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->selected);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_STORE));
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    rig_press(APP_BTN_BOOT, 104000, 100);
    TEST_ASSERT_EQUAL_UINT8(2, rig_render()->selected);
}

static void test_pwr_short_requests_card(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson"); /* the three blocked agents page at 4 s */
    TEST_ASSERT_NOT_NULL(strstr(rig_render()->band.pwr.text, "OPEN"));

    /* release (w1:p5) paged at 4,000 ms; PWR's release at 6,100 ms opens its card: pg is the 2,100 ms between. */
    rig_press(APP_BTN_BOOT, 5000, SHORT);
    rig_clear_log();
    rig_press(APP_BTN_PWR, 6000, SHORT);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    TEST_ASSERT_EQUAL_STRING("{\"t\":\"show\",\"id\":1,\"pane\":\"w1:p5\",\"pg\":2100}", rig_sent(0));

    /* The list stays until the card arrives; a card answering another request is ignored. */
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    rig_card(6500, "to-device/valid/card-danger.ndjson", 7);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    rig_card(6600, "to-device/valid/card-danger.ndjson", 1);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);

    /* An agent that never paged: pg -1, and the next id. */
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_press(APP_BTN_BOOT, 2000, SHORT);
    rig_press(APP_BTN_BOOT, 3000, SHORT); /* spike-ble, w4:p1 */
    rig_press(APP_BTN_PWR, 4000, SHORT);
    rig_press(APP_BTN_PWR, 5000, SHORT); /* asked again before the card came */
    TEST_ASSERT_EQUAL_size_t(2, rig_n_sent());
    TEST_ASSERT_EQUAL_STRING("{\"t\":\"show\",\"id\":1,\"pane\":\"w4:p1\",\"pg\":-1}", rig_sent(0));
    TEST_ASSERT_EQUAL_STRING("{\"t\":\"show\",\"id\":2,\"pane\":\"w4:p1\",\"pg\":-1}", rig_sent(1));
}

/* From a fresh core with rig_snap_quiet's list: opens the card of `tests` (w3:p1, working), shown at time at. */
static void status_card_at(app_ms_t at)
{
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_press(APP_BTN_PWR, at - 1000, SHORT);
    rig_card(at, "to-device/valid/card-status.ndjson", rig_sent_id(rig_sent(rig_n_sent() - 1)));
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
}

/* From a fresh core: api-refactor (w1:p3, seq 41) blocked; its card (the fixture rel) shown at time at. */
static void permission_card_at(app_ms_t at, const char *rel)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_press(APP_BTN_PWR, at - 1000, SHORT);
    rig_card(at, rel, rig_sent_id(rig_sent(rig_n_sent() - 1)));
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    rig_clear_log();
}

static void test_card_guard_500ms(void)
{
    /* A press beginning 499 ms after the card appears is ignored, its release (at 700 ms) included. */
    status_card_at(3000);
    rig_press(APP_BTN_PWR, 3499, 201);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    rig_btn(3800, APP_BTN_BOOT, true);
    rig_btn(3900, APP_BTN_BOOT, false); /* a later press is the card's (on a status card, BOOT is back) */
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);

    /* One beginning at 500 ms counts: on a status card, PWR goes back. */
    status_card_at(3000);
    rig_press(APP_BTN_PWR, 3500, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);

    /* A press already down when the card appears was meant for the list: it does nothing on the card. */
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_press(APP_BTN_PWR, 2000, SHORT);
    uint32_t id = rig_sent_id(rig_sent(0));
    rig_btn(2900, APP_BTN_BOOT, true);
    rig_card(3000, "to-device/valid/card-status.ndjson", id);
    rig_btn(3100, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());

    /* On a permission card, where a press is a verdict (lane core's review): a BOOT hold beginning at 499 ms
     * neither fills nor approves, held past 2 s; PWR beginning at 499 ms doesn't deny, and at 500 ms it does. */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_btn(3499, APP_BTN_BOOT, true);
    rig_at(4500);
    TEST_ASSERT_EQUAL_UINT16(0, rig_render()->band.boot.fill_permille);
    rig_at(5600);
    rig_btn(5600, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_press(APP_BTN_PWR, 3499, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_press(APP_BTN_PWR, 3500, SHORT);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    TEST_ASSERT_NOT_NULL(strstr(rig_sent(0), "\"t\":\"deny\""));
}

#define HASH_PERMISSION "6a0c2aae60a7e3df1151bdb24c231c88e163a90d8a7ed200c43a454bc910e402"

/* The kit hands an edge over at least 10 ms after it happened, stamped with when it did (lcd349_input.h). A
 * release stamped before a hold's threshold must win over that threshold, however late it arrives within the
 * 30 ms grace (the owner's decision, 2026-10-27, open-items O-22). */
static void test_late_release_before_the_threshold(void)
{
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_btn(4000, APP_BTN_BOOT, true);
    rig_at(5010);
    app_event_t up = {.kind = APP_EV_BUTTON};
    up.button.btn = APP_BTN_BOOT;
    up.button.down = false;
    up.button.t = 4995; /* released 5 ms short of 1 s, handed over 15 ms later */
    rig_event(5010, &up);
    rig_at(9000);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);

    /* Released at the threshold or after, it approves, however late its edge comes. */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_btn(4000, APP_BTN_BOOT, true);
    rig_at(5010);
    up.button.t = 5000;
    rig_event(5010, &up);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    TEST_ASSERT_NOT_NULL(strstr(rig_sent(0), "\"t\":\"approve\""));
}

static void test_hold_one_second_approves(void)
{
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    TEST_ASSERT_NOT_NULL(strstr(rig_render()->band.boot.text, "APPROVE"));
    TEST_ASSERT_EQUAL_UINT16(0, rig_render()->band.boot.fill_permille);

    /* The BOOT cell fills as the hold goes on. */
    rig_btn(4000, APP_BTN_BOOT, true);
    uint16_t fill[3];
    for (int i = 0; i < 3; i++) {
        rig_at(4300 + 300 * (app_ms_t)i);
        fill[i] = rig_render()->band.boot.fill_permille;
    }
    TEST_ASSERT_GREATER_THAN_UINT16(0, fill[0]);
    TEST_ASSERT_GREATER_THAN_UINT16(fill[0], fill[1]);
    TEST_ASSERT_GREATER_THAN_UINT16(fill[1], fill[2]);
    TEST_ASSERT_LESS_THAN_UINT16(1000, fill[2]);

    /* At 1 s the verdict goes, with the card's pane, seq and hash: on the deadline, 30 ms after, so that a release
     * stamped before 1 s and handed over late still wins (O-22). */
    rig_at(5029);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    rig_at(5030);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    char want[256]; /* the show was id 1: the verdict's is the next, pinned (open-items O-38) */
    snprintf(want, sizeof(want), "{\"t\":\"approve\",\"id\":2,\"pane\":\"w1:p3\",\"seq\":41,\"hash\":\"%s\"}",
             HASH_PERMISSION);
    TEST_ASSERT_EQUAL_STRING(want, rig_sent(0));
    TEST_ASSERT_NOT_NULL(strstr(rig_render()->band.boot.text, "SENT"));

    /* The rest of the press is consumed: held on to 2 s (past the menu's 1.5 s) and released, nothing more. */
    rig_at(6000);
    rig_btn(6000, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
}

static void test_early_release_sends_nothing(void)
{
    static const app_ms_t held[] = {600, 800, 999};
    for (size_t i = 0; i < sizeof(held) / sizeof(held[0]); i++) {
        permission_card_at(3000, "to-device/valid/card-permission.ndjson");
        rig_btn(4000, APP_BTN_BOOT, true);
        rig_at(4000 + held[i]);
        rig_btn(4000 + held[i], APP_BTN_BOOT, false);
        rig_at(9000);
        TEST_ASSERT_EQUAL_size_t_MESSAGE(0, rig_n_sent(), "a release between 600 ms and 1 s sent something");
        TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
        TEST_ASSERT_EQUAL_UINT16(0, rig_render()->band.boot.fill_permille);
        TEST_ASSERT_NULL(strstr(rig_render()->band.boot.text, "SENT"));
    }
}

#define HASH_DANGER "760e2ea33f6918596dd2c7fd6da19184b53b9be9a0dac12e19b447992a944bb4"

static void test_danger_needs_two_seconds(void)
{
    /* The danger card opens with its banner as the body's first row, and its own tone. */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson"); /* release, w1:p5: the force push */
    rig_press(APP_BTN_BOOT, 1500, SHORT);
    rig_press(APP_BTN_PWR, 2000, SHORT);
    uint32_t id = rig_sent_id(rig_sent(0));
    rig_clear_log();
    rig_card(2500, "to-device/valid/card-danger.ndjson", id);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("DANGER: FORCE PUSH", rig_render()->banner);
    TEST_ASSERT_EQUAL_STRING("Bash command", rig_render()->title);
    TEST_ASSERT_EQUAL_STRING("git -C work push --force", rig_render()->lines[0]);
    TEST_ASSERT_EQUAL_STRING("Force push in the work test repo", rig_render()->lines[1]);
    TEST_ASSERT_EQUAL(APP_TONE_DANGER, rig_last(APP_ACT_TONE)->tone);
    TEST_ASSERT_NOT_NULL(strstr(rig_render()->band.boot.text, "2S"));

    /* A release at 1.5 s sends nothing; the fill is then only three quarters full. */
    rig_clear_log();
    rig_btn(3500, APP_BTN_BOOT, true);
    rig_at(4500);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent()); /* not at 1 s */
    TEST_ASSERT_UINT16_WITHIN(10, 500, rig_render()->band.boot.fill_permille);
    rig_at(5000);
    TEST_ASSERT_UINT16_WITHIN(10, 750, rig_render()->band.boot.fill_permille);
    rig_btn(5000, APP_BTN_BOOT, false);
    rig_at(9000);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);

    /* A 2 s hold approves, at 2 s: on the deadline, 30 ms after (O-22). */
    rig_btn(10000, APP_BTN_BOOT, true);
    rig_at(12029);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    rig_at(12030);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    char want[256];
    snprintf(want, sizeof(want), "{\"t\":\"approve\",\"id\":2,\"pane\":\"w1:p5\",\"seq\":9,\"hash\":\"%s\"}",
             HASH_DANGER);
    TEST_ASSERT_EQUAL_STRING(want, rig_sent(0));
    rig_btn(12500, APP_BTN_BOOT, false);
}

static void test_pwr_denies(void)
{
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    TEST_ASSERT_EQUAL_STRING("DENY", rig_render()->band.pwr.text);
    rig_press(APP_BTN_PWR, 3600, SHORT);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    char want[256];
    snprintf(want, sizeof(want), "{\"t\":\"deny\",\"id\":2,\"pane\":\"w1:p3\",\"seq\":41,\"hash\":\"%s\"}",
             HASH_PERMISSION);
    TEST_ASSERT_EQUAL_STRING(want, rig_sent(0));
    TEST_ASSERT_EQUAL_STRING("SENT", rig_render()->band.pwr.text);

    /* Once denied, a BOOT hold can't approve the same card. */
    rig_btn(4000, APP_BTN_BOOT, true);
    rig_at(6500);
    rig_btn(6500, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
}

static void test_boot_short_is_back_on_permission_card(void)
{
    static const char *const cards[] = {"to-device/valid/card-permission.ndjson", "to-device/valid/card-danger.ndjson"};
    for (int i = 0; i < 2; i++) {
        permission_card_at(3000, cards[i]);
        rig_press(APP_BTN_BOOT, 3600, SHORT);
        TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
        rig_at(10000);
        TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    }
}

/* An MCP call's card (O-90's review) holds 4 lines, the protocol's most: its server and tool, then one line per
 * argument. The pager draws all 4: the first in the body font, the title, then the other 3; and offers the hold. */
static void test_mcp_card_shows_four_lines(void)
{
    permission_card_at(3000, "to-device/valid/card-mcp.ndjson");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL_STRING("github - create_issue", r->lines[0]);
    TEST_ASSERT_EQUAL_STRING("Tool use", r->title);
    TEST_ASSERT_EQUAL_STRING("owner: \"octo\"", r->lines[1]);
    TEST_ASSERT_EQUAL_STRING("repo: \"hello\"", r->lines[2]);
    TEST_ASSERT_EQUAL_STRING("title: \"Fix the build\"", r->lines[3]);
    TEST_ASSERT_EQUAL_STRING("HOLD:\nAPPROVE", r->band.boot.text);
}

#define HASH_UNREAD "3f9a1c7e5b2d4086a1e3c5f7092b4d6e8a0c2e4f6183a5c7e9b1d3f5072a4c6e"

/* A permission card the bridge couldn't read in full (the owner's decision, 2026-11-01: fail closed) offers deny
 * alone: its first line says so, PWR's cell says DENY and denies, and BOOT's says to approve at the keyboard. No
 * BOOT hold approves it, fills the bar or opens a menu; BOOT short goes back to the list. */
static void test_deny_only_card(void)
{
    permission_card_at(3000, "to-device/valid/card-permission-unread.ndjson");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL_STRING("COMMAND NOT READ", r->lines[0]);
    TEST_ASSERT_EQUAL_STRING("Bash command", r->title);
    TEST_ASSERT_EQUAL_STRING("DENY", r->band.pwr.text);
    TEST_ASSERT_EQUAL_STRING("APPROVE:\nKEYBOARD", r->band.boot.text);

    /* Held 1 s, 1.5 s, 2 s and on to 3 s: no verdict, no fill, no menu. */
    rig_btn(4000, APP_BTN_BOOT, true);
    for (app_ms_t t = 4100; t <= 7000; t += 100) {
        rig_at(t);
        TEST_ASSERT_EQUAL_UINT16(0, rig_render()->band.boot.fill_permille);
    }
    rig_btn(7000, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());

    /* BOOT short: back to the list, sending nothing. */
    rig_press(APP_BTN_BOOT, 7500, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());

    /* PWR short denies, with the card's pane, seq and hash. */
    permission_card_at(3000, "to-device/valid/card-permission-unread.ndjson");
    rig_press(APP_BTN_PWR, 3600, SHORT);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    char want[256];
    snprintf(want, sizeof(want), "{\"t\":\"deny\",\"id\":2,\"pane\":\"w1:p3\",\"seq\":41,\"hash\":\"%s\"}", HASH_UNREAD);
    TEST_ASSERT_EQUAL_STRING(want, rig_sent(0));
    TEST_ASSERT_EQUAL_STRING("SENT", rig_render()->band.pwr.text);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.boot.text);
}

/* True when any sent line is a verdict. */
static bool sent_a_verdict(void)
{
    for (size_t i = 0; i < rig_n_sent(); i++) {
        if (strstr(rig_sent(i), "\"t\":\"approve\"") || strstr(rig_sent(i), "\"t\":\"deny\"")) {
            return true;
        }
    }
    return false;
}

static void test_question_card_sends_no_verdict(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson");
    app_ms_t t = rig_open_card(2000, "colour-picker", "to-device/valid/card-question.ndjson");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, r->screen);
    TEST_ASSERT_EQUAL_STRING("? Colour", r->title);
    TEST_ASSERT_EQUAL_STRING("QUESTION - ANSWER AT KEYBOARD", r->lines[0]);
    TEST_ASSERT_EQUAL_STRING("Which colour?", r->lines[1]);
    TEST_ASSERT_NULL(strstr(r->band.boot.text, "APPROVE"));
    TEST_ASSERT_NULL(strstr(r->band.pwr.text, "DENY"));

    /* Past 1 s and released: nothing. */
    rig_btn(t + 1000, APP_BTN_BOOT, true);
    rig_at(t + 2200);
    rig_btn(t + 2200, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    /* Held 2.5 s: never a verdict, whatever else it opens. */
    rig_btn(t + 3000, APP_BTN_BOOT, true);
    rig_at(t + 5500);
    rig_btn(t + 5500, APP_BTN_BOOT, false);
    TEST_ASSERT_FALSE(sent_a_verdict());
    /* That hold opened the agent's menu (6.31): another closes it, back to the card. BOOT short: back. */
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, rig_render()->screen);
    rig_btn(t + 5800, APP_BTN_BOOT, true);
    rig_at(t + 7400);
    rig_btn(t + 7400, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    rig_press(APP_BTN_BOOT, t + 7600, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_FALSE(sent_a_verdict());
    t += 2000;
    /* PWR short: back, not deny. */
    t = rig_open_card(t + 7000, "colour-picker", "to-device/valid/card-question.ndjson");
    rig_press(APP_BTN_PWR, t + 1000, SHORT);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_FALSE(sent_a_verdict());
}

static void test_card_without_keymap_offers_jump_only(void)
{
    /* codex has no keymap: the bridge's card for its prompt offers only jump, kind permission or not. */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson");
    app_ms_t t = rig_open_card_line(
        2000, "codex w2:p5",
        "{\"t\":\"card\",\"id\":1,\"pane\":\"w2:p5\",\"st\":\"blocked\",\"seq\":18,\"hash\":"
        "\"" HASH_PERMISSION "\",\"kind\":\"permission\",\"summary\":\"Run command\",\"lines\":[\"make clean\"],"
        "\"can\":[\"jump\"]}");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, r->screen);
    TEST_ASSERT_EQUAL_STRING("Run command", r->title);
    TEST_ASSERT_NULL(strstr(r->band.boot.text, "APPROVE"));
    TEST_ASSERT_NULL(strstr(r->band.pwr.text, "DENY"));

    /* No hold approves it and PWR doesn't deny it. */
    rig_btn(t + 1000, APP_BTN_BOOT, true);
    rig_at(t + 3500);
    rig_btn(t + 3500, APP_BTN_BOOT, false);
    rig_press(APP_BTN_PWR, t + 4000, SHORT);
    TEST_ASSERT_FALSE(sent_a_verdict());
}

/* Answers the last sent line (a verdict) with the result fixture rel. */
static void answer(app_ms_t t, const char *rel)
{
    rig_card(t, rel, rig_sent_id(rig_sent(rig_n_sent() - 1)));
}

static void assert_printable(const char *s)
{
    for (; *s; s++) {
        TEST_ASSERT_TRUE_MESSAGE((*s >= 0x20 && *s <= 0x7e) || *s == '\n', "not printable ASCII");
    }
}

static void test_verdict_results(void)
{
    /* ok: "OK" for 2 s, then the list. */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_btn(4000, APP_BTN_BOOT, true);
    rig_at(5000);
    rig_btn(5200, APP_BTN_BOOT, false);
    answer(5300, "to-device/valid/result-ok.ndjson");
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("OK", rig_render()->title);
    rig_at(7299);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    rig_at(7300);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());

    /* stale: "SCREEN CHANGED" for 2 s, and the card is asked for again; the verdict is never resent. */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_btn(4000, APP_BTN_BOOT, true);
    rig_at(5000);
    rig_btn(5200, APP_BTN_BOOT, false);
    answer(5300, "to-device/valid/result-stale.ndjson");
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("SCREEN CHANGED", rig_render()->title);
    TEST_ASSERT_EQUAL_size_t(2, rig_n_sent());
    TEST_ASSERT_NOT_NULL(strstr(rig_sent(1), "\"t\":\"show\""));
    TEST_ASSERT_NOT_NULL(strstr(rig_sent(1), "\"pane\":\"w1:p3\""));
    rig_at(7300);
    rig_card(7400, "to-device/valid/card-permission.ndjson", rig_sent_id(rig_sent(1)));
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_NOT_NULL(strstr(rig_render()->band.boot.text, "APPROVE")); /* a fresh card, nothing sent yet */
    rig_at(20000);
    TEST_ASSERT_EQUAL_size_t(2, rig_n_sent());

    /* An error: "X" and the reason, for 2 s, then the list. */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_press(APP_BTN_PWR, 4000, SHORT);
    answer(4300, "to-device/valid/result-herdr_error.ndjson");
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("X HERDR ERROR", rig_render()->title);
    assert_printable(rig_render()->title);
    rig_at(6300);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());

    /* A result for another id changes nothing. */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_press(APP_BTN_PWR, 4000, SHORT);
    rig_card(4300, "to-device/valid/result-ok.ndjson", 999);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
}

/* A card under a toast is out of sight: no press acts on it until it has been in view for the guard's 500 ms
 * (parent 8.1, 8.4; group 13's review). */
static void test_card_under_a_toast_takes_no_verdict(void)
{
    /* The stale path's new card comes back inside SCREEN CHANGED's 2 s (until 7300). */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_btn(4000, APP_BTN_BOOT, true);
    rig_at(5030); /* the 1 s hold's deadline, 30 ms after it (O-22) */
    rig_btn(5200, APP_BTN_BOOT, false);
    answer(5300, "to-device/valid/result-stale.ndjson");
    rig_card(5400, "to-device/valid/card-permission.ndjson", rig_sent_id(rig_sent(1)));
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    rig_btn(6000, APP_BTN_BOOT, true); /* a 1 s hold, under the toast */
    rig_at(7100);
    rig_btn(7200, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL_size_t(2, rig_n_sent()); /* the approve and the show; nothing more */
    rig_press(APP_BTN_PWR, 7700, SHORT);       /* the card in view for 400 ms: inside the guard */
    TEST_ASSERT_EQUAL_size_t(2, rig_n_sent());
    rig_btn(7800, APP_BTN_BOOT, true); /* in view for 500 ms: the hold approves */
    rig_at(8830);
    TEST_ASSERT_EQUAL_size_t(3, rig_n_sent());
    TEST_ASSERT_NOT_NULL(strstr(rig_sent(2), "\"t\":\"approve\""));

    /* PWR held 2 s on a card: SILENT ON covers it until 8000. */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_btn(4000, APP_BTN_PWR, true);
    rig_at(6030);
    rig_btn(6100, APP_BTN_PWR, false);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    rig_btn(6200, APP_BTN_BOOT, true);
    rig_at(7300);
    rig_btn(7400, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
}

/* From asleep, a verdict takes three inputs: wake, open, then a 1 s hold (parent design §Input router, T14). A
 * card open when the screen goes dark is out of sight, so it closes then (group 13's review). */
static void test_sleep_closes_the_card(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson"); /* the blocked agent pages, lit for 60 s */
    rig_at(70000);
    TEST_ASSERT_FALSE(rig.s.disp.awake);
    rig_press(APP_BTN_BOOT, 71000, SHORT); /* wake */
    rig_press(APP_BTN_PWR, 72000, SHORT);  /* open */
    rig_card(72200, "to-device/valid/card-permission.ndjson", rig_sent_id(rig_sent(rig_n_sent() - 1)));
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    rig_clear_log();
    rig_at(110000); /* dark 30 s after the last press, inside the card's 60 s */
    TEST_ASSERT_FALSE(rig.s.disp.awake);
    rig_press(APP_BTN_BOOT, 111000, SHORT); /* wakes the screen, and nothing else */
    TEST_ASSERT_TRUE(rig.s.disp.awake);
    rig_btn(112000, APP_BTN_BOOT, true);
    rig_at(113200);
    rig_btn(113300, APP_BTN_BOOT, false);
    for (size_t i = 0; i < rig_n_sent(); i++) {
        TEST_ASSERT_NULL_MESSAGE(strstr(rig_sent(i), "\"t\":\"approve\""), "approved with a wake and a hold");
    }
}

static uint32_t xorshift(uint32_t *x)
{
    *x ^= *x << 13;
    *x ^= *x >> 17;
    *x ^= *x << 5;
    return *x;
}

typedef struct {
    app_ms_t t;
    app_btn_t btn;
    bool down;
} edge_t;

static int by_time(const void *a, const void *b)
{
    const edge_t *x = a, *y = b;
    return x->t < y->t ? -1 : x->t > y->t ? 1 : (int)x->down - (int)y->down; /* an up before a down at one time */
}

/* Answers each line the device sent since *seen as the bridge would: a show with a permission card (plain or
 * danger, by the seed), a verdict with ok. */
static void bridge_answers(app_ms_t t, size_t *seen, uint32_t *rng)
{
    while (*seen < rig_n_sent()) {
        const char *line = rig_sent((*seen)++);
        uint32_t id = rig_sent_id(line);
        if (strstr(line, "\"t\":\"show\"")) {
            rig_card(t, xorshift(rng) % 2 ? "to-device/valid/card-permission.ndjson" : "to-device/valid/card-danger.ndjson",
                     id);
        } else {
            rig_card(t, "to-device/valid/result-ok.ndjson", id);
        }
    }
}

#define POCKET_RUNS 10000
#define POCKET_PRESSES 24

static void test_pocket_fuzz_never_approves(void)
{
    unsigned shows = 0, denies = 0, holds_on_cards = 0;
    for (uint32_t run = 0; run < POCKET_RUNS; run++) {
        uint32_t rng = run * 2654435761u + 1;
        rig_boot(true);
        rig_fixture(1000, "to-device/valid/snap.ndjson"); /* a blocked agent: its card is a permission card */
        rig_at(70000);                                    /* its page has let the backlight go: asleep */
        TEST_ASSERT_FALSE(rig.s.disp.awake);

        /* Short presses (under 1 s each) of both buttons, overlapping at random. */
        static edge_t edges[2 * POCKET_PRESSES];
        app_ms_t free_at[2] = {70000, 70000}, t = 70000;
        for (int i = 0; i < POCKET_PRESSES; i++) {
            app_btn_t btn = (app_btn_t)(xorshift(&rng) % 2);
            t += xorshift(&rng) % 1200;
            app_ms_t down = t > free_at[btn] ? t : free_at[btn] + 1 + xorshift(&rng) % 300;
            app_ms_t up = down + 20 + xorshift(&rng) % 980;
            edges[2 * i] = (edge_t){down, btn, true};
            edges[2 * i + 1] = (edge_t){up, btn, false};
            free_at[btn] = up;
        }
        qsort(edges, 2 * POCKET_PRESSES, sizeof(edges[0]), by_time);
        size_t seen = 0;
        for (int i = 0; i < 2 * POCKET_PRESSES; i++) {
            /* A BOOT press of 600 ms or more starting on a permission card, the guard past: the near misses. */
            if (edges[i].down && edges[i].btn == APP_BTN_BOOT && rig.s.card.open && rig.s.verdict.id == 0 &&
                edges[i].t >= rig.s.card.shown_at + 500 && rig.s.disp.awake) {
                for (int j = i + 1; j < 2 * POCKET_PRESSES; j++) {
                    if (edges[j].btn == APP_BTN_BOOT) {
                        holds_on_cards += edges[j].t - edges[i].t >= 600;
                        break;
                    }
                }
            }
            rig_btn(edges[i].t, edges[i].btn, edges[i].down);
            bridge_answers(edges[i].t, &seen, &rng);
        }
        rig_at(edges[2 * POCKET_PRESSES - 1].t + 5000);
        uint32_t last_id = 0;
        for (size_t i = 0; i < rig_n_sent(); i++) {
            if (strstr(rig_sent(i), "\"id\":")) { /* every request a fresh id, rising (open-items O-38) */
                char msg[96];
                snprintf(msg, sizeof(msg), "run %u: id %u after %u", (unsigned)run, (unsigned)rig_sent_id(rig_sent(i)),
                         (unsigned)last_id);
                TEST_ASSERT_GREATER_THAN_UINT32_MESSAGE(last_id, rig_sent_id(rig_sent(i)), msg);
                last_id = rig_sent_id(rig_sent(i));
            }
            shows += strstr(rig_sent(i), "\"t\":\"show\"") != NULL;
            denies += strstr(rig_sent(i), "\"t\":\"deny\"") != NULL;
            if (strstr(rig_sent(i), "\"t\":\"approve\"")) {
                char msg[64];
                snprintf(msg, sizeof(msg), "run %u approved", (unsigned)run);
                TEST_FAIL_MESSAGE(msg);
            }
        }
    }
    /* The runs reach cards, deny on them, and hold BOOT on them for 600 ms to 1 s. */
    printf("pocket fuzz: %u shows, %u denies, %u long BOOT presses on cards\n", shows, denies, holds_on_cards);
    TEST_ASSERT_GREATER_THAN_UINT(POCKET_RUNS, shows);
    TEST_ASSERT_GREATER_THAN_UINT(POCKET_RUNS / 10, denies);
    TEST_ASSERT_GREATER_THAN_UINT(POCKET_RUNS / 10, holds_on_cards);
}

/* Touch samples every 30 ms from t for ms: down and moving, then up. Each produces no action at all, and
 * leaves the core's deadline where it was. */
static void touch_storm(app_ms_t t, app_ms_t ms)
{
    for (app_ms_t dt = 0; dt < ms; dt += 30) {
        rig_at(t + dt);
        app_ms_t before = rig.out.deadline;
        size_t n = rig.n_log;
        rig_touch(t + dt, (uint16_t)(20 + dt % 600), (uint16_t)(30 + dt % 140), dt + 30 < ms);
        TEST_ASSERT_EQUAL_size_t_MESSAGE(n, rig.n_log, "a touch produced an action");
        TEST_ASSERT_EQUAL_UINT64_MESSAGE(before, rig.out.deadline, "a touch moved the deadline");
    }
}

static void test_touch_never_acts(void)
{
    /* On the list: no selection, no card, and the backlight still goes off 30 s after the last press. */
    rig_boot(true);
    rig_snap_quiet(1000);
    touch_storm(2000, 3000);
    rig_press(APP_BTN_BOOT, 10000, SHORT);
    touch_storm(11000, 25000);
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->selected);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    rig_at(39999);
    TEST_ASSERT_TRUE(rig.s.disp.awake);
    rig_at(40000);
    TEST_ASSERT_FALSE(rig.s.disp.awake);

    /* Asleep: touch never wakes the screen. */
    rig_clear_log();
    touch_storm(41000, 5000);
    TEST_ASSERT_FALSE(rig.s.disp.awake);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_BACKLIGHT));

    /* On a permission card, past its guard: no verdict, no back. */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    touch_storm(4000, 3000);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
}

static void test_verdict_dropped_when_link_down(void)
{
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_link(3200, APP_LINK_DOWN);

    /* A full approve hold and a deny press: nothing is sent, and the device says why. */
    rig_btn(4000, APP_BTN_BOOT, true);
    rig_at(5200);
    rig_btn(5200, APP_BTN_BOOT, false);
    TEST_ASSERT_FALSE(sent_a_verdict());
    TEST_ASSERT_EQUAL_STRING("X NO LINK", rig_render()->title);
    rig_press(APP_BTN_PWR, 8000, SHORT);
    TEST_ASSERT_FALSE(sent_a_verdict());
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());

    /* Nothing was queued: the link coming back sends no verdict. */
    rig_link(9000, APP_LINK_CONNECTING);
    rig_link(9500, APP_LINK_CONNECTED);
    rig_at(20000);
    TEST_ASSERT_FALSE(sent_a_verdict());
}

static void test_verdict_discarded_when_link_drops_before_reply(void)
{
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_btn(4000, APP_BTN_BOOT, true);
    rig_at(5000);
    rig_btn(5100, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    uint32_t id = rig_sent_id(rig_sent(0));

    /* The link drops before the result: the verdict is discarded, and the device says so. */
    rig_link(5500, APP_LINK_DOWN);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("X LINK LOST", rig_render()->title);
    rig_at(7500);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);

    /* After reconnecting, nothing is resent, and a result for the old request means nothing. */
    rig_link(8000, APP_LINK_CONNECTING);
    rig_link(8500, APP_LINK_CONNECTED);
    rig_card(9000, "to-device/valid/result-ok.ndjson", id);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    rig_at(30000);
    size_t verdicts = 0;
    for (size_t i = 0; i < rig_n_sent(); i++) {
        verdicts += strstr(rig_sent(i), "\"t\":\"approve\"") != NULL;
    }
    TEST_ASSERT_EQUAL_size_t(1, verdicts);
}

/* The link drops asked for in the log. */
static size_t disconnects(void)
{
    size_t n = 0;
    for (size_t i = 0; i < rig.n_log; i++) {
        n += rig.log[i].kind == APP_ACT_LINK && rig.log[i].link_op == APP_LINK_OP_DISCONNECT;
    }
    return n;
}

static void test_unanswered_show_drops_the_link(void)
{
    /* A show the bridge hasn't answered in 8 s: the link is taken as dead, as on a network the Mac has left
     * (acceptance day 1, R3). The device says so and drops the link, which redials after its backoff. */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_clear_log();
    rig_press(APP_BTN_PWR, 2000, SHORT); /* the show goes at the release, 2,100 ms */
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    uint32_t id = rig_sent_id(rig_sent(0));
    rig_at(10099);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_LINK));
    rig_at(10100);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("X NO LINK", rig_render()->title);
    TEST_ASSERT_EQUAL(APP_LINK_OP_DISCONNECT, rig_last(APP_ACT_LINK)->link_op);

    /* The card coming late answers a request given up: it isn't shown. The link redials. */
    rig_link(10150, APP_LINK_DOWN);
    rig_card(10200, "to-device/valid/card-permission.ndjson", id);
    TEST_ASSERT_NOT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    rig_at(12000);
    TEST_ASSERT_EQUAL(APP_LINK_OP_CONNECT, rig_last(APP_ACT_LINK)->link_op);

    /* Answered within 8 s: shown, and the link is kept. */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_clear_log();
    rig_press(APP_BTN_PWR, 2000, SHORT);
    rig_card(10099, "to-device/valid/card-permission.ndjson", rig_sent_id(rig_sent(0)));
    rig_at(20000);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_LINK));

    /* A link already down when the 8 s are up: nothing more to drop, and nothing more to say. */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_press(APP_BTN_PWR, 2000, SHORT);
    rig_link(3000, APP_LINK_DOWN);
    rig_clear_log();
    rig_at(10100);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, disconnects()); /* the backoff's redial, only */
}

static void test_show_answered_with_an_error_keeps_the_link(void)
{
    /* A show the bridge answers with an error has its answer: the reason for 2 s, then the list, and the link
     * kept (parent design §Error handling: unknown_pane is GONE; group 13's review). */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_clear_log();
    rig_press(APP_BTN_PWR, 2000, SHORT);
    answer(2300, "to-device/valid/result-unknown_pane.ndjson");
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("X GONE", rig_render()->title);
    rig_at(11000);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_LINK));
}

static void test_pressing_again_keeps_the_first_deadline(void)
{
    /* Pressing again while a show is unanswered asks again, but the 8 s still run from the first: on a dead link,
     * pressing more doesn't put the toast off (acceptance day 1, R5). */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_clear_log();
    rig_press(APP_BTN_PWR, 2000, SHORT); /* the first show goes at 2,100 ms */
    rig_press(APP_BTN_PWR, 5000, SHORT);
    rig_press(APP_BTN_PWR, 8000, SHORT);
    TEST_ASSERT_EQUAL_size_t(3, rig_n_sent());
    rig_at(10099);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_LINK));
    rig_at(10100);
    TEST_ASSERT_EQUAL_STRING("X NO LINK", rig_render()->title);
    TEST_ASSERT_EQUAL(APP_LINK_OP_DISCONNECT, rig_last(APP_ACT_LINK)->link_op);

    /* The latest show is the one answered: its card, in time, is shown, and the link kept. */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_clear_log();
    rig_press(APP_BTN_PWR, 2000, SHORT);
    rig_press(APP_BTN_PWR, 5000, SHORT);
    rig_card(9000, "to-device/valid/card-permission.ndjson", rig_sent_id(rig_sent(1)));
    rig_at(20000);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_LINK));

    /* A show lost with its link: the next one, on the new link, has its own 8 s. */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_press(APP_BTN_PWR, 2000, SHORT);
    rig_link(3000, APP_LINK_DOWN);
    rig_link(4000, APP_LINK_CONNECTED);
    rig_clear_log();
    rig_press(APP_BTN_PWR, 5000, SHORT); /* 5,100 ms */
    rig_at(13099);
    TEST_ASSERT_EQUAL_size_t(0, disconnects());
    rig_at(13100);
    TEST_ASSERT_EQUAL_size_t(1, disconnects());
}

static void test_unanswered_verdict_drops_the_link(void)
{
    /* An approve the bridge hasn't answered in 8 s (more than its slowest: three herdr calls of 2 s): the
     * link is dropped, and the verdict is discarded as on any lost link. It is never resent. */
    permission_card_at(3000, "to-device/valid/card-permission.ndjson");
    rig_btn(4000, APP_BTN_BOOT, true);
    rig_at(5030); /* the approve goes at the 1 s hold's deadline, 30 ms after it (O-22) */
    rig_btn(5100, APP_BTN_BOOT, false);
    TEST_ASSERT_TRUE(sent_a_verdict());
    uint32_t id = rig_sent_id(rig_sent(0));
    rig_at(13029);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_LINK));
    rig_at(13030);
    TEST_ASSERT_EQUAL(APP_LINK_OP_DISCONNECT, rig_last(APP_ACT_LINK)->link_op);
    TEST_ASSERT_EQUAL_STRING("X LINK LOST", rig_render()->title);
    rig_link(13050, APP_LINK_DOWN); /* asked for once: nothing more to drop */
    rig_at(15100);
    TEST_ASSERT_EQUAL_size_t(1, disconnects());
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);

    /* Back on the link: a late result means nothing, and no verdict is resent. */
    rig_link(16000, APP_LINK_CONNECTED);
    rig_card(16500, "to-device/valid/result-ok.ndjson", id);
    rig_at(30000);
    size_t verdicts = 0;
    for (size_t i = 0; i < rig_n_sent(); i++) {
        verdicts += strstr(rig_sent(i), "\"t\":\"approve\"") != NULL;
    }
    TEST_ASSERT_EQUAL_size_t(1, verdicts);
}

/* api-refactor's permission card, shown at at (in the 3 s debounce, so its own page never fires: O-21), while two
 * other blocked agents page at 4 s and light the screen to 64 s. */
static void card_while_others_page(app_ms_t at)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson"); /* api-refactor, release, colour-picker blocked */
    TEST_ASSERT_EQUAL_STRING("api-refactor", rig_render()->rows[rig_render()->selected].name);
    rig_press(APP_BTN_PWR, at - 1000, SHORT);
    rig_card(at, "to-device/valid/card-permission.ndjson", rig_sent_id(rig_sent(rig_n_sent() - 1)));
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    rig_clear_log();
}

static void test_card_timeout(void)
{
    /* 60 s with no press: back to the list, no verdict. Touch all along doesn't hold it open (R1.3). The screen
     * stays lit past 60 s through other agents' pages: the card's own page, in its debounce, never fires (O-21). */
    card_while_others_page(3000);
    touch_storm(10000, 2000);
    touch_storm(50000, 2000);
    touch_storm(62500, 400);
    rig_at(62999);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    rig_at(63000);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());

    /* A press that does nothing on the card (PWR held 1 s) still counts: 60 s from its release, so past 63 s.
     * Its 60 s would end at 91 s, but the screen goes dark at 64 s (the pages' light), and that closes the card
     * (test_sleep_closes_the_card). */
    card_while_others_page(3000);
    rig_btn(30000, APP_BTN_PWR, true);
    rig_btn(31000, APP_BTN_PWR, false);
    rig_at(63999);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    rig_at(64000);
    TEST_ASSERT_FALSE(rig.s.disp.awake);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
}

TEST_AREA(router)
{
    RUN_TEST(test_card_timeout);
    RUN_TEST(test_unanswered_show_drops_the_link);
    RUN_TEST(test_pressing_again_keeps_the_first_deadline);
    RUN_TEST(test_show_answered_with_an_error_keeps_the_link);
    RUN_TEST(test_unanswered_verdict_drops_the_link);
    RUN_TEST(test_verdict_discarded_when_link_drops_before_reply);
    RUN_TEST(test_verdict_dropped_when_link_down);
    RUN_TEST(test_touch_never_acts);
    RUN_TEST(test_pocket_fuzz_never_approves);
    RUN_TEST(test_verdict_results);
    RUN_TEST(test_card_under_a_toast_takes_no_verdict);
    RUN_TEST(test_sleep_closes_the_card);
    RUN_TEST(test_card_without_keymap_offers_jump_only);
    RUN_TEST(test_deny_only_card);
    RUN_TEST(test_mcp_card_shows_four_lines);
    RUN_TEST(test_question_card_sends_no_verdict);
    RUN_TEST(test_boot_short_is_back_on_permission_card);
    RUN_TEST(test_pwr_denies);
    RUN_TEST(test_danger_needs_two_seconds);
    RUN_TEST(test_early_release_sends_nothing);
    RUN_TEST(test_hold_one_second_approves);
    RUN_TEST(test_late_release_before_the_threshold);
    RUN_TEST(test_card_guard_500ms);
    RUN_TEST(test_pwr_short_requests_card);
    RUN_TEST(test_wake_press_consumed_whole);
    RUN_TEST(test_boot_short_moves_selection_and_wraps);
    RUN_TEST(test_viewing_list_marks_nothing);
}
