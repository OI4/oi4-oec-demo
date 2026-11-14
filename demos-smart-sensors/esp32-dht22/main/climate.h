/* The DHT22 (AM2302) climate sensor of the smart sensor demo: Data
 * "Climate" (Temperature, Humidity, Label, read-only) and the Config
 * context "Sensor" (Interval 2 … 3600 s, Label). */
#ifndef CLIMATE_H
#define CLIMATE_H

#include <stdbool.h>
#include <stddef.h>

#include "oecm.h"

/* Starts the task that reads the sensor (before the network). */
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

#endif
