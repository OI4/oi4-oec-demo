/* A small JSON reader and writer for the logger demo (ADR-0028 §7): the
 * logger reads Payloads and Config proposals and writes JSON records;
 * no library is needed for that. The reader works on spans of the input
 * text and never allocates. */
#ifndef LOGGER_JSON_H
#define LOGGER_JSON_H

#include <stdbool.h>
#include <stddef.h>
#include <stdio.h>

typedef enum lj_type {
    LJ_NULL,
    LJ_FALSE,
    LJ_TRUE,
    LJ_NUMBER,
    LJ_STRING,
    LJ_OBJECT,
    LJ_ARRAY,
} lj_type;

/* A JSON value: its type and the span of its text (strings with quotes). */
typedef struct lj_value {
    lj_type type;
    const char *p;
    size_t n;
} lj_value;

/* The maximum nesting of arrays and objects the reader accepts. */
#define LJ_MAX_DEPTH 64

/* Parses the JSON text s (RFC 8259: one value, whitespace around it, NUL
 * terminated); false if it is no valid JSON text. */
bool lj_parse(const char *s, lj_value *out);

/* Iterates over the members of an object, in order. */
typedef struct lj_iter {
    const char *cur;
} lj_iter;

/* Starts an iteration; false if v is no object. */
bool lj_object_begin(const lj_value *v, lj_iter *it);

/* The next member: true with key (a string) and value, false at the end. */
bool lj_object_next(lj_iter *it, lj_value *key, lj_value *val);

/* The first member of the object v named key; false if absent or v is no
 * object. */
bool lj_member(const lj_value *v, const char *key, lj_value *out);

/* The value of a number. */
bool lj_number(const lj_value *v, double *out);

/* Decodes a string into buf (NUL terminated, UTF-8); returns its length
 * without the NUL, or (size_t)-1 if v is no string, it contains a NUL
 * character or it does not fit into len octets. */
size_t lj_string(const lj_value *v, char *buf, size_t len);

/* Whether v is the string s. */
bool lj_string_eq(const lj_value *v, const char *s);

/* Writes s as JSON string with quotes; NULL as null. */
void lj_write_string(FILE *f, const char *s);

#endif
