/* The display and the RGB LED of the ESP32-S3-LCD-1.47 boards (lcd147.h).
 *
 * Display: ST7789, 172 x 320, SPI (MOSI 45, SCLK 40, CS 42, DC 41, RST 39,
 * backlight DEMO_LCD147_BL_GPIO: 46 on the 1.47B with USB-C, 48 on the 1.47
 * with USB-A), used in landscape. It is drawn in bands of BAND rows from
 * one DMA buffer, so no frame buffer is needed. LED: one WS2812-type LED on
 * GPIO 38 that takes its bytes in the order R, G, B (not G, R, B as a
 * WS2812B; tested on the 1.47B), driven by the RMT bytes encoder, in the colour of the scale at its marker
 * (gamma-corrected, so that the LED shows the colours of the display). Both
 * only on a change of the state; the scale and the brightness of the LED
 * come from the Config context "Display" (climate_display). */
#include "sdkconfig.h"
#if CONFIG_DEMO_LCD147
#include <math.h>
#include <string.h>

#include "driver/gpio.h"
#include "driver/rmt_tx.h"
#include "driver/spi_master.h"
#include "esp_check.h"
#include "esp_heap_caps.h"
#include "esp_lcd_panel_io.h"
#include "esp_lcd_panel_ops.h"
#include "esp_lcd_panel_st7789.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "freertos/task.h"

#include "climate.h"
#include "lcd147.h"
#include "screen.h"

_Static_assert(CONFIG_DEMO_COLD_C < CONFIG_DEMO_HOT_C, "DEMO_COLD_C must be below DEMO_HOT_C");

#define LCD_HOST SPI2_HOST
#define PIN_MOSI 45
#define PIN_SCLK 40
#define PIN_CS 42
#define PIN_DC 41
#define PIN_RST 39
#define PIN_BL CONFIG_DEMO_LCD147_BL_GPIO
#define PIN_LED 38
#define PANEL_GAP 34 /* (240 - 172) / 2: the visible rows of the ST7789 */
#define BAND 20

static const char *TAG = "lcd147";

static esp_lcd_panel_handle_t panel;
static uint16_t *band;
static SemaphoreHandle_t flushed;
static rmt_channel_handle_t led;
static rmt_encoder_handle_t led_encoder;
static TaskHandle_t task;

static portMUX_TYPE lock = portMUX_INITIALIZER_UNLOCKED;
static bool online; /* guarded by lock */
static char status[SCREEN_STATUS] = "starting ...";

static bool on_flushed(esp_lcd_panel_io_handle_t io, esp_lcd_panel_io_event_data_t *e, void *ctx) {
    (void)io;
    (void)e;
    (void)ctx;
    BaseType_t woken = pdFALSE;
    xSemaphoreGiveFromISR(flushed, &woken);
    return woken == pdTRUE;
}

static esp_err_t display_init(void) {
    gpio_config_t bl = {.pin_bit_mask = 1ULL << PIN_BL, .mode = GPIO_MODE_OUTPUT};
    ESP_RETURN_ON_ERROR(gpio_config(&bl), TAG, "backlight");
    spi_bus_config_t bus = {
        .mosi_io_num = PIN_MOSI,
        .miso_io_num = -1,
        .sclk_io_num = PIN_SCLK,
        .quadwp_io_num = -1,
        .quadhd_io_num = -1,
        .max_transfer_sz = SCREEN_W * BAND * sizeof(uint16_t),
    };
    ESP_RETURN_ON_ERROR(spi_bus_initialize(LCD_HOST, &bus, SPI_DMA_CH_AUTO), TAG, "SPI bus");
    esp_lcd_panel_io_handle_t io;
    esp_lcd_panel_io_spi_config_t ioc = {
        .cs_gpio_num = PIN_CS,
        .dc_gpio_num = PIN_DC,
        .spi_mode = 0,
        .pclk_hz = 40 * 1000 * 1000,
        .trans_queue_depth = 4,
        .on_color_trans_done = on_flushed,
        .lcd_cmd_bits = 8,
        .lcd_param_bits = 8,
    };
    ESP_RETURN_ON_ERROR(esp_lcd_new_panel_io_spi((esp_lcd_spi_bus_handle_t)LCD_HOST, &ioc, &io), TAG, "panel IO");
    esp_lcd_panel_dev_config_t pc = {
        .reset_gpio_num = PIN_RST,
        .rgb_ele_order = LCD_RGB_ELEMENT_ORDER_RGB,
        .bits_per_pixel = 16,
    };
    ESP_RETURN_ON_ERROR(esp_lcd_new_panel_st7789(io, &pc, &panel), TAG, "ST7789");
    ESP_RETURN_ON_ERROR(esp_lcd_panel_reset(panel), TAG, "reset");
    ESP_RETURN_ON_ERROR(esp_lcd_panel_init(panel), TAG, "init");
    ESP_RETURN_ON_ERROR(esp_lcd_panel_invert_color(panel, true), TAG, "invert"); /* IPS panel */
    ESP_RETURN_ON_ERROR(esp_lcd_panel_swap_xy(panel, true), TAG, "landscape");
#if CONFIG_DEMO_LCD147_FLIP
    ESP_RETURN_ON_ERROR(esp_lcd_panel_mirror(panel, false, true), TAG, "mirror");
#else
    ESP_RETURN_ON_ERROR(esp_lcd_panel_mirror(panel, true, false), TAG, "mirror");
#endif
    ESP_RETURN_ON_ERROR(esp_lcd_panel_set_gap(panel, 0, PANEL_GAP), TAG, "gap");
    band = heap_caps_malloc(SCREEN_W * BAND * sizeof(uint16_t), MALLOC_CAP_DMA | MALLOC_CAP_INTERNAL);
    flushed = xSemaphoreCreateBinary();
    if (!band || !flushed)
        return ESP_ERR_NO_MEM;
    xSemaphoreGive(flushed);
    return ESP_OK;
}

static esp_err_t led_init(void) {
    rmt_tx_channel_config_t ch = {
        .gpio_num = PIN_LED,
        .clk_src = RMT_CLK_SRC_DEFAULT,
        .resolution_hz = 10 * 1000 * 1000, /* 0.1 µs */
        .mem_block_symbols = 48,
        .trans_queue_depth = 2,
    };
    ESP_RETURN_ON_ERROR(rmt_new_tx_channel(&ch, &led), TAG, "RMT");
    rmt_bytes_encoder_config_t enc = {
        .bit0 = {.level0 = 1, .duration0 = 3, .level1 = 0, .duration1 = 9}, /* 0.3 µs high, 0.9 µs low */
        .bit1 = {.level0 = 1, .duration0 = 9, .level1 = 0, .duration1 = 3},
        .flags.msb_first = 1,
    };
    ESP_RETURN_ON_ERROR(rmt_new_bytes_encoder(&enc, &led_encoder), TAG, "RMT encoder");
    return rmt_enable(led);
}

/* A display (sRGB) channel as the duty of an LED channel: the WS2812 is
 * linear, the display has a gamma of about 2.2. */
static uint8_t led_channel(uint8_t c, unsigned level) {
    return (uint8_t)lroundf(powf(c / 255.0f, 2.2f) * (float)level);
}

static void led_set(const uint8_t rgb[3], unsigned level) {
    if (!led)
        return;
    uint8_t out[3] = {led_channel(rgb[0], level), led_channel(rgb[1], level), led_channel(rgb[2], level)};
    rmt_transmit_config_t t = {.loop_count = 0};
    esp_err_t e = rmt_transmit(led, led_encoder, out, sizeof out, &t);
    if (e == ESP_OK)
        e = rmt_tx_wait_all_done(led, 100); /* out lives on this stack */
    if (e != ESP_OK)
        ESP_LOGW(TAG, "RGB LED: %s", esp_err_to_name(e));
    else
        ESP_LOGD(TAG, "RGB LED %u %u %u", out[0], out[1], out[2]);
}

static void draw(const screen_state *s) {
    for (int y = 0; y < SCREEN_H; y += BAND) {
        int rows = SCREEN_H - y < BAND ? SCREEN_H - y : BAND;
        xSemaphoreTake(flushed, portMAX_DELAY); /* the previous band has left the buffer */
        screen_render(s, y, rows, band);
        if (esp_lcd_panel_draw_bitmap(panel, 0, y, SCREEN_W, y + rows, band) != ESP_OK)
            xSemaphoreGive(flushed);
    }
}

static void display_task(void *arg) {
    (void)arg;
    screen_state last;
    uint8_t last_led[3] = {0, 0, 0};
    unsigned last_level = 0;
    bool first = true;
    for (;;) {
        screen_state s;
        unsigned level;
        memset(&s, 0, sizeof s);
        s.reading = climate_latest(&s.t10, &s.h10, &s.health);
        climate_display(&s.cold10, &s.hot10, &level);
        climate_label(s.label, sizeof s.label);
        taskENTER_CRITICAL(&lock);
        s.online = online;
        memcpy(s.status, status, sizeof s.status);
        taskEXIT_CRITICAL(&lock);
        if (first || memcmp(&s, &last, sizeof s) != 0) {
            draw(&s);
            if (first) { /* after the first frame: no noise of the panel RAM */
                (void)esp_lcd_panel_disp_on_off(panel, true);
                gpio_set_level(PIN_BL, 1);
                ESP_LOGI(TAG, "first frame drawn, backlight on (GPIO %d)", PIN_BL);
            }
            last = s;
        }
        uint8_t rgb[3] = {0, 0, 0}; /* off without a valid reading */
        if (s.reading && s.health != OECM_HEALTH_FAILURE)
            screen_heat(s.t10, s.cold10, s.hot10, rgb);
        if (first || level != last_level || memcmp(rgb, last_led, sizeof rgb) != 0) {
            led_set(rgb, level);
            memcpy(last_led, rgb, sizeof rgb);
            last_level = level;
        }
        first = false;
        ulTaskNotifyTake(pdTRUE, pdMS_TO_TICKS(500));
    }
}

void lcd147_start(void) {
    esp_err_t e = led_init();
    if (e != ESP_OK) {
        ESP_LOGW(TAG, "no RGB LED: %s", esp_err_to_name(e));
        led = NULL;
    }
    e = display_init();
    if (e != ESP_OK) {
        ESP_LOGE(TAG, "no display: %s", esp_err_to_name(e));
        return;
    }
    if (xTaskCreate(display_task, "lcd147", 4096, NULL, 3, &task) != pdPASS)
        ESP_LOGE(TAG, "no display task");
    else
        ESP_LOGI(TAG, "display %dx%d, RGB LED %s", SCREEN_W, SCREEN_H, led ? "on GPIO 38" : "none");
}

void lcd147_status(bool on, const char *text) {
    bool changed;
    taskENTER_CRITICAL(&lock);
    changed = on != online || strncmp(text, status, sizeof status - 1) != 0;
    if (changed) {
        online = on;
        strlcpy(status, text, sizeof status);
    }
    taskEXIT_CRITICAL(&lock);
    if (changed && task)
        xTaskNotifyGive(task);
}
#endif
