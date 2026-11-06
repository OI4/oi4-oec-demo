/* SO_NOSIGPIPE is hidden by _POSIX_C_SOURCE on macOS. */
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE
#endif

#include "http.h"

#include <arpa/inet.h>
#include <errno.h>
#include <netdb.h>
#include <netinet/in.h>
#include <poll.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

#include "json.h"

#ifdef MSG_NOSIGNAL
#define SEND_FLAGS MSG_NOSIGNAL
#else
#define SEND_FLAGS 0
#endif

/* Time to drop what a client still sends after the response. */
#define DRAIN_MS 100
/* The accept thread looks for the stop this often. */
#define ACCEPT_TICK_MS 200

typedef struct slot {
    struct lh_server *srv;
    pthread_t thread;
    int fd;
    bool used; /* the thread exists and is not joined */
    bool done; /* the thread has ended */
    bool stream;
} slot;

struct lh_server {
    int fd;
    char address[INET6_ADDRSTRLEN + 8];
    lh_handler handler;
    void *user_data;
    pthread_t accept_thread;
    bool started;
    pthread_mutex_t lock;
    bool stopping;
    unsigned streams;
    slot slots[LH_MAX_CONNECTIONS];
};

struct lh_conn {
    lh_server *srv;
    slot *slot;
    int fd;
    bool answered;
};

/* ---- parsing ------------------------------------------------------------- */

static int lower(int c) { return c >= 'A' && c <= 'Z' ? c + 32 : c; }

static bool ieq(const char *a, size_t n, const char *b) {
    if (strlen(b) != n)
        return false;
    for (size_t i = 0; i < n; i++)
        if (lower((unsigned char)a[i]) != lower((unsigned char)b[i]))
            return false;
    return true;
}

static bool digits(const char *p, size_t n) {
    if (n == 0 || n > 5)
        return false;
    for (size_t i = 0; i < n; i++)
        if (p[i] < '0' || p[i] > '9')
            return false;
    return true;
}

static bool ip_literal(const char *p, size_t n, int family) {
    char buf[INET6_ADDRSTRLEN + 1];
    unsigned char out[sizeof(struct in6_addr)];
    if (n == 0 || n >= sizeof buf)
        return false;
    memcpy(buf, p, n);
    buf[n] = 0;
    return inet_pton(family, buf, out) == 1;
}

bool lh_host_ok(const char *host) {
    size_t n = strlen(host);
    if (host[0] == '[') {
        const char *end = strchr(host, ']');
        if (!end)
            return false;
        const char *port = end + 1;
        if (*port && (*port != ':' || !digits(port + 1, strlen(port + 1))))
            return false;
        return ip_literal(host + 1, (size_t)(end - host - 1), AF_INET6);
    }
    const char *colon = strrchr(host, ':');
    if (colon) {
        if (!digits(colon + 1, strlen(colon + 1)))
            return false;
        n = (size_t)(colon - host);
    }
    return ieq(host, n, "localhost") || ip_literal(host, n, AF_INET) || ip_literal(host, n, AF_INET6);
}

/* A header value without the white space around it. */
static void trim(const char **p, size_t *n) {
    while (*n && (**p == ' ' || **p == '\t')) {
        (*p)++;
        (*n)--;
    }
    while (*n && ((*p)[*n - 1] == ' ' || (*p)[*n - 1] == '\t'))
        (*n)--;
}

static bool copy(char *dst, size_t len, const char *p, size_t n) {
    if (n >= len)
        return false;
    memcpy(dst, p, n);
    dst[n] = 0;
    return true;
}

static int refuse(const char **why, const char *reason, int status) {
    *why = reason;
    return status;
}

int lh_parse(const char *head, size_t n, lh_request *r, const char **why) {
    memset(r, 0, sizeof *r);
    const char *end = head + n;
    const char *eol = NULL;
    for (const char *p = head; p + 1 < end; p++)
        if (p[0] == '\r' && p[1] == '\n') {
            eol = p;
            break;
        }
    if (!eol || memchr(head, 0, n))
        return refuse(why, "invalid request line", 400);

    /* METHOD SP target SP HTTP/1.x */
    const char *sp1 = memchr(head, ' ', (size_t)(eol - head));
    const char *sp2 = sp1 ? memchr(sp1 + 1, ' ', (size_t)(eol - sp1 - 1)) : NULL;
    if (!sp1 || !sp2 || sp1 == head || memchr(sp2 + 1, ' ', (size_t)(eol - sp2 - 1)) ||
        !copy(r->method, sizeof r->method, head, (size_t)(sp1 - head)))
        return refuse(why, "invalid request line", 400);
    const char *target = sp1 + 1;
    size_t tlen = (size_t)(sp2 - target);
    size_t vlen = (size_t)(eol - sp2 - 1);
    if (tlen == 0 || target[0] != '/' || vlen != 8 || memcmp(sp2 + 1, "HTTP/1.", 7) != 0)
        return refuse(why, "invalid request line", 400);
    const char *q = memchr(target, '?', tlen);
    size_t plen = q ? (size_t)(q - target) : tlen;
    if (!copy(r->path, sizeof r->path, target, plen) ||
        (q && !copy(r->query, sizeof r->query, q + 1, tlen - plen - 1)))
        return refuse(why, "request target too long", 414);

    bool host = false, origin = false, te = false, json = false, length = false;
    const char *origin_p = NULL;
    size_t origin_n = 0;
    for (const char *line = eol + 2; line < end;) {
        const char *le = NULL;
        for (const char *p = line; p + 1 < end; p++)
            if (p[0] == '\r' && p[1] == '\n') {
                le = p;
                break;
            }
        if (!le)
            return refuse(why, "invalid header", 400);
        if (le == line)
            break; /* the empty line */
        const char *colon = memchr(line, ':', (size_t)(le - line));
        if (!colon || colon == line || line[0] == ' ' || line[0] == '\t' ||
            memchr(line, ' ', (size_t)(colon - line)) || memchr(line, '\t', (size_t)(colon - line)))
            return refuse(why, "invalid header", 400);
        const char *name = line;
        size_t nlen = (size_t)(colon - line);
        const char *value = colon + 1;
        size_t len = (size_t)(le - value);
        trim(&value, &len);
        if (ieq(name, nlen, "host")) {
            if (host)
                return refuse(why, "repeated header", 400);
            host = true;
            if (!copy(r->host, sizeof r->host, value, len))
                return refuse(why, "Host is not localhost or an IP address", 403);
        } else if (ieq(name, nlen, "origin")) {
            if (origin)
                return refuse(why, "repeated header", 400);
            origin = true;
            origin_p = value;
            origin_n = len;
        } else if (ieq(name, nlen, "transfer-encoding")) {
            te = true;
        } else if (ieq(name, nlen, "content-type")) {
            const char *semi = memchr(value, ';', len);
            size_t mlen = semi ? (size_t)(semi - value) : len;
            trim(&value, &mlen);
            json = ieq(value, mlen, "application/json");
        } else if (ieq(name, nlen, "content-length")) {
            if (length)
                return refuse(why, "repeated header", 400);
            length = true;
            if (len == 0 || len > 18)
                return refuse(why, "invalid Content-Length", len > 18 ? 413 : 400);
            size_t v = 0;
            for (size_t i = 0; i < len; i++) {
                if (value[i] < '0' || value[i] > '9')
                    return refuse(why, "invalid Content-Length", 400);
                v = v * 10 + (size_t)(value[i] - '0');
            }
            r->content_length = v;
        }
        line = le + 2;
    }

    if (!host)
        return refuse(why, "Host is required", 400);
    if (!lh_host_ok(r->host))
        return refuse(why, "Host is not localhost or an IP address", 403);
    if (origin) {
        size_t hl = strlen(r->host);
        if (origin_n != 7 + hl || memcmp(origin_p, "http://", 7) != 0 ||
            memcmp(origin_p + 7, r->host, hl) != 0)
            return refuse(why, "cross-origin request", 403);
    }
    if (te)
        return refuse(why, "Transfer-Encoding is not supported", 501);
    if (strcmp(r->method, "POST") == 0 && !json)
        return refuse(why, "Content-Type application/json is required", 415);
    if (r->content_length > LH_MAX_BODY)
        return refuse(why, "body too large", 413);
    return 0;
}

static int hex(int c) {
    if (c >= '0' && c <= '9')
        return c - '0';
    c = lower(c);
    return c >= 'a' && c <= 'f' ? c - 'a' + 10 : -1;
}

/* Decodes p[0..n) into out; false if invalid or too long. */
static bool decode(const char *p, size_t n, char *out, size_t len) {
    size_t o = 0;
    for (size_t i = 0; i < n; i++) {
        int c = (unsigned char)p[i];
        if (c == '+')
            c = ' ';
        else if (c == '%') {
            if (i + 2 >= n)
                return false;
            int h = hex((unsigned char)p[i + 1]), l = hex((unsigned char)p[i + 2]);
            if (h < 0 || l < 0)
                return false;
            c = h * 16 + l;
            i += 2;
        }
        if (c == 0 || o + 1 >= len)
            return false;
        out[o++] = (char)c;
    }
    out[o] = 0;
    return true;
}

int lh_query_next(const char **q, char *name, size_t name_len, char *value, size_t value_len) {
    const char *p = *q;
    while (*p == '&')
        p++;
    if (!*p) {
        *q = p;
        return 0;
    }
    const char *amp = strchr(p, '&');
    size_t n = amp ? (size_t)(amp - p) : strlen(p);
    const char *eq = memchr(p, '=', n);
    size_t kn = eq ? (size_t)(eq - p) : n;
    *q = p + n;
    if (!decode(p, kn, name, name_len))
        return -1;
    if (!decode(eq ? eq + 1 : p + n, eq ? n - kn - 1 : 0, value, value_len))
        return -1;
    return 1;
}

bool lh_split_listen(const char *address, char *host, size_t host_len, char *port, size_t port_len) {
    const char *colon;
    const char *h = address;
    size_t hn;
    if (address[0] == '[') {
        const char *end = strchr(address, ']');
        if (!end || end[1] != ':')
            return false;
        h = address + 1;
        hn = (size_t)(end - h);
        colon = end + 1;
    } else {
        colon = strrchr(address, ':');
        if (!colon || memchr(address, ':', (size_t)(colon - address)))
            return false;
        hn = (size_t)(colon - address);
    }
    const char *p = colon + 1;
    size_t pn = strlen(p);
    if (hn == 0 || !digits(p, pn) || atol(p) > 65535)
        return false;
    return copy(host, host_len, h, hn) && copy(port, port_len, p, pn);
}

/* ---- sockets ------------------------------------------------------------- */

static int64_t now_ms(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static void socket_options(int fd) {
    struct timeval tv = {LH_IO_MS / 1000, (LH_IO_MS % 1000) * 1000};
    setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv);
#ifdef SO_NOSIGPIPE
    int one = 1;
    setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, sizeof one);
#endif
}

static bool send_all(int fd, const void *buf, size_t n) {
    const char *p = buf;
    while (n > 0) {
        ssize_t w = send(fd, p, n, SEND_FLAGS);
        if (w < 0 && errno == EINTR)
            continue;
        if (w <= 0)
            return false;
        p += w;
        n -= (size_t)w;
    }
    return true;
}

/* Reads into buf until the deadline: >0 octets, 0 at the end of the
 * stream, -1 on timeout or error. */
static ssize_t recv_until(int fd, char *buf, size_t n, int64_t deadline) {
    for (;;) {
        int64_t left = deadline - now_ms();
        if (left <= 0)
            return -1;
        struct pollfd pfd = {fd, POLLIN, 0};
        int rc = poll(&pfd, 1, (int)left);
        if (rc < 0 && errno == EINTR)
            continue;
        if (rc <= 0)
            return -1;
        ssize_t got = recv(fd, buf, n, 0);
        if (got < 0 && errno == EINTR)
            continue;
        return got;
    }
}

/* After the response: what the client still sends is dropped for a short
 * time, so that the close does not reset the connection before the client
 * has read the response. */
static void finish(int fd) {
    char sink[4096];
    size_t left = LH_MAX_HEAD + LH_MAX_BODY;
    shutdown(fd, SHUT_WR);
    int64_t deadline = now_ms() + DRAIN_MS;
    while (left > 0) {
        ssize_t got = recv_until(fd, sink, sizeof sink, deadline);
        if (got <= 0)
            break;
        left = (size_t)got < left ? left - (size_t)got : 0;
    }
}

static const char *reason(int status) {
    switch (status) {
    case 200: return "OK";
    case 400: return "Bad Request";
    case 403: return "Forbidden";
    case 404: return "Not Found";
    case 405: return "Method Not Allowed";
    case 408: return "Request Timeout";
    case 413: return "Content Too Large";
    case 414: return "URI Too Long";
    case 415: return "Unsupported Media Type";
    case 431: return "Request Header Fields Too Large";
    case 500: return "Internal Server Error";
    case 501: return "Not Implemented";
    case 503: return "Service Unavailable";
    default: return "Error";
    }
}

#define COMMON_HEADERS                                                                     \
    "Connection: close\r\nCache-Control: no-store\r\nX-Content-Type-Options: nosniff\r\n"  \
    "Content-Security-Policy: default-src 'self'; frame-ancestors 'none'\r\n"            \
    "Referrer-Policy: no-referrer\r\n"

static bool respond_fd(int fd, int status, const char *content_type, const void *body, size_t n,
                       const char *extra) {
    char head[1024];
    int len = snprintf(head, sizeof head,
                       "HTTP/1.1 %d %s\r\nContent-Type: %s\r\nContent-Length: %zu\r\n" COMMON_HEADERS
                       "%s\r\n",
                       status, reason(status), content_type, n, extra ? extra : "");
    if (len < 0 || (size_t)len >= sizeof head)
        return false;
    return send_all(fd, head, (size_t)len) && (n == 0 || send_all(fd, body, n));
}

static void error_fd(int fd, int status, const char *message, const char *extra) {
    char *json = NULL;
    size_t len = 0;
    FILE *f = open_memstream(&json, &len);
    if (!f)
        return;
    fputs("{\"error\":", f);
    lj_write_string(f, message);
    fputc('}', f);
    if (fclose(f) == 0 && json)
        respond_fd(fd, status, "application/json", json, len, extra);
    free(json);
}

void lh_respond(lh_conn *c, int status, const char *content_type, const void *body, size_t n,
                const char *extra) {
    if (c->answered)
        return;
    c->answered = true;
    respond_fd(c->fd, status, content_type, body, n, extra);
}

void lh_error(lh_conn *c, int status, const char *message, const char *extra) {
    if (c->answered)
        return;
    c->answered = true;
    error_fd(c->fd, status, message, extra);
}

bool lh_stream_begin(lh_conn *c) {
    if (c->answered)
        return false;
    pthread_mutex_lock(&c->srv->lock);
    bool ok = c->srv->streams < LH_MAX_STREAMS && !c->srv->stopping;
    if (ok) {
        c->srv->streams++;
        c->slot->stream = true;
    }
    pthread_mutex_unlock(&c->srv->lock);
    if (!ok) {
        lh_error(c, 503, "too many event streams", NULL);
        return false;
    }
    c->answered = true;
    static const char head[] = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n" COMMON_HEADERS
                               "\r\n";
    return send_all(c->fd, head, sizeof head - 1);
}

static bool stopping(lh_server *s) {
    pthread_mutex_lock(&s->lock);
    bool stop = s->stopping;
    pthread_mutex_unlock(&s->lock);
    return stop;
}

bool lh_stream_write(lh_conn *c, const char *s, size_t n) {
    return !stopping(c->srv) && send_all(c->fd, s, n);
}

bool lh_stream_alive(lh_conn *c) {
    if (stopping(c->srv))
        return false;
    struct pollfd pfd = {c->fd, POLLIN, 0};
    if (poll(&pfd, 1, 0) <= 0)
        return true;
    if (pfd.revents & (POLLERR | POLLHUP | POLLNVAL))
        return false;
    /* What a client sends on an event stream is dropped; the end of its
     * stream ends the event stream. */
    char sink[512];
    return recv(c->fd, sink, sizeof sink, 0) > 0;
}

/* ---- connections --------------------------------------------------------- */

/* Reads a request: 0 with the head in buf, or the status to refuse it
 * (-1: the client went away, no answer). */
static int read_request(int fd, char *buf, size_t cap, lh_request *r, const char **why) {
    int64_t deadline = now_ms() + LH_IO_MS;
    size_t n = 0, head_end = 0;
    for (;;) {
        for (size_t i = head_end ? head_end : 0; i + 3 < n; i++)
            if (memcmp(buf + i, "\r\n\r\n", 4) == 0) {
                head_end = i + 4;
                break;
            }
        if (head_end)
            break;
        if (n > LH_MAX_HEAD)
            return refuse(why, "request head too large", 431);
        ssize_t got = recv_until(fd, buf + n, cap - n, deadline);
        if (got == 0)
            return n ? refuse(why, "incomplete request", 400) : -1;
        if (got < 0)
            return refuse(why, "request too slow", 408);
        n += (size_t)got;
    }
    if (head_end > LH_MAX_HEAD + 4)
        return refuse(why, "request head too large", 431);
    int status = lh_parse(buf, head_end, r, why);
    if (status)
        return status;
    while (n < head_end + r->content_length) {
        ssize_t got = recv_until(fd, buf + n, cap - n, deadline);
        if (got == 0)
            return refuse(why, "incomplete request", 400);
        if (got < 0)
            return refuse(why, "request too slow", 408);
        n += (size_t)got;
    }
    if (n != head_end + r->content_length)
        return refuse(why, "body longer than Content-Length", 400);
    r->body = buf + head_end;
    return 0;
}

static int listen_fd(int fd) { return listen(fd, 64); }

static void *connection(void *arg) {
    slot *sl = arg;
    lh_server *srv = sl->srv;
    lh_conn c = {.srv = srv, .slot = sl, .fd = sl->fd};
    /* the head, the body and room for one more read */
    size_t cap = LH_MAX_HEAD + 4 + LH_MAX_BODY + 4096;
    char *buf = malloc(cap);
    if (!buf)
        error_fd(c.fd, 500, "out of memory", NULL);
    else {
        lh_request r;
        const char *why = "";
        int status = read_request(c.fd, buf, cap, &r, &why);
        if (status > 0)
            lh_error(&c, status, why, NULL);
        else if (status == 0) {
            srv->handler(srv->user_data, &c, &r);
            if (!c.answered)
                lh_error(&c, 500, "no answer", NULL);
        }
        free(buf);
    }
    finish(c.fd);
    pthread_mutex_lock(&srv->lock);
    if (sl->stream)
        srv->streams--;
    sl->stream = false;
    close(sl->fd);
    sl->fd = -1;
    sl->done = true;
    pthread_mutex_unlock(&srv->lock);
    return NULL;
}

/* Under the lock: joins the ended connection threads. */
static void reap(lh_server *s) {
    for (int i = 0; i < LH_MAX_CONNECTIONS; i++)
        if (s->slots[i].used && s->slots[i].done) {
            pthread_join(s->slots[i].thread, NULL);
            s->slots[i].used = false;
        }
}

static void *accept_loop(void *arg) {
    lh_server *s = arg;
    while (!stopping(s)) {
        struct pollfd pfd = {s->fd, POLLIN, 0};
        if (poll(&pfd, 1, ACCEPT_TICK_MS) <= 0)
            continue;
        int fd = accept(s->fd, NULL, NULL);
        if (fd < 0)
            continue;
        socket_options(fd);
        pthread_mutex_lock(&s->lock);
        reap(s);
        slot *free_slot = NULL;
        for (int i = 0; i < LH_MAX_CONNECTIONS && !free_slot; i++)
            if (!s->slots[i].used)
                free_slot = &s->slots[i];
        if (free_slot && !s->stopping) {
            *free_slot = (slot){.srv = s, .fd = fd, .used = true};
            if (pthread_create(&free_slot->thread, NULL, connection, free_slot) != 0) {
                free_slot->used = false;
                free_slot = NULL;
            }
        } else
            free_slot = NULL;
        pthread_mutex_unlock(&s->lock);
        if (!free_slot) {
            error_fd(fd, 503, "too many connections", NULL);
            finish(fd);
            close(fd);
        }
    }
    return NULL;
}

lh_server *lh_listen(const char *address, char *err, size_t errlen) {
    char host[256], port[8];
    if (!lh_split_listen(address, host, sizeof host, port, sizeof port)) {
        snprintf(err, errlen, "invalid address %s (host:port, [IPv6]:port)", address);
        return NULL;
    }
    struct addrinfo hints = {.ai_flags = AI_PASSIVE | AI_NUMERICSERV, .ai_socktype = SOCK_STREAM};
    struct addrinfo *res = NULL;
    int rc = getaddrinfo(host, port, &hints, &res);
    if (rc != 0) {
        snprintf(err, errlen, "cannot resolve %s: %s", address, gai_strerror(rc));
        return NULL;
    }
    int fd = -1, saved = 0;
    for (struct addrinfo *a = res; a && fd < 0; a = a->ai_next) {
        fd = socket(a->ai_family, a->ai_socktype, a->ai_protocol);
        if (fd < 0) {
            saved = errno;
            continue;
        }
        int one = 1;
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
        if (bind(fd, a->ai_addr, a->ai_addrlen) != 0 || listen_fd(fd) != 0) {
            saved = errno;
            close(fd);
            fd = -1;
        }
    }
    freeaddrinfo(res);
    if (fd < 0) {
        snprintf(err, errlen, "cannot listen on %s: %s", address, strerror(saved));
        return NULL;
    }
    lh_server *s = calloc(1, sizeof *s);
    if (!s || pthread_mutex_init(&s->lock, NULL) != 0) {
        free(s);
        close(fd);
        snprintf(err, errlen, "out of memory");
        return NULL;
    }
    s->fd = fd;
    struct sockaddr_storage ss;
    memset(&ss, 0, sizeof ss);
    socklen_t sl = sizeof ss;
    char ip[INET6_ADDRSTRLEN] = "?";
    unsigned p = 0;
    if (getsockname(fd, (struct sockaddr *)&ss, &sl) == 0) {
        if (ss.ss_family == AF_INET6) {
            const struct sockaddr_in6 *a6 = (const struct sockaddr_in6 *)&ss;
            inet_ntop(AF_INET6, &a6->sin6_addr, ip, sizeof ip);
            p = ntohs(a6->sin6_port);
        } else {
            const struct sockaddr_in *a4 = (const struct sockaddr_in *)&ss;
            inet_ntop(AF_INET, &a4->sin_addr, ip, sizeof ip);
            p = ntohs(a4->sin_port);
        }
    }
    snprintf(s->address, sizeof s->address, ss.ss_family == AF_INET6 ? "[%s]:%u" : "%s:%u", ip, p);
    return s;
}

const char *lh_address(const lh_server *s) { return s->address; }

bool lh_start(lh_server *s, lh_handler handler, void *user_data) {
    s->handler = handler;
    s->user_data = user_data;
    s->started = pthread_create(&s->accept_thread, NULL, accept_loop, s) == 0;
    return s->started;
}

void lh_stop(lh_server *s) {
    if (!s)
        return;
    pthread_mutex_lock(&s->lock);
    s->stopping = true;
    pthread_mutex_unlock(&s->lock);
    if (s->started)
        pthread_join(s->accept_thread, NULL);
    close(s->fd);
    pthread_mutex_lock(&s->lock);
    for (int i = 0; i < LH_MAX_CONNECTIONS; i++)
        if (s->slots[i].used && !s->slots[i].done)
            shutdown(s->slots[i].fd, SHUT_RDWR);
    pthread_mutex_unlock(&s->lock);
    for (int i = 0; i < LH_MAX_CONNECTIONS; i++)
        if (s->slots[i].used)
            pthread_join(s->slots[i].thread, NULL);
    pthread_mutex_destroy(&s->lock);
    free(s);
}
