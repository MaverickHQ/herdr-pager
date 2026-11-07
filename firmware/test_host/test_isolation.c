/* Only net/ssh_link reaches the SSH library (design D4, R2.2): no other file in firmware/ includes an SSH
 * library's headers, and none includes Arduino's (plain ESP-IDF, no arduino-esp32). The check reads every
 * source file under firmware/ (not build output or managed components) for #include lines. */
#include <dirent.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#include "test_registry.h"

#define PATH_MAX_LEN 1024
#define REPORT_MAX 2048

/* The files allowed an SSH library's headers, relative to firmware/. */
static const char *const s_ssh_allowed[] = {"main/net/ssh_link.c", "main/net/ssh_link.h"};

typedef struct {
    char report[REPORT_MAX]; /* "<file>: <header>" per offending include */
    int n_bad;
    int n_files;
    bool link_includes_libssh; /* net/ssh_link.c's own include was seen: the matcher matches the real one */
} scan_t;

static bool starts_with(const char *s, const char *prefix)
{
    return strncmp(s, prefix, strlen(prefix)) == 0;
}

static bool ssh_header(const char *h)
{
    return starts_with(h, "libssh/") || starts_with(h, "libssh2") || starts_with(h, "wolfssh/") ||
           strcmp(h, "libssh.h") == 0;
}

static bool arduino_header(const char *h)
{
    return strcmp(h, "Arduino.h") == 0 || starts_with(h, "esp32-hal") || starts_with(h, "WiFi") ||
           strcmp(h, "HardwareSerial.h") == 0;
}

static bool source_file(const char *name)
{
    const char *dot = strrchr(name, '.');
    if (!dot) {
        return false;
    }
    return strcmp(dot, ".c") == 0 || strcmp(dot, ".h") == 0 || strcmp(dot, ".cc") == 0 || strcmp(dot, ".cpp") == 0 ||
           strcmp(dot, ".hpp") == 0 || strcmp(dot, ".ino") == 0;
}

/* Build output, fetched components and hidden directories aren't the firmware's own sources. */
static bool skipped_dir(const char *name)
{
    return name[0] == '.' || starts_with(name, "build") || strcmp(name, "managed_components") == 0;
}

/* The header an #include line names ("#  include <a/b.h>" gives a/b.h), or false for any other line. */
static bool included_header(const char *line, char *out, size_t cap)
{
    const char *p = line + strspn(line, " \t");
    if (*p != '#') {
        return false;
    }
    p++;
    p += strspn(p, " \t");
    if (!starts_with(p, "include")) {
        return false;
    }
    p += strlen("include");
    p += strspn(p, " \t");
    char close;
    if (*p == '<') {
        close = '>';
    } else if (*p == '"') {
        close = '"';
    } else {
        return false;
    }
    p++;
    const char *end = strchr(p, close);
    if (!end || (size_t)(end - p) >= cap) {
        return false;
    }
    memcpy(out, p, end - p);
    out[end - p] = '\0';
    return true;
}

/* Joins a, sep and b into out, or fails the test: a cut path would leave a
 * file out of the scan without a word (GCC's -Wformat-truncation, on CI). */
static void join_path(char *out, size_t cap, const char *a, const char *sep, const char *b)
{
    int n = snprintf(out, cap, "%s%s%s", a, sep, b);
    TEST_ASSERT_TRUE_MESSAGE(n >= 0 && (size_t)n < cap, "a path too long for the isolation scan's buffer");
}

static void note_bad(scan_t *sc, const char *rel, const char *header)
{
    sc->n_bad++;
    size_t used = strlen(sc->report);
    snprintf(sc->report + used, sizeof(sc->report) - used, "%s%s: <%s>", used ? "; " : "", rel, header);
}

static void scan_file(scan_t *sc, const char *path, const char *rel)
{
    FILE *f = fopen(path, "r");
    TEST_ASSERT_NOT_NULL_MESSAGE(f, path);
    sc->n_files++;
    bool ssh_ok = false;
    for (size_t i = 0; i < sizeof(s_ssh_allowed) / sizeof(s_ssh_allowed[0]); i++) {
        ssh_ok |= strcmp(rel, s_ssh_allowed[i]) == 0;
    }
    char line[1024];
    char header[256];
    while (fgets(line, sizeof(line), f)) {
        if (!included_header(line, header, sizeof(header))) {
            continue;
        }
        if (ssh_header(header)) {
            if (ssh_ok) {
                sc->link_includes_libssh |= strcmp(rel, "main/net/ssh_link.c") == 0;
            } else {
                note_bad(sc, rel, header);
            }
        } else if (arduino_header(header)) {
            note_bad(sc, rel, header);
        }
    }
    fclose(f);
}

/* Scans root/rel (rel "" for root itself) and everything under it. */
static void scan_dir(scan_t *sc, const char *root, const char *rel)
{
    char dir_path[PATH_MAX_LEN];
    join_path(dir_path, sizeof(dir_path), root, rel[0] ? "/" : "", rel);
    DIR *d = opendir(dir_path);
    TEST_ASSERT_NOT_NULL_MESSAGE(d, dir_path);
    struct dirent *e;
    while ((e = readdir(d)) != NULL) {
        if (strcmp(e->d_name, ".") == 0 || strcmp(e->d_name, "..") == 0) {
            continue;
        }
        char path[PATH_MAX_LEN], child[PATH_MAX_LEN];
        join_path(path, sizeof(path), dir_path, "/", e->d_name);
        join_path(child, sizeof(child), rel, rel[0] ? "/" : "", e->d_name);
        struct stat st;
        if (lstat(path, &st) != 0) {
            continue;
        }
        if (S_ISDIR(st.st_mode)) {
            if (!skipped_dir(e->d_name)) {
                scan_dir(sc, root, child);
            }
        } else if (S_ISREG(st.st_mode) && source_file(e->d_name)) {
            scan_file(sc, path, child);
        }
    }
    closedir(d);
}

static void scan(scan_t *sc, const char *root)
{
    memset(sc, 0, sizeof(*sc));
    scan_dir(sc, root, "");
}

static void write_file(const char *dir, const char *rel, const char *text)
{
    char path[PATH_MAX_LEN];
    join_path(path, sizeof(path), dir, "/", rel);
    for (char *slash = strchr(path + strlen(dir) + 1, '/'); slash; slash = strchr(slash + 1, '/')) {
        *slash = '\0';
        mkdir(path, 0700); /* it may exist */
        *slash = '/';
    }
    FILE *f = fopen(path, "w");
    TEST_ASSERT_NOT_NULL_MESSAGE(f, path);
    fputs(text, f);
    fclose(f);
}

static void remove_tree(const char *path)
{
    DIR *d = opendir(path);
    if (d) {
        struct dirent *e;
        while ((e = readdir(d)) != NULL) {
            if (strcmp(e->d_name, ".") != 0 && strcmp(e->d_name, "..") != 0) {
                char child[PATH_MAX_LEN];
                join_path(child, sizeof(child), path, "/", e->d_name);
                remove_tree(child);
            }
        }
        closedir(d);
        rmdir(path);
    } else {
        unlink(path);
    }
}

/* The checker itself, on a planted tree: it finds each kind of offending include, however the line is spaced,
 * and passes ssh_link's own, build output and managed components. */
static void isolation_check_finds_planted_includes(void)
{
    char dir[] = "/tmp/hp-isoXXXXXX";
    TEST_ASSERT_NOT_NULL(mkdtemp(dir));
    write_file(dir, "main/net/ssh_link.c", "#include \"ssh_link.h\"\n#include <libssh/libssh.h>\n");
    write_file(dir, "main/net/ssh_link.h", "#pragma once\n#include <libssh/callbacks.h>\n");
    write_file(dir, "main/net/other.c", "#include <libssh/server.h>\n");
    write_file(dir, "main/app/arduino.cpp", "  #  include \"Arduino.h\"\n");
    write_file(dir, "main/platform/wifi.c", "#include <WiFi.h>\n#include \"esp_wifi.h\"\n");
    write_file(dir, "main/prov/ssh2.h", "#include <libssh2.h>\n");
    write_file(dir, "main/ui/fine.c", "/* #include <libssh/libssh.h> is a comment */\n#include \"lvgl.h\"\n");
    write_file(dir, "build/gen.c", "#include <libssh/libssh.h>\n");
    write_file(dir, "managed_components/x.c", "#include <libssh/libssh.h>\n");

    scan_t sc;
    scan(&sc, dir);
    remove_tree(dir);

    TEST_ASSERT_TRUE(sc.link_includes_libssh);
    TEST_ASSERT_EQUAL_INT_MESSAGE(4, sc.n_bad, sc.report);
    TEST_ASSERT_NOT_NULL(strstr(sc.report, "main/net/other.c: <libssh/server.h>"));
    TEST_ASSERT_NOT_NULL(strstr(sc.report, "main/app/arduino.cpp: <Arduino.h>"));
    TEST_ASSERT_NOT_NULL(strstr(sc.report, "main/platform/wifi.c: <WiFi.h>"));
    TEST_ASSERT_NOT_NULL(strstr(sc.report, "main/prov/ssh2.h: <libssh2.h>"));
}

/* The rule, on the firmware itself (PAGER_TESTDATA is bridge/testdata, so firmware/ is two levels up). */
static void isolation_check(void)
{
    char root[PATH_MAX_LEN];
    snprintf(root, sizeof(root), "%s/../../firmware", PAGER_TESTDATA);
    scan_t sc;
    scan(&sc, root);
    TEST_ASSERT_GREATER_THAN_INT_MESSAGE(20, sc.n_files, "too few files: is firmware/ where the check looks?");
    TEST_ASSERT_TRUE_MESSAGE(sc.link_includes_libssh, "net/ssh_link.c's libssh include wasn't seen");
    if (sc.n_bad) {
        char msg[REPORT_MAX + 64];
        snprintf(msg, sizeof(msg), "only net/ssh_link may include an SSH library; no Arduino: %s", sc.report);
        TEST_FAIL_MESSAGE(msg);
    }
}

TEST_AREA(isolation)
{
    RUN_TEST(isolation_check_finds_planted_includes);
    RUN_TEST(isolation_check);
}
