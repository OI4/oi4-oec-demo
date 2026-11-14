/* The 5 x 7 pixel font of the display (font5x7.c): glyph c - 0x20 for the
 * characters 0x20 … 0x7E, FONT_DEGREE (0x7F) the degree sign, 0x80 … 0x86
 * the German letters ä ö ü Ä Ö Ü ß. */
#ifndef FONT5X7_H
#define FONT5X7_H

#include <stdint.h>

#define FONT_GLYPHS 103
#define FONT_DEGREE "\x7f"

extern const uint8_t font5x7[FONT_GLYPHS][7];

#endif
