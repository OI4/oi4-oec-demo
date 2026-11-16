/* The display and the RGB LED of the ESP32-S3-LCD-1.47 board (Waveshare
 * and compatible boards; CONFIG_DEMO_LCD147): the readings, the Health and
 * the connection on the 1.47" ST7789 display (screen.c), the temperature
 * as the colour of the RGB LED below it: the colour of the scale at its
 * marker, blue (cold) over green and yellow to red (hot). */
#ifndef LCD147_H
#define LCD147_H

#include <stdbool.h>

/* Starts the display task (after climate_start); without a working
 * display the sensor runs on, with a log message. */
void lcd147_start(void);
/* The connection to the broker and the footer text: the address or the
 * start-up phase. Any task. */
void lcd147_status(bool online, const char *text);

#endif
