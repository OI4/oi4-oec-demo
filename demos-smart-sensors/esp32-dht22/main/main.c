/*
 * The smart sensor demo: a DHT22 climate sensor on an ESP32 or ESP32-S3 as
 * an OEC smart device, built with the OEC Micro SDK (its ESP-IDF example
 * with the DHT22 of climate.c instead of the simulated sensor, Wi-Fi only).
 *
 * Provisioning (README): the NVS partition "oecprov", namespace "oec",
 * strings uri (mqtts://host:port), ca (PEM), user + password or cert + key
 * (PEM), optional serial (default: the base MAC address), ntp (default
 * pool.ntp.org), wifi_ssid, wifi_pass; u8 level (1 … 3, default Kconfig);
 * u32 max_packet (the MaxPacketSize of broker.json, default 262144).
 * The persisted records of Level 3 live in the default "nvs" partition.
 *
 * Serial console (DEMO_CONSOLE): "stop" ends the device gracefully (Close,
 * DISCONNECT) and prints "oecm: stopped" (restart hooks of D7); "time <unix
 * ms>" sets the clock when no SNTP server is reachable.
 */
#include <inttypes.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/time.h>
#include <time.h>

#include "esp_app_desc.h"
#include "esp_chip_info.h"
#include "esp_event.h"
#include "esp_log.h"
#include "esp_mac.h"
#include "esp_netif.h"
#include "esp_netif_sntp.h"
#include "esp_system.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/event_groups.h"
#include "freertos/task.h"
#include "nvs.h"
#include "nvs_flash.h"
#include "sdkconfig.h"

#include "esp_wifi.h"
#if CONFIG_DEMO_CONSOLE
#include "driver/uart.h"
#endif

#include "oecm.h"
#include "oecm_esp.h"
#include "climate.h"

static const char *TAG = "demo";

#if CONFIG_IDF_TARGET_ESP32S3
#define MODEL "OEC Micro ESP32-S3 DHT22"
#define PRODUCT_CODE "OECM-ESP32S3-DHT22"
#else
#define MODEL "OEC Micro ESP32 DHT22"
#define PRODUCT_CODE "OECM-ESP32-DHT22"
#endif

/* The transmit buffer: room for the largest publication (the Config with
 * its definition), as in the example of the Micro SDK. */
#define TX_SIZE 8192
#define REFDES_SIZE 2048
/* A wall clock before 2023 is not set yet. */
#define TIME_PLAUSIBLE 1672531200

static oecm_device dev;
static uint8_t tx[TX_SIZE];
static char refdes[REFDES_SIZE];
static oecm_config cfg;
static EventGroupHandle_t net_events;
static atomic_bool stop_requested;

/* Reads a provisioning string (heap copy, NULL if absent). */
static char *prov_str(nvs_handle_t h, const char *key) {
    size_t n = 0;
    if (nvs_get_str(h, key, NULL, &n) != ESP_OK || n == 0)
        return NULL;
    char *s = malloc(n);
    if (s && nvs_get_str(h, key, s, &n) != ESP_OK) {
        free(s);
        return NULL;
    }
    return s;
}

static void on_ip(void *arg, esp_event_base_t base, int32_t id, void *data) {
    (void)arg;
    (void)base;
    (void)id;
    ip_event_got_ip_t *e = data;
    ESP_LOGI(TAG, "address " IPSTR, IP2STR(&e->ip_info.ip));
    xEventGroupSetBits(net_events, 1);
}

static void on_wifi(void *arg, esp_event_base_t base, int32_t id, void *data) {
    (void)arg;
    (void)base;
    (void)data;
    if (id == WIFI_EVENT_STA_START || id == WIFI_EVENT_STA_DISCONNECTED)
        esp_wifi_connect();
}

static esp_err_t net_start(nvs_handle_t prov) {
    char *ssid = prov_str(prov, "wifi_ssid"), *pass = prov_str(prov, "wifi_pass");
    if (!ssid) {
        ESP_LOGE(TAG, "provisioning: wifi_ssid missing");
        return ESP_ERR_INVALID_ARG;
    }
    esp_netif_create_default_wifi_sta();
    wifi_init_config_t init = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&init));
    ESP_ERROR_CHECK(esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, on_wifi, NULL));
    ESP_ERROR_CHECK(esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP, on_ip, NULL));
    wifi_config_t wc = {0};
    strlcpy((char *)wc.sta.ssid, ssid, sizeof wc.sta.ssid);
    if (pass)
        strlcpy((char *)wc.sta.password, pass, sizeof wc.sta.password);
    free(ssid);
    free(pass);
    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_STA));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_STA, &wc));
    return esp_wifi_start();
}

#if CONFIG_DEMO_CONSOLE
static void console_task(void *arg) {
    (void)arg;
    const uart_port_t uart = CONFIG_ESP_CONSOLE_UART_NUM;
    if (uart_driver_install(uart, 256, 0, 0, NULL, 0) != ESP_OK) {
        ESP_LOGE(TAG, "console: no UART driver");
        vTaskDelete(NULL);
    }
    char line[48];
    size_t n = 0;
    for (;;) {
        uint8_t c;
        if (uart_read_bytes(uart, &c, 1, portMAX_DELAY) != 1)
            continue;
        if (c != '\n' && c != '\r') {
            if (n < sizeof line - 1)
                line[n++] = (char)c;
            continue;
        }
        line[n] = 0;
        n = 0;
        if (strcmp(line, "stop") == 0) {
            atomic_store(&stop_requested, true);
        } else if (strncmp(line, "time ", 5) == 0) {
            long long ms = strtoll(line + 5, NULL, 10);
            struct timeval tv = {.tv_sec = (time_t)(ms / 1000), .tv_usec = (suseconds_t)(ms % 1000) * 1000};
            settimeofday(&tv, NULL);
            ESP_LOGI(TAG, "console: time set");
        }
    }
}
#endif

void app_main(void) {
    esp_err_t e = nvs_flash_init();
    if (e == ESP_ERR_NVS_NO_FREE_PAGES || e == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        e = nvs_flash_init();
    }
    ESP_ERROR_CHECK(e);
    climate_start();
    nvs_handle_t prov;
    if (nvs_flash_init_partition("oecprov") != ESP_OK ||
        nvs_open_from_partition("oecprov", "oec", NVS_READONLY, &prov) != ESP_OK) {
        ESP_LOGE(TAG, "provisioning partition oecprov missing (README)");
        return;
    }
    oecm_esp_config pc = {
        .uri = prov_str(prov, "uri"),
        .ca_pem = prov_str(prov, "ca"),
        .username = prov_str(prov, "user"),
        .password = prov_str(prov, "password"),
        .client_cert_pem = prov_str(prov, "cert"),
        .client_key_pem = prov_str(prov, "key"),
    };
    char *serial = prov_str(prov, "serial");
    char *ntp = prov_str(prov, "ntp");
    uint8_t level = CONFIG_DEMO_LEVEL;
    (void)nvs_get_u8(prov, "level", &level);
    if (level < 1 || level > 3)
        level = CONFIG_DEMO_LEVEL;
    uint32_t max_packet = 0;
    (void)nvs_get_u32(prov, "max_packet", &max_packet);
    if (!serial) {
        uint8_t mac[6];
        serial = malloc(13);
        ESP_ERROR_CHECK(esp_read_mac(mac, ESP_MAC_BASE));
        snprintf(serial, 13, "%02X%02X%02X%02X%02X%02X", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
    }

#if CONFIG_DEMO_CONSOLE
    xTaskCreate(console_task, "console", 2560, NULL, 5, NULL);
#endif
    net_events = xEventGroupCreate();
    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());
    e = net_start(prov);
    nvs_close(prov);
    if (e != ESP_OK)
        return;
    xEventGroupWaitBits(net_events, 1, pdFALSE, pdTRUE, portMAX_DELAY);
    esp_sntp_config_t sntp = ESP_NETIF_SNTP_DEFAULT_CONFIG(ntp ? ntp : "pool.ntp.org");
    esp_netif_sntp_init(&sntp);
    while (time(NULL) < TIME_PLAUSIBLE) {
        ESP_LOGI(TAG, "waiting for the time");
        esp_netif_sntp_sync_wait(pdMS_TO_TICKS(2000));
    }

    esp_chip_info_t chip;
    esp_chip_info(&chip);
    static char hw[16];
    snprintf(hw, sizeof hw, "v%u.%u", chip.revision / 100, chip.revision % 100);
    cfg = (oecm_config){
        .level = (oecm_level)level,
        .service_type = OECM_ST_OT_CONNECTOR, /* a directly connected smart device (SF-102) */
        .max_packet_size = max_packet,
        .mam =
            {
                .manufacturer = "OI4",
                .manufacturer_uri = "oi4.org",
                .model = MODEL,
                .product_code = PRODUCT_CODE,
                .hardware_revision = hw,
                .software_revision = esp_app_get_description()->version,
                .device_manual = "https://oi4.org",
                .device_class = "Sensor",
                .serial_number = serial,
                .revision_counter = -1,
            },
    };
    oecm_esp *port;
    oecm_pal pal;
    e = oecm_esp_init(&pc, &port, &pal);
    if (e != ESP_OK) {
        ESP_LOGE(TAG, "oecm_esp_init: %s (provisioning uri, ca, user or cert)", esp_err_to_name(e));
        return;
    }
    if (level >= 2)
        climate_setup(&cfg, &dev);
    if (level >= 3) {
        cfg.refdes_buf = refdes;
        cfg.refdes_cap = sizeof refdes;
    }
    oecm_err oe = oecm_init(&dev, &cfg, &pal, tx, sizeof tx);
    if (oe == OECM_OK)
        oe = oecm_start(&dev);
    if (oe != OECM_OK) {
        ESP_LOGE(TAG, "oecm: %s", oecm_err_str(oe));
        return;
    }
    ESP_LOGI(TAG, "device %s (Level %u), broker %s", oecm_identification_link(&dev), level, pc.uri);

    int64_t next_tick = 0;
    int64_t next_report = 0;
    for (;;) {
        uint32_t wait = oecm_poll_interval_ms(&dev);
        oecm_esp_dispatch(port, &dev, wait > 200 ? 200 : wait);
        if (atomic_load(&stop_requested)) {
            oe = oecm_stop(&dev);
            printf("oecm: stopped (%s)\n", oecm_err_str(oe));
            fflush(stdout);
            for (;;)
                vTaskDelay(portMAX_DELAY);
        }
        int64_t now = esp_timer_get_time() / 1000;
        if (level >= 2 && now >= next_tick) { /* the sensor task reads at its interval */
            climate_tick();
            next_tick = now + 1000;
        }
        if (now >= next_report) { /* footprint */
            ESP_LOGI(TAG, "heap free %" PRIu32 ", minimum %" PRIu32 "; main stack unused %u",
                     esp_get_free_heap_size(), esp_get_minimum_free_heap_size(),
                     (unsigned)uxTaskGetStackHighWaterMark(NULL));
            next_report = now + 60000;
        }
        oe = oecm_poll(&dev);
        if (oe != OECM_OK)
            ESP_LOGW(TAG, "oecm_poll: %s", oecm_err_str(oe));
    }
}
