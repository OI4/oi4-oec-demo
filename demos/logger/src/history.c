#include "history.h"

#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "json.h"

typedef struct record {
    uint64_t seq;
    char *json;
    lg_resource resource;
    int level;
} record;

typedef struct series {
    lg_resource resource;
    char *app, *source, *filter, *field;
    lg_point points[LG_SERIES_POINTS];
    unsigned start, n;
} series;

struct lg_history {
    pthread_mutex_t lock;
    pthread_cond_t added;
    record *ring;
    unsigned cap, start, count;
    uint64_t next_seq;
    series *series[LG_MAX_SERIES];
    unsigned n_series;
    uint64_t dropped;
};

lg_history *lg_history_new(unsigned capacity) {
    if (capacity < LG_HISTORY_MIN || capacity > LG_HISTORY_MAX)
        return NULL;
    lg_history *h = calloc(1, sizeof *h);
    if (!h)
        return NULL;
    h->ring = calloc(capacity, sizeof *h->ring);
    if (!h->ring || pthread_mutex_init(&h->lock, NULL) != 0) {
        free(h->ring);
        free(h);
        return NULL;
    }
    if (pthread_cond_init(&h->added, NULL) != 0) {
        pthread_mutex_destroy(&h->lock);
        free(h->ring);
        free(h);
        return NULL;
    }
    h->cap = capacity;
    h->next_seq = 1;
    return h;
}

static void series_free(series *s) {
    free(s->app);
    free(s->source);
    free(s->filter);
    free(s->field);
    free(s);
}

void lg_history_free(lg_history *h) {
    if (!h)
        return;
    for (unsigned i = 0; i < h->count; i++)
        free(h->ring[(h->start + i) % h->cap].json);
    for (unsigned i = 0; i < h->n_series; i++)
        series_free(h->series[i]);
    free(h->ring);
    pthread_cond_destroy(&h->added);
    pthread_mutex_destroy(&h->lock);
    free(h);
}

bool lg_history_resize(lg_history *h, unsigned capacity) {
    if (capacity < LG_HISTORY_MIN || capacity > LG_HISTORY_MAX)
        return false;
    record *ring = calloc(capacity, sizeof *ring);
    if (!ring)
        return false;
    pthread_mutex_lock(&h->lock);
    unsigned drop = h->count > capacity ? h->count - capacity : 0;
    for (unsigned i = 0; i < h->count; i++) {
        record *r = &h->ring[(h->start + i) % h->cap];
        if (i < drop)
            free(r->json);
        else
            ring[i - drop] = *r;
    }
    free(h->ring);
    h->ring = ring;
    h->cap = capacity;
    h->start = 0;
    h->count -= drop;
    pthread_mutex_unlock(&h->lock);
    return true;
}

static bool same(const char *a, const char *b) {
    return (!a && !b) || (a && b && strcmp(a, b) == 0);
}

static char *dup(const char *s) {
    if (!s)
        return NULL;
    size_t n = strlen(s) + 1;
    char *d = malloc(n);
    if (d)
        memcpy(d, s, n);
    return d;
}

/* Under the lock. */
static void add_point(lg_history *h, const lg_msg *m, const char *field, double value) {
    series *s = NULL;
    for (unsigned i = 0; i < h->n_series && !s; i++) {
        series *c = h->series[i];
        if (c->resource == m->resource && strcmp(c->field, field) == 0 && same(c->app, m->app) &&
            same(c->source, m->source) && same(c->filter, m->filter))
            s = c;
    }
    if (!s) {
        if (h->n_series == LG_MAX_SERIES) {
            h->dropped++;
            return;
        }
        s = calloc(1, sizeof *s);
        if (!s)
            return;
        s->resource = m->resource;
        s->app = dup(m->app);
        s->source = dup(m->source);
        s->filter = dup(m->filter);
        s->field = dup(field);
        if ((m->app && !s->app) || (m->source && !s->source) || (m->filter && !s->filter) ||
            !s->field) {
            series_free(s);
            return;
        }
        h->series[h->n_series++] = s;
    }
    lg_point *p;
    if (s->n < LG_SERIES_POINTS)
        p = &s->points[(s->start + s->n++) % LG_SERIES_POINTS];
    else {
        p = &s->points[s->start];
        s->start = (s->start + 1) % LG_SERIES_POINTS;
    }
    p->time_ms = m->time_ms;
    p->value = value;
}

/* Under the lock: the numbers of the message. */
static void add_values(lg_history *h, const lg_msg *m) {
    lj_value v, key, val;
    lj_iter it;
    char field[128];
    double x;
    if (m->resource == LG_EVENT || !lj_parse(m->payload, &v))
        return;
    if (m->resource == LG_HEALTH) {
        if (lj_member(&v, "HealthScore", &val) && lj_number(&val, &x))
            add_point(h, m, "HealthScore", x);
        return;
    }
    if (!lj_object_begin(&v, &it))
        return;
    while (lj_object_next(&it, &key, &val))
        if (val.type == LJ_NUMBER && lj_number(&val, &x) &&
            lj_string(&key, field, sizeof field) != (size_t)-1)
            add_point(h, m, field, x);
}

void lg_history_add(lg_history *h, const lg_msg *m, int level) {
    char *json = NULL;
    size_t len = 0;
    FILE *f = open_memstream(&json, &len);
    if (!f)
        return;
    lg_write_record(f, LG_FORMAT_JSON, m, level);
    if (fclose(f) != 0 || !json) {
        free(json);
        return;
    }
    if (len && json[len - 1] == '\n')
        json[len - 1] = 0;

    pthread_mutex_lock(&h->lock);
    record *r;
    if (h->count < h->cap)
        r = &h->ring[(h->start + h->count++) % h->cap];
    else {
        r = &h->ring[h->start];
        free(r->json);
        h->start = (h->start + 1) % h->cap;
    }
    r->seq = h->next_seq++;
    r->json = json;
    r->resource = m->resource;
    r->level = m->resource == LG_EVENT ? level : -1;
    add_values(h, m);
    pthread_cond_broadcast(&h->added);
    pthread_mutex_unlock(&h->lock);
}

unsigned lg_history_count(lg_history *h) {
    pthread_mutex_lock(&h->lock);
    unsigned n = h->count;
    pthread_mutex_unlock(&h->lock);
    return n;
}

unsigned lg_history_series_count(lg_history *h) {
    pthread_mutex_lock(&h->lock);
    unsigned n = h->n_series;
    pthread_mutex_unlock(&h->lock);
    return n;
}

uint64_t lg_history_series_dropped(lg_history *h) {
    pthread_mutex_lock(&h->lock);
    uint64_t n = h->dropped;
    pthread_mutex_unlock(&h->lock);
    return n;
}

void lg_history_each(lg_history *h, lg_record_fn f, void *user_data) {
    pthread_mutex_lock(&h->lock);
    for (unsigned i = 0; i < h->count; i++) {
        const record *r = &h->ring[(h->start + i) % h->cap];
        f(user_data, r->seq, r->json);
    }
    pthread_mutex_unlock(&h->lock);
}

void lg_history_series_each(lg_history *h, lg_series_fn f, void *user_data) {
    lg_point points[LG_SERIES_POINTS];
    pthread_mutex_lock(&h->lock);
    for (unsigned i = 0; i < h->n_series; i++) {
        const series *s = h->series[i];
        for (unsigned k = 0; k < s->n; k++)
            points[k] = s->points[(s->start + k) % LG_SERIES_POINTS];
        lg_series_view v = {s->resource, s->app, s->source, s->filter, s->field, points, s->n};
        f(user_data, &v);
    }
    pthread_mutex_unlock(&h->lock);
}

uint64_t lg_history_last_seq(lg_history *h) {
    pthread_mutex_lock(&h->lock);
    uint64_t seq = h->next_seq - 1;
    pthread_mutex_unlock(&h->lock);
    return seq;
}

static int ascii_lower(int c) { return c >= 'A' && c <= 'Z' ? c + 32 : c; }

static bool contains(const char *hay, const char *needle) {
    size_t n = strlen(needle);
    for (; *hay; hay++) {
        size_t i = 0;
        while (i < n && hay[i] &&
               ascii_lower((unsigned char)hay[i]) == ascii_lower((unsigned char)needle[i]))
            i++;
        if (i == n)
            return true;
    }
    return n == 0;
}

static bool matches(const record *r, const lg_query *q) {
    return r->seq > q->after && (q->resource < 0 || (int)r->resource == q->resource) &&
           (q->max_level < 0 || (r->level >= 0 && r->level <= q->max_level)) &&
           (!q->text || !*q->text || contains(r->json, q->text));
}

uint64_t lg_history_query(lg_history *h, const lg_query *q, lg_record_fn f, void *user_data) {
    pthread_mutex_lock(&h->lock);
    /* the oldest of the newest limit matches */
    unsigned first = h->count, found = 0;
    for (unsigned i = h->count; i-- > 0 && found < q->limit;)
        if (matches(&h->ring[(h->start + i) % h->cap], q)) {
            first = i;
            found++;
        }
    for (unsigned i = first; i < h->count; i++) {
        const record *r = &h->ring[(h->start + i) % h->cap];
        if (matches(r, q))
            f(user_data, r->seq, r->json);
    }
    uint64_t last = h->next_seq - 1;
    pthread_mutex_unlock(&h->lock);
    return last;
}

uint64_t lg_history_since(lg_history *h, uint64_t after, unsigned max, lg_record_fn f, void *user_data,
                          bool *missed) {
    pthread_mutex_lock(&h->lock);
    uint64_t last = h->next_seq - 1;
    uint64_t oldest = h->count ? h->ring[h->start].seq : h->next_seq;
    *missed = last > after && oldest > after + 1;
    if (!*missed) {
        last = after;
        for (unsigned i = (unsigned)(after + 1 - oldest), sent = 0; i < h->count && sent < max; i++, sent++) {
            const record *r = &h->ring[(h->start + i) % h->cap];
            f(user_data, r->seq, r->json);
            last = r->seq;
        }
    }
    pthread_mutex_unlock(&h->lock);
    return last;
}

uint64_t lg_history_wait(lg_history *h, uint64_t after, unsigned timeout_ms) {
    struct timespec until;
    clock_gettime(CLOCK_REALTIME, &until);
    until.tv_sec += (time_t)(timeout_ms / 1000);
    until.tv_nsec += (long)(timeout_ms % 1000) * 1000000L;
    if (until.tv_nsec >= 1000000000L) {
        until.tv_sec++;
        until.tv_nsec -= 1000000000L;
    }
    pthread_mutex_lock(&h->lock);
    while (h->next_seq - 1 <= after)
        if (pthread_cond_timedwait(&h->added, &h->lock, &until) != 0)
            break;
    uint64_t last = h->next_seq - 1;
    pthread_mutex_unlock(&h->lock);
    return last;
}
