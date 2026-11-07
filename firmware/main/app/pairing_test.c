/* Pairing mode and the @P lines, against the provisioning fixtures (PAGER_TESTDATA "/provisioning"; their README
 * is the contract). Built only by firmware/test_host. */
#include <dirent.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "../../test_host/test_rig.h"
#include "app.h"
#include "test_registry.h"

#define SHORT 100
#define CONFIRM_MS 30000

/* Reads bridge/testdata/provisioning/<rel> into a static buffer (one of two, so two can be compared), without
 * its \n. */
static const char *fixture(const char *rel)
{
    static char bufs[2][1024];
    static int which;
    char *buf = bufs[which ^= 1];
    char path[512];
    snprintf(path, sizeof(path), "%s/provisioning/%s", PAGER_TESTDATA, rel);
    FILE *f = fopen(path, "rb");
    TEST_ASSERT_NOT_NULL_MESSAGE(f, path);
    size_t n = fread(buf, 1, sizeof(bufs[0]) - 1, f);
    fclose(f);
    if (n > 0 && buf[n - 1] == '\n') {
        n--;
    }
    buf[n] = '\0';
    return buf;
}

static void prov_line(app_ms_t t, const char *line)
{
    app_event_t ev = {.kind = APP_EV_PROV_LINE};
    ev.line.data = line;
    ev.line.len = strlen(line);
    rig_event(t, &ev);
}

static void prov_fixture(app_ms_t t, const char *rel)
{
    prov_line(t, fixture(rel));
}

/* An unpaired boot at 0 with the device key pubkey, its actions kept in the log. */
static void boot_unpaired_with_key(const char *pubkey)
{
    rig_boot(false);
    memset(&rig.s, 0, sizeof(rig.s));
    memset(&rig.out, 0, sizeof(rig.out));
    rig.out.deadline = APP_NEVER;
    rig.n_log = 0;
    app_init(&rig.s);
    snprintf(rig.boot.pubkey, sizeof(rig.boot.pubkey), "%s", pubkey);
    app_event_t ev = {.kind = APP_EV_BOOT, .boot = &rig.boot};
    rig_event(0, &ev);
}

/* A paired boot at 0 (pager-01, the bridge's host key stored) whose device key couldn't be read: prov.c leaves
 * boot.pubkey empty (open-items O-07). Its actions are kept in the log. */
static void boot_paired_without_key(void)
{
    rig_boot(true);
    memset(&rig.s, 0, sizeof(rig.s));
    memset(&rig.out, 0, sizeof(rig.out));
    rig.out.deadline = APP_NEVER;
    rig.n_log = 0;
    app_init(&rig.s);
    rig.boot.pubkey[0] = '\0';
    app_event_t ev = {.kind = APP_EV_BOOT, .boot = &rig.boot};
    rig_event(0, &ev);
}

/* An unpaired boot at 0 with the fixtures' device key, its actions kept in the log. */
static void boot_unpaired(void)
{
    boot_unpaired_with_key(RIG_DEVICE_KEY);
}

static size_t n_serial(void)
{
    return rig_count(APP_ACT_SERIAL);
}

/* The i-th logged serial line (0 is the first). */
static const char *serial(size_t i)
{
    for (size_t k = 0; k < rig.n_log; k++) {
        if (rig.log[k].kind == APP_ACT_SERIAL && i-- == 0) {
            return rig.log[k].text;
        }
    }
    TEST_FAIL_MESSAGE("no such serial line");
    return NULL;
}

/* The serial lines that aren't prov_hello. */
static size_t n_results(void)
{
    size_t n = 0;
    for (size_t i = 0; i < n_serial(); i++) {
        n += strstr(serial(i), "\"prov_hello\"") == NULL;
    }
    return n;
}

static const char *last_result(void)
{
    for (size_t i = n_serial(); i-- > 0;) {
        if (strstr(serial(i), "\"prov_hello\"") == NULL) {
            return serial(i);
        }
    }
    TEST_FAIL_MESSAGE("no prov_result");
    return NULL;
}

/* The index in the log of the last action of kind (with key, for a store), or -1. */
static int log_index(app_action_kind_t kind, const char *key)
{
    for (size_t i = rig.n_log; i-- > 0;) {
        if (rig.log[i].kind == kind && (!key || strcmp(rig.log[i].key, key) == 0)) {
            return (int)i;
        }
    }
    return -1;
}

static bool render_has(const char *text)
{
    const app_render_t *r = rig_render();
    if (strstr(r->title, text)) {
        return true;
    }
    for (int i = 0; i < APP_BODY_LINES; i++) {
        if (strstr(r->lines[i], text)) {
            return true;
        }
    }
    return false;
}

/* The offer at t, and BOOT on it (a short press) at t + 1000: confirmed, waiting for its prov_set. */
static void offer_and_boot(app_ms_t t, const char *offer)
{
    prov_fixture(t, offer);
    rig_press(APP_BTN_BOOT, t + 1000, SHORT);
}

static bool any_store(void)
{
    return rig_count(APP_ACT_STORE) > 0;
}

static void test_pairing_mode_sends_prov_hello(void)
{
    /* An unpaired boot enters pairing mode: PAIR VIA USB, and a prov_hello at once, waiting. */
    boot_unpaired();
    TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    TEST_ASSERT_EQUAL_size_t(1, n_serial());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello.txt"), serial(0));

    /* Then every 2 s: none at 1.999 s, one at 2 s; five more by 12 s. */
    rig_at(1999);
    TEST_ASSERT_EQUAL_size_t(1, n_serial());
    rig_at(2000);
    TEST_ASSERT_EQUAL_size_t(2, n_serial());
    rig_at(12000);
    TEST_ASSERT_EQUAL_size_t(7, n_serial());
    for (size_t i = 0; i < 7; i++) {
        TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello.txt"), serial(i));
    }

    /* While an offer is shown, they go on, in state confirming. */
    prov_fixture(12500, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    rig_at(14000);
    TEST_ASSERT_EQUAL_size_t(1, n_serial());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello-confirming.txt"), serial(0));

    /* BOOT confirms it: a hello in state confirmed at once, and every 2 s after. */
    rig_clear_log();
    rig_press(APP_BTN_BOOT, 15000, SHORT);
    TEST_ASSERT_EQUAL_size_t(1, n_serial());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello-confirmed.txt"), serial(0));
    rig_at(15000 + SHORT + 1999);
    TEST_ASSERT_EQUAL_size_t(1, n_serial());
    rig_at(15000 + SHORT + 2000);
    TEST_ASSERT_EQUAL_size_t(2, n_serial());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello-confirmed.txt"), serial(1));

    /* A paired boot isn't in pairing mode: no prov_hello, and an @P line is ignored. */
    rig_boot(true);
    rig_snap_quiet(500);
    rig_at(10000);
    prov_fixture(10000, "to-device/valid/prov_offer.txt");
    prov_fixture(10100, "to-device/valid/prov_set.txt");
    rig_at(20000);
    TEST_ASSERT_EQUAL_size_t(0, n_serial());
    TEST_ASSERT_EQUAL(APP_SCREEN_LIST, rig_render()->screen);
}

static void test_offer_shows_both_fingerprints(void)
{
    boot_unpaired();
    rig_clear_log();
    prov_fixture(1000, "to-device/valid/prov_offer.txt");

    /* Both full fingerprints and the host name; BOOT pairs and PWR cancels, each under its button. */
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_PAIRING, r->screen);
    TEST_ASSERT_EQUAL_STRING("PAIR WITH studio-mac?", r->title);
    TEST_ASSERT_TRUE_MESSAGE(render_has("HOST " RIG_HOST_FP), "the bridge's fingerprint");
    TEST_ASSERT_TRUE_MESSAGE(render_has("DEV " RIG_DEVICE_FP), "the device's fingerprint");
    TEST_ASSERT_EQUAL_STRING("PAIR", r->band.boot.text);
    TEST_ASSERT_EQUAL_STRING("CANCEL", r->band.pwr.text);
    TEST_ASSERT_GREATER_THAN_size_t(0, rig_count(APP_ACT_RENDER));
    TEST_ASSERT_FALSE(any_store());
    TEST_ASSERT_EQUAL_size_t(0, n_results());

    /* BOOT 29.9 s after it (its release, a short press): nothing stored yet, no result, and the same two
     * fingerprints stay up while the owner confirms on the Mac. PWR still cancels; BOOT has done its part. */
    app_ms_t boot = 1000 + CONFIRM_MS - SHORT - 100;
    rig_press(APP_BTN_BOOT, boot, SHORT);
    boot += SHORT;
    r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_PAIRING, r->screen);
    TEST_ASSERT_EQUAL_STRING("NOW CONFIRM ON studio-mac", r->title);
    TEST_ASSERT_TRUE_MESSAGE(render_has("HOST " RIG_HOST_FP), "the bridge's fingerprint, confirmed");
    TEST_ASSERT_TRUE_MESSAGE(render_has("DEV " RIG_DEVICE_FP), "the device's fingerprint, confirmed");
    TEST_ASSERT_EQUAL_STRING("", r->band.boot.text);
    TEST_ASSERT_EQUAL_STRING("CANCEL", r->band.pwr.text);
    TEST_ASSERT_TRUE(r->band.blank);
    TEST_ASSERT_FALSE(any_store());
    TEST_ASSERT_EQUAL_size_t(0, n_results());
    rig_press(APP_BTN_BOOT, boot + 1000, SHORT); /* a second BOOT does nothing */
    TEST_ASSERT_FALSE(any_store());
    TEST_ASSERT_EQUAL_size_t(0, n_results());

    /* Its prov_set, 29.9 s after BOOT and with no press: everything is stored, the pinned key last, then ok, then
     * the device restarts paired. The offer gives the name, address and host name; the prov_set the network. */
    prov_fixture(boot + CONFIRM_MS - 100, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING("pager-01", rig_last_store(APP_KEY_NAME)->text);
    TEST_ASSERT_EQUAL_STRING("studio-mac.local", rig_last_store(APP_KEY_HOST)->text);
    TEST_ASSERT_EQUAL_STRING("2277", rig_last_store(APP_KEY_PORT)->text);
    TEST_ASSERT_EQUAL_STRING("studio-mac", rig_last_store(APP_KEY_HOST_NAME)->text);
    TEST_ASSERT_EQUAL_STRING("1", rig_last_store(APP_KEY_WIFI_N)->text);
    TEST_ASSERT_EQUAL_STRING("HomeNet-2G", rig_last_store("wifi0_ssid")->text);
    TEST_ASSERT_EQUAL_STRING("correct-horse-battery", rig_last_store("wifi0_pass")->text);
    TEST_ASSERT_EQUAL_STRING(RIG_HOST_KEY, rig_last_store(APP_KEY_HOST_KEY)->text);
    int host_key = log_index(APP_ACT_STORE, APP_KEY_HOST_KEY);
    for (size_t i = 0; i < rig.n_log; i++) {
        if (rig.log[i].kind == APP_ACT_STORE) {
            TEST_ASSERT_LESS_OR_EQUAL_INT_MESSAGE(host_key, (int)i, "the pinned key is stored last");
        }
    }
    /* ok goes out three times: a pager's USB console that the Mac isn't reading can cut a line short, and a
     * lost ok would leave the pager paired and the bridge not (the desk checkpoint, 2026-10-24). */
    TEST_ASSERT_EQUAL_size_t(3, n_results());
    for (size_t i = 0; i < n_serial(); i++) {
        if (strstr(serial(i), "\"prov_hello\"") == NULL) {
            TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-ok.txt"), serial(i));
        }
    }
    int ok = log_index(APP_ACT_SERIAL, NULL);
    TEST_ASSERT_GREATER_THAN_INT(host_key, ok);
    TEST_ASSERT_GREATER_THAN_INT(ok, log_index(APP_ACT_REBOOT, NULL));
}

static void test_prov_pwr_or_timeout_discards(void)
{
    /* PWR cancels the offer: cancelled, nothing stored, and back to waiting; a later BOOT confirms nothing, and a
     * prov_set has no confirmed offer. */
    boot_unpaired();
    prov_fixture(1000, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    rig_press(APP_BTN_PWR, 5000, SHORT);
    TEST_ASSERT_EQUAL_size_t(1, n_results());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-cancelled.txt"), last_result());
    TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    rig_press(APP_BTN_BOOT, 6000, SHORT);
    rig_at(8000);
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello.txt"), serial(n_serial() - 1));
    prov_fixture(8100, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-invalid.txt"), last_result());
    TEST_ASSERT_FALSE(any_store());
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_REBOOT));

    /* 30 s with no button: timeout, at exactly 30 s, and nothing kept. */
    prov_fixture(9000, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    rig_at(9000 + CONFIRM_MS - 1);
    TEST_ASSERT_EQUAL_size_t(0, n_results());
    rig_at(9000 + CONFIRM_MS);
    TEST_ASSERT_EQUAL_size_t(1, n_results());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-timeout.txt"), last_result());
    TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
    rig_press(APP_BTN_BOOT, 9000 + CONFIRM_MS + 1000, SHORT);
    TEST_ASSERT_FALSE(any_store());
    TEST_ASSERT_EQUAL_size_t(1, n_results());

    /* BOOT, then no prov_set in 30 s: timeout, at exactly 30 s after BOOT, nothing kept, and back to waiting; the
     * prov_set that comes later has no confirmed offer. */
    app_ms_t t = 50000;
    offer_and_boot(t, "to-device/valid/prov_offer.txt");
    app_ms_t boot = t + 1000 + SHORT;
    rig_clear_log();
    rig_at(boot + CONFIRM_MS - 1);
    TEST_ASSERT_EQUAL_size_t(0, n_results());
    rig_at(boot + CONFIRM_MS);
    TEST_ASSERT_EQUAL_size_t(1, n_results());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-timeout.txt"), last_result());
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    prov_fixture(boot + CONFIRM_MS + 500, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-invalid.txt"), last_result());
    TEST_ASSERT_FALSE(any_store());
    rig_at(boot + CONFIRM_MS + 2500);
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello.txt"), serial(n_serial() - 1));

    /* BOOT, then PWR before the prov_set: cancelled, nothing kept. */
    t = 100000;
    offer_and_boot(t, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    rig_press(APP_BTN_PWR, t + 3000, SHORT);
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-cancelled.txt"), last_result());
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    prov_fixture(t + 4000, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-invalid.txt"), last_result());
    TEST_ASSERT_FALSE(any_store());

    /* Still in pairing mode: the next offer is shown, BOOT confirms it, and its prov_set pairs. */
    t = 150000;
    offer_and_boot(t, "to-device/valid/prov_offer.txt");
    prov_fixture(t + 3000, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-ok.txt"), last_result());
}

static void test_prov_confirm_takes_only_a_press_in_its_window(void)
{
    /* A BOOT press already down when the question appears doesn't answer it (group 13's review). */
    boot_unpaired();
    rig_btn(900, APP_BTN_BOOT, true);
    prov_fixture(1000, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    rig_btn(1200, APP_BTN_BOOT, false);
    TEST_ASSERT_EQUAL_size_t(0, n_serial());
    TEST_ASSERT_EQUAL_STRING("PAIR WITH studio-mac?", rig_render()->title);
    prov_fixture(1300, "to-device/valid/prov_set.txt"); /* not confirmed: it ends the offer */
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-invalid.txt"), last_result());
    TEST_ASSERT_FALSE(any_store());
    offer_and_boot(2000, "to-device/valid/prov_offer.txt");
    prov_fixture(4000, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-ok.txt"), last_result());

    /* A release stamped after the 30 s, which the platform hands the core before its deadline call: timeout,
     * and nothing kept. */
    boot_unpaired();
    prov_fixture(1000, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    rig_btn(1000 + CONFIRM_MS - 100, APP_BTN_BOOT, true);
    app_event_t up = {.kind = APP_EV_BUTTON};
    up.button.btn = APP_BTN_BOOT;
    up.button.down = false;
    up.button.t = 1000 + CONFIRM_MS + 50;
    rig_event(1000 + CONFIRM_MS - 1, &up);
    TEST_ASSERT_FALSE(any_store());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-timeout.txt"), last_result());
}

static void test_private_key_never_in_a_line(void)
{
    /* A whole pairing: every line out holds the public key only, and no Wi-Fi password. */
    boot_unpaired();
    offer_and_boot(1000, "to-device/valid/prov_offer.txt");
    prov_fixture(5000, "to-device/valid/prov_set-4-networks.txt");
    /* Not a real key or password among them. "openssh-key-v1" ends its line, so no scanner reads the string after
     * it as that key's value. */
    static const char *const secrets[] = {
        "PRIVATE", "b3BlbnNzaC1rZXktdjE", "correct-horse-battery", "hotspot-pass-8", "openssh-key-v1",
        "3f6a1c0e9b2d47a8c5e1f09b3d7a2c6e8f4b1d0a9c7e5b3f2d1a0c9e8b7f6a5d",
    };
    TEST_ASSERT_GREATER_THAN_size_t(1, n_serial());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-ok.txt"), last_result());
    for (size_t i = 0; i < rig.n_log; i++) {
        const app_action_t *a = &rig.log[i];
        if (a->kind != APP_ACT_SERIAL && a->kind != APP_ACT_SEND) {
            continue;
        }
        for (size_t k = 0; k < sizeof(secrets) / sizeof(secrets[0]); k++) {
            TEST_ASSERT_NULL_MESSAGE(strstr(a->text, secrets[k]), a->text);
        }
        const char *pk = strstr(a->text, "\"pubkey\":\"");
        if (pk) {
            pk += strlen("\"pubkey\":\"");
            TEST_ASSERT_EQUAL_STRING_LEN(RIG_DEVICE_KEY "\"", pk, strlen(RIG_DEVICE_KEY) + 1);
        }
    }

    /* A device key that isn't an ed25519 public key is never sent: not private key text, not an OpenSSH
     * private key's blob behind the public key's type, not a public key with a comment. */
    static const char *const not_public[] = {
        "-----BEGIN OPENSSH PRIVATE KEY----- b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAAB",
        "ssh-ed25519 b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZWQyNTUx",
        RIG_DEVICE_KEY " pager-01",
        "",
    };
    for (size_t k = 0; k < sizeof(not_public) / sizeof(not_public[0]); k++) {
        boot_unpaired_with_key(not_public[k]);
        rig_at(10000);
        TEST_ASSERT_EQUAL_size_t_MESSAGE(0, n_serial(), not_public[k]);
        TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
        TEST_ASSERT_EQUAL_STRING("NO DEVICE KEY", rig_render()->title);

        /* Nor is an offer or a prov_set taken, though provision never sends one unasked: paired with no key the
         * bridge has seen, the next boot would make one it never saw (open-items O-29). */
        prov_fixture(11000, "to-device/valid/prov_offer.txt");
        TEST_ASSERT_EQUAL_STRING_MESSAGE(fixture("to-tool/valid/prov_result-invalid.txt"), last_result(),
                                         not_public[k]);
        TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
        rig_press(APP_BTN_BOOT, 12000, SHORT);
        prov_fixture(13000, "to-device/valid/prov_set.txt");
        TEST_ASSERT_EQUAL_STRING_MESSAGE(fixture("to-tool/valid/prov_result-invalid.txt"), last_result(),
                                         not_public[k]);
        TEST_ASSERT_EQUAL_size_t_MESSAGE(0, rig_count(APP_ACT_STORE), not_public[k]);
        TEST_ASSERT_EQUAL_size_t_MESSAGE(0, rig_count(APP_ACT_REBOOT), not_public[k]);
    }
}

/* Paired with fewer networks than before, the ones it no longer has go: their names and passwords aren't left
 * in flash (open-items O-30). They go before the pin, which is stored last, and the restart still comes. */
static void test_fewer_networks_erase_the_rest(void)
{
    boot_unpaired();
    offer_and_boot(1000, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    prov_fixture(3000, "to-device/valid/prov_set.txt"); /* one network */
    int pin = log_index(APP_ACT_STORE, APP_KEY_HOST_KEY);
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, log_index(APP_ACT_STORE, "wifi0_pass"));
    TEST_ASSERT_EQUAL_INT(-1, log_index(APP_ACT_ERASE, "wifi0_ssid"));
    TEST_ASSERT_EQUAL_INT(-1, log_index(APP_ACT_ERASE, "wifi0_pass"));
    static const char *const gone[] = {"wifi1_ssid", "wifi1_pass", "wifi2_ssid", "wifi2_pass", "wifi3_ssid", "wifi3_pass"};
    for (size_t i = 0; i < sizeof(gone) / sizeof(gone[0]); i++) {
        int e = log_index(APP_ACT_ERASE, gone[i]);
        TEST_ASSERT_GREATER_OR_EQUAL_INT_MESSAGE(0, e, gone[i]);
        TEST_ASSERT_LESS_THAN_INT_MESSAGE(pin, e, gone[i]);
    }
    TEST_ASSERT_GREATER_THAN_INT(pin, log_index(APP_ACT_REBOOT, NULL));

    /* With four, there's nothing to erase but the pin. */
    boot_unpaired();
    offer_and_boot(1000, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    prov_fixture(3000, "to-device/valid/prov_set-4-networks.txt");
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_ERASE));
    TEST_ASSERT_GREATER_THAN_INT(log_index(APP_ACT_STORE, APP_KEY_HOST_KEY), log_index(APP_ACT_REBOOT, NULL));
}

/* The factory-reset chord, both buttons for 10 s, works on the pairing screens too: parent 2.2 says "while the
 * firmware is running", and it's the way out for a pager whose key can't be read (open-items O-28, O-07). On the
 * confirmation, confirmed or not, it cancels the offer, and CANCEL goes back to PAIR VIA USB. */
static void test_reset_chord_on_pairing_screens(void)
{
    boot_unpaired();
    rig_btn(1000, APP_BTN_BOOT, true);
    rig_btn(1300, APP_BTN_PWR, true);
    rig_at(11329); /* 10 s, and the 30 ms a deadline waits for a late edge (O-22) */
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    TEST_ASSERT_TRUE(rig_render()->band.blank); /* PAIR VIA USB's band has no status (O-32) */
    rig_at(11330);
    TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("FACTORY RESET?", rig_render()->title);
    TEST_ASSERT_TRUE(rig_render()->band.blank); /* nor has the question's over it */
    TEST_ASSERT_EQUAL_STRING("RESET", rig_render()->band.boot.text);
    TEST_ASSERT_EQUAL_STRING("CANCEL", rig_render()->band.pwr.text);
    rig_btn(11500, APP_BTN_BOOT, false);
    rig_btn(11500, APP_BTN_PWR, false);
    prov_fixture(11700, "to-device/valid/prov_offer.txt"); /* while the question is up: busy, and it stays */
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-busy.txt"), last_result());
    TEST_ASSERT_EQUAL_STRING("FACTORY RESET?", rig_render()->title);
    rig_clear_log();
    rig_press(APP_BTN_BOOT, 12000, SHORT); /* RESET: every key, key pair included, then a restart */
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, log_index(APP_ACT_ERASE, ""));
    TEST_ASSERT_GREATER_THAN_INT(log_index(APP_ACT_ERASE, ""), log_index(APP_ACT_REBOOT, NULL));

    /* On the confirmation, before BOOT and after it: the offer is answered cancelled, and nothing is kept. */
    for (int confirmed = 0; confirmed <= 1; confirmed++) {
        boot_unpaired();
        if (confirmed) {
            offer_and_boot(1000, "to-device/valid/prov_offer.txt");
            TEST_ASSERT_EQUAL_STRING("NOW CONFIRM ON studio-mac", rig_render()->title);
        } else {
            prov_fixture(1000, "to-device/valid/prov_offer.txt");
        }
        TEST_ASSERT_EQUAL(APP_SCREEN_PAIRING, rig_render()->screen);
        TEST_ASSERT_TRUE(rig_render()->band.blank);
        rig_btn(2200, APP_BTN_BOOT, true);
        rig_btn(2500, APP_BTN_PWR, true);
        rig_at(12530);
        TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, rig_render()->screen);
        TEST_ASSERT_EQUAL_STRING("FACTORY RESET?", rig_render()->title);
        TEST_ASSERT_TRUE(rig_render()->band.blank);
        TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-cancelled.txt"), last_result());
        rig_btn(12700, APP_BTN_BOOT, false);
        rig_btn(12700, APP_BTN_PWR, false);
        rig_clear_log();
        rig_press(APP_BTN_PWR, 13000, SHORT); /* CANCEL */
        TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
        TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
        prov_fixture(13500, "to-device/valid/prov_set.txt"); /* the offer is gone: no confirmed offer */
        TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-invalid.txt"), last_result());
        TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_STORE));
        TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_ERASE));
        TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_REBOOT));
    }

    /* Pairing goes on: the next offer is shown, BOOT confirms it, and its prov_set pairs. */
    offer_and_boot(14000, "to-device/valid/prov_offer.txt");
    prov_fixture(16000, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-ok.txt"), last_result());
}

/* A paired pager that can't read its device key shows NO DEVICE KEY, as an unpaired one does: it dials nothing,
 * sends nothing, stores and erases nothing, and makes no key. Its pairing stays stored; the way out is the
 * factory-reset chord (the owner's decision, 2026-10-27, open-items O-07). */
static void test_paired_without_key_shows_no_device_key(void)
{
    boot_paired_without_key();
    rig_wifi(500, true, "home"); /* its network joins, as stored */
    rig_at(60000);
    TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("NO DEVICE KEY", rig_render()->title);
    /* It says the way out, and not provision, which a pager with no key never answers. */
    TEST_ASSERT_TRUE(render_has("Hold BOOT and PWR 10 s to reset"));
    TEST_ASSERT_FALSE(render_has("provision"));
    TEST_ASSERT_TRUE(rig_render()->band.blank);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_LINK)); /* no dial */
    TEST_ASSERT_EQUAL_size_t(0, rig_n_sent());
    TEST_ASSERT_EQUAL_size_t(0, n_serial()); /* no prov_hello: there's no key to send */
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_STORE));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_ERASE));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_REBOOT));

    /* The chord, once a press has woken the screen: FACTORY RESET? over it, the band still blank; RESET erases
     * everything and restarts. */
    rig_press(APP_BTN_BOOT, 60500, SHORT);
    rig_btn(61000, APP_BTN_BOOT, true);
    rig_btn(61300, APP_BTN_PWR, true);
    rig_at(71330);
    TEST_ASSERT_EQUAL(APP_SCREEN_CONFIRM, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("FACTORY RESET?", rig_render()->title);
    TEST_ASSERT_TRUE(rig_render()->band.blank);
    rig_btn(71500, APP_BTN_BOOT, false);
    rig_btn(71500, APP_BTN_PWR, false);
    rig_press(APP_BTN_BOOT, 72000, SHORT);
    TEST_ASSERT_GREATER_OR_EQUAL_INT(0, log_index(APP_ACT_ERASE, ""));
    TEST_ASSERT_GREATER_THAN_INT(log_index(APP_ACT_ERASE, ""), log_index(APP_ACT_REBOOT, NULL));
}

static void test_four_networks_stored_in_order(void)
{
    boot_unpaired();
    offer_and_boot(1000, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    prov_fixture(3000, "to-device/valid/prov_set-4-networks.txt");

    /* Priority order, as sent: a passphrase, an iPhone hotspot (its SSID's curly apostrophe kept), a raw key
     * and an open network. */
    static const char *const want[][2] = {
        {APP_KEY_WIFI_N, "4"},
        {"wifi0_ssid", "HomeNet-2G"},
        {"wifi0_pass", "correct-horse-battery"},
        {"wifi1_ssid", "Sam\xe2\x80\x99s iPhone"},
        {"wifi1_pass", "hotspot-pass-8"},
        {"wifi2_ssid", "Studio"},
        {"wifi2_pass", "3f6a1c0e9b2d47a8c5e1f09b3d7a2c6e8f4b1d0a9c7e5b3f2d1a0c9e8b7f6a5d"},
        {"wifi3_ssid", "Guest"},
        {"wifi3_pass", ""},
    };
    size_t k = 0;
    for (size_t i = 0; i < rig.n_log; i++) {
        const app_action_t *a = &rig.log[i];
        if (a->kind != APP_ACT_STORE || strncmp(a->key, "wifi", 4) != 0) {
            continue;
        }
        TEST_ASSERT_LESS_THAN_size_t_MESSAGE(sizeof(want) / sizeof(want[0]), k, "more wifi stores than sent");
        TEST_ASSERT_EQUAL_STRING(want[k][0], a->key);
        TEST_ASSERT_EQUAL_STRING(want[k][1], a->text);
        k++;
    }
    TEST_ASSERT_EQUAL_size_t(sizeof(want) / sizeof(want[0]), k);
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-ok.txt"), last_result());

    /* The longest batch (four networks) still ends in the restart: nothing was cut off for want of room. */
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_REBOOT));
    TEST_ASSERT_GREATER_THAN_INT(log_index(APP_ACT_SERIAL, NULL), log_index(APP_ACT_REBOOT, NULL));
}

/* A BOOT hold of ms from t. */
static void hold_boot(app_ms_t t, app_ms_t ms)
{
    rig_btn(t, APP_BTN_BOOT, true);
    rig_at(t + ms);
    rig_btn(t + ms, APP_BTN_BOOT, false);
}

/* With a menu open, moves to item with BOOT presses from t and selects it with PWR. Returns a later time. */
static app_ms_t pick(app_ms_t t, const char *item)
{
    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_MENU, r->screen);
    int want = -1;
    for (int i = 0; i < r->n_items; i++) {
        want = strcmp(r->items[i], item) == 0 ? i : want;
    }
    TEST_ASSERT_GREATER_OR_EQUAL_INT_MESSAGE(0, want, item);
    while (rig_render()->selected_item != want) {
        rig_press(APP_BTN_BOOT, t, SHORT);
        t += 300;
    }
    rig_press(APP_BTN_PWR, t, SHORT);
    return t + 300;
}

static void test_pair_again_keeps_key(void)
{
    /* A paired device: the menu's Pair again, then YES (BOOT). */
    rig_boot(true);
    rig_snap_quiet(1000);
    hold_boot(2000, 1600);
    app_ms_t t = pick(4000, "SETTINGS");
    t = pick(t, "PAIR AGAIN");
    rig_clear_log();
    rig_press(APP_BTN_BOOT, t, SHORT);
    t += 300;

    /* Pairing mode, with the same key pair: nothing erased or stored, and its prov_hello at once and every 2 s
     * carries the key the device had. */
    TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_ERASE));
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_STORE));
    TEST_ASSERT_EQUAL_size_t(1, n_serial());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello.txt"), serial(0));

    /* The link it dropped stays down while pairing: no dial, and so no page, however long it waits. */
    rig_link(t, APP_LINK_DOWN);
    rig_at(t + 60000);
    TEST_ASSERT_EQUAL_size_t(1, rig_count(APP_ACT_LINK)); /* Pair again's disconnect, and no dial */
    TEST_ASSERT_EQUAL(APP_LINK_OP_DISCONNECT, rig_last(APP_ACT_LINK)->link_op);
    TEST_ASSERT_EQUAL_size_t(31, n_serial());
    for (size_t i = 0; i < n_serial(); i++) {
        TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello.txt"), serial(i));
    }

    /* Pairing completes, storing the new host and nothing of the key pair. */
    t += 61000;
    offer_and_boot(t, "to-device/valid/prov_offer-ip-host.txt");
    prov_fixture(t + 2000, "to-device/valid/prov_set-emu.txt");
    TEST_ASSERT_EQUAL_STRING("127.0.0.1", rig_last_store(APP_KEY_HOST)->text);
    TEST_ASSERT_EQUAL_STRING("52817", rig_last_store(APP_KEY_PORT)->text);
    TEST_ASSERT_EQUAL_STRING("emu-01", rig_last_store(APP_KEY_NAME)->text);
    TEST_ASSERT_EQUAL_STRING("lcd349-emu", rig_last_store("wifi0_ssid")->text);
    for (size_t i = 0; i < rig.n_log; i++) {
        const app_action_t *a = &rig.log[i];
        TEST_ASSERT_FALSE_MESSAGE(a->kind == APP_ACT_STORE && strcmp(a->key, "dev_key") == 0, "dev_key stored");
        if (a->kind == APP_ACT_ERASE) { /* the pin it replaces and unused networks, never every key or the key pair */
            TEST_ASSERT_NOT_EQUAL_MESSAGE(0, strlen(a->key), "every key erased");
            TEST_ASSERT_FALSE_MESSAGE(strcmp(a->key, "dev_key") == 0, "dev_key erased");
        }
    }
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-ok.txt"), last_result());

    /* A fresh boot (unpaired) sends prov_hello with the same key at once. */
    boot_unpaired();
    TEST_ASSERT_EQUAL_size_t(1, n_serial());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello.txt"), serial(0));
}

/* Each to-device/invalid fixture, rel, is invalid at start: waiting, or confirmed (an offer and BOOT first). It's
 * answered invalid with nothing kept, and the pager waits for a new offer: a BOOT press then pairs nothing. */
static void invalid_while(bool confirmed, const char *rel)
{
    boot_unpaired();
    if (confirmed) {
        offer_and_boot(500, "to-device/valid/prov_offer.txt");
    }
    rig_clear_log();
    prov_fixture(2000, rel);
    TEST_ASSERT_EQUAL_size_t_MESSAGE(1, n_results(), rel);
    TEST_ASSERT_EQUAL_STRING_MESSAGE(fixture("to-tool/valid/prov_result-invalid.txt"), last_result(), rel);
    TEST_ASSERT_EQUAL_MESSAGE(APP_SCREEN_BOOT, rig_render()->screen, rel);
    rig_press(APP_BTN_BOOT, 3000, SHORT);
    prov_fixture(4000, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING_MESSAGE(fixture("to-tool/valid/prov_result-invalid.txt"), last_result(), rel);
    TEST_ASSERT_EQUAL_size_t_MESSAGE(0, rig_count(APP_ACT_STORE), rel);
    TEST_ASSERT_EQUAL_size_t_MESSAGE(0, rig_count(APP_ACT_REBOOT), rel);
}

static void test_prov_invalid_busy_and_storage(void)
{
    /* Every invalid line while waiting: invalid. Every one that isn't a prov_offer, once confirmed: invalid too,
     * and the confirmed offer is gone; a prov_offer, valid or not, is busy there (below). */
    char dir_path[512];
    snprintf(dir_path, sizeof(dir_path), "%s/provisioning/to-device/invalid", PAGER_TESTDATA);
    DIR *d = opendir(dir_path);
    TEST_ASSERT_NOT_NULL(d);
    int n_files = 0, n_after_boot = 0;
    struct dirent *e;
    while ((e = readdir(d)) != NULL) {
        if (!strstr(e->d_name, ".txt")) {
            continue;
        }
        char rel[300];
        snprintf(rel, sizeof(rel), "to-device/invalid/%s", e->d_name);
        invalid_while(false, rel);
        if (strncmp(e->d_name, "prov_offer", 10) != 0) {
            invalid_while(true, rel);
            n_after_boot++;
        }
        n_files++;
    }
    closedir(d);
    TEST_ASSERT_EQUAL_INT(16, n_files);
    TEST_ASSERT_EQUAL_INT(11, n_after_boot);

    /* A valid prov_set with no confirmed offer: alone (an older bridge's one message), or before BOOT, which
     * ends the offer shown. And one whose key isn't the confirmed offer's. Each is invalid, and nothing is kept. */
    invalid_while(false, "to-device/valid/prov_set.txt");
    invalid_while(false, "to-device/valid/prov_set-4-networks.txt");
    invalid_while(true, "to-device/valid/prov_set-other-host-key.txt");
    boot_unpaired();
    prov_fixture(1000, "to-device/valid/prov_offer.txt");
    prov_fixture(2000, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-invalid.txt"), last_result());
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    rig_press(APP_BTN_BOOT, 3000, SHORT);
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    TEST_ASSERT_EQUAL_size_t(0, rig_count(APP_ACT_STORE));

    /* A line of exactly 512 bytes is accepted. */
    boot_unpaired();
    offer_and_boot(1000, "to-device/valid/prov_offer.txt");
    prov_fixture(3000, "to-device/valid/prov_set-512-bytes.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-ok.txt"), last_result());

    /* A second offer, valid or not, while the first is shown, and again once it's confirmed: busy; the first
     * still stands, and pairs. */
    boot_unpaired();
    prov_fixture(1000, "to-device/valid/prov_offer.txt");
    rig_clear_log();
    prov_fixture(2000, "to-device/valid/prov_offer-ip-host.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-busy.txt"), last_result());
    prov_fixture(2100, "to-device/invalid/prov_offer-bad-host.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-busy.txt"), last_result());
    prov_fixture(2200, "to-device/invalid/malformed.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-busy.txt"), last_result());
    TEST_ASSERT_TRUE(render_has("HOST " RIG_HOST_FP));
    TEST_ASSERT_EQUAL_STRING("PAIR WITH studio-mac?", rig_render()->title);
    rig_press(APP_BTN_BOOT, 3000, SHORT);
    prov_fixture(4000, "to-device/valid/prov_offer-ip-host.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-busy.txt"), last_result());
    prov_fixture(4100, "to-device/invalid/prov_offer-port-zero.txt");
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-busy.txt"), last_result());
    TEST_ASSERT_EQUAL_STRING("NOW CONFIRM ON studio-mac", rig_render()->title);
    prov_fixture(5000, "to-device/valid/prov_set.txt");
    TEST_ASSERT_EQUAL_STRING("pager-01", rig_last_store(APP_KEY_NAME)->text);
    TEST_ASSERT_EQUAL_STRING("studio-mac.local", rig_last_store(APP_KEY_HOST)->text);
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-ok.txt"), last_result());

    /* Saving fails: storage, and back to waiting, as before the offer. */
    boot_unpaired();
    offer_and_boot(1000, "to-device/valid/prov_offer.txt");
    prov_fixture(2500, "to-device/valid/prov_set.txt");
    rig_clear_log();
    app_event_t failed = {.kind = APP_EV_STORE_FAILED};
    rig_event(2600, &failed);
    TEST_ASSERT_EQUAL_size_t(1, n_results());
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_result-storage.txt"), last_result());
    TEST_ASSERT_EQUAL(APP_SCREEN_BOOT, rig_render()->screen);
    TEST_ASSERT_EQUAL_STRING("PAIR VIA USB", rig_render()->title);
    rig_at(6000);
    TEST_ASSERT_EQUAL_STRING(fixture("to-tool/valid/prov_hello.txt"), serial(n_serial() - 1));
}

static void test_longest_host_name_keeps_the_question(void)
{
    /* The title is one body line, 63 characters: a 64-character host name is cut to 52, after "PAIR WITH " and
     * before the "?" (lane board's review; task 10.2 cut it so the board's compiler could see it fit); and once
     * confirmed, to 48 after "NOW CONFIRM ON ". */
    char name[APP_HOST_NAME_MAX + 1], line[1024];
    memset(name, 'h', APP_HOST_NAME_MAX);
    name[APP_HOST_NAME_MAX] = '\0';
    const char *offer = fixture("to-device/valid/prov_offer.txt");
    const char *at = strstr(offer, "\"host_name\":\"studio-mac\"}");
    TEST_ASSERT_NOT_NULL(at);
    snprintf(line, sizeof(line), "%.*s\"host_name\":\"%s\"}", (int)(at - offer), offer, name);
    boot_unpaired();
    prov_line(1000, line);

    const app_render_t *r = rig_render();
    TEST_ASSERT_EQUAL(APP_SCREEN_PAIRING, r->screen);
    TEST_ASSERT_EQUAL_size_t(63, strlen(r->title));
    TEST_ASSERT_EQUAL_STRING_LEN("PAIR WITH ", r->title, 10);
    TEST_ASSERT_EQUAL_size_t(52, strspn(r->title + 10, "h"));
    TEST_ASSERT_EQUAL_CHAR('?', r->title[62]);

    rig_press(APP_BTN_BOOT, 2000, SHORT);
    r = rig_render();
    TEST_ASSERT_EQUAL_size_t(63, strlen(r->title));
    TEST_ASSERT_EQUAL_STRING_LEN("NOW CONFIRM ON ", r->title, 15);
    TEST_ASSERT_EQUAL_size_t(48, strspn(r->title + 15, "h"));
}

TEST_AREA(pairing_flow)
{
    RUN_TEST(test_pairing_mode_sends_prov_hello);
    RUN_TEST(test_offer_shows_both_fingerprints);
    RUN_TEST(test_prov_pwr_or_timeout_discards);
    RUN_TEST(test_prov_confirm_takes_only_a_press_in_its_window);
    RUN_TEST(test_private_key_never_in_a_line);
    RUN_TEST(test_four_networks_stored_in_order);
    RUN_TEST(test_fewer_networks_erase_the_rest);
    RUN_TEST(test_reset_chord_on_pairing_screens);
    RUN_TEST(test_paired_without_key_shows_no_device_key);
    RUN_TEST(test_pair_again_keeps_key);
    RUN_TEST(test_prov_invalid_busy_and_storage);
    RUN_TEST(test_longest_host_name_keeps_the_question);
}
