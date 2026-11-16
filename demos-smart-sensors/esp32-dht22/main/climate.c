/* The DHT22 (AM2302) climate sensor of the smart sensor demo (README).
 *
 * The single-wire protocol needs microsecond timing: a task of its own reads
 * the sensor inside a critical section (about 5 ms) on the second core if
 * there is one, at most every 2 s; Wi-Fi stays on core 0. The device loop
 * only takes over the result (climate_tick).
 *
 * The Config values are kept in the default NVS partition, one namespace
 * per context ("demo.<context>"), and survive a restart. */
#include <math.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "driver/gpio.h"
#include "esp_log.h"
#include "esp_rom_sys.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "nvs.h"
#include "sdkconfig.h"

#include "climate.h"

#define DHT22_GPIO ((gpio_num_t)CONFIG_DEMO_DHT22_GPIO)
#define FAILURES_FOR_HEALTH 3
#define INTERVAL_DEFAULT 10u
#define INTERVAL_MIN 2u

static const char *TAG = "dht22";

/* Values of the Data and the Config; written on the device task only. */
static double temperature, humidity;
static char label[33] = "DHT22 climate sensor";
static char interval[16] = "10";

static const oecm_field fields[] = {
    {.name = "Temperature", .description = "Air temperature measured by the DHT22",
     .type = OECM_TYPE_DOUBLE, .value = &temperature, .decimals = 1, .unit = "°C",
     .eu_low = "-40", .eu_high = "80"},
    {.name = "Humidity", .description = "Relative humidity measured by the DHT22",
     .type = OECM_TYPE_DOUBLE, .value = &humidity, .decimals = 1, .unit = "%",
     .eu_low = "0", .eu_high = "100"},
    {.name = "Label", .description = "Label of the sensor", .type = OECM_TYPE_STRING,
     .value = label, .cap = sizeof label},
};

static const oecm_data data[] = {{
    .tag = "Climate",
    .description = "Temperature and relative humidity of a DHT22",
    .fields = fields,
    .field_count = sizeof fields / sizeof fields[0],
    .interval_ms = 60000,
    .writable = false,
}};

static const oecm_cfg_object general[] = {
    {.key = "Interval", .name = "Interval", .description = "Seconds between two measurements",
     .type = OECM_CFG_NUMBER, .value = interval, .cap = sizeof interval, .default_value = "10",
     .unit = "s", .mandatory = true, .min = "2", .max = "3600"},
    {.key = "Label", .name = "Label", .description = "Label of the sensor",
     .type = OECM_CFG_STRING, .value = label, .cap = sizeof label,
     .default_value = "DHT22 climate sensor", .length = 32},
};

static const oecm_cfg_group groups[] = {{
    .key = "General",
    .name = "General",
    .objects = general,
    .count = sizeof general / sizeof general[0],
}};

#if CONFIG_DEMO_LCD147
#define STR_(x) #x
#define STR(x) STR_(x)

/* The ends of the temperature scale of the display and of the colour of
 * the RGB LED (Kconfig: the defaults). */
static char cold[8] = STR(CONFIG_DEMO_COLD_C);
static char hot[8] = STR(CONFIG_DEMO_HOT_C);
static char brightness[8] = STR(CONFIG_DEMO_LED_BRIGHTNESS);

static const oecm_cfg_object scale[] = {
    {.key = "ColdTemperature", .name = "Cold end",
     .description = "Temperature shown blue, at the left end of the scale, and below",
     .type = OECM_CFG_NUMBER, .value = cold, .cap = sizeof cold,
     .default_value = STR(CONFIG_DEMO_COLD_C), .unit = "°C", .mandatory = true, .min = "-40",
     .max = "80"},
    {.key = "HotTemperature", .name = "Hot end",
     .description = "Temperature shown red, at the right end of the scale, and above",
     .type = OECM_CFG_NUMBER, .value = hot, .cap = sizeof hot,
     .default_value = STR(CONFIG_DEMO_HOT_C), .unit = "°C", .mandatory = true, .min = "-40",
     .max = "80"},
};

static const oecm_cfg_object led[] = {
    {.key = "Brightness", .name = "Brightness",
     .description = "Brightness of the RGB LED, 0 (off) … 255", .type = OECM_CFG_NUMBER,
     .value = brightness, .cap = sizeof brightness,
     .default_value = STR(CONFIG_DEMO_LED_BRIGHTNESS), .mandatory = true, .min = "0",
     .max = "255"},
};

static const oecm_cfg_group display_groups[] = {
    {.key = "Scale",
     .name = "Temperature scale",
     .description = "The colour runs from blue (cold end) over green and yellow to red (hot end)",
     .objects = scale,
     .count = sizeof scale / sizeof scale[0]},
    {.key = "Led", .name = "RGB LED", .objects = led, .count = sizeof led / sizeof led[0]},
};
#endif

enum { CTX_SENSOR, CTX_DISPLAY };

static const oecm_cfg_context contexts[] = {
    [CTX_SENSOR] = {.name = "Sensor",
                    .title = "Sensor",
                    .description = "Settings of the DHT22 sensor",
                    .groups = groups,
                    .count = 1},
#if CONFIG_DEMO_LCD147
    [CTX_DISPLAY] = {.name = "Display",
                     .title = "Display",
                     .description = "Settings of the display and the RGB LED",
                     .groups = display_groups,
                     .count = sizeof display_groups / sizeof display_groups[0]},
#endif
};

static oecm_device *device;
static TaskHandle_t sensor;
/* Shared between the sensor task and the device task. */
static atomic_uint interval_s = INTERVAL_DEFAULT;
static atomic_int last_t10, last_h10; /* tenths of °C and % */
static atomic_uint reading_seq, failures;
/* Device task only. */
static unsigned seen_seq;
static bool failed;
/* The Label for a local display, a copy taken on the device task. */
static portMUX_TYPE label_lock = portMUX_INITIALIZER_UNLOCKED;
static char label_copy[sizeof label] = "DHT22 climate sensor";

static void label_publish(void) {
    taskENTER_CRITICAL(&label_lock);
    memcpy(label_copy, label, sizeof label_copy);
    taskEXIT_CRITICAL(&label_lock);
}

static unsigned parse_interval(void) {
    unsigned s = 0;
    for (const char *p = interval; *p >= '0' && *p <= '9' && s < 100000u; p++)
        s = s * 10u + (unsigned)(*p - '0');
    return s < INTERVAL_MIN ? INTERVAL_DEFAULT : s;
}

/* A Number value within its Min and Max (NAN if not). */
static double number(const oecm_cfg_object *o, const char *v) {
    char *end;
    double x = strtod(v, &end);
    if (end == v || *end || !isfinite(x) || (o->min && x < strtod(o->min, NULL)) ||
        (o->max && x > strtod(o->max, NULL)))
        return NAN;
    return x;
}

static void namespace_of(size_t context, char ns[NVS_NS_NAME_MAX_SIZE]) {
    snprintf(ns, NVS_NS_NAME_MAX_SIZE, "demo.%s", contexts[context].name);
}

/* Takes over the stored values of the contexts that are still valid. */
static void settings_load(void) {
    for (size_t c = 0; c < sizeof contexts / sizeof contexts[0]; c++) {
        char ns[NVS_NS_NAME_MAX_SIZE];
        nvs_handle_t h;
        namespace_of(c, ns);
        if (nvs_open(ns, NVS_READONLY, &h) != ESP_OK)
            continue; /* nothing stored yet */
        for (size_t g = 0; g < contexts[c].count; g++)
            for (size_t k = 0; k < contexts[c].groups[g].count; k++) {
                const oecm_cfg_object *o = &contexts[c].groups[g].objects[k];
                char v[64];
                size_t n = sizeof v;
                if (nvs_get_str(h, o->key, v, &n) != ESP_OK || strlen(v) >= o->cap ||
                    (o->type == OECM_CFG_NUMBER && v[0] && isnan(number(o, v))))
                    continue;
                strcpy(o->value, v);
            }
        nvs_close(h);
    }
}

static void settings_save(size_t c) {
    char ns[NVS_NS_NAME_MAX_SIZE];
    nvs_handle_t h;
    namespace_of(c, ns);
    esp_err_t e = nvs_open(ns, NVS_READWRITE, &h);
    for (size_t g = 0; e == ESP_OK && g < contexts[c].count; g++)
        for (size_t k = 0; e == ESP_OK && k < contexts[c].groups[g].count; k++) {
            const oecm_cfg_object *o = &contexts[c].groups[g].objects[k];
            e = nvs_set_str(h, o->key, o->value);
        }
    if (e == ESP_OK)
        e = nvs_commit(h);
    if (e != ESP_OK)
        ESP_LOGW(TAG, "Config %s not stored: %s", contexts[c].name, esp_err_to_name(e));
    nvs_close(h);
}

#if CONFIG_DEMO_LCD147
/* Shared with the display task. */
static atomic_int cold10 = CONFIG_DEMO_COLD_C * 10, hot10 = CONFIG_DEMO_HOT_C * 10;
static atomic_uint led_level = CONFIG_DEMO_LED_BRIGHTNESS;

/* A temperature of the scale in tenths of °C; the default if empty. */
static int tenths_of(const oecm_cfg_object *o) {
    double x = number(o, o->value);
    if (isnan(x))
        x = strtod(o->default_value, NULL);
    return (int)lround(x * 10);
}

static void display_apply(void) {
    int lo = tenths_of(&scale[0]), hi = tenths_of(&scale[1]);
    if (lo > hi) { /* the scale runs from the lower to the higher value */
        int t = lo;
        lo = hi;
        hi = t;
    }
    if (hi == lo)
        hi = lo + 10; /* at least 1 °C */
    double b = number(&led[0], brightness);
    atomic_store(&cold10, lo);
    atomic_store(&hot10, hi);
    atomic_store(&led_level, isnan(b) ? CONFIG_DEMO_LED_BRIGHTNESS : (unsigned)lround(b));
}

void climate_display(int *cold_10, int *hot_10, unsigned *led_brightness) {
    *cold_10 = atomic_load(&cold10);
    *hot_10 = atomic_load(&hot10);
    *led_brightness = atomic_load(&led_level);
}
#endif

/* The core has stored the new values of the context (Set). */
static void on_config(void *ctx, size_t context) {
    (void)ctx;
    settings_save(context);
#if CONFIG_DEMO_LCD147
    if (context == CTX_DISPLAY) {
        display_apply();
        return;
    }
#endif
    unsigned s = parse_interval();
    if (atomic_exchange(&interval_s, s) != s && sensor)
        xTaskNotifyGive(sensor); /* the new interval applies now, not after the old one */
    label_publish();
    (void)oecm_data_changed(device, 0); /* the Label is also in the Data */
}

/* Waits while the line has the given level; the time in µs, -1 on timeout. */
static int IRAM_ATTR level_time(int level, int timeout_us) {
    int64_t start = esp_timer_get_time();
    while (gpio_get_level(DHT22_GPIO) == level) {
        if (esp_timer_get_time() - start > timeout_us)
            return -1;
    }
    return (int)(esp_timer_get_time() - start);
}

static portMUX_TYPE mux = portMUX_INITIALIZER_UNLOCKED;

/* One transaction: start signal, response, 40 bits, checksum. */
static esp_err_t dht22_read(int *t10, int *h10) {
    uint8_t b[5] = {0};
    gpio_set_level(DHT22_GPIO, 0);
    esp_rom_delay_us(1500);
    taskENTER_CRITICAL(&mux);
    gpio_set_level(DHT22_GPIO, 1);
    bool ok = level_time(1, 60) >= 0 && level_time(0, 100) >= 0 && level_time(1, 100) >= 0;
    for (int i = 0; ok && i < 40; i++) {
        int high;
        if (level_time(0, 80) < 0 || (high = level_time(1, 100)) < 0) {
            ok = false;
            break;
        }
        b[i / 8] = (uint8_t)(b[i / 8] << 1 | (high > 40));
    }
    taskEXIT_CRITICAL(&mux);
    if (!ok)
        return ESP_ERR_TIMEOUT;
    if ((uint8_t)(b[0] + b[1] + b[2] + b[3]) != b[4])
        return ESP_ERR_INVALID_CRC;
    *h10 = b[0] << 8 | b[1];
    int t = (b[2] & 0x7f) << 8 | b[3];
    *t10 = (b[2] & 0x80) ? -t : t;
    if (*h10 > 1000 || *t10 < -400 || *t10 > 800)
        return ESP_ERR_INVALID_RESPONSE;
    return ESP_OK;
}

static void sensor_task(void *arg) {
    (void)arg;
    gpio_config_t io = {
        .pin_bit_mask = 1ULL << DHT22_GPIO,
        .mode = GPIO_MODE_INPUT_OUTPUT_OD,
        .pull_up_en = GPIO_PULLUP_ENABLE,
    };
    ESP_ERROR_CHECK(gpio_config(&io));
    gpio_set_level(DHT22_GPIO, 1);
    vTaskDelay(pdMS_TO_TICKS(2000)); /* the DHT22 needs about 1 s after power-up */
    for (;;) {
        int t10 = 0, h10 = 0;
        esp_err_t e = ESP_FAIL;
        for (int attempt = 0; attempt < 3 && e != ESP_OK; attempt++) {
            if (attempt)
                vTaskDelay(pdMS_TO_TICKS(2100));
            e = dht22_read(&t10, &h10);
        }
        if (e == ESP_OK) {
            atomic_store(&last_t10, t10);
            atomic_store(&last_h10, h10);
            atomic_store(&failures, 0);
            atomic_fetch_add(&reading_seq, 1);
            ESP_LOGI(TAG, "temperature %d.%d °C, humidity %d.%d %%", t10 / 10, abs(t10 % 10), h10 / 10, h10 % 10);
        } else {
            unsigned n = atomic_fetch_add(&failures, 1) + 1;
            ESP_LOGW(TAG, "read failed on GPIO %d: %s (%u in a row)", DHT22_GPIO, esp_err_to_name(e), n);
        }
        if (ulTaskNotifyTake(pdTRUE, pdMS_TO_TICKS(1000u * atomic_load(&interval_s))))
            vTaskDelay(pdMS_TO_TICKS(2000)); /* woken by a new interval: still 2 s between readings */
    }
}

void climate_start(void) {
    settings_load();
    atomic_store(&interval_s, parse_interval());
    label_publish();
#if CONFIG_DEMO_LCD147
    display_apply();
#endif
    xTaskCreatePinnedToCore(sensor_task, "dht22", 3072, NULL, 10, &sensor, portNUM_PROCESSORS > 1 ? 1 : 0);
}

void climate_setup(oecm_config *cfg, oecm_device *dev) {
    device = dev;
    cfg->data = data;
    cfg->data_count = 1;
    cfg->config = contexts;
    cfg->config_count = sizeof contexts / sizeof contexts[0];
    cfg->on_config = on_config;
    /* The first publication carries a measurement, not 0.0. */
    for (int i = 0; i < 50 && atomic_load(&reading_seq) == 0; i++)
        vTaskDelay(pdMS_TO_TICKS(100));
    seen_seq = atomic_load(&reading_seq);
    if (seen_seq) {
        temperature = atomic_load(&last_t10) / 10.0;
        humidity = atomic_load(&last_h10) / 10.0;
    }
}

void climate_tick(void) {
    if (!device)
        return;
    if (!failed && atomic_load(&failures) >= FAILURES_FOR_HEALTH) {
        failed = true;
        (void)oecm_set_health(device, OECM_HEALTH_FAILURE, 0);
    }
    unsigned seq = atomic_load(&reading_seq);
    if (seq == seen_seq)
        return;
    seen_seq = seq;
    if (failed) {
        failed = false;
        (void)oecm_set_health(device, OECM_HEALTH_NORMAL, 100);
    }
    double t = atomic_load(&last_t10) / 10.0, h = atomic_load(&last_h10) / 10.0;
    if (t == temperature && h == humidity)
        return;
    temperature = t;
    humidity = h;
    (void)oecm_data_changed(device, 0);
}

bool climate_latest(int *t10, int *h10, bool *failure) {
    *failure = atomic_load(&failures) >= FAILURES_FOR_HEALTH;
    if (atomic_load(&reading_seq) == 0)
        return false;
    *t10 = atomic_load(&last_t10);
    *h10 = atomic_load(&last_h10);
    return true;
}

void climate_label(char *out, size_t cap) {
    if (!cap)
        return;
    taskENTER_CRITICAL(&label_lock);
    size_t n = strnlen(label_copy, sizeof label_copy - 1);
    if (n > cap - 1)
        n = cap - 1;
    memcpy(out, label_copy, n);
    taskEXIT_CRITICAL(&label_lock);
    out[n] = 0;
}
