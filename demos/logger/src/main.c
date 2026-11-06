/* The logger demo D4 (ADR-0028, Messaging Level 2, Utility): subscribes to
 * the Events, the Health and optionally the Data of all participants,
 * writes one record per accepted DataSetMessage to standard output (and
 * optionally a file in /opt/oi4/app) and keeps a history for the Web UI.
 * Its own Config (context "logger") selects resources, sources, the
 * minimum Event level, the format, the file and the history size; Get and
 * Set by the SDK. OEC_ROOT (optional) is the root of the container
 * storage, "/" in a container (REQ-CNT-004). The Web UI (ui.c) listens on
 * OEC_UI_LISTEN (default 127.0.0.1:8781, empty: off); OEC_UI_KEEPALIVE_MS
 * shortens its SSE keep-alive for tests. Diagnostics go to standard
 * error. Runs until SIGINT/SIGTERM. */
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <time.h>

#include "core.h"
#include "history.h"
#include "oec.h"
#include "ui.h"

#define LOGGER_VERSION "0.1.0"
/* A file is rotated to <file>.1 at this size. */
#define ROTATE_SIZE (10L * 1024 * 1024)
#define PATH_LEN 4096

typedef struct logger {
    oec_app *app;
    oec_config *cfg;
    lg_settings s;
    oec_subscription *subs[3]; /* by lg_resource */
    lg_history *history;
    lg_ui *ui; /* NULL: no Web UI */
    char root[PATH_LEN];
    FILE *file;
    char path[PATH_LEN];
    long size;
    bool file_failed;
} logger;

static int64_t now_ms(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

/* An error of the logger itself: standard error and a Syslog event. */
static void report(logger *lg, const char *what, const char *detail) {
    char msg[512];
    snprintf(msg, sizeof msg, "%s: %s", what, detail);
    fprintf(stderr, "oec-logger: %s\n", msg);
    oec_app_syslog(lg->app, OEC_SYSLOG_ERROR, msg);
}

/* ---- file ---------------------------------------------------------------- */

static void make_dirs(const char *root) {
    static const char *const parts[] = {"/opt", "/opt/oi4", "/opt/oi4/app"};
    char dir[PATH_LEN];
    for (size_t i = 0; i < sizeof parts / sizeof parts[0]; i++) {
        int n = snprintf(dir, sizeof dir, "%s%s", root, parts[i]);
        if (n < 0 || (size_t)n >= sizeof dir)
            return;
        mkdir(dir, 0750);
    }
}

static FILE *open_file(const logger *lg, const char *name, char *path, long *size) {
    make_dirs(lg->root);
    int n = snprintf(path, PATH_LEN, "%s/opt/oi4/app/%s", lg->root, name);
    if (n < 0 || n >= PATH_LEN) {
        errno = ENAMETOOLONG;
        return NULL;
    }
    FILE *f = fopen(path, "a");
    if (!f)
        return NULL;
    if (fseek(f, 0, SEEK_END) != 0 || (*size = ftell(f)) < 0) {
        fclose(f);
        return NULL;
    }
    return f;
}

static void write_file(logger *lg, const char *rec, size_t len) {
    if (!lg->file)
        return;
    if (lg->size > 0 && lg->size + (long)len > ROTATE_SIZE) {
        char old[PATH_LEN + 2];
        snprintf(old, sizeof old, "%s.1", lg->path);
        fclose(lg->file);
        rename(lg->path, old);
        lg->file = fopen(lg->path, "a");
        lg->size = 0;
        if (!lg->file) {
            report(lg, "cannot reopen the log file after rotation", strerror(errno));
            lg->file_failed = true;
            return;
        }
    }
    if (fwrite(rec, 1, len, lg->file) != len || fflush(lg->file) != 0) {
        /* Reported once until a write succeeds: the error event is logged
         * as well. */
        if (!lg->file_failed)
            report(lg, "cannot write the log file", strerror(errno));
        lg->file_failed = true;
        return;
    }
    lg->file_failed = false;
    lg->size += (long)len;
}

/* ---- messages ------------------------------------------------------------ */

static void on_msg(void *user_data, const oec_received *r) {
    logger *lg = user_data;
    lg_msg m = {
        .service_type = r->service_type,
        .app = r->app,
        .source = r->source,
        .filter = r->filter,
        .payload = r->payload_json,
        .time_ms = now_ms(),
    };
    switch (r->resource) {
    case OEC_RES_EVENT: m.resource = LG_EVENT; break;
    case OEC_RES_HEALTH: m.resource = LG_HEALTH; break;
    case OEC_RES_DATA: m.resource = LG_DATA; break;
    default: return;
    }
    int level;
    if (!lg_accept(&lg->s, &m, &level))
        return;

    char *rec = NULL;
    size_t len = 0;
    FILE *f = open_memstream(&rec, &len);
    if (!f)
        return;
    lg_write_record(f, lg->s.format, &m, level);
    if (fclose(f) == 0 && rec) {
        fwrite(rec, 1, len, stdout);
        fflush(stdout);
        write_file(lg, rec, len);
    }
    free(rec);
    lg_history_add(lg->history, &m, level);
}

/* ---- settings ------------------------------------------------------------ */

static const oec_resource resources[3] = {OEC_RES_EVENT, OEC_RES_HEALTH, OEC_RES_DATA};

static bool wanted(const lg_settings *s, int r) {
    return r == LG_EVENT ? s->event : r == LG_HEALTH ? s->health : s->data;
}

/* Applies next: file and history first (they can fail, then nothing
 * changes), then the subscriptions. */
static bool apply(logger *lg, const lg_settings *next) {
    FILE *file = lg->file;
    char path[PATH_LEN];
    long size = lg->size;
    bool new_file = strcmp(next->file, lg->s.file) != 0 || (next->file[0] && !lg->file);

    if (new_file && next->file[0]) {
        file = open_file(lg, next->file, path, &size);
        if (!file) {
            fprintf(stderr, "oec-logger: cannot open %s: %s\n", path, strerror(errno));
            return false;
        }
    }
    if (next->history != lg->s.history && !lg_history_resize(lg->history, next->history)) {
        if (new_file && file)
            fclose(file);
        fprintf(stderr, "oec-logger: no memory for a history of %u records\n", next->history);
        return false;
    }
    if (new_file) {
        if (lg->file)
            fclose(lg->file);
        lg->file = next->file[0] ? file : NULL;
        if (lg->file)
            memcpy(lg->path, path, sizeof path);
        lg->size = size;
        lg->file_failed = false;
    }
    lg->s = *next;
    for (int r = 0; r < 3; r++) {
        if (wanted(next, r) && !lg->subs[r]) {
            if (oec_subscribe(lg->app, resources[r], OEC_ST_ANY, NULL, NULL, NULL, on_msg, lg,
                              &lg->subs[r]) != OEC_OK)
                report(lg, "cannot subscribe", oec_last_error());
        } else if (!wanted(next, r) && lg->subs[r]) {
            oec_subscription_free(lg->subs[r]);
            lg->subs[r] = NULL;
        }
    }
    if (lg->ui)
        lg_ui_settings(lg->ui, next);
    return true;
}

/* Set Config (REQ-RES-CONFIG-023): the complete, validated proposal. */
static oec_status_code on_set_config(void *user_data, const char *json) {
    logger *lg = user_data;
    lg_settings next;
    char err[256];
    if (!lg_settings_from_proposal(json, &next, err, sizeof err)) {
        fprintf(stderr, "oec-logger: Set Config refused: %s\n", err);
        return OEC_BAD_INVALID_ARGUMENT;
    }
    if (!apply(lg, &next))
        return OEC_BAD_INVALID_ARGUMENT;
    fprintf(stderr, "oec-logger: Config applied\n");
    return OEC_GOOD;
}

/* The persisted values (or the defaults) at start. */
static void load_settings(logger *lg, lg_settings *s) {
    char err[256];
    lg_settings_default(s);
    for (int i = 0; lg_config_names[i]; i++) {
        char *value = NULL;
        if (oec_config_get(lg->cfg, LG_CONFIG_GROUP, lg_config_names[i], &value) != OEC_OK)
            continue;
        if (value && !lg_settings_set(s, lg_config_names[i], value, err, sizeof err))
            fprintf(stderr, "oec-logger: stored value ignored, %s\n", err);
        oec_string_free(value);
    }
}

/* ---- main ---------------------------------------------------------------- */

static int fail(const char *what) {
    fprintf(stderr, "oec-logger: %s: %s\n", what, oec_last_error());
    return 1;
}

static oec_app *build(const char *root) {
    oec_product *product = NULL;
    oec_builder *builder = NULL;
    oec_app *app = NULL;
    static const char *const mam[][2] = {
        {"Manufacturer", "Open Industry 4.0 Alliance"},
        {"Description", "OEC demo logger: logs Events, Health and Data of all participants"},
        {"SoftwareRevision", LOGGER_VERSION},
    };
    if (oec_product_new("oi4.org", "OEC Logger", "OEC-LOGGER", &product) != OEC_OK) {
        fail("product");
        return NULL;
    }
    for (size_t i = 0; i < sizeof mam / sizeof mam[0]; i++) {
        if (oec_product_set(product, mam[i][0], mam[i][1]) != OEC_OK) {
            fail("product");
            oec_product_free(product);
            return NULL;
        }
    }
    oec_status st = oec_builder_new(OEC_LEVEL_2, OEC_ST_UTILITY, product, &builder);
    oec_product_free(product);
    if (st != OEC_OK) {
        fail("builder");
        return NULL;
    }
    if (oec_builder_container_storage(builder, root) != OEC_OK) {
        fail("container storage");
        oec_builder_free(builder);
        return NULL;
    }
    if (oec_builder_build(builder, &app) != OEC_OK) {
        fail("build");
        return NULL;
    }
    return app;
}

static const char *app_state(void *user_data) {
    const logger *lg = user_data;
    oec_connection_state st;
    if (oec_app_state(lg->app, &st) != OEC_OK)
        return "unknown";
    switch (st) {
    case OEC_STATE_CONNECTING: return "connecting";
    case OEC_STATE_ONLINE: return "online";
    case OEC_STATE_RECONNECTING: return "reconnecting";
    case OEC_STATE_CLOSED: return "closed";
    default: return "unknown";
    }
}

/* OEC_UI_LISTEN and OEC_UI_KEEPALIVE_MS: binds the Web UI before the app
 * starts (a wrong address stops the logger). false on an error. */
static bool listen_ui(logger *lg, unsigned *keepalive_ms) {
    const char *ka = getenv("OEC_UI_KEEPALIVE_MS");
    *keepalive_ms = LG_UI_KEEPALIVE_MS;
    if (ka) {
        char *end;
        errno = 0;
        unsigned long v = strtoul(ka, &end, 10);
        if (errno || end == ka || *end || v < 1 || v > LG_UI_KEEPALIVE_MS) {
            fprintf(stderr, "oec-logger: Web UI: OEC_UI_KEEPALIVE_MS %s is no number 1 … %d\n", ka,
                    LG_UI_KEEPALIVE_MS);
            return false;
        }
        *keepalive_ms = (unsigned)v;
    }
    const char *address = getenv("OEC_UI_LISTEN");
    if (!address)
        address = LG_UI_DEFAULT_LISTEN;
    if (!*address) {
        fprintf(stderr, "oec-logger: Web UI off\n");
        return true;
    }
    char err[256];
    lg->ui = lg_ui_listen(address, err, sizeof err);
    if (!lg->ui) {
        fprintf(stderr, "oec-logger: Web UI: %s\n", err);
        return false;
    }
    return true;
}

int main(void) {
    static logger lg;
    const char *root = getenv("OEC_ROOT");
    if (!root || !*root)
        root = "/";
    /* "/" + "/opt/…" would give "//opt/…": no trailing slash */
    snprintf(lg.root, sizeof lg.root, "%s", root);
    size_t n = strlen(lg.root);
    while (n > 0 && lg.root[n - 1] == '/')
        lg.root[--n] = 0;

    unsigned keepalive_ms;
    if (!listen_ui(&lg, &keepalive_ms))
        return 1;
    if (!(lg.app = build(root))) {
        lg_ui_stop(lg.ui);
        return 1;
    }
    lg_settings next;
    int rc = 1;
    if (oec_asset_add_config(oec_app_this(lg.app), LG_CONFIG_CONTEXT, lg_config_definition(),
                             &lg.cfg) != OEC_OK) {
        fail("config");
        goto out;
    }
    load_settings(&lg, &next);
    lg_settings_default(&lg.s);
    lg.s.event = lg.s.health = false; /* apply subscribes */
    lg.history = lg_history_new(next.history);
    if (!lg.history) {
        fprintf(stderr, "oec-logger: no memory for the history\n");
        goto out;
    }
    lg.s.history = next.history;
    if (!apply(&lg, &next)) {
        /* e.g. the stored file cannot be opened: log without it */
        next.file[0] = 0;
        if (!apply(&lg, &next))
            goto out;
    }
    if (oec_config_on_set(lg.cfg, on_set_config, &lg) != OEC_OK) {
        fail("config");
        goto out;
    }
    if (lg.ui) {
        if (!lg_ui_start(lg.ui, lg.history, LOGGER_VERSION, keepalive_ms, app_state, &lg)) {
            fprintf(stderr, "oec-logger: Web UI: no thread\n");
            goto out;
        }
        lg_ui_settings(lg.ui, &lg.s);
        fprintf(stderr, "oec-logger: Web UI on http://%s/\n", lg_ui_address(lg.ui));
    }
    fprintf(stderr, "oec-logger %s: running\n", LOGGER_VERSION);
    rc = oec_app_run(lg.app) == OEC_OK ? 0 : fail("run");
out:
    lg_ui_stop(lg.ui); /* before the app: the UI reads its state */
    for (int r = 0; r < 3; r++)
        oec_subscription_free(lg.subs[r]);
    oec_app_free(lg.app);
    if (lg.file)
        fclose(lg.file);
    lg_history_free(lg.history);
    return rc;
}
