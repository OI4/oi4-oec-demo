/* The screen of the ESP32-S3-LCD-1.47 variant (screen.h), 320 x 172:
 *
 *   header      the Label of the sensor, a dot green (online) or red
 *   temperature large, in the colour of the temperature
 *   scale       the colour scale from the cold to the hot end, a red field
 *               for out of range (above the hot end), a marker
 *   humidity    large, sky blue
 *   footer      the Health and the address (or the start-up phase) */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "font5x7.h"
#include "screen.h"

#define RGB565(r, g, b) ((uint16_t)(((r) & 0xF8) << 8 | ((g) & 0xFC) << 3 | (b) >> 3))
#define BLACK RGB565(0, 0, 0)
#define WHITE RGB565(255, 255, 255)
#define GREY RGB565(110, 110, 110)
#define LIGHT RGB565(180, 180, 180)
#define HEADER RGB565(36, 44, 60)
#define SKY RGB565(110, 190, 255)
#define GREEN RGB565(40, 200, 80)
#define RED RGB565(235, 45, 45)
#define YELLOW RGB565(255, 210, 0)

#define SCALE_X 10
#define SCALE_W 300
#define SCALE_Y 92
#define SCALE_H 8
#define OUT_W 30                         /* the red field: above the hot end */
#define RANGE_W (SCALE_W - OUT_W - 4)    /* the range, cold … hot end */
#define HUE_COLD 2400                    /* blue */
#define HUE_HOT 600                      /* yellow */
#define VALUE_RIGHT 222
#define UNIT_X 232

typedef struct band {
    uint16_t *px;
    int y0, y1; /* rows y0 … y1 - 1 */
} band;

static void fill(const band *b, int x, int y, int w, int h, uint16_t c) {
    int ya = y < b->y0 ? b->y0 : y, yb = y + h > b->y1 ? b->y1 : y + h;
    int xa = x < 0 ? 0 : x, xb = x + w > SCREEN_W ? SCREEN_W : x + w;
    uint16_t be = (uint16_t)(c >> 8 | c << 8);
    for (int yy = ya; yy < yb; yy++)
        for (int xx = xa; xx < xb; xx++)
            b->px[(yy - b->y0) * SCREEN_W + xx] = be;
}

static void disc(const band *b, int cx, int cy, int r, uint16_t c) {
    for (int dy = -r; dy <= r; dy++)
        for (int dx = -r; dx <= r; dx++)
            if (dx * dx + dy * dy <= r * r + r)
                fill(b, cx + dx, cy + dy, 1, 1, c);
}

/* The glyph of the next character of a UTF-8 string (NULL at its end):
 * ASCII, °, ä ö ü Ä Ö Ü ß; any other character is shown as '?'. */
static const uint8_t *next_glyph(const unsigned char **s) {
    static const unsigned char latin[][2] = {{0xA4, 0x80}, {0xB6, 0x81}, {0xBC, 0x82}, {0x84, 0x83},
                                             {0x96, 0x84}, {0x9C, 0x85}, {0x9F, 0x86}};
    unsigned c = *(*s)++;
    if (!c)
        return NULL;
    if (c >= 0x20 && c <= 0x7F)
        return font5x7[c - 0x20];
    unsigned c2 = (**s & 0xC0) == 0x80 ? **s : 0;
    unsigned g = '?';
    if (c == 0xC2 && c2 == 0xB0)
        g = 0x7F;
    for (size_t i = 0; c == 0xC3 && i < sizeof latin / sizeof latin[0]; i++)
        if (latin[i][0] == c2)
            g = latin[i][1];
    while ((**s & 0xC0) == 0x80) /* the rest of the character */
        (*s)++;
    return font5x7[g - 0x20];
}

static int text_width(const char *s, int scale) {
    const unsigned char *p = (const unsigned char *)s;
    int n = 0;
    while (next_glyph(&p))
        n++;
    return n ? n * 6 * scale - scale : 0;
}

static void text(const band *b, int x, int y, int scale, uint16_t c, const char *s) {
    if (y >= b->y1 || y + 7 * scale <= b->y0)
        return;
    const unsigned char *p = (const unsigned char *)s;
    for (const uint8_t *g; (g = next_glyph(&p)) != NULL;) {
        for (int r = 0; r < 7; r++)
            for (int k = 0; k < 5; k++)
                if (g[r] & (0x10 >> k))
                    fill(b, x + k * scale, y + r * scale, scale, scale, c);
        x += 6 * scale;
    }
}

static void text_right(const band *b, int right, int y, int scale, uint16_t c, const char *s) {
    text(b, right - text_width(s, scale), y, scale, c, s);
}

/* Hue in tenths of a degree, 0 (red) … 2400 (blue), full saturation. */
static void hue(int h, uint8_t rgb[3]) {
    int f = (h % 600) * 255 / 600;
    switch (h / 600) {
    case 0: /* red → yellow */
        rgb[0] = 255, rgb[1] = (uint8_t)f, rgb[2] = 0;
        break;
    case 1: /* yellow → green */
        rgb[0] = (uint8_t)(255 - f), rgb[1] = 255, rgb[2] = 0;
        break;
    case 2: /* green → cyan */
        rgb[0] = 0, rgb[1] = 255, rgb[2] = (uint8_t)f;
        break;
    case 3: /* cyan → blue */
        rgb[0] = 0, rgb[1] = (uint8_t)(255 - f), rgb[2] = 255;
        break;
    default:
        rgb[0] = 0, rgb[1] = 0, rgb[2] = 255;
    }
}

/* Position of a temperature on the scale, 0 (cold end) … 1000 (hot end). */
static int heat_position(int t10, int lo, int hi) {
    if (t10 <= lo)
        return 0;
    if (t10 >= hi)
        return 1000;
    return (t10 - lo) * 1000 / (hi - lo);
}

void screen_heat(int t10, int cold10, int hot10, uint8_t rgb[3]) {
    hue(t10 > hot10 ? 0 : HUE_COLD - (HUE_COLD - HUE_HOT) * heat_position(t10, cold10, hot10) / 1000, rgb);
}

static void tenths(char *out, size_t cap, int v10) {
    snprintf(out, cap, "%s%d.%d", v10 < 0 ? "-" : "", abs(v10) / 10, abs(v10) % 10);
}

/* An end of the scale: whole degrees without a decimal. */
static void scale_end(char *out, size_t cap, int v10) {
    if (v10 % 10 == 0)
        snprintf(out, cap, "%d" FONT_DEGREE, v10 / 10);
    else {
        tenths(out, cap - 2, v10);
        strcat(out, FONT_DEGREE);
    }
}

void screen_render(const screen_state *s, int y0, int rows, uint16_t *px) {
    const band b = {.px = px, .y0 = y0, .y1 = y0 + rows};
    bool valid = s->reading && s->health != OECM_HEALTH_FAILURE;
    char t[16];

    fill(&b, 0, y0, SCREEN_W, rows, BLACK);

    fill(&b, 0, 0, SCREEN_W, 30, HEADER);
    char label[SCREEN_LABEL];
    snprintf(label, sizeof label, "%s", s->label);
    for (size_t n = strlen(label); n && text_width(label, 2) > SCREEN_W - 8 - 28;) {
        while (n && ((unsigned char)label[n - 1] & 0xC0) == 0x80) /* a whole UTF-8 character */
            label[--n] = 0;
        if (n)
            label[--n] = 0;
    }
    text(&b, 8, 8, 2, WHITE, label);
    disc(&b, SCREEN_W - 16, 15, 7, s->online ? GREEN : RED);

    uint8_t rgb[3];
    screen_heat(s->t10, s->cold10, s->hot10, rgb);
    uint16_t heat = valid ? RGB565(rgb[0], rgb[1], rgb[2]) : GREY;
    if (s->reading)
        tenths(t, sizeof t, s->t10);
    else
        snprintf(t, sizeof t, "--.-");
    text_right(&b, VALUE_RIGHT, 40, 6, heat, t);
    text(&b, UNIT_X, 40, 3, heat, FONT_DEGREE "C");

    for (int x = 0; x < RANGE_W; x++) {
        hue(HUE_COLD - (HUE_COLD - HUE_HOT) * x / (RANGE_W - 1), rgb);
        fill(&b, SCALE_X + x, SCALE_Y, 1, SCALE_H, RGB565(rgb[0], rgb[1], rgb[2]));
    }
    hue(0, rgb);
    fill(&b, SCALE_X + SCALE_W - OUT_W, SCALE_Y, OUT_W, SCALE_H, RGB565(rgb[0], rgb[1], rgb[2]));
    if (valid) {
        int mx = s->t10 > s->hot10 ? SCALE_X + SCALE_W - OUT_W / 2
                                   : SCALE_X + heat_position(s->t10, s->cold10, s->hot10) * (RANGE_W - 1) / 1000;
        fill(&b, mx - 2, SCALE_Y - 5, 5, SCALE_H + 10, BLACK);
        fill(&b, mx - 1, SCALE_Y - 4, 3, SCALE_H + 8, WHITE);
    }
    scale_end(t, sizeof t, s->cold10);
    text(&b, SCALE_X, SCALE_Y + SCALE_H + 5, 1, LIGHT, t);
    scale_end(t, sizeof t, s->hot10);
    text_right(&b, SCALE_X + RANGE_W, SCALE_Y + SCALE_H + 5, 1, LIGHT, t);

    if (s->reading)
        tenths(t, sizeof t, s->h10);
    else
        snprintf(t, sizeof t, "--.-");
    uint16_t hum = valid ? SKY : GREY;
    text_right(&b, VALUE_RIGHT, 118, 4, hum, t);
    text(&b, UNIT_X, 125, 3, hum, "%");

    /* the Health in words, the dot in its NE107 colour (MG 8.3.2, Table 13) */
    const char *health = "no reading yet";
    uint16_t dot = GREY;
    if (s->health == OECM_HEALTH_FAILURE)
        health = "Failure", dot = RED;
    else if (s->reading && s->health == OECM_HEALTH_OFF_SPEC)
        health = "Off spec", dot = YELLOW;
    else if (s->reading)
        health = "Normal", dot = GREEN;
    disc(&b, 14, 161, 6, dot);
    text(&b, 28, 154, 2, WHITE, health);
    text_right(&b, SCREEN_W - 8, 158, 1, LIGHT, s->status);
}
