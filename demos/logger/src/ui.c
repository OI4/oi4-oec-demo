/* The Web UI of the logger demo, see ui.h. */
#include "ui.h"

#include <errno.h>
#include <math.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "http.h"
#include "json.h"

/* Records per GET /api/records by default and at most. */
#define DEFAULT_LIMIT 200
#define MAX_TEXT 256
/* How long an event stream waits for a record before it checks the
 * client, the settings and the keep-alive. */
#define TICK_MS 250
/* Records per write of an event stream. */
#define BATCH 64

struct lg_ui {
    lh_server *srv;
    lg_history *history;
    const char *version;
    unsigned keepalive_ms;
    lg_ui_state_fn state;
    void *user_data;

    pthread_mutex_t lock;
    lg_settings settings;
    bool has_settings;
    uint64_t settings_version;
};

static int64_t mono_ms(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

lg_ui *lg_ui_listen(const char *address, char *err, size_t errlen) {
    lg_ui *ui = calloc(1, sizeof *ui);
    if (!ui) {
        snprintf(err, errlen, "out of memory");
        return NULL;
    }
    if (pthread_mutex_init(&ui->lock, NULL) != 0) {
        free(ui);
        snprintf(err, errlen, "no mutex");
        return NULL;
    }
    ui->srv = lh_listen(address, err, errlen);
    if (!ui->srv) {
        pthread_mutex_destroy(&ui->lock);
        free(ui);
        return NULL;
    }
    return ui;
}

const char *lg_ui_address(const lg_ui *ui) { return lh_address(ui->srv); }

void lg_ui_settings(lg_ui *ui, const lg_settings *s) {
    pthread_mutex_lock(&ui->lock);
    ui->settings = *s;
    ui->has_settings = true;
    ui->settings_version++;
    pthread_mutex_unlock(&ui->lock);
}

static uint64_t settings_version(lg_ui *ui) {
    pthread_mutex_lock(&ui->lock);
    uint64_t v = ui->settings_version;
    pthread_mutex_unlock(&ui->lock);
    return v;
}

static const char *state_name(lg_ui *ui) {
    const char *s = ui->state ? ui->state(ui->user_data) : NULL;
    return s ? s : "unknown";
}

/* ---- JSON ---------------------------------------------------------------- */

/* A response body under construction. */
typedef struct buf {
    char *data;
    size_t len;
    FILE *f;
} buf;

static bool buf_open(buf *b) {
    b->data = NULL;
    b->len = 0;
    b->f = open_memstream(&b->data, &b->len);
    return b->f != NULL;
}

/* Closes the stream; false (and freed) on an error. */
static bool buf_close(buf *b) {
    bool ok = !ferror(b->f);
    if (fclose(b->f) != 0)
        ok = false;
    b->f = NULL;
    if (!ok || !b->data) {
        free(b->data);
        b->data = NULL;
        return false;
    }
    return true;
}

/* The status; with a type as an event of the event stream. */
static void write_status(lg_ui *ui, FILE *f, const char *type) {
    pthread_mutex_lock(&ui->lock);
    lg_settings s = ui->settings;
    bool has = ui->has_settings;
    pthread_mutex_unlock(&ui->lock);

    fputc('{', f);
    if (type) {
        fputs("\"type\":", f);
        lj_write_string(f, type);
        fputc(',', f);
    }
    fputs("\"version\":", f);
    lj_write_string(f, ui->version);
    fputs(",\"state\":", f);
    lj_write_string(f, state_name(ui));
    fputs(",\"settings\":", f);
    if (!has)
        fputs("null", f);
    else {
        fprintf(f, "{\"event\":%s,\"health\":%s,\"data\":%s,\"sources\":[", s.event ? "true" : "false",
                s.health ? "true" : "false", s.data ? "true" : "false");
        for (int i = 0; i < s.n_sources; i++) {
            if (i)
                fputc(',', f);
            lj_write_string(f, s.sources[i]);
        }
        fputs("],\"minEventLevel\":", f);
        lj_write_string(f, lg_level_name(s.min_level));
        fputs(",\"format\":", f);
        lj_write_string(f, s.format == LG_FORMAT_TEXT ? "text" : "json");
        fputs(",\"file\":", f);
        lj_write_string(f, s.file);
        fprintf(f, ",\"historySize\":%u}", s.history);
    }
    fprintf(f, ",\"records\":%u,\"lastSeq\":%llu,\"series\":%u}", lg_history_count(ui->history),
            (unsigned long long)lg_history_last_seq(ui->history), lg_history_series_count(ui->history));
}

/* A record of GET /api/records: {"seq": n, "record": {…}}. */
typedef struct record_list {
    FILE *f;
    bool first;
} record_list;

static void write_entry(void *user_data, uint64_t seq, const char *json) {
    record_list *l = user_data;
    fprintf(l->f, "%s{\"seq\":%llu,\"record\":%s}", l->first ? "" : ",", (unsigned long long)seq, json);
    l->first = false;
}

static void write_number(FILE *f, double x) {
    if (isfinite(x))
        fprintf(f, "%.17g", x);
    else
        fputs("null", f);
}

static void write_series(void *user_data, const lg_series_view *s) {
    record_list *l = user_data;
    FILE *f = l->f;
    fputs(l->first ? "{\"resource\":" : ",{\"resource\":", f);
    l->first = false;
    lj_write_string(f, lg_resource_name(s->resource));
    fputs(",\"app\":", f);
    lj_write_string(f, s->app);
    fputs(",\"source\":", f);
    lj_write_string(f, s->source);
    fputs(",\"filter\":", f);
    lj_write_string(f, s->filter);
    fputs(",\"field\":", f);
    lj_write_string(f, s->field);
    fputs(",\"points\":[", f);
    for (unsigned i = 0; i < s->n; i++) {
        fprintf(f, "%s[%lld,", i ? "," : "", (long long)s->points[i].time_ms);
        write_number(f, s->points[i].value);
        fputc(']', f);
    }
    fputs("]}", f);
}

/* ---- routes -------------------------------------------------------------- */

static void respond_buf(lh_conn *c, buf *b) {
    if (!buf_close(b)) {
        lh_error(c, 500, "out of memory", NULL);
        return;
    }
    lh_respond(c, 200, "application/json", b->data, b->len, NULL);
    free(b->data);
}

static bool parse_uint(const char *s, uint64_t max, uint64_t *out) {
    if (!*s || strlen(s) > 20)
        return false;
    uint64_t v = 0;
    for (; *s; s++) {
        if (*s < '0' || *s > '9')
            return false;
        unsigned d = (unsigned)(*s - '0');
        if (v > (max - d) / 10)
            return false;
        v = v * 10 + d;
    }
    *out = v;
    return true;
}

/* GET /api/records?limit&after&resource&level&q */
static void get_records(lg_ui *ui, lh_conn *c, const char *query) {
    lg_query q = {.resource = -1, .max_level = -1, .text = NULL, .after = 0, .limit = DEFAULT_LIMIT};
    char name[16], value[MAX_TEXT + 1], text[MAX_TEXT + 1] = "";
    const char *p = query;
    int rc;
    while ((rc = lh_query_next(&p, name, sizeof name, value, sizeof value)) == 1) {
        uint64_t n;
        if (strcmp(name, "limit") == 0) {
            if (!parse_uint(value, LG_HISTORY_MAX, &n) || n == 0) {
                lh_error(c, 400, "limit: 1 … 10000", NULL);
                return;
            }
            q.limit = (unsigned)n;
        } else if (strcmp(name, "after") == 0) {
            if (!parse_uint(value, UINT64_MAX, &n)) {
                lh_error(c, 400, "after: a seq", NULL);
                return;
            }
            q.after = n;
        } else if (strcmp(name, "resource") == 0) {
            if (strcmp(value, "Event") == 0)
                q.resource = LG_EVENT;
            else if (strcmp(value, "Health") == 0)
                q.resource = LG_HEALTH;
            else if (strcmp(value, "Data") == 0)
                q.resource = LG_DATA;
            else {
                lh_error(c, 400, "resource: Event, Health or Data", NULL);
                return;
            }
        } else if (strcmp(name, "level") == 0) {
            q.max_level = lg_level_parse(value);
            if (q.max_level < 0) {
                lh_error(c, 400, "level: a Syslog severity name", NULL);
                return;
            }
        } else if (strcmp(name, "q") == 0) {
            memcpy(text, value, sizeof text);
            q.text = text;
        } else {
            lh_error(c, 400, "unknown parameter", NULL);
            return;
        }
    }
    if (rc < 0) {
        lh_error(c, 400, "invalid query", NULL);
        return;
    }
    buf b;
    if (!buf_open(&b)) {
        lh_error(c, 500, "out of memory", NULL);
        return;
    }
    record_list l = {b.f, true};
    fputs("{\"records\":[", b.f);
    uint64_t last = lg_history_query(ui->history, &q, write_entry, &l);
    fprintf(b.f, "],\"lastSeq\":%llu}", (unsigned long long)last);
    respond_buf(c, &b);
}

static void get_series(lg_ui *ui, lh_conn *c) {
    buf b;
    if (!buf_open(&b)) {
        lh_error(c, 500, "out of memory", NULL);
        return;
    }
    record_list l = {b.f, true};
    fputs("{\"series\":[", b.f);
    lg_history_series_each(ui->history, write_series, &l);
    fputs("]}", b.f);
    respond_buf(c, &b);
}

static void get_status(lg_ui *ui, lh_conn *c) {
    buf b;
    if (!buf_open(&b)) {
        lh_error(c, 500, "out of memory", NULL);
        return;
    }
    write_status(ui, b.f, NULL);
    respond_buf(c, &b);
}

/* The records of an event stream: data: {"type":"record",…} lines. */
typedef struct stream_batch {
    FILE *f;
} stream_batch;

static void write_record_event(void *user_data, uint64_t seq, const char *json) {
    stream_batch *s = user_data;
    fprintf(s->f, "data: {\"type\":\"record\",\"seq\":%llu,\"record\":%s}\n\n", (unsigned long long)seq,
            json);
}

static bool send_status(lg_ui *ui, lh_conn *c) {
    buf b;
    if (!buf_open(&b))
        return false;
    fputs("data: ", b.f);
    write_status(ui, b.f, "status");
    fputs("\n\n", b.f);
    if (!buf_close(&b))
        return false;
    bool ok = lh_stream_write(c, b.data, b.len);
    free(b.data);
    return ok;
}

/* GET /api/events: the status first, then every new record, a reset if
 * records were missed, the status when the settings or the state change,
 * a keep-alive comment after keepalive_ms without an event. */
static void get_events(lg_ui *ui, lh_conn *c) {
    if (!lh_stream_begin(c))
        return;
    uint64_t version = settings_version(ui);
    const char *state = state_name(ui);
    uint64_t last = lg_history_last_seq(ui->history);
    if (!send_status(ui, c))
        return;
    int64_t written = mono_ms();
    while (lh_stream_alive(c)) {
        uint64_t newest = lg_history_wait(ui->history, last, TICK_MS);
        while (newest > last) {
            buf b;
            if (!buf_open(&b))
                return;
            stream_batch s = {b.f};
            bool missed;
            uint64_t upto = lg_history_since(ui->history, last, BATCH, write_record_event, &s, &missed);
            if (missed)
                fprintf(b.f, "data: {\"type\":\"reset\",\"lastSeq\":%llu}\n\n", (unsigned long long)upto);
            last = upto;
            if (!buf_close(&b))
                return;
            bool ok = b.len == 0 || lh_stream_write(c, b.data, b.len);
            free(b.data);
            if (!ok)
                return;
            written = mono_ms();
            newest = lg_history_last_seq(ui->history);
        }
        uint64_t v = settings_version(ui);
        const char *st = state_name(ui);
        if (v != version || strcmp(st, state) != 0) {
            version = v;
            state = st;
            if (!send_status(ui, c))
                return;
            written = mono_ms();
        }
        if (mono_ms() - written >= (int64_t)ui->keepalive_ms) {
            static const char alive[] = ": keep-alive\n\n";
            if (!lh_stream_write(c, alive, sizeof alive - 1))
                return;
            written = mono_ms();
        }
    }
}

static const lg_asset *asset(const char *path) {
    if (strcmp(path, "/") == 0)
        path = "/index.html";
    for (size_t i = 0; i < lg_ui_asset_count; i++)
        if (strcmp(lg_ui_assets[i].path, path) == 0)
            return &lg_ui_assets[i];
    return NULL;
}

static void handle(void *user_data, lh_conn *c, const lh_request *r) {
    lg_ui *ui = user_data;
    const lg_asset *a = asset(r->path);
    bool api = strcmp(r->path, "/api/status") == 0 || strcmp(r->path, "/api/records") == 0 ||
               strcmp(r->path, "/api/series") == 0 || strcmp(r->path, "/api/events") == 0;
    if (!a && !api) {
        lh_error(c, 404, "not found", NULL);
        return;
    }
    if (strcmp(r->method, "GET") != 0) {
        lh_error(c, 405, "method not allowed", "Allow: GET\r\n");
        return;
    }
    if (a)
        lh_respond(c, 200, a->content_type, a->data, a->size, NULL);
    else if (strcmp(r->path, "/api/records") == 0)
        get_records(ui, c, r->query);
    else if (r->query[0])
        lh_error(c, 400, "no parameters", NULL);
    else if (strcmp(r->path, "/api/status") == 0)
        get_status(ui, c);
    else if (strcmp(r->path, "/api/series") == 0)
        get_series(ui, c);
    else
        get_events(ui, c);
}

bool lg_ui_start(lg_ui *ui, lg_history *h, const char *version, unsigned keepalive_ms,
                 lg_ui_state_fn state, void *user_data) {
    ui->history = h;
    ui->version = version;
    ui->keepalive_ms = keepalive_ms ? keepalive_ms : LG_UI_KEEPALIVE_MS;
    ui->state = state;
    ui->user_data = user_data;
    return lh_start(ui->srv, handle, ui);
}

void lg_ui_stop(lg_ui *ui) {
    if (!ui)
        return;
    lh_stop(ui->srv);
    pthread_mutex_destroy(&ui->lock);
    free(ui);
}
