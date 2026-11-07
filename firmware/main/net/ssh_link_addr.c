/* The SSH link's TCP connection (ssh_link_addr.h). */
#include "ssh_link_addr.h"

#include <errno.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <stdio.h>
#include <string.h>
#include <sys/select.h>
#include <sys/socket.h>
#include <unistd.h>

int ssh_link_tcp_connect(const char *host, uint16_t port, int timeout_s, ssh_link_gai_t gai, const char **why)
{
    /* IPv4 (lwip's mDNS answers A records); no AI_NUMERICHOST, which would refuse a name. */
    struct addrinfo hints = {.ai_family = AF_INET, .ai_socktype = SOCK_STREAM}, *ai = NULL;
    char service[8];
    snprintf(service, sizeof(service), "%u", (unsigned)port);
    if (gai(host, service, &hints, &ai) != 0 || !ai) {
        *why = "can't resolve the host";
        return -1;
    }
    int fd = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
    if (fd < 0) {
        freeaddrinfo(ai);
        *why = "no socket";
        return -1;
    }
    int fl = fcntl(fd, F_GETFL, 0);
    fcntl(fd, F_SETFL, fl | O_NONBLOCK);
    int rc = connect(fd, ai->ai_addr, ai->ai_addrlen);
    freeaddrinfo(ai);
    if (rc != 0 && errno != EINPROGRESS) {
        close(fd);
        *why = "connect refused";
        return -1;
    }
    if (rc != 0) {
        fd_set w;
        FD_ZERO(&w);
        FD_SET(fd, &w);
        struct timeval tv = {.tv_sec = timeout_s};
        rc = select(fd + 1, NULL, &w, NULL, &tv);
        int soerr = 0;
        socklen_t sl = sizeof(soerr);
        if (rc <= 0 || getsockopt(fd, SOL_SOCKET, SO_ERROR, &soerr, &sl) != 0 || soerr != 0) {
            close(fd);
            *why = rc == 0 ? "connect timed out" : "connect failed";
            return -1;
        }
    }
    fcntl(fd, F_SETFL, fl & ~O_NONBLOCK);
    int one = 1;
    setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &one, sizeof(one));
    return fd;
}
