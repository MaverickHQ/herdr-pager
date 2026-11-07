/* The pump's Wi-Fi join retry policy (platform/wifi_retry.c), which needs no FreeRTOS or kit, so the host suite
 * builds it here. It's driven as platform/pump.c drives it from the kit's status: a failed join (refused or
 * not), whether a retry is already due, and a join that went through. */
#include <stdbool.h>
#include <string.h>

#include "../main/platform/wifi_retry.c"
#include "test_registry.h"

/* Any other failure (no saved network in range, a lost network): 1, 2, 4, 8, 16, then 30 s; a retry already
 * due stays as it is; a join starts it over (open-items O-09). */
static void test_other_failures_back_off(void)
{
    wifi_retry_t r = {0};
    static const uint32_t want[] = {1000, 2000, 4000, 8000, 16000, 30000, 30000};
    for (size_t i = 0; i < sizeof(want) / sizeof(want[0]); i++) {
        TEST_ASSERT_EQUAL_UINT32(want[i], wifi_retry_failed(&r, false, false));
        TEST_ASSERT_EQUAL_UINT32(WIFI_RETRY_KEEP, wifi_retry_failed(&r, false, true));
    }
    wifi_retry_joined(&r);
    TEST_ASSERT_EQUAL_UINT32(1000, wifi_retry_failed(&r, false, false));
}

/* A join the access point refuses is tried again at once, three refusals in a row, then with the backoff, so
 * a boot that meets two refusals still shows the list within N7's 15 s (the owner's decision, 2026-10-27,
 * open-items O-45). At once even with a retry due: the pump drops that one. */
static void test_refused_joins_tried_again_at_once(void)
{
    wifi_retry_t r = {0};
    TEST_ASSERT_EQUAL_UINT32(0, wifi_retry_failed(&r, true, false));
    TEST_ASSERT_EQUAL_UINT32(0, wifi_retry_failed(&r, true, true));
    TEST_ASSERT_EQUAL_UINT32(0, wifi_retry_failed(&r, true, false));
    TEST_ASSERT_EQUAL_UINT32(1000, wifi_retry_failed(&r, true, false)); /* the fourth: the backoff, from 1 s */
    TEST_ASSERT_EQUAL_UINT32(2000, wifi_retry_failed(&r, true, false));

    /* Another failure breaks the row; the next refusal is tried at once again. */
    TEST_ASSERT_EQUAL_UINT32(4000, wifi_retry_failed(&r, false, false));
    TEST_ASSERT_EQUAL_UINT32(0, wifi_retry_failed(&r, true, false));

    /* A join starts both over. */
    wifi_retry_joined(&r);
    TEST_ASSERT_EQUAL_UINT32(0, wifi_retry_failed(&r, true, false));
    TEST_ASSERT_EQUAL_UINT32(0, wifi_retry_failed(&r, true, false));
    TEST_ASSERT_EQUAL_UINT32(0, wifi_retry_failed(&r, true, false));
    TEST_ASSERT_EQUAL_UINT32(1000, wifi_retry_failed(&r, true, false));
}

TEST_AREA(wifi_retry)
{
    RUN_TEST(test_other_failures_back_off);
    RUN_TEST(test_refused_joins_tried_again_at_once);
}
