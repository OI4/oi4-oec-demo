/* The DHT22 (AM2302) climate sensor of the smart sensor demo: Data
 * "Climate" (Temperature, Humidity, Label, read-only), the Config context
 * "Sensor" (Interval 2 … 3600 s, Label) and, with a display (DEMO_LCD147),
 * the Config context "Display" (ColdTemperature, HotTemperature of the
 * temperature scale, Brightness of the RGB LED). The Config values are
 * stored in NVS and survive a restart. */
#ifndef CLIMATE_H
#define CLIMATE_H

#include <stdbool.h>
#include <stddef.h>

#include "oecm.h"
#include "sdkconfig.h"

/* Loads the stored Config values and starts the task that reads the
 * sensor (before the network; after nvs_flash_init). */
void climate_start(void);
/* Sets the Data and Config definitions of cfg; dev is the device the
 * sensor reports to. Waits up to 5 s for the first reading. */
void climate_setup(oecm_config *cfg, oecm_device *dev);
/* Takes over the latest reading: marks the Data changed and sets the
 * Health after three failed readings in a row and on recovery. */
void climate_tick(void);
/* For a local display, from any task: the latest good reading (false
 * before the first one) and whether the Health is FAILURE_1 (three failed
 * rounds of readings in a row). */
bool climate_latest(int *t10, int *h10, bool *failure);
/* For a local display, from any task: a copy of the Label of the Config. */
void climate_label(char *out, size_t cap);
#if CONFIG_DEMO_LCD147
/* For the display, from any task: the ends of the temperature scale in
 * tenths of °C, cold10 < hot10 (the lower of ColdTemperature and
 * HotTemperature is the cold end; equal values span 1 °C), and the
 * brightness of the RGB LED (0 … 255). */
void climate_display(int *cold10, int *hot10, unsigned *led_brightness);
#endif

#endif
