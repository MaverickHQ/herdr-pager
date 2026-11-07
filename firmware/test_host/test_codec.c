/* The codec, against the protocol fixtures (PAGER_TESTDATA "/protocol"). */
#include <dirent.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "app.h"
#include "test_registry.h"
#include "test_rig.h"

#define SHORT 100

/* A show in flight: the 16-agent list, and api-refactor's card asked for. Returns the show's id. */
static uint32_t show_pending(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-16-agents-more.ndjson");
    rig_press(APP_BTN_PWR, 1500, SHORT);
    return rig_sent_id(rig_sent(rig_n_sent() - 1));
}

/* A verdict in flight: api-refactor's permission card approved. Returns the approve's id. */
static uint32_t verdict_pending(void)
{
    uint32_t id = show_pending();
    rig_card(2000, "to-device/valid/card-permission.ndjson", id);
    rig_btn(3000, APP_BTN_BOOT, true);
    rig_at(4000);
    rig_btn(4100, APP_BTN_BOOT, false);
    return rig_sent_id(rig_sent(rig_n_sent() - 1));
}

static void test_codec_overflow_and_malformed(void)
{
    /* 8,193 bytes, not counting the newline: dropped, and a fresh snapshot asked for. */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_clear_log();
    size_t len;
    rig_read_fixture("to-device/invalid/snap-8193-bytes.ndjson", &len);
    TEST_ASSERT_EQUAL_size_t(8193, len);
    rig_fixture(2000, "to-device/invalid/snap-8193-bytes.ndjson");
    TEST_ASSERT_EQUAL_size_t(1, rig_n_sent());
    TEST_ASSERT_EQUAL_STRING("{\"t\":\"list\",\"id\":1}", rig_sent(0)); /* the boot's first request; pinned (O-38) */
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->n_rows);
    TEST_ASSERT_EQUAL_STRING("api-refactor", rig_render()->rows[0].name);

    /* 8,192 bytes: accepted. */
    rig_fixture(2500, "to-device/valid/snap-empty.ndjson");
    TEST_ASSERT_EQUAL_UINT8(0, rig_render()->n_rows);
    rig_read_fixture("to-device/valid/snap-8192-bytes.ndjson", &len);
    TEST_ASSERT_EQUAL_size_t(8192, len);
    rig_fixture(3000, "to-device/valid/snap-8192-bytes.ndjson");
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->n_rows);
    TEST_ASSERT_EQUAL_STRING("api-refactor", rig_render()->rows[0].name);

    /* Every other invalid example changes nothing at all, and sends nothing. A card or a result is fed as the
     * answer to the request in flight, so that only its own fault can stop it. */
    char dir[512];
    snprintf(dir, sizeof(dir), "%s/protocol/to-device/invalid", PAGER_TESTDATA);
    DIR *d = opendir(dir);
    TEST_ASSERT_NOT_NULL(d);
    int n_files = 0;
    struct dirent *e;
    int n_ignored = 0;
    while ((e = readdir(d)) != NULL) {
        if (!strstr(e->d_name, ".ndjson") || strcmp(e->d_name, "snap-8193-bytes.ndjson") == 0) {
            continue;
        }
        /* The device ignores the bridge's hello and pong, valid or not (the fixtures' README; below), so their
         * invalid examples can't fail here: they're the bridge's to send right (open-items O-39). */
        if (strncmp(e->d_name, "hello-", 6) == 0 || strncmp(e->d_name, "pong-", 5) == 0) {
            n_ignored++;
            continue;
        }
        char rel[300];
        snprintf(rel, sizeof(rel), "to-device/invalid/%s", e->d_name);
        static char line[APP_LINE_MAX + 64];
        snprintf(line, sizeof(line), "%s", rig_read_fixture(rel, &len));
        bool card = strncmp(e->d_name, "card-", 5) == 0, result = strncmp(e->d_name, "result-", 7) == 0;
        uint32_t id = result ? verdict_pending() : show_pending();
        static app_state_t before;
        size_t sent = rig_n_sent();
        if ((card || result) && strstr(line, "\"id\":")) {
            rig_card_line(rig.now, line, id);
        } else {
            before = rig.s;
            rig_line(rig.now, line);
        }
        if ((card || result) && strstr(line, "\"id\":")) {
            /* Answering with a valid line would have changed the screen; the invalid one leaves it. */
            TEST_ASSERT_EQUAL_MESSAGE(result ? APP_SCREEN_CARD : APP_SCREEN_LIST, rig_render()->screen, e->d_name);
        } else {
            TEST_ASSERT_EQUAL_MEMORY_MESSAGE(&before, &rig.s, sizeof(before), e->d_name);
        }
        TEST_ASSERT_EQUAL_size_t_MESSAGE(sent, rig_n_sent(), e->d_name);
        n_files++;
    }
    closedir(d);
    TEST_ASSERT_GREATER_OR_EQUAL_INT(36, n_files);
    TEST_ASSERT_EQUAL_INT(3, n_ignored); /* hello-no-now, hello-version-2, pong-no-now */

    /* The bridge's hello and pong, valid, change nothing either: the device reads neither. */
    static const char *const ignored[] = {"to-device/valid/hello.ndjson", "to-device/valid/pong.ndjson"};
    for (size_t k = 0; k < sizeof(ignored) / sizeof(ignored[0]); k++) {
        static app_state_t before;
        before = rig.s;
        size_t sent = rig_n_sent();
        rig_fixture(rig.now, ignored[k]);
        TEST_ASSERT_EQUAL_MEMORY_MESSAGE(&before, &rig.s, sizeof(before), ignored[k]);
        TEST_ASSERT_EQUAL_size_t_MESSAGE(sent, rig_n_sent(), ignored[k]);
    }

    /* And the valid answers do change it: the checks above aren't vacuous. */
    uint32_t id = show_pending();
    rig_card(rig.now, "to-device/valid/card-permission.ndjson", id);
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, rig_render()->screen);
    id = verdict_pending();
    rig_card(rig.now, "to-device/valid/result-ok.ndjson", id);
    TEST_ASSERT_EQUAL(APP_SCREEN_TOAST, rig_render()->screen);

    /* Every valid card is shown and every valid result answers its verdict, read from the directory as the
     * bridge's TestProtocolFixturesParse reads it, so the two sides can't read a valid line differently (lane
     * core's review). */
    snprintf(dir, sizeof(dir), "%s/protocol/to-device/valid", PAGER_TESTDATA);
    d = opendir(dir);
    TEST_ASSERT_NOT_NULL(d);
    int n_cards = 0, n_results = 0;
    while ((e = readdir(d)) != NULL) {
        bool card = strncmp(e->d_name, "card-", 5) == 0, result = strncmp(e->d_name, "result-", 7) == 0;
        if (!strstr(e->d_name, ".ndjson") || (!card && !result)) {
            continue;
        }
        char rel[300];
        snprintf(rel, sizeof(rel), "to-device/valid/%s", e->d_name);
        id = result ? verdict_pending() : show_pending();
        rig_card(rig.now, rel, id);
        TEST_ASSERT_EQUAL_MESSAGE(result ? APP_SCREEN_TOAST : APP_SCREEN_CARD, rig_render()->screen, e->d_name);
        n_cards += card;
        n_results += result;
    }
    closedir(d);
    TEST_ASSERT_GREATER_OR_EQUAL_INT(6, n_cards);
    TEST_ASSERT_GREATER_OR_EQUAL_INT(8, n_results);
}

/* herdr counts its ids past 9 with letters (w6:pE, wA:p1; seen live at the
 * desk checkpoint): a snapshot of such a pane is accepted, not dropped. */
/* line is the to-bridge fixture rel, byte for byte, but for its id, which must be id: the fixtures' ids are
 * their own, and the rig numbers from 1 (open-items O-39). */
static void assert_fixture(const char *line, const char *rel, uint32_t id)
{
    size_t len;
    const char *fx = rig_read_fixture(rel, &len);
    char want[APP_SEND_MAX + 1];
    const char *at = strstr(fx, "\"id\":");
    if (at) {
        const char *end = at + 5;
        while (*end >= '0' && *end <= '9') {
            end++;
        }
        snprintf(want, sizeof(want), "%.*s\"id\":%u%s", (int)(at - fx), fx, (unsigned)id, end);
    } else {
        snprintf(want, sizeof(want), "%s", fx);
    }
    TEST_ASSERT_NOT_NULL_MESSAGE(line, rel);
    TEST_ASSERT_EQUAL_STRING_MESSAGE(want, line, rel);
}

/* Every line the app core writes to the bridge is its to-bridge/valid fixture: the contract both sides read
 * (the fixtures' README). A change to either side shows here, not only in the bridge's own tests (O-39). The
 * ping is ssh_link.c's, not the core's: the emulator harness checks it, with every other line, at the bridge. */
static void test_device_lines_are_the_fixtures(void)
{
    rig_boot(true); /* pager-01, firmware 0.1.0 */
    rig_link(1000, APP_LINK_DOWN);
    rig_clear_log();
    rig_link(2000, APP_LINK_CONNECTED); /* a new session: hello first */
    assert_fixture(rig_sent(0), "to-bridge/valid/hello.ndjson", 0);

    /* show: api-refactor's card, 4.2 s after its page at 4 s */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_at(4000);
    rig_press(APP_BTN_PWR, 8100, SHORT);
    assert_fixture(rig_sent(0), "to-bridge/valid/show.ndjson", 1);

    /* approve and deny: card-permission's card (w1:p3, seq 41) */
    uint32_t id = verdict_pending();
    TEST_ASSERT_EQUAL_UINT32(2, id);
    assert_fixture(rig_sent(rig_n_sent() - 1), "to-bridge/valid/approve.ndjson", 2);
    rig_card(2000, "to-device/valid/card-permission.ndjson", show_pending());
    rig_press(APP_BTN_PWR, 3000, SHORT);
    assert_fixture(rig_sent(rig_n_sent() - 1), "to-bridge/valid/deny.ndjson", 2);

    /* interrupt: tests (w3:p1), working at seq 50, from its menu and YES */
    rig_boot(true);
    rig_snap_quiet(1000);
    rig_btn(2000, APP_BTN_BOOT, true);
    rig_at(3600);
    rig_btn(3600, APP_BTN_BOOT, false); /* its menu: JUMP, INTERRUPT, SETTINGS */
    rig_press(APP_BTN_BOOT, 4000, SHORT);
    rig_press(APP_BTN_PWR, 4400, SHORT); /* INTERRUPT tests? */
    rig_press(APP_BTN_PWR, 4800, SHORT); /* YES */
    assert_fixture(rig_sent(0), "to-bridge/valid/interrupt.ndjson", 1);

    /* jump: api-refactor's (w1:p3) menu, inside its 3 s */
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap.ndjson");
    rig_btn(1100, APP_BTN_BOOT, true);
    rig_at(2700);
    rig_btn(2700, APP_BTN_BOOT, false); /* its menu: JUMP, SETTINGS */
    rig_press(APP_BTN_PWR, 3000, SHORT);
    assert_fixture(rig_sent(0), "to-bridge/valid/jump.ndjson", 1);

    /* list: after a line too long to read */
    rig_boot(true);
    rig_fixture(1000, "to-device/invalid/snap-8193-bytes.ndjson");
    assert_fixture(rig_sent(0), "to-bridge/valid/list.ndjson", 1);
}

/* JSON escapes as the bridge's encoder writes them: `&` and `>` as \u0026 and \u003e, a quote and a backslash
 * escaped (open-items O-40). Every real card with `&&` in a command crosses the wire like this. */
static void test_codec_card_escapes(void)
{
    rig_card(2000, "to-device/valid/card-escapes.ndjson", show_pending());
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, r->screen);
    TEST_ASSERT_EQUAL_STRING("make build && make test", r->lines[0]);
    TEST_ASSERT_EQUAL_STRING("echo \"done\" > C:\\tmp\\log", r->lines[1]);
}

static void test_codec_pane_ids_with_letters(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-letter-pane.ndjson");
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->n_rows);
    TEST_ASSERT_EQUAL_STRING("api-refactor", rig_render()->rows[0].name);
}

/* A snapshot entry may say where its agent works (`at`, its folder's name):
 * the list shows it in place of the kind, and the kind when it's absent (the
 * desk checkpoint, 2026-10-24). An `at` of more than 16 characters makes the
 * snapshot invalid, so the last good one stays. */
static void test_codec_snapshot_at(void)
{
    rig_boot(true);
    rig_fixture(1000, "to-device/valid/snap-folder.ndjson");
    TEST_ASSERT_EQUAL_UINT8(1, rig_render()->n_rows);
    TEST_ASSERT_EQUAL_STRING("Fix the flaky login test", rig_render()->rows[0].name);
    TEST_ASSERT_EQUAL_STRING("herdr-pager", rig_render()->rows[0].kind);
    rig_fixture(2000, "to-device/valid/snap.ndjson");
    TEST_ASSERT_EQUAL_STRING("claude", rig_render()->rows[0].kind);
    rig_fixture(3000, "to-device/invalid/snap-at-too-long.ndjson");
    TEST_ASSERT_EQUAL_STRING("api-refactor", rig_render()->rows[0].name);
    TEST_ASSERT_EQUAL_STRING("claude", rig_render()->rows[0].kind);
}

static void test_device_text_filter(void)
{
    /* A NUL (decoded from \u0000, so a C string would end there), a tab, DEL, a two-byte é and raw UTF-8: each
     * byte outside 0x20-0x7E is drawn as '?'. The bridge sanitizes first; this is the device's own check. */
    rig_boot(true);
    rig_line(1000, "{\"t\":\"snap\",\"now\":1760220001,\"herdr\":\"ok\",\"more\":0,\"agents\":["
                   "{\"pane\":\"w1:p3\",\"name\":\"a\\u0000b\\tc\\u007fd\\u00e9\",\"kind\":\"cl\xc3\xa9\","
                   "\"st\":\"blocked\",\"seq\":41,\"dseq\":7,\"since\":1760219900,\"can\":[\"show\",\"jump\"]}]}");
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL_UINT8(1, r->n_rows);
    TEST_ASSERT_EQUAL_STRING("a?b?c?d??", r->rows[0].name);
    TEST_ASSERT_EQUAL_STRING("cl??", r->rows[0].kind);

    rig_press(APP_BTN_PWR, 2000, SHORT);
    rig_card_line(2500,
                  "{\"t\":\"card\",\"id\":1,\"pane\":\"w1:p3\",\"st\":\"blocked\",\"seq\":41,\"hash\":"
                  "\"6a0c2aae60a7e3df1151bdb24c231c88e163a90d8a7ed200c43a454bc910e402\",\"kind\":\"permission\","
                  "\"risk\":\"danger\",\"rl\":\"RM\\u001b[31m\",\"summary\":\"Bash\\r\\ncommand\","
                  "\"lines\":[\"rm -rf \\u202e/\",\"\\u0000\"],\"can\":[\"approve\",\"deny\",\"jump\"]}",
                  rig_sent_id(rig_sent(0)));
    r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_CARD, r->screen);
    TEST_ASSERT_EQUAL_STRING("Bash??command", r->title);
    TEST_ASSERT_EQUAL_STRING("rm -rf ?" "?" "?/", r->lines[0]); /* split: "??/" is a trigraph */
    TEST_ASSERT_EQUAL_STRING("?", r->lines[1]);
    TEST_ASSERT_EQUAL_STRING("DANGER: RM?[31m", r->banner);
}

TEST_AREA(codec)
{
    RUN_TEST(test_device_text_filter);
    RUN_TEST(test_codec_overflow_and_malformed);
    RUN_TEST(test_codec_card_escapes);
    RUN_TEST(test_device_lines_are_the_fixtures);
    RUN_TEST(test_codec_pane_ids_with_letters);
    RUN_TEST(test_codec_snapshot_at);
}
