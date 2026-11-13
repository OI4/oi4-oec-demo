/* The consumer side of the display demo (climate_view.h). Runs on the
 * device task only: on_message and on_config are called inside
 * oecm_esp_dispatch, the display reads the state in the same loop. */
#include <string.h>

#include "esp_timer.h"

#include "climate_view.h"

enum { SUB_CLIMATE, SUB_HEALTH };

static const oecm_subscription subscriptions[] = {
    [SUB_CLIMATE] = {.topic_filter = "Oi4v2/OTConnector/+/Pub/Data/+/Climate"},
    [SUB_HEALTH] = {.topic_filter = "Oi4v2/OTConnector/+/Pub/Health/+"},
};

static view_sensor slots[VIEW_SLOTS];
static uint32_t revision;
static oecm_device *device;

/* Values of the Data and the Config; written on the device task only. */
static uint32_t sensors;
static char shown[OECM_IL_BUF];
static char title[25] = "OEC Climate";
static char stale_after[8] = "120";

static const oecm_field fields[] = {
    {.name = "Sensors", .description = "Climate sensors received", .type = OECM_TYPE_UINT32,
     .value = &sensors},
    {.name = "Shown", .description = "Identification Link of the shown sensor",
     .type = OECM_TYPE_STRING, .value = shown, .cap = sizeof shown},
};

static const oecm_data data[] = {{
    .tag = "View",
    .description = "What the display shows",
    .fields = fields,
    .field_count = sizeof fields / sizeof fields[0],
    .interval_ms = 60000,
    .writable = false,
}};

static const oecm_cfg_object general[] = {
    {.key = "Title", .name = "Title", .description = "Title line of the display",
     .type = OECM_CFG_STRING, .value = title, .cap = sizeof title, .default_value = "OEC Climate",
     .length = 24},
    {.key = "StaleAfter", .name = "Stale after",
     .description = "Seconds without Climate data until a sensor is shown as stale",
     .type = OECM_CFG_NUMBER, .value = stale_after, .cap = sizeof stale_after,
     .default_value = "120", .unit = "s", .mandatory = true, .min = "10", .max = "3600"},
};

static const oecm_cfg_group groups[] = {{
    .key = "General",
    .name = "General",
    .objects = general,
    .count = sizeof general / sizeof general[0],
}};

static const oecm_cfg_context contexts[] = {{
    .name = "Display",
    .title = "Display",
    .description = "Settings of the climate display",
    .groups = groups,
    .count = 1,
}};

int64_t view_now_ms(void) { return esp_timer_get_time() / 1000; }

static bool has_data(const view_sensor *s) { return s->data_ms != 0; }

static void copy(char *dst, size_t cap, const char *src) {
    size_t n = strlen(src);
    if (n >= cap)
        n = cap - 1;
    memcpy(dst, src, n);
    dst[n] = 0;
}

static view_sensor *find(const char *source) {
    for (size_t i = 0; i < VIEW_SLOTS; i++)
        if (slots[i].source[0] && strcmp(slots[i].source, source) == 0)
            return &slots[i];
    return NULL;
}

/* A free slot, else (for Climate data) one that only holds a Health. */
static view_sensor *add(const char *source, bool climate) {
    view_sensor *s = NULL;
    for (size_t i = 0; i < VIEW_SLOTS && !s; i++)
        if (!slots[i].source[0])
            s = &slots[i];
    for (size_t i = 0; i < VIEW_SLOTS && !s && climate; i++)
        if (!has_data(&slots[i]))
            s = &slots[i];
    if (!s)
        return NULL;
    memset(s, 0, sizeof *s);
    copy(s->source, sizeof s->source, source);
    return s;
}

size_t view_count(void) {
    size_t n = 0;
    for (size_t i = 0; i < VIEW_SLOTS; i++)
        n += has_data(&slots[i]);
    return n;
}

const view_sensor *view_at(size_t i) {
    for (size_t k = 0; k < VIEW_SLOTS; k++)
        if (has_data(&slots[k]) && i-- == 0)
            return &slots[k];
    return NULL;
}

size_t view_shown(void) {
    for (size_t i = 0, n = view_count(); i < n; i++)
        if (strcmp(view_at(i)->source, shown) == 0)
            return i;
    return 0;
}

/* Publishes the View when the number of sensors or the shown one changed. */
static void update_view(void) {
    size_t n = view_count();
    const char *want = n ? view_at(view_shown())->source : "";
    if (sensors == n && strcmp(shown, want) == 0)
        return;
    sensors = (uint32_t)n;
    copy(shown, sizeof shown, want);
    (void)oecm_data_changed(device, 0);
}

void view_next(void) {
    size_t n = view_count();
    if (n < 2)
        return;
    copy(shown, sizeof shown, view_at((view_shown() + 1) % n)->source);
    (void)oecm_data_changed(device, 0);
    revision++;
}

const char *view_title(void) { return title; }

unsigned view_stale_s(void) {
    unsigned s = 0;
    for (const char *p = stale_after; *p >= '0' && *p <= '9' && s < 100000u; p++)
        s = s * 10u + (unsigned)(*p - '0');
    return s < 10u ? 120u : s;
}

uint32_t view_revision(void) { return revision; }

static void climate(const uint8_t *payload, size_t len, size_t i, const char *source) {
    double t, h;
    if (oecm_msg_number(payload, len, i, "Payload.Temperature", &t) != OECM_OK ||
        oecm_msg_number(payload, len, i, "Payload.Humidity", &h) != OECM_OK)
        return;
    view_sensor *s = find(source);
    if (!s && !(s = add(source, true)))
        return; /* all slots hold sensors */
    s->temperature = t;
    s->humidity = h;
    if (oecm_msg_text(payload, len, i, "Payload.Label", s->label, sizeof s->label) != OECM_OK)
        s->label[0] = 0;
    int64_t now = view_now_ms();
    s->data_ms = now ? now : 1;
}

static void health(const uint8_t *payload, size_t len, size_t i, const char *source) {
    char text[VIEW_HEALTH];
    double score;
    if (oecm_msg_text(payload, len, i, "Payload.Health", text, sizeof text) != OECM_OK)
        return;
    view_sensor *s = find(source);
    if (!s && !(s = add(source, false)))
        return;
    copy(s->health, sizeof s->health, text);
    s->health_score = oecm_msg_number(payload, len, i, "Payload.HealthScore", &score) == OECM_OK
                          ? (int)score
                          : -1;
}

static void on_message(void *ctx, size_t subscription, const char *topic, const uint8_t *payload,
                       size_t len) {
    (void)ctx;
    char app[OECM_IL_BUF], source[OECM_IL_BUF];
    size_t count;
    if (oecm_topic_app(topic, app, sizeof app) != OECM_OK ||
        strcmp(app, oecm_identification_link(device)) == 0 ||
        oecm_topic_source(topic, source, sizeof source) != OECM_OK ||
        oecm_msg_count(payload, len, &count) != OECM_OK)
        return; /* not an OEC publication of another application */
    for (size_t i = 0; i < count; i++) {
        if (subscription == SUB_CLIMATE)
            climate(payload, len, i, source);
        else
            health(payload, len, i, source);
    }
    update_view();
    revision++;
}

static void on_config(void *ctx, size_t context) {
    (void)ctx;
    (void)context;
    revision++;
}

void view_setup(oecm_config *cfg, oecm_device *dev) {
    device = dev;
    cfg->data = data;
    cfg->data_count = 1;
    cfg->config = contexts;
    cfg->config_count = 1;
    cfg->on_config = on_config;
    cfg->subscriptions = subscriptions;
    cfg->subscription_count = sizeof subscriptions / sizeof subscriptions[0];
    cfg->on_message = on_message;
}
