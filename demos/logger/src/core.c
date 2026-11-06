#include "core.h"

#include <math.h>
#include <string.h>
#include <time.h>

#include "json.h"

const char *const lg_config_names[] = {
    "event", "health", "data", "sources", "minEventLevel", "format", "file", "historySize", NULL,
};

void lg_settings_default(lg_settings *s) {
    memset(s, 0, sizeof *s);
    s->event = true;
    s->health = true;
    s->data = false;
    s->min_level = 6;
    s->format = LG_FORMAT_JSON;
    s->history = 1000;
}

#define TEXT(t) "{\"Locale\":\"en-US\",\"Text\":\"" t "\"}"
#define BOOL_OBJ(name, label, def, desc)                                                    \
    "\"" name "\":{\"Type\":\"Boolean\",\"Value\":\"" def "\",\"DefaultValue\":\"" def      \
    "\",\"Name\":" TEXT(label) ",\"Description\":" TEXT(desc) "}"

static const char definition[] =
    "{\"Context\":{\"Name\":" TEXT("Logger") ",\"Description\":" TEXT(
        "What the logger subscribes to, filters and writes") "},"
    "\"" LG_CONFIG_GROUP "\":{\"Name\":" TEXT("Common") ","
    BOOL_OBJ("event", "Log Events", "true", "Subscribe to the Event resource of all applications") ","
    BOOL_OBJ("health", "Log Health", "true", "Subscribe to the Health resource of all applications") ","
    BOOL_OBJ("data", "Log Data", "false", "Subscribe to the Data resource of all applications") ","
    "\"sources\":{\"Type\":\"String\",\"Value\":\"\",\"DefaultValue\":\"\",\"Name\":" TEXT(
        "Sources") ",\"Description\":" TEXT(
        "Identification Links (unencoded, separated by white space) whose AppId or Source is "
        "logged, each also for the levels below it; empty: all") ","
    "\"Validation\":{\"Length\":2048}},"
    "\"minEventLevel\":{\"Type\":\"String\",\"Value\":\"Informational\",\"DefaultValue\":"
    "\"Informational\",\"Name\":" TEXT("Minimum Event level") ",\"Description\":" TEXT(
        "Events of a lower severity are not logged") ","
    "\"Validation\":{\"Values\":[\"Emergency\",\"Alert\",\"Critical\",\"Error\",\"Warning\","
    "\"Notice\",\"Informational\",\"Debug\"]}},"
    "\"format\":{\"Type\":\"String\",\"Value\":\"json\",\"DefaultValue\":\"json\",\"Name\":" TEXT(
        "Output format") ",\"Description\":" TEXT(
        "One JSON object or one text line per record") ","
    "\"Validation\":{\"Values\":[\"json\",\"text\"]}},"
    "\"file\":{\"Type\":\"String\",\"Value\":\"\",\"DefaultValue\":\"\",\"Name\":" TEXT(
        "Output file") ",\"Description\":" TEXT(
        "Name of a file in /opt/oi4/app the records are appended to as well; empty: standard "
        "output only") ","
    "\"Validation\":{\"Length\":64,\"Pattern\":\"^[A-Za-z0-9_][A-Za-z0-9._-]*$\"}},"
    "\"historySize\":{\"Type\":\"Number\",\"Value\":\"1000\",\"DefaultValue\":\"1000\",\"Name\":" TEXT(
        "History size") ",\"Description\":" TEXT(
        "Number of the last records kept for the Web UI") ","
    "\"Validation\":{\"Min\":10,\"Max\":10000}}"
    "}}";

const char *lg_config_definition(void) { return definition; }

static void set_err(char *err, size_t len, const char *name, const char *why) {
    if (err && len)
        snprintf(err, len, "%s: %s", name, why);
}

bool lg_file_name_ok(const char *name) {
    size_t n = strlen(name);
    if (n == 0 || n >= LG_FILE_MAX || name[0] == '.' || name[0] == '-')
        return false;
    for (size_t i = 0; i < n; i++) {
        char c = name[i];
        if (!((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
              c == '.' || c == '_' || c == '-'))
            return false;
    }
    return true;
}

static bool space(char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\r'; }

static bool set_sources(lg_settings *s, const char *value, char *err, size_t errlen) {
    char list[LG_MAX_SOURCES][LG_SOURCE_MAX];
    int n = 0;
    const char *p = value;
    for (;;) {
        while (space(*p))
            p++;
        if (!*p)
            break;
        const char *start = p;
        while (*p && !space(*p))
            p++;
        size_t len = (size_t)(p - start);
        if (n == LG_MAX_SOURCES) {
            set_err(err, errlen, "sources", "more than 32 entries");
            return false;
        }
        if (len >= LG_SOURCE_MAX) {
            set_err(err, errlen, "sources", "an entry is longer than 255 octets");
            return false;
        }
        memcpy(list[n], start, len);
        list[n][len] = 0;
        n++;
    }
    memcpy(s->sources, list, sizeof list[0] * (size_t)n);
    s->n_sources = n;
    return true;
}

bool lg_settings_set(lg_settings *s, const char *name, const char *value, char *err,
                     size_t errlen) {
    lg_settings d;
    lg_settings_default(&d);
    bool unset = value[0] == 0;
    bool *flag = strcmp(name, "event") == 0    ? &s->event
                 : strcmp(name, "health") == 0 ? &s->health
                 : strcmp(name, "data") == 0   ? &s->data
                                               : NULL;
    if (flag) {
        if (unset)
            *flag = strcmp(name, "data") != 0;
        else if (strcmp(value, "true") == 0)
            *flag = true;
        else if (strcmp(value, "false") == 0)
            *flag = false;
        else {
            set_err(err, errlen, name, "is not true or false");
            return false;
        }
        return true;
    }
    if (strcmp(name, "sources") == 0)
        return set_sources(s, value, err, errlen);
    if (strcmp(name, "minEventLevel") == 0) {
        int level = unset ? d.min_level : lg_level_parse(value);
        if (level < 0) {
            set_err(err, errlen, name, "is no Syslog severity name");
            return false;
        }
        s->min_level = level;
        return true;
    }
    if (strcmp(name, "format") == 0) {
        if (unset || strcmp(value, "json") == 0)
            s->format = LG_FORMAT_JSON;
        else if (strcmp(value, "text") == 0)
            s->format = LG_FORMAT_TEXT;
        else {
            set_err(err, errlen, name, "is not json or text");
            return false;
        }
        return true;
    }
    if (strcmp(name, "file") == 0) {
        if (!unset && !lg_file_name_ok(value)) {
            set_err(err, errlen, name, "is no plain file name");
            return false;
        }
        snprintf(s->file, sizeof s->file, "%s", value);
        return true;
    }
    if (strcmp(name, "historySize") == 0) {
        if (unset) {
            s->history = d.history;
            return true;
        }
        lj_value v;
        double x;
        if (!lj_parse(value, &v) || v.type != LJ_NUMBER || v.n != strlen(value) ||
            !lj_number(&v, &x) || x != floor(x) || x < LG_HISTORY_MIN || x > LG_HISTORY_MAX) {
            set_err(err, errlen, name, "is no whole number in 10 … 10000");
            return false;
        }
        s->history = (unsigned)x;
        return true;
    }
    set_err(err, errlen, name, "is unknown");
    return false;
}

bool lg_settings_from_proposal(const char *json, lg_settings *s, char *err, size_t errlen) {
    lj_value root, group, key, val;
    lj_iter it;
    char name[64], value[4 * LG_SOURCES_MAX + 1];
    lg_settings next;

    lg_settings_default(&next);
    if (!lj_parse(json, &root) || root.type != LJ_OBJECT) {
        set_err(err, errlen, "proposal", "is no JSON object");
        return false;
    }
    if (lj_member(&root, LG_CONFIG_GROUP, &group)) {
        if (!lj_object_begin(&group, &it)) {
            set_err(err, errlen, LG_CONFIG_GROUP, "is no object");
            return false;
        }
        while (lj_object_next(&it, &key, &val)) {
            if (lj_string(&key, name, sizeof name) == (size_t)-1)
                continue;
            bool known = false;
            for (int i = 0; lg_config_names[i]; i++)
                known |= strcmp(lg_config_names[i], name) == 0;
            if (!known)
                continue;
            if (lj_string(&val, value, sizeof value) == (size_t)-1) {
                set_err(err, errlen, name, "is no string of at most 2048 characters");
                return false;
            }
            if (!lg_settings_set(&next, name, value, err, errlen))
                return false;
        }
    }
    *s = next;
    return true;
}

static const char *const level_names[] = {
    "Emergency", "Alert", "Critical", "Error", "Warning", "Notice", "Informational", "Debug",
};

const char *lg_level_name(int level) {
    return level >= 0 && level <= 7 ? level_names[level] : NULL;
}

int lg_level_parse(const char *name) {
    for (int i = 0; i <= 7; i++)
        if (strcmp(level_names[i], name) == 0)
            return i;
    return -1;
}

int lg_event_level(const char *payload_json) {
    lj_value v, cat, num;
    double n;
    if (!lj_parse(payload_json, &v) || !lj_member(&v, "Category", &cat) ||
        !lj_member(&v, "Number", &num) || !lj_number(&num, &n) || n < 0 || n != floor(n))
        return 5;
    if (lj_string_eq(&cat, "CAT_SYSLOG_0"))
        return (int)fmod(n, 8.0);
    if (lj_string_eq(&cat, "CAT_STATUS_1") && n <= 4294967295.0) {
        switch ((unsigned long)n >> 30) {
        case 0: return 6;
        case 1: return 4;
        default: return 3;
        }
    }
    return 5;
}

const char *lg_resource_name(lg_resource r) {
    switch (r) {
    case LG_EVENT: return "Event";
    case LG_HEALTH: return "Health";
    default: return "Data";
    }
}

static bool il_match(const char *entry, const char *il) {
    if (!il)
        return false;
    size_t n = strlen(entry);
    return strncmp(entry, il, n) == 0 && (il[n] == 0 || il[n] == '/');
}

bool lg_source_match(const lg_settings *s, const char *app, const char *source) {
    if (s->n_sources == 0)
        return true;
    for (int i = 0; i < s->n_sources; i++)
        if (il_match(s->sources[i], app) || il_match(s->sources[i], source))
            return true;
    return false;
}

bool lg_accept(const lg_settings *s, const lg_msg *m, int *level) {
    *level = -1;
    switch (m->resource) {
    case LG_EVENT:
        if (!s->event)
            return false;
        *level = lg_event_level(m->payload);
        if (*level > s->min_level)
            return false;
        break;
    case LG_HEALTH:
        if (!s->health)
            return false;
        break;
    case LG_DATA:
        if (!s->data)
            return false;
        break;
    }
    return lg_source_match(s, m->app, m->source);
}

void lg_format_time(int64_t time_ms, char *buf, size_t len) {
    time_t secs = (time_t)(time_ms / 1000);
    struct tm tm;
    gmtime_r(&secs, &tm);
    snprintf(buf, len, "%04d-%02d-%02dT%02d:%02d:%02d.%03dZ", tm.tm_year + 1900, tm.tm_mon + 1,
             tm.tm_mday, tm.tm_hour, tm.tm_min, tm.tm_sec, (int)(time_ms % 1000));
}

static void put_text(FILE *f, const char *s) {
    if (!s) {
        fputc('-', f);
        return;
    }
    for (; *s; s++)
        fputc(*s == '\n' || *s == '\r' ? ' ' : *s, f);
}

void lg_write_record(FILE *f, lg_format format, const lg_msg *m, int level) {
    char t[32];
    lg_format_time(m->time_ms, t, sizeof t);
    const char *lname = m->resource == LG_EVENT ? lg_level_name(level) : NULL;
    if (format == LG_FORMAT_TEXT) {
        fprintf(f, "%s %s %s ", t, lg_resource_name(m->resource), lname ? lname : "-");
        put_text(f, m->service_type);
        fputc(' ', f);
        put_text(f, m->app);
        fputc(' ', f);
        put_text(f, m->source);
        fputc(' ', f);
        put_text(f, m->filter);
        fputc(' ', f);
        put_text(f, m->payload);
        fputc('\n', f);
        return;
    }
    lj_value v;
    fprintf(f, "{\"time\":\"%s\",\"resource\":\"%s\",\"serviceType\":", t,
            lg_resource_name(m->resource));
    lj_write_string(f, m->service_type);
    fputs(",\"app\":", f);
    lj_write_string(f, m->app);
    fputs(",\"source\":", f);
    lj_write_string(f, m->source);
    fputs(",\"filter\":", f);
    lj_write_string(f, m->filter);
    if (lname)
        fprintf(f, ",\"level\":\"%s\"", lname);
    fputs(",\"payload\":", f);
    if (lj_parse(m->payload, &v) && !memchr(v.p, '\n', v.n) && !memchr(v.p, '\r', v.n))
        fwrite(v.p, 1, v.n, f);
    else if (lj_parse(m->payload, &v)) {
        /* white space between tokens only: the record stays one line */
        for (size_t i = 0; i < v.n; i++)
            fputc(v.p[i] == '\n' || v.p[i] == '\r' ? ' ' : v.p[i], f);
    } else
        lj_write_string(f, m->payload);
    fputs("}\n", f);
}
