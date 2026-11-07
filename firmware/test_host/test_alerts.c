/* The alert engine: pages, reminders, auto-clear, the backlight's limits. */
#include <stdio.h>
#include <string.h>

#include "app.h"
#include "test_registry.h"
#include "test_rig.h"

/* A snapshot of one agent, at Unix time now (the bridge's clock). */
static const char *snap1(int64_t now, const char *pane, const char *st, uint64_t seq)
{
    static char buf[512];
    snprintf(buf, sizeof(buf),
             "{\"t\":\"snap\",\"now\":%lld,\"herdr\":\"ok\",\"more\":0,\"agents\":[{\"pane\":\"%s\",\"name\":\"agent\","
             "\"kind\":\"claude\",\"st\":\"%s\",\"seq\":%llu,\"dseq\":0,\"since\":%lld,\"can\":[\"show\",\"jump\"]}]}",
             (long long)now, pane, st, (unsigned long long)seq, (long long)now);
    return buf;
}

static size_t count_tone(app_tone_t tone)
{
    size_t n = 0;
    for (size_t i = 0; i < rig.n_log; i++) {
        n += rig.log[i].kind == APP_ACT_TONE && rig.log[i].tone == tone;
    }
    return n;
}

static void test_blocked_pages_after_debounce(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson"); /* w1:p3 blocked, seq 41 */
    TEST_ASSERT_EQUAL_UINT64(4000, rig.out.deadline);

    /* Another event in between doesn't lose the debounce's deadline (lane fw's review, finding 2). */
    rig_btn(2000, APP_BTN_BOOT, true);
    TEST_ASSERT_LESS_OR_EQUAL_UINT64(4000, rig.out.deadline); /* BOOT's own 1 s threshold may come first */
    rig_btn(2050, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL_UINT64(4000, rig.out.deadline);

    rig_at(3999);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_TONE));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_FLASH));
    TEST_ASSERT_FALSE(rig_render()->rows[0].paged);

    rig_at(4000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));
    TEST_ASSERT_EQUAL_UINT8(60, rig_last(APP_ACT_TONE)->level); /* the stored volume */
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_FLASH));
    TEST_ASSERT_NOT_NULL(rig_last(APP_ACT_BACKLIGHT));
    TEST_ASSERT_EQUAL_UINT8(80, rig_last(APP_ACT_BACKLIGHT)->level); /* the stored brightness */
    TEST_ASSERT_TRUE(rig_render()->rows[0].paged);

    /* The 3 s are with the same seq: a new seq starts them again, and leaving blocked before them pages nothing. */
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_line(2500, snap1(1760220002, "w1:p3", "blocked", 42));
    rig_at(5499);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_TONE));
    rig_at(5500);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));

    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_line(3000, snap1(1760220003, "w1:p3", "working", 43));
    rig_at(10000);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_TONE));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_FLASH));
}

/* Seven agents first seen blocked in one snapshot page in one tick, with one tone, one flash and one backlight
 * for them all, and the render that marks their rows isn't lost to the batch's limit (open-items O-20: three
 * actions a page made 21 of APP_MAX_ACTIONS' 20, and the RENDER was dropped). */
static void test_many_pages_at_once(void)
{
    static char buf[4096];
    int n = snprintf(buf, sizeof(buf), "{\"t\":\"snap\",\"now\":1760220001,\"herdr\":\"ok\",\"more\":0,\"agents\":[");
    for (int i = 0; i < 7; i++) {
        n += snprintf(buf + n, sizeof(buf) - n,
                      "%s{\"pane\":\"w1:p%d\",\"name\":\"agent%d\",\"kind\":\"claude\",\"st\":\"blocked\",\"seq\":%d,"
                      "\"dseq\":0,\"since\":1760220000,\"can\":[\"show\",\"jump\"]}",
                      i == 0 ? "" : ",", i + 1, i + 1, 10 + i);
    }
    snprintf(buf + n, sizeof(buf) - n, "]}");
    rig_boot(true);
    rig_line(1000, buf);
    rig_clear_log();
    rig_at(4000);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_RENDER));
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_FLASH));
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));
    for (int i = 0; i < 7; i++) {
        TEST_ASSERT_TRUE(rig_render()->rows[i].paged);
    }

    /* Their reminders fall due together too: one tone. */
    rig_clear_log();
    rig_at(4000 + 5 * 60 * 1000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));
}

/* A card opened during the 3 s debounce answers the page before it fires: no tone, no flash and no 60 s light
 * (the owner's decision, 2026-10-27, open-items O-21). The screen sleeps 30 s after the last press, as for any
 * press. */
static void test_card_opened_in_the_debounce_pages_nothing(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson"); /* w1:p3 blocked, seq 41 */
    app_ms_t t = rig_open_card(2000, "api-refactor", "to-device/valid/card-permission.ndjson");
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    rig_clear_log();
    rig_at(t + 30000 - 1);
    TEST_ASSERT_EQUAL_size_t(0, count_tone(APP_TONE_BLOCKED));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_FLASH));
    TEST_ASSERT_FALSE(rig_render()->rows[0].paged);
    rig_at(t + 30000);
    TEST_ASSERT_FALSE(rig.s.disp.awake);
    rig_at(1000000); /* and no reminder */
    TEST_ASSERT_EQUAL_size_t(0, count_tone(APP_TONE_BLOCKED));
}

/* Three idle agents; alpha's status is st (with seq), the others stay idle. */
static const char *snap3(int64_t now, const char *alpha_st, uint64_t alpha_seq)
{
    static char buf[1024];
    snprintf(buf, sizeof(buf),
             "{\"t\":\"snap\",\"now\":%lld,\"herdr\":\"ok\",\"more\":0,\"agents\":["
             "{\"pane\":\"w1:p1\",\"name\":\"alpha\",\"kind\":\"claude\",\"st\":\"%s\",\"seq\":%llu,\"dseq\":0,"
             "\"since\":1760220000,\"can\":[\"show\",\"jump\"]},"
             "{\"pane\":\"w1:p2\",\"name\":\"bravo\",\"kind\":\"claude\",\"st\":\"idle\",\"seq\":2,\"dseq\":0,"
             "\"since\":1760220001,\"can\":[\"show\",\"jump\"]},"
             "{\"pane\":\"w1:p3\",\"name\":\"charlie\",\"kind\":\"claude\",\"st\":\"idle\",\"seq\":3,\"dseq\":0,"
             "\"since\":1760220002,\"can\":[\"show\",\"jump\"]}]}",
             (long long)now, alpha_st, (unsigned long long)alpha_seq);
    return buf;
}

/* A page moves the list's selection to the paged agent, so one PWR press opens its card: with a dozen
 * sessions the owner had to tap BOOT through all of them to reach it (the desk checkpoint, 2026-10-24). */
static void test_page_selects_the_paged_agent(void)
{
    rig_boot(true);
    rig_line(1000, snap3(1760220003, "idle", 1));
    rig_press(APP_BTN_BOOT, 1500, 100);
    rig_press(APP_BTN_BOOT, 1800, 100);
    TEST_ASSERT_EQUAL_STRING("charlie", rig_render()->rows[rig_render()->selected].name);

    rig_line(2000, snap3(1760220004, "blocked", 4)); /* alpha blocks; it pages 3 s later */
    rig_at(4999);
    TEST_ASSERT_EQUAL_STRING("charlie", rig_render()->rows[rig_render()->selected].name);
    rig_at(5000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));
    TEST_ASSERT_EQUAL_STRING("alpha", rig_render()->rows[rig_render()->selected].name);
    TEST_ASSERT_TRUE(rig_render()->rows[rig_render()->selected].paged);
}

/* The done tone (parent 6.2), pulled into the desk pager on the acceptance day (2026-10-25): herdr calls every
 * finished turn done, a turn that ends asking for the owner too, and the desk pager gave it no sign. */

/* A snapshot of one agent with its completion, dseq. */
static const char *snap1d(int64_t now, const char *pane, const char *st, uint64_t seq, uint64_t dseq)
{
    static char buf[512];
    snprintf(buf, sizeof(buf),
             "{\"t\":\"snap\",\"now\":%lld,\"herdr\":\"ok\",\"more\":0,\"agents\":[{\"pane\":\"%s\",\"name\":\"agent\","
             "\"kind\":\"claude\",\"st\":\"%s\",\"seq\":%llu,\"dseq\":%llu,\"since\":%lld,\"can\":[\"show\",\"jump\"]}]}",
             (long long)now, pane, st, (unsigned long long)seq, (unsigned long long)dseq, (long long)now);
    return buf;
}

/* Three agents: alpha's status, seq and dseq are given, bravo stays idle, and charlie's status and seq are given. */
static const char *snap3d(int64_t now, const char *alpha_st, uint64_t alpha_seq, uint64_t alpha_dseq,
                          const char *charlie_st, uint64_t charlie_seq)
{
    static char buf[1024];
    snprintf(buf, sizeof(buf),
             "{\"t\":\"snap\",\"now\":%lld,\"herdr\":\"ok\",\"more\":0,\"agents\":["
             "{\"pane\":\"w1:p1\",\"name\":\"alpha\",\"kind\":\"claude\",\"st\":\"%s\",\"seq\":%llu,\"dseq\":%llu,"
             "\"since\":1760220000,\"can\":[\"show\",\"jump\"]},"
             "{\"pane\":\"w1:p2\",\"name\":\"bravo\",\"kind\":\"claude\",\"st\":\"idle\",\"seq\":2,\"dseq\":0,"
             "\"since\":1760220001,\"can\":[\"show\",\"jump\"]},"
             "{\"pane\":\"w1:p3\",\"name\":\"charlie\",\"kind\":\"claude\",\"st\":\"%s\",\"seq\":%llu,\"dseq\":0,"
             "\"since\":1760220002,\"can\":[\"show\",\"jump\"]}]}",
             (long long)now, alpha_st, (unsigned long long)alpha_seq, (unsigned long long)alpha_dseq, charlie_st,
             (unsigned long long)charlie_seq);
    return buf;
}

/* A finished turn chimes once: not again for the same (pane, dseq), with no reminders, and not after a
 * reconnect; the next turn's completion chimes again. No flash: that's for blocked pages. */
static void test_done_tone_once(void)
{
    rig_boot(true);
    rig_line(1000, snap1d(1760220001, "w1:p3", "working", 40, 0));
    rig_line(2000, snap1d(1760220002, "w1:p3", "done", 41, 7));
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_DONE));
    TEST_ASSERT_EQUAL_UINT8(60, rig_last(APP_ACT_TONE)->level); /* the stored volume */
    rig_line(3000, snap1d(1760220003, "w1:p3", "done", 41, 7));
    rig_at(600000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_DONE));

    rig_link(601000, APP_LINK_DOWN);
    rig_link(602000, APP_LINK_CONNECTED);
    rig_line(602100, snap1d(1760220602, "w1:p3", "done", 41, 7));
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_DONE));

    rig_line(603000, snap1d(1760220603, "w1:p3", "working", 42, 7));
    rig_line(604000, snap1d(1760220604, "w1:p3", "done", 43, 8));
    TEST_ASSERT_EQUAL_size_t(2, count_tone(APP_TONE_DONE));
    TEST_ASSERT_EQUAL_size_t(0, count_tone(APP_TONE_BLOCKED));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_FLASH));
}

/* The first snapshot after boot only notes the agents done already: a restart doesn't chime for them. One that
 * finishes while the link is down chimes when the link is back. */
static void test_done_tone_not_for_the_first_snapshot(void)
{
    rig_boot(true);
    rig_line(1000, snap1d(1760220001, "w1:p3", "done", 41, 7));
    rig_line(2000, snap1d(1760220002, "w1:p3", "done", 41, 7));
    rig_at(10000);
    TEST_ASSERT_EQUAL_size_t(0, count_tone(APP_TONE_DONE));

    rig_link(11000, APP_LINK_DOWN);
    rig_link(12000, APP_LINK_CONNECTED);
    rig_line(12100, snap1d(1760220012, "w1:p3", "done", 45, 9));
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_DONE));
}

/* While silent a finished turn makes no sound; the screen still lights. */
static void test_done_tone_silent(void)
{
    rig_boot(true);
    rig_line(1000, snap1d(1760220001, "w1:p3", "working", 40, 0));
    rig_btn(2000, APP_BTN_PWR, true);
    rig_at(4000);
    rig_btn(4100, APP_BTN_PWR, false);
    TEST_ASSERT_TRUE(rig_render()->band.silent);

    rig_at(40000); /* the screen has gone dark */
    rig_clear_log();
    rig_line(40000, snap1d(1760220040, "w1:p3", "done", 41, 7));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_TONE));
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));
    TEST_ASSERT_EQUAL_UINT8(80, rig_last(APP_ACT_BACKLIGHT)->level);
}

/* A finished turn lights the screen for 30 s and selects its agent, so the next press acts on it, unless the
 * selection is on a blocked agent (as with pages). Several in one snapshot: one tone. */
static void test_done_lights_and_selects(void)
{
    rig_boot(true);
    rig_line(1000, snap3d(1760220001, "working", 1, 0, "idle", 3));
    rig_press(APP_BTN_BOOT, 1500, 100);
    rig_press(APP_BTN_BOOT, 1800, 100);
    TEST_ASSERT_EQUAL_STRING("charlie", rig_render()->rows[rig_render()->selected].name);

    rig_at(40000); /* dark */
    rig_clear_log();
    rig_line(40000, snap3d(1760220040, "done", 4, 7, "idle", 3));
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_DONE));
    TEST_ASSERT_EQUAL_UINT8(80, rig_last(APP_ACT_BACKLIGHT)->level);
    TEST_ASSERT_EQUAL_STRING("alpha", rig_render()->rows[rig_render()->selected].name);
    rig_at(69999);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));
    rig_at(70000);
    TEST_ASSERT_EQUAL_UINT8(0, rig_last(APP_ACT_BACKLIGHT)->level);

    /* On a blocked agent, the selection stays. */
    rig_boot(true);
    rig_line(1000, snap3d(1760220001, "working", 1, 0, "blocked", 3));
    rig_at(4000); /* charlie pages, and is selected */
    TEST_ASSERT_EQUAL_STRING("charlie", rig_render()->rows[rig_render()->selected].name);
    rig_line(5000, snap3d(1760220005, "done", 4, 7, "blocked", 3));
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_DONE));
    TEST_ASSERT_EQUAL_STRING("charlie", rig_render()->rows[rig_render()->selected].name);

    /* Two finish in one snapshot: one tone. */
    rig_boot(true);
    rig_line(1000, snap3d(1760220001, "working", 1, 0, "working", 3));
    rig_line(2000, snap3d(1760220002, "done", 4, 7, "done", 5));
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_DONE));
}

static void test_one_page_per_agent_seq_across_reconnect(void)
{
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_at(4000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));

    /* The link drops and comes back; the bridge's first snapshot lacks the agent, the next has it again with
     * the same seq. Snapshots go on arriving: none pages it again. */
    rig_link(5000, APP_LINK_DOWN);
    rig_link(6000, APP_LINK_CONNECTING);
    rig_link(6500, APP_LINK_CONNECTED);
    rig_fixture(6600, "to-device/valid/snap-empty.ndjson");
    for (app_ms_t t = 7000; t < 20000; t += 1000) {
        rig_line(t, snap1(1760220007 + (int64_t)(t - 7000) / 1000, "w1:p3", "blocked", 41));
    }
    rig_at(20000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_FLASH));

    /* The same pane blocked with a new seq is a new page. */
    rig_line(21000, snap1(1760220021, "w1:p3", "blocked", 44));
    rig_at(24000);
    TEST_ASSERT_EQUAL_size_t(2, count_tone(APP_TONE_BLOCKED));
}

static void test_reminders_every_five_minutes(void)
{
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_at(4000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));

    rig_at(303999);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));
    rig_at(304000);
    TEST_ASSERT_EQUAL_size_t(2, count_tone(APP_TONE_BLOCKED));

    /* Looking at the list acknowledges nothing (6.6): the reminders go on. */
    for (app_ms_t t = 400000; t < 410000; t += 1000) {
        rig_press(APP_BTN_BOOT, t, 100);
    }
    rig_at(603999);
    TEST_ASSERT_EQUAL_size_t(2, count_tone(APP_TONE_BLOCKED));
    rig_at(604000);
    TEST_ASSERT_EQUAL_size_t(3, count_tone(APP_TONE_BLOCKED));
    rig_at(904000);
    TEST_ASSERT_EQUAL_size_t(4, count_tone(APP_TONE_BLOCKED));
}

static void test_auto_clear(void)
{
    /* Answered elsewhere: the agent leaves blocked. Its highlight goes, and no reminder follows. */
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_at(4000);
    TEST_ASSERT_TRUE(rig_render()->rows[0].paged);
    rig_line(10000, snap1(1760220010, "w1:p3", "working", 42));
    TEST_ASSERT_FALSE(rig_render()->rows[0].paged);
    rig_at(1000000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));

    /* Its seq changes while it stays blocked (a new prompt): the old page clears; the new one pages anew. */
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_at(4000);
    rig_line(10000, snap1(1760220010, "w1:p3", "blocked", 45));
    TEST_ASSERT_FALSE(rig_render()->rows[0].paged);
    rig_at(13000);
    TEST_ASSERT_TRUE(rig_render()->rows[0].paged);
    TEST_ASSERT_EQUAL_size_t(2, count_tone(APP_TONE_BLOCKED));
    rig_line(20000, snap1(1760220020, "w1:p3", "idle", 46));
    rig_at(1000000);
    TEST_ASSERT_EQUAL_size_t(2, count_tone(APP_TONE_BLOCKED));

    /* It disappears from herdr. */
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_at(4000);
    rig_fixture(10000, "to-device/valid/snap-empty.ndjson");
    rig_at(1000000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));
}

static void test_silent_toggle_at_two_seconds(void)
{
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "idle", 40));
    TEST_ASSERT_FALSE(rig_render()->band.silent);

    /* The toggle fires at 2 s while PWR is still held, and is stored: on the deadline, 30 ms after, so that a
     * release stamped before 2 s and handed over late still wins (O-22). */
    rig_btn(2000, APP_BTN_PWR, true);
    rig_at(4029);
    TEST_ASSERT_FALSE(rig_render()->band.silent);
    rig_at(4030);
    TEST_ASSERT_TRUE(rig_render()->band.silent);
    TEST_ASSERT_EQUAL_STRING("1", rig_last_store(APP_KEY_SILENT)->text);
    /* Its confirmation shows for 2 s. */
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("SILENT ON", rig_render()->title);
    rig_at(6029);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);
    rig_at(6030);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    /* Holding on, then letting go, does nothing more. */
    rig_at(9000);
    rig_btn(9000, APP_BTN_PWR, false);
    TEST_ASSERT_TRUE(rig_render()->band.silent);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_STORE));

    /* While silent a page makes no sound and no flash, but the list still updates and the backlight comes on. */
    rig_clear_log();
    rig_line(10000, snap1(1760220010, "w1:p3", "blocked", 41));
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->band.blocked);
    rig_at(13000);
    TEST_ASSERT_TRUE(rig_render()->rows[0].paged);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_TONE));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_FLASH));
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));
    rig_at(313000); /* its reminder is silent too */
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_TONE));

    /* Another 2 s hold turns it off (after a press that wakes the screen, if it slept). */
    rig_press(APP_BTN_BOOT, 313500, 100);
    rig_btn(314000, APP_BTN_PWR, true);
    rig_at(316030);
    TEST_ASSERT_FALSE(rig_render()->band.silent);
    TEST_ASSERT_EQUAL_STRING("SILENT OFF", rig_render()->title);
    TEST_ASSERT_EQUAL_STRING("0", rig_last_store(APP_KEY_SILENT)->text);
    rig_btn(316500, APP_BTN_PWR, false);
}

static void test_backlight_limit_for_page(void)
{
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_at(4000);
    TEST_ASSERT_EQUAL_UINT8(80, rig_last(APP_ACT_BACKLIGHT)->level);

    /* Unacknowledged, with snapshots still arriving, the page keeps the backlight on for 60 s, then lets it go:
     * the blocked count stays as the indicator. */
    rig_clear_log();
    for (app_ms_t t = 5000; t < 63999; t += 1000) {
        rig_line(t, snap1(1760220005 + (int64_t)(t - 5000) / 1000, "w1:p3", "blocked", 41));
    }
    rig_at(63999);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_BACKLIGHT));
    rig_at(64000);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));
    TEST_ASSERT_EQUAL_UINT8(0, rig_last(APP_ACT_BACKLIGHT)->level);
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->band.blocked);

    /* Its reminders don't turn it back on. */
    rig_at(400000);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));
}

static void test_idle_backlight_off(void)
{
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "working", 40));
    /* 30 s after the last press, with no page standing, the backlight goes off. */
    rig_press(APP_BTN_BOOT, 10000, 100);
    rig_at(39999);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_BACKLIGHT));
    rig_at(40000);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));
    TEST_ASSERT_EQUAL_UINT8(0, rig_last(APP_ACT_BACKLIGHT)->level);

    /* Rendering stops: snapshots change the list and the clock moves on, but nothing is drawn, and the core
     * asks for no call just to turn the clock over. */
    rig_clear_log();
    rig_line(50000, snap1(1760220050, "w1:p3", "idle", 41));
    rig_line(60000, snap1(1760220060, "w1:p3", "working", 42)); /* not done: a finished turn lights it */
    TEST_ASSERT_EQUAL_UINT64(APP_NEVER, rig.out.deadline);
    rig_at(400000);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_RENDER));

    /* With no press at all after boot, it goes off at 30 s. */
    rig_boot(true);
    rig_at(29999);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_BACKLIGHT));
    rig_at(30000);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_BACKLIGHT));
}

/* card-permission.ndjson's card, for pane at seq. */
static const char *card_for(const char *pane, uint64_t seq)
{
    static char buf[512];
    snprintf(buf, sizeof(buf),
             "{\"t\":\"card\",\"id\":9,\"pane\":\"%s\",\"st\":\"blocked\",\"seq\":%llu,\"hash\":"
             "\"6a0c2aae60a7e3df1151bdb24c231c88e163a90d8a7ed200c43a454bc910e402\",\"kind\":\"permission\","
             "\"summary\":\"Bash command\",\"lines\":[\"touch approved.txt\"],\"can\":[\"approve\",\"deny\",\"jump\"]}",
             pane, (unsigned long long)seq);
    return buf;
}

static void test_card_display_acknowledges(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson"); /* three blocked agents page at 4 s */
    rig_at(4000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED)); /* one tone for the three (O-20) */
    TEST_ASSERT_TRUE(rig_render()->rows[0].paged);

    /* api-refactor's card (w1:p3, seq 41) is shown: that (pane, seq) is acknowledged. */
    rig_open_card(10000, "api-refactor", "to-device/valid/card-permission.ndjson");
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_FALSE(rig_render()->rows[0].paged);
    TEST_ASSERT_TRUE(rig_render()->rows[1].paged);

    /* The other two go on reminding, with one tone each time. */
    rig_clear_log();
    rig_at(304000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));

    /* Once their cards are shown too, nothing reminds: api-refactor's reminders had stopped. */
    rig_open_card_line(310000, "release", card_for("w1:p5", 9));
    rig_open_card_line(320000, "colour-picker", card_for("w1:p4", 33));
    rig_clear_log();
    rig_at(1000000);
    TEST_ASSERT_EQUAL_size_t(0, count_tone(APP_TONE_BLOCKED));
}

static void test_no_page_while_link_down(void)
{
    /* Blocked, and the link drops inside the 3 s: the old snapshot never pages, and the list says how old it is. */
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_link(2000, APP_LINK_DOWN);
    rig_at(61000);
    TEST_ASSERT_EQUAL_size_t(0, count_tone(APP_TONE_BLOCKED));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_FLASH));
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    rig_press(APP_BTN_BOOT, 61000, 100); /* asleep since 30 s: wake it to look */
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->band.blocked);
    TEST_ASSERT_EQUAL_STRING("LAST SEEN 1m", rig_render()->title);

    /* Back, and a fresh snapshot still has it blocked: it pages 3 s after that. */
    rig_link(70000, APP_LINK_CONNECTED);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->title);
    rig_line(70500, snap1(1760220070, "w1:p3", "blocked", 41));
    rig_at(73499);
    TEST_ASSERT_EQUAL_size_t(0, count_tone(APP_TONE_BLOCKED));
    rig_at(73500);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));

    /* A page that has fired has no reminders while the link is down. */
    rig_link(80000, APP_LINK_DOWN);
    rig_at(80000 + 600000);
    TEST_ASSERT_EQUAL_size_t(1, count_tone(APP_TONE_BLOCKED));

    /* herdr offline is stale too: the list stays as it was, marked, and nothing pages from it. */
    rig_boot(true);
    rig_line(1000, snap1(1760220001, "w1:p3", "blocked", 41));
    rig_fixture(2000, "to-device/valid/snap-offline.ndjson");
    TEST_ASSERT_TRUE(rig_render()->band.herdr_offline);
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->n_rows);
    rig_at(60000);
    TEST_ASSERT_EQUAL_size_t(0, count_tone(APP_TONE_BLOCKED));
}

TEST_AREA(alerts)
{
    RUN_TEST(test_no_page_while_link_down);
    RUN_TEST(test_card_display_acknowledges);
    RUN_TEST(test_idle_backlight_off);
    RUN_TEST(test_backlight_limit_for_page);
    RUN_TEST(test_silent_toggle_at_two_seconds);
    RUN_TEST(test_blocked_pages_after_debounce);
    RUN_TEST(test_many_pages_at_once);
    RUN_TEST(test_card_opened_in_the_debounce_pages_nothing);
    RUN_TEST(test_page_selects_the_paged_agent);
    RUN_TEST(test_one_page_per_agent_seq_across_reconnect);
    RUN_TEST(test_reminders_every_five_minutes);
    RUN_TEST(test_auto_clear);
    RUN_TEST(test_done_tone_once);
    RUN_TEST(test_done_tone_not_for_the_first_snapshot);
    RUN_TEST(test_done_tone_silent);
    RUN_TEST(test_done_lights_and_selects);
}
