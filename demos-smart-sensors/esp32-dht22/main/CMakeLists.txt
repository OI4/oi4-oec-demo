# lcd147.c, screen.c and font5x7.c: the variant of the ESP32-S3-LCD-1.47
# board (CONFIG_DEMO_LCD147); without it lcd147.c is empty and the linker
# drops the rest.
idf_component_register(SRCS "main.c" "climate.c" "lcd147.c" "screen.c" "font5x7.c"
    PRIV_REQUIRES oecm nvs_flash esp_netif esp_event esp_wifi esp_driver_gpio esp_driver_uart
                  esp_app_format esp_hw_support esp_timer esp_lcd esp_driver_spi esp_driver_rmt)
