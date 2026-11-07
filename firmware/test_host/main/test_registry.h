/* Test areas register themselves, so adding a test file never edits a list.
 *
 *     #include "test_registry.h"
 *     static void test_snapshot_sorts_by_urgency(void) { ... }
 *     TEST_AREA(model)
 *     {
 *         RUN_TEST(test_snapshot_sorts_by_urgency);
 *     }
 *
 * The runner calls every area in name order. Fixture paths start from
 * PAGER_TESTDATA, bridge/testdata's absolute path.
 */
#pragma once

#include "unity.h"

typedef void (*test_area_fn_t)(void);

/** Registers an area's runner and its file (so a failure names the right file); called before main by TEST_AREA. */
void test_area_register(const char *name, const char *file, test_area_fn_t fn);

#define TEST_AREA(name)                                                                \
    static void test_area_run_##name(void);                                            \
    __attribute__((constructor)) static void test_area_register_##name(void)          \
    {                                                                                  \
        test_area_register(#name, __FILE__, test_area_run_##name);                     \
    }                                                                                  \
    static void test_area_run_##name(void)
