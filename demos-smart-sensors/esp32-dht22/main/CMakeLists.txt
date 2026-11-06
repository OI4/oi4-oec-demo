idf_component_register(SRCS "main.c" "climate.c"
    PRIV_REQUIRES oecm nvs_flash esp_netif esp_event esp_wifi esp_driver_gpio esp_driver_uart
                  esp_app_format esp_hw_support esp_timer)
