/* The screen of the ESP32-S3-LCD-1.47 variant (lcd147.c), independent of
 * the hardware: it renders the state band by band into RGB565 pixels in
 * the byte order of the panel (big-endian), so that no frame buffer is
 * needed. The temperature colour runs from blue (cold) to red (hot). */
#ifndef SCREEN_H_INCLUDED
#define SCREEN_H_INCLUDED

#include <stdbool.h>
#include <stdint.h>

#define SCREEN_W 320
#define SCREEN_H 172
#define SCREEN_LABEL 33
#define SCREEN_STATUS 24

typedef struct screen_state {
    bool reading;  /* at least one good reading */
    bool failure;  /* Health FAILURE_1: three failed rounds in a row */
    bool online;   /* connected to the broker */
    int t10, h10;  /* the latest good reading, tenths of °C and % */
    char label[SCREEN_LABEL];
    char status[SCREEN_STATUS]; /* the address or the start-up phase */
} screen_state;

/* The colour of a temperature: blue at cold_c and below, then cyan, green,
 * yellow, red at hot_c and above (hue 240° … 0°); cold_c < hot_c. */
void screen_heat(int t10, int cold_c, int hot_c, uint8_t rgb[3]);
/* Renders the rows y0 … y0 + rows - 1 of the screen into px (rows *
 * SCREEN_W pixels). */
void screen_render(const screen_state *s, int cold_c, int hot_c, int y0, int rows, uint16_t *px);

#endif
