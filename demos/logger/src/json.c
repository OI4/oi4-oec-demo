#include "json.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

/* ---- validation ---------------------------------------------------------- */

static const char *ws(const char *p) {
    while (*p == ' ' || *p == '\t' || *p == '\n' || *p == '\r')
        p++;
    return p;
}

static int hex(char c) {
    if (c >= '0' && c <= '9')
        return c - '0';
    if (c >= 'a' && c <= 'f')
        return c - 'a' + 10;
    if (c >= 'A' && c <= 'F')
        return c - 'A' + 10;
    return -1;
}

static bool hex4(const char *p, unsigned *out) {
    unsigned v = 0;
    for (int i = 0; i < 4; i++) {
        int h = hex(p[i]);
        if (h < 0)
            return false;
        v = v << 4 | (unsigned)h;
    }
    *out = v;
    return true;
}

/* The length of the UTF-8 sequence at p (RFC 3629), 0 if invalid. */
static size_t utf8_len(const unsigned char *p) {
    unsigned char c = p[0];
    if (c < 0x80)
        return 1;
    if (c >= 0xc2 && c <= 0xdf)
        return (p[1] & 0xc0) == 0x80 ? 2 : 0;
    if (c >= 0xe0 && c <= 0xef) {
        unsigned char lo = c == 0xe0 ? 0xa0 : 0x80, hi = c == 0xed ? 0x9f : 0xbf;
        return p[1] >= lo && p[1] <= hi && (p[2] & 0xc0) == 0x80 ? 3 : 0;
    }
    if (c >= 0xf0 && c <= 0xf4) {
        unsigned char lo = c == 0xf0 ? 0x90 : 0x80, hi = c == 0xf4 ? 0x8f : 0xbf;
        return p[1] >= lo && p[1] <= hi && (p[2] & 0xc0) == 0x80 && (p[3] & 0xc0) == 0x80 ? 4
                                                                                           : 0;
    }
    return 0;
}

/* Scans a string starting at the quote; returns the end after the closing
 * quote or NULL. Escaped surrogates must form pairs. */
static const char *scan_string(const char *p) {
    p++;
    for (;;) {
        unsigned char c = (unsigned char)*p;
        if (c == '"')
            return p + 1;
        if (c < 0x20)
            return NULL; /* control character or end of text */
        if (c == '\\') {
            p++;
            switch (*p) {
            case '"': case '\\': case '/': case 'b': case 'f': case 'n': case 'r': case 't':
                p++;
                break;
            case 'u': {
                unsigned u, lo;
                if (!hex4(p + 1, &u))
                    return NULL;
                p += 5;
                if (u >= 0xdc00 && u <= 0xdfff)
                    return NULL;
                if (u >= 0xd800 && u <= 0xdbff) {
                    if (p[0] != '\\' || p[1] != 'u' || !hex4(p + 2, &lo) || lo < 0xdc00 ||
                        lo > 0xdfff)
                        return NULL;
                    p += 6;
                }
                break;
            }
            default:
                return NULL;
            }
            continue;
        }
        size_t n = utf8_len((const unsigned char *)p);
        if (!n)
            return NULL;
        p += n;
    }
}

static bool digit(char c) { return c >= '0' && c <= '9'; }

static const char *scan_number(const char *p) {
    if (*p == '-')
        p++;
    if (*p == '0')
        p++;
    else if (*p >= '1' && *p <= '9')
        while (digit(*p))
            p++;
    else
        return NULL;
    if (*p == '.') {
        p++;
        if (!digit(*p))
            return NULL;
        while (digit(*p))
            p++;
    }
    if (*p == 'e' || *p == 'E') {
        p++;
        if (*p == '+' || *p == '-')
            p++;
        if (!digit(*p))
            return NULL;
        while (digit(*p))
            p++;
    }
    return p;
}

/* Scans one value at p (no leading whitespace); returns its end or NULL. */
static const char *scan_value(const char *p, int depth, lj_type *type) {
    switch (*p) {
    case 'n':
        *type = LJ_NULL;
        return strncmp(p, "null", 4) == 0 ? p + 4 : NULL;
    case 't':
        *type = LJ_TRUE;
        return strncmp(p, "true", 4) == 0 ? p + 4 : NULL;
    case 'f':
        *type = LJ_FALSE;
        return strncmp(p, "false", 5) == 0 ? p + 5 : NULL;
    case '"':
        *type = LJ_STRING;
        return scan_string(p);
    case '[':
    case '{': {
        bool obj = *p == '{';
        char close = obj ? '}' : ']';
        lj_type t;
        *type = obj ? LJ_OBJECT : LJ_ARRAY;
        if (depth >= LJ_MAX_DEPTH)
            return NULL;
        p = ws(p + 1);
        if (*p == close)
            return p + 1;
        for (;;) {
            if (obj) {
                if (*p != '"' || !(p = scan_string(p)))
                    return NULL;
                p = ws(p);
                if (*p != ':')
                    return NULL;
                p = ws(p + 1);
            }
            if (!(p = scan_value(p, depth + 1, &t)))
                return NULL;
            p = ws(p);
            if (*p == close)
                return p + 1;
            if (*p != ',')
                return NULL;
            p = ws(p + 1);
        }
    }
    default:
        *type = LJ_NUMBER;
        return scan_number(p);
    }
}

bool lj_parse(const char *s, lj_value *out) {
    const char *p = ws(s);
    const char *end = scan_value(p, 0, &out->type);
    if (!end || *ws(end))
        return false;
    out->p = p;
    out->n = (size_t)(end - p);
    return true;
}

/* ---- access -------------------------------------------------------------- */

bool lj_object_begin(const lj_value *v, lj_iter *it) {
    if (v->type != LJ_OBJECT)
        return false;
    it->cur = ws(v->p + 1);
    return true;
}

/* Values are validated: scanning cannot fail here. */
bool lj_object_next(lj_iter *it, lj_value *key, lj_value *val) {
    const char *p = it->cur;
    if (*p == '}')
        return false;
    key->type = LJ_STRING;
    key->p = p;
    p = scan_string(p);
    key->n = (size_t)(p - key->p);
    p = ws(ws(p) + 1);
    val->p = p;
    p = scan_value(p, 0, &val->type);
    val->n = (size_t)(p - val->p);
    p = ws(p);
    if (*p == ',')
        p = ws(p + 1);
    it->cur = p;
    return true;
}

bool lj_member(const lj_value *v, const char *key, lj_value *out) {
    lj_iter it;
    lj_value k, val;
    if (!lj_object_begin(v, &it))
        return false;
    while (lj_object_next(&it, &k, &val)) {
        if (lj_string_eq(&k, key)) {
            *out = val;
            return true;
        }
    }
    return false;
}

bool lj_number(const lj_value *v, double *out) {
    char buf[64];
    if (v->type != LJ_NUMBER || v->n >= sizeof buf)
        return false;
    memcpy(buf, v->p, v->n);
    buf[v->n] = 0;
    *out = strtod(buf, NULL);
    return true;
}

static size_t put_utf8(char *dst, uint32_t cp) {
    if (cp < 0x80) {
        dst[0] = (char)cp;
        return 1;
    }
    if (cp < 0x800) {
        dst[0] = (char)(0xc0 | cp >> 6);
        dst[1] = (char)(0x80 | (cp & 0x3f));
        return 2;
    }
    if (cp < 0x10000) {
        dst[0] = (char)(0xe0 | cp >> 12);
        dst[1] = (char)(0x80 | (cp >> 6 & 0x3f));
        dst[2] = (char)(0x80 | (cp & 0x3f));
        return 3;
    }
    dst[0] = (char)(0xf0 | cp >> 18);
    dst[1] = (char)(0x80 | (cp >> 12 & 0x3f));
    dst[2] = (char)(0x80 | (cp >> 6 & 0x3f));
    dst[3] = (char)(0x80 | (cp & 0x3f));
    return 4;
}

size_t lj_string(const lj_value *v, char *buf, size_t len) {
    if (v->type != LJ_STRING || len == 0)
        return (size_t)-1;
    const char *p = v->p + 1, *end = v->p + v->n - 1;
    size_t n = 0;
    while (p < end) {
        char tmp[4];
        size_t k;
        if (*p != '\\') {
            tmp[0] = *p++;
            k = 1;
        } else {
            p++;
            char c = *p++;
            switch (c) {
            case 'b': tmp[0] = '\b'; k = 1; break;
            case 'f': tmp[0] = '\f'; k = 1; break;
            case 'n': tmp[0] = '\n'; k = 1; break;
            case 'r': tmp[0] = '\r'; k = 1; break;
            case 't': tmp[0] = '\t'; k = 1; break;
            case 'u': {
                unsigned u, lo;
                hex4(p, &u);
                p += 4;
                uint32_t cp = u;
                if (u >= 0xd800 && u <= 0xdbff) {
                    hex4(p + 2, &lo);
                    p += 6;
                    cp = 0x10000 + ((uint32_t)(u - 0xd800) << 10) + (lo - 0xdc00);
                }
                if (cp == 0)
                    return (size_t)-1;
                k = put_utf8(tmp, cp);
                break;
            }
            default: tmp[0] = c; k = 1; break;
            }
        }
        if (n + k >= len)
            return (size_t)-1;
        memcpy(buf + n, tmp, k);
        n += k;
    }
    buf[n] = 0;
    return n;
}

bool lj_string_eq(const lj_value *v, const char *s) {
    char buf[256];
    size_t n = strlen(s);
    if (n >= sizeof buf)
        return false;
    size_t got = lj_string(v, buf, sizeof buf);
    return got == n && memcmp(buf, s, n) == 0;
}

/* ---- writer -------------------------------------------------------------- */

void lj_write_string(FILE *f, const char *s) {
    if (!s) {
        fputs("null", f);
        return;
    }
    fputc('"', f);
    for (const unsigned char *p = (const unsigned char *)s; *p; p++) {
        switch (*p) {
        case '"': fputs("\\\"", f); break;
        case '\\': fputs("\\\\", f); break;
        case '\n': fputs("\\n", f); break;
        case '\r': fputs("\\r", f); break;
        case '\t': fputs("\\t", f); break;
        default:
            if (*p < 0x20)
                fprintf(f, "\\u%04x", *p);
            else
                fputc(*p, f);
        }
    }
    fputc('"', f);
}
