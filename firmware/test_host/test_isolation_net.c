/* ssh_link's code that needs no SSH library, so the host suite builds it here:
 * - its address code (net/ssh_link_addr.c): the provisioned host goes to the system resolver unchanged, so
 *   `.local` names resolve (parent 2.9; lwip's one-shot mDNS query on the board, the Mac's resolver in the
 *   emulator). No mDNS component.
 * - its line assembler (net/ssh_link_lines.c): a line over 8,192 bytes reaches the core cut to 8,193, which the
 *   core drops (parent 13.5; test_codec.c), and its tail never becomes a line of its own. */
#include <netdb.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>

#include "../main/net/ssh_link_addr.c"
#include "../main/net/ssh_link_lines.c"
#include "test_registry.h"

static struct {
    int calls;
    char node[128];
    char service[16];
    struct addrinfo hints;
} s_gai;

/* A resolver that records what it was asked, and finds nothing. */
static int fake_gai(const char *node, const char *service, const struct addrinfo *hints, struct addrinfo **res)
{
    s_gai.calls++;
    snprintf(s_gai.node, sizeof(s_gai.node), "%s", node ? node : "(null)");
    snprintf(s_gai.service, sizeof(s_gai.service), "%s", service ? service : "(null)");
    s_gai.hints = *hints;
    *res = NULL;
    return EAI_NONAME;
}

/* firmware/<rel> into buf. */
static void read_firmware_file(const char *rel, char *buf, size_t cap)
{
    char path[512];
    snprintf(path, sizeof(path), "%s/../../firmware/%s", PAGER_TESTDATA, rel);
    FILE *f = fopen(path, "r");
    TEST_ASSERT_NOT_NULL_MESSAGE(f, path);
    size_t n = fread(buf, 1, cap - 1, f);
    buf[n] = '\0';
    fclose(f);
}

static void test_ssh_link_resolves_by_name(void)
{
    /* The provisioning fixtures' host, a .local name, as provision sends it by default: unchanged, with no
     * flag that would refuse a name (the spike's AI_NUMERICHOST did). */
    static const struct {
        const char *host;
        uint16_t port;
        const char *service;
    } cases[] = {{"studio-mac.local", 2277, "2277"}, {"127.0.0.1", 52817, "52817"}};
    for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
        memset(&s_gai, 0, sizeof(s_gai));
        const char *why = NULL;
        TEST_ASSERT_EQUAL_INT(-1, ssh_link_tcp_connect(cases[i].host, cases[i].port, 1, fake_gai, &why));
        TEST_ASSERT_EQUAL_INT(1, s_gai.calls);
        TEST_ASSERT_EQUAL_STRING(cases[i].host, s_gai.node);
        TEST_ASSERT_EQUAL_STRING(cases[i].service, s_gai.service);
        TEST_ASSERT_EQUAL_INT(0, s_gai.hints.ai_flags & AI_NUMERICHOST);
        TEST_ASSERT_EQUAL_INT(SOCK_STREAM, s_gai.hints.ai_socktype);
        TEST_ASSERT_NOT_NULL(why);
    }

    /* On the board, lwip answers .local names with its one-shot mDNS query; there's no mDNS component. */
    static char text[8192];
    read_firmware_file("sdkconfig.defaults.esp32s3", text, sizeof(text));
    TEST_ASSERT_NOT_NULL_MESSAGE(strstr(text, "\nCONFIG_LWIP_DNS_SUPPORT_MDNS_QUERIES=y"),
                                 "sdkconfig.defaults.esp32s3 doesn't enable CONFIG_LWIP_DNS_SUPPORT_MDNS_QUERIES");
    read_firmware_file("main/idf_component.yml", text, sizeof(text));
    TEST_ASSERT_NULL_MESSAGE(strstr(text, "mdns"), "main/idf_component.yml lists an mDNS component");
}

/* The lines the assembler handed on: each one's length, and its first bytes. */
#define LINES_MAX 4
static struct {
    int n;
    size_t len[LINES_MAX];
    char text[LINES_MAX][256];
} s_lines_out;

static void took_line(const char *line, size_t len, void *ctx)
{
    (void)ctx;
    TEST_ASSERT_LESS_THAN_INT_MESSAGE(LINES_MAX, s_lines_out.n, "more lines than sent");
    s_lines_out.len[s_lines_out.n] = len;
    memcpy(s_lines_out.text[s_lines_out.n], line, len < 255 ? len : 255);
    s_lines_out.n++;
}

/* Appends bridge/testdata/protocol/<rel>, its \n included, to text at *n. */
static void append_protocol_fixture(char *text, size_t cap, size_t *n, const char *rel)
{
    char path[512];
    snprintf(path, sizeof(path), "%s/protocol/%s", PAGER_TESTDATA, rel);
    FILE *f = fopen(path, "rb");
    TEST_ASSERT_NOT_NULL_MESSAGE(f, path);
    *n += fread(text + *n, 1, cap - *n, f);
    fclose(f);
}

/* Feeds len bytes of text to l 1,024 at a time, as the link reads them (READ_CHUNK). */
static void feed_lines(ssh_link_lines_t *l, const char *text, size_t len)
{
    for (size_t at = 0; at < len; at += 1024) {
        ssh_link_lines_take(l, text + at, len - at < 1024 ? len - at : 1024, took_line, NULL);
    }
}

static void test_ssh_link_cuts_long_lines(void)
{
    static ssh_link_lines_t l;
    static char text[3 * APP_LINE_MAX];

    /* The protocol fixtures at the limit and one byte over it, then an ordinary snapshot: the first whole, the
     * second cut to 8,193 bytes (the core drops it), the third whole. */
    size_t n = 0;
    append_protocol_fixture(text, sizeof(text), &n, "to-device/valid/snap-8192-bytes.ndjson");
    append_protocol_fixture(text, sizeof(text), &n, "to-device/invalid/snap-8193-bytes.ndjson");
    size_t snap_at = n;
    append_protocol_fixture(text, sizeof(text), &n, "to-device/valid/snap.ndjson");
    memset(&s_lines_out, 0, sizeof(s_lines_out));
    ssh_link_lines_reset(&l);
    feed_lines(&l, text, n);
    TEST_ASSERT_EQUAL_INT(3, s_lines_out.n);
    TEST_ASSERT_EQUAL_size_t(8192, s_lines_out.len[0]);
    TEST_ASSERT_EQUAL_size_t(8193, s_lines_out.len[1]);
    TEST_ASSERT_EQUAL_size_t(n - snap_at - 1, s_lines_out.len[2]);
    TEST_ASSERT_EQUAL_STRING_LEN(text + snap_at, s_lines_out.text[2], s_lines_out.len[2]);

    /* A longer line: its first 8,193 bytes, and the rest of it skipped up to its \n. */
    memset(text, 'x', 10000);
    memcpy(text + 10000, "\nok\n", 4);
    memset(&s_lines_out, 0, sizeof(s_lines_out));
    feed_lines(&l, text, 10004);
    TEST_ASSERT_EQUAL_INT(2, s_lines_out.n);
    TEST_ASSERT_EQUAL_size_t(8193, s_lines_out.len[0]);
    TEST_ASSERT_EQUAL_STRING("ok", s_lines_out.text[1]);

    /* A new session forgets the last one's partial line. */
    memset(&s_lines_out, 0, sizeof(s_lines_out));
    ssh_link_lines_take(&l, "{\"t\":\"sn", 8, took_line, NULL);
    ssh_link_lines_reset(&l);
    ssh_link_lines_take(&l, "ok\n", 3, took_line, NULL);
    TEST_ASSERT_EQUAL_INT(1, s_lines_out.n);
    TEST_ASSERT_EQUAL_STRING("ok", s_lines_out.text[0]);
}

TEST_AREA(net)
{
    RUN_TEST(test_ssh_link_resolves_by_name);
    RUN_TEST(test_ssh_link_cuts_long_lines);
}
