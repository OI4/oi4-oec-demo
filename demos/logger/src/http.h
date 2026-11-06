/* A minimal HTTP/1.1 server on POSIX sockets for the Web UI of the logger
 * demo (ADR-0028 §3, amendment C-083): one request per connection
 * (Connection: close), bounded in size, time and number of connections,
 * one thread per connection, event streams (SSE) with an own limit.
 * Without TLS and authentication like every demo UI (A-305): it listens
 * on loopback by default and refuses a Host that is a DNS name other than
 * localhost and an Origin of another site (DNS rebinding, CSRF). The
 * same rules as the HTTP server of the config service (A-357). */
#ifndef LOGGER_HTTP_H
#define LOGGER_HTTP_H

#include <stdbool.h>
#include <stddef.h>

/* At most this many connections at a time (more get a 503), of them at
 * most LH_MAX_STREAMS event streams. */
#define LH_MAX_CONNECTIONS 16
#define LH_MAX_STREAMS 8
/* Request line and headers, and the body. */
#define LH_MAX_HEAD 8192
#define LH_MAX_BODY 65536
/* Time to receive a request and to send a part of the response. */
#define LH_IO_MS 5000

/* A request. */
typedef struct lh_request {
    char method[16];
    char path[1024];  /* without the query */
    char query[2048]; /* after '?', "" without */
    char host[256];
    size_t content_length;
    const char *body; /* content_length octets */
} lh_request;

/* Parses a complete request head of n octets (the request line and the
 * headers up to and including the empty line) and applies the guards in
 * this order: Host (400 without, 403 not localhost or an IP address),
 * Origin (403 if not http://<Host>), Transfer-Encoding (501), a POST
 * without Content-Type application/json (415), Content-Length above
 * LH_MAX_BODY (413). 0 if accepted, else the status of the refusal and
 * its reason in *why. */
int lh_parse(const char *head, size_t n, lh_request *r, const char **why);

/* localhost or an IP literal ([…] for IPv6), with an optional port. */
bool lh_host_ok(const char *host);

/* The next parameter of a query (application/x-www-form-urlencoded):
 * 1 with the decoded name and value, 0 at the end, -1 if it is invalid
 * (bad percent encoding, NUL, too long). *q advances. */
int lh_query_next(const char **q, char *name, size_t name_len, char *value, size_t value_len);

/* Splits a listen address host:port or [IPv6]:port; false if invalid. */
bool lh_split_listen(const char *address, char *host, size_t host_len, char *port, size_t port_len);

typedef struct lh_conn lh_conn;
typedef struct lh_server lh_server;

/* Handles one request on the connection thread; it answers with
 * lh_respond, lh_error or an event stream. */
typedef void (*lh_handler)(void *user_data, lh_conn *c, const lh_request *r);

/* Binds and listens on listen; NULL with a message in err. */
lh_server *lh_listen(const char *address, char *err, size_t errlen);

/* The bound address, e.g. "127.0.0.1:8781" or "[::1]:8781". */
const char *lh_address(const lh_server *s);

/* Starts the accept thread; false if no thread. */
bool lh_start(lh_server *s, lh_handler handler, void *user_data);

/* Stops accepting, ends every connection and event stream, waits for
 * the threads and frees s; NULL is ignored. */
void lh_stop(lh_server *s);

/* Answers with the status, the content type and the body; extra is
 * further header lines ("Name: value\r\n…") or NULL. Every response has
 * Connection: close, Cache-Control: no-store, X-Content-Type-Options:
 * nosniff, a Content-Security-Policy that allows the own origin only and
 * Referrer-Policy: no-referrer. */
void lh_respond(lh_conn *c, int status, const char *content_type, const void *body, size_t n,
                const char *extra);

/* Answers with {"error": message}. */
void lh_error(lh_conn *c, int status, const char *message, const char *extra);

/* Starts an event stream (text/event-stream); false if LH_MAX_STREAMS
 * are open (then it has answered 503). */
bool lh_stream_begin(lh_conn *c);

/* Writes to the event stream; false if the client is gone, too slow
 * (LH_IO_MS) or the server stops. */
bool lh_stream_write(lh_conn *c, const char *s, size_t n);

/* Whether the client is still there and the server runs. */
bool lh_stream_alive(lh_conn *c);

#endif
