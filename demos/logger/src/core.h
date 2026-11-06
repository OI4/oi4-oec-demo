/* The core of the logger demo (D4, ADR-0028): settings from its Config,
 * the filter, the Event level and the records. Free of oec.h, so the unit
 * tests run without the SDK; main.c is the glue to the C ABI. */
#ifndef LOGGER_CORE_H
#define LOGGER_CORE_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

/* The resources the logger logs. */
typedef enum lg_resource {
    LG_EVENT,
    LG_HEALTH,
    LG_DATA,
} lg_resource;

typedef enum lg_format {
    LG_FORMAT_JSON,
    LG_FORMAT_TEXT,
} lg_format;

/* Limits of the Config values. */
#define LG_MAX_SOURCES 32
#define LG_SOURCE_MAX 256 /* octets of one entry incl. NUL */
#define LG_SOURCES_MAX 2048 /* Validation Length of sources */
#define LG_FILE_MAX 65 /* Validation Length of file (64) incl. NUL */
#define LG_HISTORY_MIN 10
#define LG_HISTORY_MAX 10000

/* The settings of the logger: the values of its Config (context
 * "logger", group "common"). */
typedef struct lg_settings {
    bool event, health, data;
    int n_sources; /* 0 = every source */
    char sources[LG_MAX_SOURCES][LG_SOURCE_MAX];
    int min_level; /* Syslog severity 0 (Emergency) … 7 (Debug) */
    lg_format format;
    char file[LG_FILE_MAX]; /* "" = no file */
    unsigned history;
} lg_settings;

/* The Filter (context) and the group of the Config. */
#define LG_CONFIG_CONTEXT "logger"
#define LG_CONFIG_GROUP "common"

/* The names of the configuration objects, NULL terminated. */
extern const char *const lg_config_names[];

/* The defaults: Event and Health, every source, Informational, JSON, no
 * file, 1000 records. */
void lg_settings_default(lg_settings *s);

/* The Config definition (MG 8.3.3), the definition_json of
 * oec_asset_add_config; its Values are the defaults. */
const char *lg_config_definition(void);

/* Sets one object from its string form (REQ-RES-CONFIG-007); "" is
 * unset and means the default. Returns false with a reason in err. */
bool lg_settings_set(lg_settings *s, const char *name, const char *value, char *err,
                     size_t errlen);

/* Settings from the proposal of a Set Config
 * ({"common": {"<name>": "<value>", …}}): starts from the defaults, unknown
 * names are ignored. Returns false with a reason in err. */
bool lg_settings_from_proposal(const char *json, lg_settings *s, char *err, size_t errlen);

/* The Syslog severity names (RFC 5424 6.2.1, MG 7.7). */
const char *lg_level_name(int level);
int lg_level_parse(const char *name); /* -1 if unknown */

/* The severity of an Event Payload (MG 8.3.6): Syslog PRI modulo 8;
 * Status by the severity class of Number (Good → Informational,
 * Uncertain → Warning, Bad → Error); Generic and anything unreadable →
 * Notice. */
int lg_event_level(const char *payload_json);

/* A received DataSetMessage. */
typedef struct lg_msg {
    lg_resource resource;
    const char *service_type;
    const char *app;    /* Identification Link, unencoded */
    const char *source; /* NULL if the topic has none */
    const char *filter; /* NULL if the topic has none */
    const char *payload; /* JSON */
    int64_t time_ms;     /* receive time, Unix ms */
} lg_msg;

const char *lg_resource_name(lg_resource r);

/* Whether the AppId or the Source matches the source filter: an entry
 * matches an Identification Link that equals it or starts with it and a
 * "/" (a prefix of whole levels). */
bool lg_source_match(const lg_settings *s, const char *app, const char *source);

/* Whether the message is logged; *level is its severity (Events) or -1. */
bool lg_accept(const lg_settings *s, const lg_msg *m, int *level);

/* Writes one record with a trailing newline: a JSON object
 * {"time","resource","serviceType","app","source","filter"[,"level"],
 * "payload"} or a text line. */
void lg_write_record(FILE *f, lg_format format, const lg_msg *m, int level);

/* RFC 3339 UTC with milliseconds into buf (at least 25 octets). */
void lg_format_time(int64_t time_ms, char *buf, size_t len);

/* Whether a file name of the Config is acceptable: 1 … 64 octets of
 * A-Z a-z 0-9 . _ -, not starting with "." or "-" (no path, the file
 * lies in /opt/oi4/app). */
bool lg_file_name_ok(const char *name);

#endif
