/* The DHT22 (AM2302) climate sensor of the smart sensor demo (README).
 *
 * The single-wire protocol needs microsecond timing: a task of its own reads
 * the sensor inside a critical section (about 5 ms) on the second core if
 * there is one, at most every 2 s; Wi-Fi stays on core 0. The device loop
 * only takes over the result (climate_tick). */
#include <stdatomic.h>
#include <stdbool.h>
#include <stdlib.h>

#include "driver/gpio.h"
#include "esp_log.h"
#include "esp_rom_sys.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
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

static const oecm_cfg_context contexts[] = {{
    .name = "Sensor",
    .title = "Sensor",
    .description = "Settings of the DHT22 sensor",
    .groups = groups,
    .count = 1,
}};

static oecm_device *device;
static TaskHandle_t sensor;
/* Shared between the sensor task and the device task. */
static atomic_uint interval_s = INTERVAL_DEFAULT;
static atomic_int last_t10, last_h10; /* tenths of °C and % */
static atomic_uint reading_seq, failures;
/* Device task only. */
static unsigned seen_seq;
static bool failed;

static unsigned parse_interval(void) {
    unsigned s = 0;
    for (const char *p = interval; *p >= '0' && *p <= '9' && s < 100000u; p++)
        s = s * 10u + (unsigned)(*p - '0');
    return s < INTERVAL_MIN ? INTERVAL_DEFAULT : s;
}

/* The core has stored the new values of the context (Set). */
static void on_config(void *ctx, size_t context) {
    (void)ctx;
    (void)context;
    unsigned s = parse_interval();
    if (atomic_exchange(&interval_s, s) != s && sensor)
        xTaskNotifyGive(sensor); /* the new interval applies now, not after the old one */
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
    xTaskCreatePinnedToCore(sensor_task, "dht22", 3072, NULL, 10, &sensor, portNUM_PROCESSORS > 1 ? 1 : 0);
}

void climate_setup(oecm_config *cfg, oecm_device *dev) {
    device = dev;
    cfg->data = data;
    cfg->data_count = 1;
    cfg->config = contexts;
    cfg->config_count = 1;
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
