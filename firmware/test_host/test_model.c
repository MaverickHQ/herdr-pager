/* The model: sort order, the status bar, rows, selection, settings. */
#include <string.h>

#include "app.h"
#include "test_registry.h"
#include "test_rig.h"

/* The rows' names, in order, joined by '|'. */
static const char *row_names(void)
{
    static char buf[16 * (APP_NAME_MAX + 1) + 1];
    const app_render_t *r = rig_render();
    buf[0] = '\0';
    for (int i = 0; i < r->n_rows; i++) {
        strcat(buf, r->rows[i].name);
        if (i + 1 < r->n_rows) {
            strcat(buf, "|");
        }
    }
    return buf;
}

static const char *row_glyphs(void)
{
    static char buf[APP_MAX_AGENTS + 1];
    const app_render_t *r = rig_render();
    for (int i = 0; i < r->n_rows; i++) {
        buf[i] = r->rows[i].glyph;
    }
    buf[r->n_rows] = '\0';
    return buf;
}

static void test_snapshot_sorts_by_urgency(void)
{
    rig_boot(true);

    /* The bridge's own order already holds in the fixture: 16 agents, 4 more. */
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, r->screen);
    TEST_ASSERT_EQUAL_UINT8(16, r->n_rows);
    TEST_ASSERT_EQUAL_UINT32(4, r->more);
    TEST_ASSERT_EQUAL_STRING("api-refactor|release|colour-picker|docs-pass|claude w2:p2|codex w2:p5|tests|migrations|"
                             "lint-fix|codex w3:p4|spike-ble|scratch|claude w4:p3|bench|infra-plan|"
                             "payments-ser..-migration",
                             row_names());
    TEST_ASSERT_EQUAL_STRING("!!!**?>>>>......", row_glyphs());

    /* The device sorts for itself: the same order from agents sent in reverse urgency, ties by the oldest
     * state change (the smallest since), equal ties in the order received. */
    rig_line(2000, "{\"t\":\"snap\",\"now\":1760220002,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                   "{\"pane\":\"w1:p1\",\"name\":\"idle-a\",\"kind\":\"claude\",\"st\":\"idle\",\"seq\":1,\"dseq\":0,"
                   "\"since\":100,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w1:p2\",\"name\":\"work-new\",\"kind\":\"claude\",\"st\":\"working\",\"seq\":2,"
                   "\"dseq\":0,\"since\":300,\"can\":[\"show\",\"jump\",\"interrupt\"]},"
                   "{\"pane\":\"w1:p3\",\"name\":\"work-old\",\"kind\":\"claude\",\"st\":\"working\",\"seq\":3,"
                   "\"dseq\":0,\"since\":200,\"can\":[\"show\",\"jump\",\"interrupt\"]},"
                   "{\"pane\":\"w1:p4\",\"name\":\"unk\",\"kind\":\"codex\",\"st\":\"unknown\",\"seq\":4,\"dseq\":0,"
                   "\"since\":50,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w1:p5\",\"name\":\"done-a\",\"kind\":\"claude\",\"st\":\"done\",\"seq\":5,\"dseq\":7,"
                   "\"since\":400,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w1:p6\",\"name\":\"blocked-new\",\"kind\":\"claude\",\"st\":\"blocked\",\"seq\":6,"
                   "\"dseq\":0,\"since\":900,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w1:p7\",\"name\":\"blocked-old\",\"kind\":\"claude\",\"st\":\"blocked\",\"seq\":7,"
                   "\"dseq\":0,\"since\":800,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w1:p8\",\"name\":\"work-tie\",\"kind\":\"claude\",\"st\":\"working\",\"seq\":8,"
                   "\"dseq\":0,\"since\":200,\"can\":[\"show\",\"jump\",\"interrupt\"]}]}");
    TEST_ASSERT_EQUAL_STRING("blocked-old|blocked-new|done-a|unk|work-old|work-tie|work-new|idle-a", row_names());
    TEST_ASSERT_EQUAL_STRING("!!*?>>>.", row_glyphs());
    TEST_ASSERT_EQUAL_UINT32(0, rig_render()->more);

    /* A done agent whose card has been shown is seen: it sorts with idle (by its since) and shows done, dimmed. */
    rig_fixture(3000, "to-device/valid/snap-16-agents-more.ndjson");
    rig_open_card(4000, "docs-pass", "to-device/valid/card-status-done.ndjson"); /* w2:p1 */
    rig_fixture(5000, "to-device/valid/snap-16-agents-more.ndjson");
    r = rig_render();
    TEST_ASSERT_EQUAL_STRING("api-refactor|release|colour-picker|claude w2:p2|codex w2:p5|tests|migrations|"
                             "lint-fix|codex w3:p4|spike-ble|scratch|claude w4:p3|bench|infra-plan|"
                             "payments-ser..-migration|docs-pass",
                             row_names());
    TEST_ASSERT_EQUAL_STRING("!!!*?>>>>......*", row_glyphs());
    TEST_ASSERT_TRUE(r->rows[15].dim);
    for (int i = 0; i < 15; i++) {
        TEST_ASSERT_FALSE_MESSAGE(r->rows[i].dim, r->rows[i].name);
    }
}

static void test_unknown_never_idle(void)
{
    rig_boot(true);
    /* An unknown agent older than every idle one, and one whose earlier done card was seen: neither shows as
     * idle. Unknown keeps its own glyph, undimmed, between done and working. */
    rig_line(1000, "{\"t\":\"snap\",\"now\":1760220002,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                   "{\"pane\":\"w1:p1\",\"name\":\"idle-a\",\"kind\":\"claude\",\"st\":\"idle\",\"seq\":1,\"dseq\":0,"
                   "\"since\":100,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w2:p1\",\"name\":\"was-done\",\"kind\":\"claude\",\"st\":\"done\",\"seq\":37,"
                   "\"dseq\":12,\"since\":20,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w1:p3\",\"name\":\"work\",\"kind\":\"claude\",\"st\":\"working\",\"seq\":3,\"dseq\":0,"
                   "\"since\":10,\"can\":[\"show\",\"jump\",\"interrupt\"]},"
                   "{\"pane\":\"w1:p4\",\"name\":\"unk\",\"kind\":\"codex\",\"st\":\"unknown\",\"seq\":4,\"dseq\":0,"
                   "\"since\":5,\"can\":[\"show\",\"jump\"]}]}");
    rig_open_card(2000, "was-done", "to-device/valid/card-status-done.ndjson"); /* w2:p1: now seen */
    rig_line(3000, "{\"t\":\"snap\",\"now\":1760220003,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                   "{\"pane\":\"w1:p1\",\"name\":\"idle-a\",\"kind\":\"claude\",\"st\":\"idle\",\"seq\":1,\"dseq\":0,"
                   "\"since\":100,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w2:p1\",\"name\":\"was-done\",\"kind\":\"claude\",\"st\":\"unknown\",\"seq\":38,"
                   "\"dseq\":12,\"since\":30,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w1:p3\",\"name\":\"work\",\"kind\":\"claude\",\"st\":\"working\",\"seq\":3,\"dseq\":0,"
                   "\"since\":10,\"can\":[\"show\",\"jump\",\"interrupt\"]},"
                   "{\"pane\":\"w1:p4\",\"name\":\"unk\",\"kind\":\"codex\",\"st\":\"unknown\",\"seq\":4,\"dseq\":0,"
                   "\"since\":5,\"can\":[\"show\",\"jump\"]}]}");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL_STRING("unk|was-done|work|idle-a", row_names());
    TEST_ASSERT_EQUAL_STRING("?" "?>.", row_glyphs()); /* split: "??>" is a trigraph */
    TEST_ASSERT_FALSE(r->rows[0].dim);
    TEST_ASSERT_FALSE(r->rows[1].dim);
}

static void test_status_bar_fields(void)
{
    rig_boot(true);
    const app_band_t *b = &rig_render()->band;
    TEST_ASSERT_EQUAL_STRING("", b->clock); /* no time yet: the RTC was unset */
    TEST_ASSERT_EQUAL(APP_LINK_CONNECTED, b->link);
    TEST_ASSERT_TRUE(b->wifi);
    TEST_ASSERT_EQUAL_STRING("home", b->ssid);
    TEST_ASSERT_FALSE(b->on_battery);

    /* now 1760220001 is 22:00:01 UTC. Counts: blocked, unseen done, working (unknown and idle aren't counted). */
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson");
    b = &rig_render()->band;
    TEST_ASSERT_EQUAL_UINT8(3, b->blocked);
    TEST_ASSERT_EQUAL_UINT8(2, b->done);
    TEST_ASSERT_EQUAL_UINT8(4, b->working);
    TEST_ASSERT_EQUAL_STRING("22:00", b->clock);

    /* The clock runs on without another snapshot: the core asks to be called at the minute. */
    rig_at(59999);
    TEST_ASSERT_EQUAL_STRING("22:00", rig_render()->band.clock);
    rig_at(60000);
    TEST_ASSERT_EQUAL_STRING("22:01", rig_render()->band.clock);

    /* A seen done agent isn't counted. */
    rig_open_card(61000, "docs-pass", "to-device/valid/card-status-done.ndjson");
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->band.done);

    /* Each link state, the network in use or none, and ON BATTERY. */
    rig_link(62000, APP_LINK_DOWN);
    TEST_ASSERT_EQUAL(APP_LINK_DOWN, rig_render()->band.link);
    rig_link(62100, APP_LINK_CONNECTING);
    TEST_ASSERT_EQUAL(APP_LINK_CONNECTING, rig_render()->band.link);
    rig_link(62200, APP_LINK_AUTH_REFUSED);
    TEST_ASSERT_EQUAL(APP_LINK_AUTH_REFUSED, rig_render()->band.link);
    rig_wifi(62300, false, "");
    TEST_ASSERT_FALSE(rig_render()->band.wifi);
    rig_wifi(62400, true, "hotspot");
    TEST_ASSERT_TRUE(rig_render()->band.wifi);
    TEST_ASSERT_EQUAL_STRING("hotspot", rig_render()->band.ssid);
    rig_power(62500, APP_POWER_BATTERY);
    TEST_ASSERT_TRUE(rig_render()->band.on_battery);
}

static void test_row_glyph_name_kind_age(void)
{
    rig_boot(true);
    /* At now 1760220001: ages 101 s, 1,001 s, 2,001 s, 20,001 s and 200,001 s. */
    rig_line(1000, "{\"t\":\"snap\",\"now\":1760220001,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                   "{\"pane\":\"w1:p3\",\"name\":\"api-refactor\",\"kind\":\"claude\",\"st\":\"blocked\",\"seq\":41,"
                   "\"dseq\":7,\"since\":1760219900,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w2:p1\",\"name\":\"docs-pass\",\"kind\":\"claude\",\"st\":\"done\",\"seq\":37,"
                   "\"dseq\":12,\"since\":1760219000,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w2:p5\",\"name\":\"codex w2:p5\",\"kind\":\"codex\",\"st\":\"unknown\",\"seq\":18,"
                   "\"dseq\":0,\"since\":1760218000,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w4:p1\",\"name\":\"spike-ble\",\"kind\":\"claude\",\"st\":\"idle\",\"seq\":2,"
                   "\"dseq\":0,\"since\":1760200000,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w4:p2\",\"name\":\"old\",\"kind\":\"claude\",\"st\":\"idle\",\"seq\":5,"
                   "\"dseq\":0,\"since\":1760020000,\"can\":[\"show\",\"jump\"]}]}");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL_UINT8(5, r->n_rows);
    static const struct {
        char glyph;
        const char *name, *kind, *age;
    } want[] = {
        {'!', "api-refactor", "claude", "1m"}, {'*', "docs-pass", "claude", "16m"},
        {'?', "codex w2:p5", "codex", "33m"},  {'.', "old", "claude", "2d"},
        {'.', "spike-ble", "claude", "5h"},
    };
    for (int i = 0; i < 5; i++) {
        TEST_ASSERT_EQUAL_CHAR(want[i].glyph, r->rows[i].glyph);
        TEST_ASSERT_EQUAL_STRING(want[i].name, r->rows[i].name);
        TEST_ASSERT_EQUAL_STRING(want[i].kind, r->rows[i].kind);
        TEST_ASSERT_EQUAL_STRING(want[i].age, r->rows[i].age);
    }

    /* Ages run on between snapshots: api-refactor turns 2m at 120 s, 19 s later. */
    rig_at(19999);
    TEST_ASSERT_EQUAL_STRING("1m", rig_render()->rows[0].age);
    rig_at(20000);
    TEST_ASSERT_EQUAL_STRING("2m", rig_render()->rows[0].age);
}

static void test_done_card_marks_seen_without_focus(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson");
    TEST_ASSERT_EQUAL_UINT8(2, rig_render()->band.done);
    rig_clear_log();
    app_ms_t t = rig_open_card(2000, "docs-pass", "to-device/valid/card-status-done.ndjson");
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->band.done);
    TEST_ASSERT_TRUE(rig_render()->rows[15].dim);
    TEST_ASSERT_EQUAL_STRING("docs-pass", rig_render()->rows[15].name);
    /* Only the show went out: no jump (seen is local; herdr's focus is untouched). */
    rig_at(t + 10000);
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    TEST_ASSERT_NOT_NULL(strstr(rig_sent(0), "\"t\":\"show\""));
}

static const char *snap_at(int64_t now)
{
    static char buf[256];
    snprintf(buf, sizeof(buf),
             "{\"t\":\"snap\",\"now\":%lld,\"herdr\":\"ok\",\"more\":0,\"agents\":[{\"pane\":\"w4:p1\",\"name\":\"a\","
             "\"kind\":\"claude\",\"st\":\"idle\",\"seq\":2,\"dseq\":0,\"since\":1760200000,\"can\":[\"show\"]}]}",
             (long long)now);
    return buf;
}

static void test_clock_sync_on_drift(void)
{
    /* The RTC's time at boot is the clock until a snapshot says otherwise. */
    rig_boot(true);
    rig.boot.rtc_s = 1760220001 - 3600; /* 21:00:01 */
    app_init(&rig.s);
    app_event_t ev = {.kind = APP_EV_BOOT, .boot = &rig.boot};
    rig_event(0, &ev);
    TEST_ASSERT_EQUAL_STRING("21:00", rig_render()->band.clock);

    /* An hour of drift: the clock is set and the RTC written. */
    rig_clear_log();
    rig_line(1000, snap_at(1760220001));
    TEST_ASSERT_EQUAL_STRING("22:00", rig_render()->band.clock);
    TEST_ASSERT_EQUAL_INT64(1760220001, rig_last(APP_ACT_RTC)->unix_s);

    /* Within 2 s, nothing is written. */
    rig_clear_log();
    rig_line(11000, snap_at(1760220011));
    rig_line(21000, snap_at(1760220023)); /* 2 s ahead */
    rig_line(31000, snap_at(1760220029)); /* 2 s behind */
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_RTC));

    /* More than 2 s: synced again. */
    rig_line(41000, snap_at(1760220044)); /* 3 s ahead */
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_RTC));
    TEST_ASSERT_EQUAL_INT64(1760220044, rig_last(APP_ACT_RTC)->unix_s);
    rig_line(51000, snap_at(1760220054));
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_RTC)); /* and the clock follows the bridge's from there */
}

static void test_card_band_names_its_agent(void)
{
    rig_boot(true);
    rig_line(1000, "{\"t\":\"snap\",\"now\":1760220001,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                   "{\"pane\":\"w3:p1\",\"name\":\"4.7 C-Claude-Code-Herdr\",\"kind\":\"claude\",\"st\":\"working\","
                   "\"seq\":50,\"dseq\":4,\"since\":1760219700,\"can\":[\"show\",\"jump\",\"interrupt\"]},"
                   "{\"pane\":\"w3:p2\",\"name\":\"migrations\",\"kind\":\"claude\",\"st\":\"working\",\"seq\":44,"
                   "\"dseq\":0,\"since\":1760219750,\"can\":[\"show\",\"jump\",\"interrupt\"]}]}");
    /* The list's band shows the counts. */
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.agent);
    TEST_ASSERT_EQUAL_UINT8(2, rig_render()->band.working);

    /* An open card's band names the session it's from: the agent with the card's pane (w3:p1). */
    app_ms_t t = rig_open_card(2000, "4.7 C-Claude-Code-Herdr", "to-device/valid/card-status.ndjson");
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("4.7 C-Claude-Code-Herdr", rig_render()->band.agent);

    /* Back on the list (the card times out), the counts again. */
    rig_at(t + 61000);
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.agent);

    /* A card whose pane no agent in the model has: the counts, as before. */
    rig_open_card_line(t + 62000, "migrations",
                       "{\"t\":\"card\",\"id\":1,\"pane\":\"w9:p9\",\"st\":\"working\",\"seq\":3,\"hash\":"
                       "\"c4e3eb3e0d103f5a6f900820371ad8d02f94c5ef909e46de81849579171e36a3\",\"kind\":\"status\","
                       "\"summary\":\"Elsewhere\",\"lines\":[],\"can\":[\"jump\"]}");
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("", rig_render()->band.agent);
}

#define CARD_HASH "c4e3eb3e0d103f5a6f900820371ad8d02f94c5ef909e46de81849579171e36a3"

/* An open card's body and cells, then PWR back to the list. */
static app_ms_t assert_card(app_ms_t t, const char *first, const char *title, const char *line)
{
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, r->screen);
    TEST_ASSERT_EQUAL_STRING(first, r->lines[0]);
    TEST_ASSERT_EQUAL_STRING(title, r->title);
    TEST_ASSERT_EQUAL_STRING(line, r->lines[1]);
    TEST_ASSERT_EQUAL_STRING("BACK", r->band.pwr.text);
    TEST_ASSERT_EQUAL_STRING("HOLD: MENU", r->band.boot.text);
    rig_press(APP_BTN_PWR, t + 600, 100); /* past the card's 500 ms guard */
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
    return t + 900;
}

/* A card for an agent that needs nothing says so (the owner's report, 2026-11-01, open-items O-82). The bridge's
 * status card has no summary or lines when the agent's screen shows no prompt, and the pager drew an empty body
 * with blank cells. Now its first line says nothing needs you, then what the agent is doing and for how long, then
 * its kind and where it works. A blocked agent whose prompt can't be read says to answer at the keyboard (O-56).
 * A card that has its own text keeps it. On every card with no verdict, the cells say what the buttons do: PWR
 * goes back, and a BOOT hold opens the agent's menu. */
static void test_status_card_says_nothing_needs_you(void)
{
    rig_boot(true);
    rig_line(1000, "{\"t\":\"snap\",\"now\":1760220001,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                   "{\"pane\":\"w1:p1\",\"name\":\"stuck\",\"kind\":\"claude\",\"st\":\"blocked\",\"seq\":7,"
                   "\"dseq\":0,\"since\":1760219941,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w2:p1\",\"name\":\"docs-pass\",\"kind\":\"claude\",\"st\":\"done\",\"seq\":9,"
                   "\"dseq\":3,\"since\":1760219821,\"can\":[\"show\",\"jump\"]},"
                   "{\"pane\":\"w3:p1\",\"name\":\"tests\",\"kind\":\"claude\",\"at\":\"api-refactor\","
                   "\"st\":\"working\",\"seq\":50,\"dseq\":4,\"since\":1760219700,"
                   "\"can\":[\"show\",\"jump\",\"interrupt\"]},"
                   "{\"pane\":\"w4:p1\",\"name\":\"spike-ble\",\"kind\":\"codex\",\"st\":\"idle\",\"seq\":2,"
                   "\"dseq\":0,\"since\":1760200000,\"can\":[\"show\",\"jump\"]}]}");

    app_ms_t t = rig_open_card_line(2000, "tests",
                                    "{\"t\":\"card\",\"id\":1,\"pane\":\"w3:p1\",\"st\":\"working\",\"seq\":50,"
                                    "\"hash\":\"" CARD_HASH "\",\"kind\":\"status\",\"summary\":\"\",\"lines\":[],"
                                    "\"can\":[\"interrupt\",\"jump\"]}");
    t = assert_card(t, "NOTHING NEEDS YOU", "Working for 5m", "claude in api-refactor");

    t = rig_open_card_line(t, "spike-ble",
                           "{\"t\":\"card\",\"id\":1,\"pane\":\"w4:p1\",\"st\":\"idle\",\"seq\":2,"
                           "\"hash\":\"" CARD_HASH "\",\"kind\":\"status\",\"summary\":\"\",\"lines\":[],"
                           "\"can\":[\"jump\"]}");
    t = assert_card(t, "NOTHING NEEDS YOU", "Idle for 5h", "codex");

    t = rig_open_card_line(t, "docs-pass",
                           "{\"t\":\"card\",\"id\":1,\"pane\":\"w2:p1\",\"st\":\"done\",\"seq\":9,"
                           "\"hash\":\"" CARD_HASH "\",\"kind\":\"status\",\"summary\":\"\",\"lines\":[],"
                           "\"can\":[\"jump\"]}");
    t = assert_card(t, "NOTHING NEEDS YOU", "Done 3m ago", "claude");

    t = rig_open_card_line(t, "stuck",
                           "{\"t\":\"card\",\"id\":1,\"pane\":\"w1:p1\",\"st\":\"blocked\",\"seq\":7,"
                           "\"hash\":\"" CARD_HASH "\",\"kind\":\"status\",\"summary\":\"\",\"lines\":[],"
                           "\"can\":[\"jump\"]}");
    t = assert_card(t, "WAITING - ANSWER AT KEYBOARD", "Blocked for 1m", "claude");

    /* A status card with its own text, and a question card: their text as before, and the same cells. */
    t = rig_open_card_line(t, "docs-pass",
                           "{\"t\":\"card\",\"id\":1,\"pane\":\"w2:p1\",\"st\":\"done\",\"seq\":9,"
                           "\"hash\":\"" CARD_HASH "\",\"kind\":\"status\",\"summary\":\"Update the README\","
                           "\"lines\":[],\"can\":[\"jump\"]}");
    t = assert_card(t, "", "Update the README", "");
    rig_open_card(t, "stuck", "to-device/valid/card-question.ndjson");
    TEST_ASSERT_EQUAL_STRING("QUESTION - ANSWER AT KEYBOARD", rig_render()->lines[0]);
    TEST_ASSERT_EQUAL_STRING("BACK", rig_render()->band.pwr.text);
    TEST_ASSERT_EQUAL_STRING("HOLD: MENU", rig_render()->band.boot.text);
}

/* With agents and none needing you (none blocked, and no done agent unseen), the band says ALL CLEAR (the owner's
 * decision, 2026-11-01, open-items O-85), but only while the list is live: on the list, linked, with herdr
 * answering. */
static void test_band_says_all_clear(void)
{
    rig_boot(true);
    TEST_ASSERT_FALSE(rig_render()->band.all_clear); /* no list yet */
    rig_snap_quiet(1000);                             /* two working, one idle */
    TEST_ASSERT_TRUE(rig_render()->band.all_clear);

    app_ms_t t = rig_open_card(2000, "tests", "to-device/valid/card-status.ndjson");
    TEST_ASSERT_FALSE(rig_render()->band.all_clear); /* a card's band names its agent */
    rig_press(APP_BTN_PWR, t + 600, 100);
    TEST_ASSERT_TRUE(rig_render()->band.all_clear);

    rig_fixture(t + 1000, "to-device/valid/snap.ndjson"); /* a blocked agent */
    TEST_ASSERT_FALSE(rig_render()->band.all_clear);
    rig_snap_quiet(t + 2000);
    TEST_ASSERT_TRUE(rig_render()->band.all_clear);

    rig_link(t + 3000, APP_LINK_DOWN); /* LAST SEEN: the list may be old */
    TEST_ASSERT_FALSE(rig_render()->band.all_clear);
    rig_link(t + 4000, APP_LINK_CONNECTING);
    rig_link(t + 4500, APP_LINK_CONNECTED);
    rig_snap_quiet(t + 5000);
    TEST_ASSERT_TRUE(rig_render()->band.all_clear);
    rig_fixture(t + 6000, "to-device/valid/snap-offline.ndjson"); /* the list freezes, HERDR OFFLINE */
    TEST_ASSERT_FALSE(rig_render()->band.all_clear);
}

/* A power-source event at t changes the band's ON BATTERY flag to want, and nothing else on screen. */
static void power_flag(app_ms_t t, app_power_t power, bool want)
{
    static app_render_t before;
    rig_at(t);
    memcpy(&before, rig_render(), sizeof(before));
    rig_power(t, power);
    TEST_ASSERT_EQUAL(want, rig_render()->band.on_battery);
    before.band.on_battery = want;
    TEST_ASSERT_EQUAL_MEMORY(&before, rig_render(), sizeof(before));
}

static void test_on_battery_flag(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson");
    TEST_ASSERT_FALSE(rig_render()->band.on_battery); /* unknown at boot: no flag */

    power_flag(2000, APP_POWER_BATTERY, true);
    power_flag(3000, APP_POWER_BATTERY, true);
    power_flag(4000, APP_POWER_USB, false);
    power_flag(5000, APP_POWER_BATTERY, true);
    power_flag(6000, APP_POWER_UNKNOWN, false);

    /* The band is on every screen, a card's included. */
    power_flag(7000, APP_POWER_BATTERY, true);
    app_ms_t t = rig_open_card(8000, "docs-pass", "to-device/valid/card-status-done.ndjson");
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    TEST_ASSERT_TRUE(rig_render()->band.on_battery);
    power_flag(t, APP_POWER_USB, false);
    power_flag(t + 1000, APP_POWER_BATTERY, true);
}

TEST_AREA(model)
{
    RUN_TEST(test_card_band_names_its_agent);
    RUN_TEST(test_on_battery_flag);
    RUN_TEST(test_clock_sync_on_drift);
    RUN_TEST(test_done_card_marks_seen_without_focus);
    RUN_TEST(test_snapshot_sorts_by_urgency);
    RUN_TEST(test_unknown_never_idle);
    RUN_TEST(test_status_bar_fields);
    RUN_TEST(test_status_card_says_nothing_needs_you);
    RUN_TEST(test_band_says_all_clear);
    RUN_TEST(test_row_glyph_name_kind_age);
}
