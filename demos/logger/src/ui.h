/* The Web UI of the logger demo (ADR-0028 §3, A-303): the embedded static
 * files, the JSON resources GET /api/status, /api/records and /api/series
 * and the event stream GET /api/events (SSE). Read only: the settings
 * stay with the OEC Config of the logger. No SDK needed (unit tests). */
#ifndef LOGGER_UI_H
#define LOGGER_UI_H

#include <stdbool.h>
#include <stddef.h>

#include "core.h"
#include "history.h"

/* The address of OEC_UI_LISTEN when unset and the SSE keep-alive period
 * (OEC_UI_KEEPALIVE_MS shortens it for tests). */
#define LG_UI_DEFAULT_LISTEN "127.0.0.1:8781"
#define LG_UI_KEEPALIVE_MS 15000

/* The connection state of the logger for the status ("online" …). */
typedef const char *(*lg_ui_state_fn)(void *user_data);

typedef struct lg_ui lg_ui;

/* Binds the Web UI to address; NULL with a message in err. */
lg_ui *lg_ui_listen(const char *address, char *err, size_t errlen);

/* The bound address. */
const char *lg_ui_address(const lg_ui *ui);

/* Starts serving the history; state may be called from any thread.
 * false if no thread. */
bool lg_ui_start(lg_ui *ui, lg_history *h, const char *version, unsigned keepalive_ms,
                 lg_ui_state_fn state, void *user_data);

/* The settings now in force (after every apply). */
void lg_ui_settings(lg_ui *ui, const lg_settings *s);

/* Stops the server and frees ui; NULL is ignored. */
void lg_ui_stop(lg_ui *ui);

/* The embedded files (ui_assets.c, generated from ui/). */
typedef struct lg_asset {
    const char *path;
    const char *content_type;
    const unsigned char *data;
    size_t size;
} lg_asset;
extern const lg_asset lg_ui_assets[];
extern const size_t lg_ui_asset_count;

#endif
