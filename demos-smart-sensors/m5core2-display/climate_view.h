/* The consumer side of the display demo (README): the climate sensors of
 * other OTConnector applications, received through two consumer
 * subscriptions of the OEC Micro SDK, the Data "View" (Sensors, Shown,
 * read-only) and the Config context "Display" (Title, StaleAfter,
 * Brightness, Cycle; ColdTemperature and HotTemperature of the temperature
 * scale), stored in NVS. */
#ifndef CLIMATE_VIEW_H
#define CLIMATE_VIEW_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "oecm.h"

#ifdef __cplusplus
extern "C" {
#endif

#define VIEW_SLOTS 6u
#define VIEW_LABEL 33u
#define VIEW_HEALTH 24u

typedef struct view_sensor {
    char source[OECM_IL_BUF]; /* Identification Link of the sensor (Source) */
    double temperature;       /* °C */
    double humidity;          /* % */
    char label[VIEW_LABEL];
    char health[VIEW_HEALTH]; /* "" until the first Health */
    int health_score;
    int64_t data_ms;          /* time of the last Climate, 0: none yet */
} view_sensor;

/* Loads the stored Config values and sets the Data, Config and
 * subscription definitions of cfg; dev is the device that publishes the
 * View. */
void view_setup(oecm_config *cfg, oecm_device *dev);
/* Sensors with Climate data, in the order of their first message. */
size_t view_count(void);
const view_sensor *view_at(size_t i);
/* The shown sensor (index below view_count) and the next one. */
size_t view_shown(void);
void view_next(void);
const char *view_title(void);
unsigned view_stale_s(void);
/* The brightness of the display, 0 … 255. */
uint8_t view_brightness(void);
/* Seconds until the next sensor is shown, 0: never. */
unsigned view_cycle_s(void);
/* The ends of the temperature scale in tenths of °C, cold10 < hot10 (the
 * lower of ColdTemperature and HotTemperature is the cold end; equal values
 * span 1 °C). */
void view_scale(int *cold10, int *hot10);
/* Position of a temperature (°C) on the scale, 0 (cold end) … 1000. */
int view_heat_position(double t);
/* Hue in tenths of a degree, 0 (red) … 2400 (blue), full saturation: the
 * colour of the scale at position p is view_hue(2400 - 2400 * p / 1000). */
void view_hue(int h, uint8_t rgb[3]);
/* Changes with every received value and every accepted Set Config. */
uint32_t view_revision(void);
/* Milliseconds of the monotonic clock of the view. */
int64_t view_now_ms(void);

#ifdef __cplusplus
}
#endif

#endif
