/* The host test runner: runs every registered area in name order, and exits 0 when every test passes. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "test_registry.h"
#include "unity.h"

#define MAX_AREAS 32

static struct {
    const char *name;
    const char *file;
    test_area_fn_t fn;
} s_areas[MAX_AREAS];
static int s_n_areas;

void test_area_register(const char *name, const char *file, test_area_fn_t fn)
{
    if (s_n_areas == MAX_AREAS) {
        fprintf(stderr, "test_main: more than %d test areas; raise MAX_AREAS\n", MAX_AREAS);
        exit(2);
    }
    s_areas[s_n_areas].name = name;
    s_areas[s_n_areas].file = file;
    s_areas[s_n_areas].fn = fn;
    s_n_areas++;
}

static int by_name(const void *a, const void *b)
{
    return strcmp(*(const char *const *)a, *(const char *const *)b);
}

void setUp(void) {}
void tearDown(void) {}

void app_main(void)
{
    qsort(s_areas, s_n_areas, sizeof(s_areas[0]), by_name);
    UNITY_BEGIN();
    for (int i = 0; i < s_n_areas; i++) {
        printf("-- %s\n", s_areas[i].name);
        UnitySetTestFile(s_areas[i].file); /* else every failure names this file */
        s_areas[i].fn();
    }
    exit(UNITY_END() == 0 ? 0 : 1);
}
