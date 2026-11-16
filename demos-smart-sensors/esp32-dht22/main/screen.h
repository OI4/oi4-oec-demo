/* The screen of the ESP32-S3-LCD-1.47 variant (lcd147.c), independent of
 * the hardware: it renders the state band by band into RGB565 pixels in
 * the byte order of the panel (big-endian), so that no frame buffer is
 * needed. Within the range of the scale the temperature colour runs from
 * blue (cold end) over green to yellow (hot end); above it is red. */
#ifndef SCREEN_H_INCLUDED
#define SCREEN_H_INCLUDED

#include <stdbool.h>
#include <stdint.h>

#include "oecm.h"

#define SCREEN_W 320
#define SCREEN_H 172
#define SCREEN_LABEL 33
#define SCREEN_STATUS 24

typedef struct screen_state {
    bool reading;  /* at least one good reading */
    oecm_health health; /* NORMAL, OFF_SPEC (above hot10), FAILURE (no readings) */
    bool online;   /* connected to the broker */
    int t10, h10;  /* the latest good reading, tenths of °C and % */
    int cold10, hot10; /* the ends of the temperature scale, tenths of °C */
    char label[SCREEN_LABEL];
    char status[SCREEN_STATUS]; /* the address or the start-up phase */
} screen_state;

/* The colour of a temperature (all in tenths of °C): blue at cold10 and
 * below, then cyan, green, yellow at hot10 (hue 240° … 60°), red above
 * hot10 (out of range) – the colour of the scale at its marker; cold10 <
 * hot10. */
void screen_heat(int t10, int cold10, int hot10, uint8_t rgb[3]);
/* Renders the rows y0 … y0 + rows - 1 of the screen into px (rows *
 * SCREEN_W pixels). */
void screen_render(const screen_state *s, int y0, int rows, uint16_t *px);

#endif
