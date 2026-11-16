/*
 * The climate display demo: an M5Stack Core2 as an OEC smart device that
 * shows the Temperature, Humidity, Label and Health of the climate sensors
 * of other OTConnector applications (e.g. the ESP32 DHT22 demo), received
 * through consumer subscriptions of the OEC Micro SDK (climate_view.c).
 * The temperature has the colour of a scale from blue (cold) over green and
 * yellow to red (hot), next to it a graph of its history (the latest point
 * flashes with each new value), a trend arrow, the humidity and the dew
 * point. Button A, a tap or a swipe to the left
 * shows the next sensor, a swipe to the right the previous one, the Config
 * Cycle also after a number of seconds. Title, stale time, brightness,
 * cycle, the time of the graph and the ends of the scale are set through
 * the Config context "Display".
 *
 * Provisioning as the DHT22 demo (README): the NVS partition "oecprov",
 * namespace "oec", strings uri, ca, user + password or cert + key,
 * optional serial (default: the base MAC address), ntp (default
 * pool.ntp.org), wifi_ssid, wifi_pass; u8 level (2 or 3, default 3);
 * u32 max_packet. The persisted records of Level 3 live in "nvs".
 *
 * Serial console (115200 Bd): "stop" ends the device gracefully (Close,
 * DISCONNECT) and prints "oecm: stopped" (restart hooks of D7).
 */
#include <M5Unified.h>
#include <WiFi.h>
#include <esp_chip_info.h>
#include <esp_mac.h>
#include <nvs.h>
#include <nvs_flash.h>
#include <time.h>

#include <OECMicro.h>

#include "climate_view.h"

#define MODEL "OEC Micro M5Stack Core2 Display"
#define PRODUCT_CODE "OECM-CORE2-DISPLAY"
#define VERSION "0.1.0"
#define TIME_PLAUSIBLE 1672531200

static oecm_device dev;
static uint8_t tx[8192];
static char refdes[2048];
static oecm_config cfg;
static oecm_esp_config pc;
static oecm_esp *port;
static oecm_pal pal;
static char serial[13];
static char hw[16];
static uint8_t level = 3;
static bool running;
static M5Canvas canvas(&M5.Display);
static uint32_t drawnRevision = UINT32_MAX;
static int64_t drawnSecond = -1;
static bool drawnConnected;
static uint8_t appliedBrightness;
static int64_t shownSince;
static String consoleLine;
static int64_t pulseUntil;  // a ring spreads from the latest point of the graph until then
static int64_t drawnMs;
static const int PULSE_MS = 1200;

/* A provisioning string (heap copy, NULL if absent). */
static char *provStr(nvs_handle_t h, const char *key) {
  size_t n = 0;
  if (nvs_get_str(h, key, nullptr, &n) != ESP_OK || n == 0)
    return nullptr;
  char *s = static_cast<char *>(malloc(n));
  if (s && nvs_get_str(h, key, s, &n) != ESP_OK) {
    free(s);
    return nullptr;
  }
  return s;
}

static void message(const char *head, const String &text) {
  canvas.fillSprite(TFT_BLACK);
  canvas.setTextColor(TFT_WHITE);
  canvas.setTextDatum(top_left);
  canvas.setFont(&fonts::FreeSansBold12pt7b);
  canvas.drawString(head, 10, 10);
  canvas.setFont(&fonts::Font2);
  int y = 50;
  int start = 0;
  while (start <= (int)text.length()) {
    int end = text.indexOf('\n', start);
    if (end < 0)
      end = text.length();
    canvas.drawString(text.substring(start, end), 10, y);
    y += 20;
    start = end + 1;
  }
  canvas.pushSprite(0, 0);
}

static void fail(const char *what, const char *why) {
  message("OEC Climate Display", String(what) + ":\n" + why);
  Serial.printf("%s: %s\n", what, why);
  running = false;
}

static const uint16_t CARD = 0x10A2;   // background of the cards
static const uint16_t EDGE = 0x31A6;   // guide lines, inactive page dots
static const uint16_t MUTED = 0x9CF3;  // small labels

static uint16_t healthColor(const char *h) {
  if (strcmp(h, "NORMAL_0") == 0)
    return TFT_GREEN;
  if (strcmp(h, "FAILURE_1") == 0)
    return TFT_RED;
  if (strcmp(h, "CHECK_FUNCTION_2") == 0)
    return TFT_CYAN;
  if (strcmp(h, "OFF_SPEC_3") == 0)
    return TFT_ORANGE;
  if (strcmp(h, "MAINTENANCE_REQUIRED_4") == 0)
    return TFT_YELLOW;
  return TFT_DARKGREY;
}

/* A Health state as words: "NORMAL_0" → "Normal", "OFF_SPEC_3" → "Off spec". */
static String healthName(const char *h) {
  String out;
  size_t n = strlen(h);
  while (n && isdigit((unsigned char)h[n - 1]))
    n--;
  if (n && n < strlen(h) && h[n - 1] == '_')
    n--;
  for (size_t i = 0; i < n; i++)
    out += h[i] == '_' ? ' ' : (char)(i ? tolower((unsigned char)h[i]) : h[i]);
  return out;
}

/* The last part of an Identification Link (the SerialNumber). */
static const char *tail(const char *il) {
  const char *t = strrchr(il, '/');
  return t ? t + 1 : il;
}

static uint16_t hueColor(int h) {
  uint8_t rgb[3];
  view_hue(h, rgb);
  return M5.Display.color565(rgb[0], rgb[1], rgb[2]);
}

/* The colour of the scale at a temperature in tenths of °C. */
static uint16_t heatColor(int t10) {
  return hueColor(2400 - 2400 * view_heat_position(t10 / 10.0) / 1000);
}

/* c mixed with the card background, pct % of c. */
static uint16_t fade(uint16_t c, int pct) {
  int r = ((c >> 11) & 31) * pct + ((CARD >> 11) & 31) * (100 - pct);
  int g = ((c >> 5) & 63) * pct + ((CARD >> 5) & 63) * (100 - pct);
  int b = (c & 31) * pct + (CARD & 31) * (100 - pct);
  return (uint16_t)(((r / 100) << 11) | ((g / 100) << 5) | (b / 100));
}

/* A temperature with one decimal, without it if whole. */
static String tenths(int t10, bool trim) {
  char text[24];
  if (trim && t10 % 10 == 0)
    snprintf(text, sizeof text, "%d", t10 / 10);
  else
    snprintf(text, sizeof text, "%s%d.%d", t10 < 0 ? "-" : "", abs(t10) / 10, abs(t10) % 10);
  return text;
}

/* Text in Font2 with a degree sign after it (the font has none), from x
 * or, if right, ending at x. */
static void withDegree(const String &text, int x, int y, bool right, uint16_t color) {
  canvas.setFont(&fonts::Font2);
  canvas.setTextColor(color);
  canvas.setTextDatum(top_left);
  int w = canvas.textWidth(text);
  int left = right ? x - w - 6 : x;
  canvas.drawString(text, left, y);
  canvas.drawCircle(left + w + 3, y + 3, 2, color);
}

/* A small filled triangle pointing up (dir < 0), down (> 0) or right (0). */
static void arrow(int cx, int cy, int r, int dir, uint16_t color) {
  if (dir < 0)
    canvas.fillTriangle(cx, cy - r, cx - r, cy + r - 2, cx + r, cy + r - 2, color);
  else if (dir > 0)
    canvas.fillTriangle(cx, cy + r, cx - r, cy - r + 2, cx + r, cy - r + 2, color);
  else
    canvas.fillTriangle(cx + r, cy, cx - r + 2, cy - r, cx - r + 2, cy + r, color);
}

/* The temperature of s, with linear interpolation, at second t of the view
 * clock; false in a gap of the history or outside it. */
static bool historyAt(const view_sensor *s, double t, uint32_t gap, double *t10) {
  for (size_t i = 1; i < s->history_count; i++) {
    const view_point *a = view_history_at(s, i - 1), *b = view_history_at(s, i);
    if (t < a->s || t > b->s)
      continue;
    if (b->s - a->s > gap)
      return false;
    *t10 = a->t10 + (b->t10 - a->t10) * (t - a->s) / (double)(b->s - a->s ? b->s - a->s : 1);
    return true;
  }
  return false;
}

/* The temperature graph of s in a card: the area below the curve in the
 * colour of the scale, fading into the card, the curve, the latest point,
 * the highest and lowest value and the time shown. */
static void graph(const view_sensor *s, bool stale, int64_t now) {
  const int cx = 8, cy = 66, cw = 146, ch = 104;
  const int x0 = cx + 6, x1 = cx + cw - 7, y0 = cy + 24, y1 = cy + ch - 22;
  canvas.fillSmoothRoundRect(cx, cy, cw, ch, 10, CARD);
  uint32_t span = view_history_s();
  canvas.setFont(&fonts::Font2);
  canvas.setTextColor(MUTED);
  canvas.setTextDatum(top_right);
  canvas.drawString(span % 3600 ? String(span / 60) + " min" : String(span / 3600) + " h", x1, cy + 5);
  uint32_t sec = (uint32_t)(now / 1000);
  int lo = INT_MAX, hi = INT_MIN;
  size_t first = s->history_count;
  for (size_t i = 0; i < s->history_count; i++) {
    const view_point *p = view_history_at(s, i);
    if (sec - p->s > span)
      continue;
    if (first == s->history_count)
      first = i;
    if (p->t10 < lo)
      lo = p->t10;
    if (p->t10 > hi)
      hi = p->t10;
  }
  uint32_t step = span / (VIEW_HISTORY - 8);
  uint32_t gap = view_stale_s();
  if (gap < 3 * (step ? step : 1))
    gap = 3 * (step ? step : 1);
  double start = (double)sec - span, edge = 0;  // the curve begins at the left edge
  bool edged = sec > span && historyAt(s, start, gap, &edge);
  if (edged && first != s->history_count) {
    if (edge < lo)
      lo = (int)floor(edge);
    if (edge > hi)
      hi = (int)ceil(edge);
  }
  if (first == s->history_count) {
    canvas.setTextDatum(middle_center);
    canvas.drawString("collecting ...", cx + cw / 2, (y0 + y1) / 2);
    return;
  }
  arrow(x0 + 4, cy + 12, 4, -1, MUTED);
  withDegree(tenths(hi, false), x0 + 12, cy + 5, false, MUTED);
  arrow(x0 + 4, y1 + 12, 4, 1, MUTED);
  withDegree(tenths(lo, false), x0 + 12, y1 + 5, false, MUTED);
  int vlo = lo, vhi = hi;
  if (vhi - vlo < 10) {  // at least 1 °C high
    int mid = (vlo + vhi) / 2;
    vlo = mid - 5;
    vhi = mid + 5;
  }
  auto ypos = [&](double t10) { return y1 - (int)lround((t10 - vlo) * (y1 - y0) / (vhi - vlo)); };
  for (int x = x0; x <= x1; x += 4)  // dotted guide lines at the highest and lowest value
    for (int y : {ypos(hi), ypos(lo)})
      canvas.drawPixel(x, y, EDGE);
  auto xpos = [&](uint32_t t) { return x1 - (int)lround((double)(sec - t) * (x1 - x0) / span); };
  canvas.setClipRect(x0, y0 - 6, x1 - x0 + 1, y1 - y0 + 13);
  for (int x = x0; x <= x1; x++) {
    double t10;
    if (!historyAt(s, sec - (double)(x1 - x) * span / (x1 - x0), gap, &t10))
      continue;
    int y = ypos(t10);
    uint16_t c = stale ? TFT_DARKGREY : heatColor((int)lround(t10));
    canvas.drawGradientVLine(x, y, y1 - y + 1, fade(c, 45), CARD);
  }
  for (size_t i = (first ? first - 1 : 0) + 1; i < s->history_count; i++) {
    const view_point *a = view_history_at(s, i - 1), *b = view_history_at(s, i);
    if (b->s - a->s > gap)
      continue;
    bool cut = a->s < start;  // drawWideLine ignores the clip rectangle
    if (cut && !edged)
      continue;
    canvas.drawWideLine(cut ? x0 : xpos(a->s), ypos(cut ? edge : a->t10), xpos(b->s), ypos(b->t10), 1.3f,
                        stale ? TFT_DARKGREY : heatColor((a->t10 + b->t10) / 2));
  }
  canvas.clearClipRect();
  const view_point *last = view_history_at(s, s->history_count - 1);
  int lx = xpos(last->s), ly = ypos(last->t10);
  int64_t since = now - s->data_ms;
  bool pulse = !stale && since >= 0 && since < PULSE_MS;
  if (pulse) {  // a new value: a ring spreads from the point and fades
    pulseUntil = s->data_ms + PULSE_MS;
    float p = since / (float)PULSE_MS;
    int r = 6 + (int)(p * 10);
    canvas.setClipRect(cx, cy, cw, ch);
    canvas.fillArc(lx, ly, r, r - 2, 0, 360, fade(heatColor(last->t10), (int)(100 * (1 - p) * (1 - p))));
    canvas.clearClipRect();
  }
  bool flash = pulse && since < PULSE_MS / 3;
  canvas.fillSmoothCircle(lx, ly, flash ? 6 : 5, TFT_WHITE);
  if (!flash)
    canvas.fillSmoothCircle(lx, ly, 3, stale ? TFT_DARKGREY : heatColor(last->t10));
}

/* Rising (-1), falling (1) or steady (0) over the last five minutes, 2 if
 * the history is too short. */
static int trend(const view_sensor *s) {
  if (s->history_count < 2)
    return 2;
  const view_point *last = view_history_at(s, s->history_count - 1);
  const view_point *then = nullptr;
  for (size_t i = s->history_count - 1; i-- > 0;) {
    then = view_history_at(s, i);
    if (last->s - then->s >= 300)
      break;
  }
  if (last->s - then->s < 60)
    return 2;
  int d = last->t10 - then->t10;
  return d >= 3 ? -1 : d <= -3 ? 1 : 0;
}

/* The temperature, its trend and below it the colour scale, as wide as the
 * number, with a knob at the temperature and the ends of the scale. */
static void temperature(const view_sensor *s, bool stale) {
  const int left = 162, right = 312;
  int t10 = (int)lround(s->temperature * 10);
  uint16_t color = stale ? TFT_DARKGREY : heatColor(t10);
  String text = tenths(t10, false);
  canvas.setFont(&fonts::Font7);
  int tw = canvas.textWidth(text);
  int xs = (left + right) / 2 - (tw + 30) / 2;
  canvas.setTextColor(color);
  canvas.setTextDatum(top_left);
  canvas.drawString(text, xs, 72);
  int ux = xs + tw + 6;
  canvas.drawCircle(ux + 5, 77, 4, color);
  canvas.drawCircle(ux + 5, 77, 3, color);
  canvas.setFont(&fonts::FreeSansBold12pt7b);
  canvas.drawString("C", ux + 11, 72);
  int dir = trend(s);
  if (dir != 2 && !stale)
    arrow(ux + 15, 110, 7, dir, dir ? color : MUTED);
  const int by = 132, bh = 8, r = bh / 2;
  for (int i = 0; i < tw; i++) {
    int d = i < tw - 1 - i ? i : tw - 1 - i;
    int inset = d < r ? r - (int)lround(sqrt((double)r * r - (r - d - 0.5) * (r - d - 0.5))) : 0;
    uint16_t c = hueColor(2400 - 2400 * i / (tw - 1));
    canvas.drawFastVLine(xs + i, by + inset, bh - 2 * inset, stale ? fade(c, 35) : c);
  }
  if (!stale) {
    int mx = xs + view_heat_position(s->temperature) * (tw - 1) / 1000;
    canvas.fillSmoothCircle(mx, by + r, 8, TFT_BLACK);
    canvas.fillSmoothCircle(mx, by + r, 7, TFT_WHITE);
    canvas.fillSmoothCircle(mx, by + r, 4, color);
  }
  int cold, hot;
  view_scale(&cold, &hot);
  withDegree(tenths(cold, true), xs, by + bh + 6, false, MUTED);
  withDegree(tenths(hot, true), xs + tw, by + bh + 6, true, MUTED);
}

/* Dew point (Magnus formula, °C) of a temperature and relative humidity. */
static double dewPoint(double t, double rh) {
  if (rh <= 0)
    return NAN;
  double g = log(rh / 100) + 17.62 * t / (243.12 + t);
  return 243.12 * g / (17.62 - g);
}

/* A card with an icon, a small caption and a value with its unit (after a
 * degree sign if degree). */
static void tile(int x, const char *caption, const String &val, const char *unit, bool degree,
                 uint16_t color, void (*icon)(int, int, uint16_t)) {
  const int y = 178, w = 146, h = 40;
  canvas.fillSmoothRoundRect(x, y, w, h, 10, CARD);
  icon(x + 16, y + h / 2, color);
  canvas.setFont(&fonts::Font2);
  canvas.setTextColor(MUTED);
  canvas.setTextDatum(top_left);
  canvas.drawString(caption, x + 32, y + 3);
  canvas.setFont(&fonts::FreeSansBold12pt7b);
  canvas.setTextColor(color);
  canvas.drawString(val, x + 32, y + 19);
  int ux = x + 32 + canvas.textWidth(val) + 4;
  if (degree) {
    canvas.drawCircle(ux + 3, y + 23, 3, color);
    ux += 9;
  }
  canvas.setFont(&fonts::FreeSans9pt7b);
  canvas.drawString(unit, ux, y + 22);
}

static void droplet(int x, int y, uint16_t c) {
  canvas.fillSmoothCircle(x, y + 4, 6, c);
  canvas.fillTriangle(x, y - 9, x - 6, y + 3, x + 6, y + 3, c);
}

static void thermometer(int x, int y, uint16_t c) {
  canvas.fillSmoothRoundRect(x - 2, y - 10, 5, 14, 2, c);
  canvas.fillSmoothCircle(x, y + 6, 4, c);
}

/* text at (x, y), middle left, in the largest font that fits into width,
 * shortened with "..." if even the smallest does not. */
static void fitted(const String &text, int x, int y, int width) {
  canvas.setTextDatum(middle_left);
  canvas.setTextColor(TFT_WHITE);
  canvas.setFont(&fonts::FreeSans12pt7b);
  if (canvas.textWidth(text) > width)
    canvas.setFont(&fonts::FreeSans9pt7b);
  String t = text;
  while (t.length() > 1 && canvas.textWidth(t) > width) {
    t.remove(t.length() - (t.endsWith("...") ? 4 : 1));
    t.trim();
    t += "...";
  }
  canvas.drawString(t, x, y);
}

static void draw(int64_t now) {
  canvas.fillSprite(TFT_BLACK);
  canvas.fillRect(0, 0, 320, 28, 0x18E3);
  canvas.setTextColor(TFT_WHITE);
  canvas.setTextDatum(middle_left);
  canvas.setFont(&fonts::FreeSansBold12pt7b);
  canvas.drawString(view_title(), 8, 14);
  bool on = oecm_connected(&dev);
  canvas.fillSmoothCircle(306, 14, 6, on ? TFT_GREEN : TFT_RED);
  size_t n = view_count();
  unsigned cycle = view_cycle_s();
  if (cycle && n > 1)  // time until the next sensor
  {
    int64_t w = (now - shownSince) * 320 / (cycle * 1000LL);
    canvas.fillRect(0, 26, w > 320 ? 320 : (int)w, 2, EDGE);
  }
  if (n == 0) {
    canvas.setTextDatum(middle_center);
    canvas.setFont(&fonts::FreeSans9pt7b);
    canvas.drawString(on ? "waiting for climate data ..." : "connecting to the broker ...", 160, 110);
    canvas.setFont(&fonts::Font2);
    canvas.setTextColor(MUTED);
    String il = oecm_identification_link(&dev);  // one segment per line
    for (int y = 140, from = 0; from <= (int)il.length(); y += 18) {
      int slash = il.indexOf('/', from);
      if (slash < 0)
        slash = il.length();
      canvas.drawString(il.substring(from, slash), 160, y);
      from = slash + 1;
    }
    canvas.pushSprite(0, 0);
    return;
  }
  const view_sensor *s = view_at(view_shown());
  int64_t age = (now - s->data_ms) / 1000;
  bool stale = age > (int64_t)view_stale_s();

  canvas.setFont(&fonts::Font2);
  String chip = s->health[0] ? healthName(s->health) : String("Health unknown");
  if (s->health[0] && s->health_score >= 0)
    chip += String("  ") + s->health_score;
  int cw = canvas.textWidth(chip) + 32;
  canvas.fillSmoothRoundRect(312 - cw, 36, cw, 22, 11, CARD);
  canvas.fillSmoothCircle(312 - cw + 12, 47, 5, healthColor(s->health));
  canvas.setTextColor(TFT_WHITE);
  canvas.setTextDatum(middle_left);
  canvas.drawString(chip, 312 - cw + 23, 47);
  fitted(s->label[0] ? s->label : tail(s->source), 10, 47, 312 - cw - 18);

  graph(s, stale, now);
  temperature(s, stale);
  tile(8, "Humidity", String(s->humidity, 1), "%", false, stale ? TFT_DARKGREY : TFT_SKYBLUE, droplet);
  double dp = dewPoint(s->temperature, s->humidity);
  tile(166, "Dew point", isnan(dp) ? String("-") : String(dp, 1), isnan(dp) ? "" : "C", !isnan(dp),
       stale ? TFT_DARKGREY : TFT_WHITE, thermometer);

  canvas.setFont(&fonts::Font2);
  canvas.setTextColor(stale ? TFT_RED : MUTED);
  canvas.setTextDatum(middle_left);
  canvas.drawString(String(tail(s->source)) + "   " + (stale ? "stale, " : "") + (long)age + " s ago", 10, 230);
  for (size_t i = 0; n > 1 && i < n; i++)
    canvas.fillSmoothCircle(308 - (int)(n - 1 - i) * 12, 230, i == view_shown() ? 4 : 3,
                            i == view_shown() ? TFT_WHITE : EDGE);
  canvas.pushSprite(0, 0);
}

static void console() {
  while (Serial.available()) {
    char c = (char)Serial.read();
    if (c != '\n' && c != '\r') {
      if (consoleLine.length() < 48)
        consoleLine += c;
      continue;
    }
    if (consoleLine == "stop") {
      oecm_err oe = oecm_stop(&dev);
      Serial.printf("oecm: stopped (%s)\n", oecm_err_str(oe));
      Serial.flush();
      message("OEC Climate Display", "stopped");
      running = false;
    }
    consoleLine = "";
  }
}

void setup() {
  M5.begin(M5.config());
  Serial.begin(115200);
  canvas.setPsram(true);
  canvas.createSprite(320, 240);
  message("OEC Climate Display", "reading the provisioning ...");

  nvs_handle_t prov;
  if (nvs_flash_init_partition("oecprov") != ESP_OK ||
      nvs_open_from_partition("oecprov", "oec", NVS_READONLY, &prov) != ESP_OK)
    return fail("provisioning", "partition oecprov missing (README)");
  pc.uri = provStr(prov, "uri");
  pc.ca_pem = provStr(prov, "ca");
  pc.username = provStr(prov, "user");
  pc.password = provStr(prov, "password");
  pc.client_cert_pem = provStr(prov, "cert");
  pc.client_key_pem = provStr(prov, "key");
  char *serialProv = provStr(prov, "serial");
  char *ntp = provStr(prov, "ntp");
  char *ssid = provStr(prov, "wifi_ssid");
  char *pass = provStr(prov, "wifi_pass");
  (void)nvs_get_u8(prov, "level", &level);
  if (level < 2 || level > 3)
    level = 3; /* the consumer subscriptions need Level 2 */
  uint32_t maxPacket = 0;
  (void)nvs_get_u32(prov, "max_packet", &maxPacket);
  nvs_close(prov);
  if (!ssid)
    return fail("provisioning", "wifi_ssid missing");

  message("OEC Climate Display", String("Wi-Fi ") + ssid + " ...");
  WiFi.mode(WIFI_STA);
  WiFi.begin(ssid, pass ? pass : "");
  while (WiFi.status() != WL_CONNECTED)
    delay(500);
  message("OEC Climate Display", WiFi.localIP().toString() + "\nwaiting for the time");
  configTime(0, 0, ntp ? ntp : "pool.ntp.org");
  while (time(nullptr) < TIME_PLAUSIBLE)
    delay(500);

  if (serialProv) {
    strlcpy(serial, serialProv, sizeof serial);
  } else {
    uint8_t mac[6];
    esp_read_mac(mac, ESP_MAC_BASE);
    snprintf(serial, sizeof serial, "%02X%02X%02X%02X%02X%02X", mac[0], mac[1], mac[2], mac[3], mac[4],
             mac[5]);
  }
  esp_chip_info_t chip;
  esp_chip_info(&chip);
  snprintf(hw, sizeof hw, "v%u.%u", chip.revision / 100, chip.revision % 100);

  cfg.level = (oecm_level)level;
  cfg.service_type = OECM_ST_OT_CONNECTOR;  // a directly connected smart device (SF-102)
  cfg.max_packet_size = maxPacket;
  cfg.mam.manufacturer = "OI4";
  cfg.mam.manufacturer_uri = "oi4.org";
  cfg.mam.model = MODEL;
  cfg.mam.product_code = PRODUCT_CODE;
  cfg.mam.hardware_revision = hw;
  cfg.mam.software_revision = VERSION;
  cfg.mam.device_manual = "https://oi4.org";
  cfg.mam.device_class = "Display";
  cfg.mam.serial_number = serial;
  cfg.mam.revision_counter = -1;

  esp_err_t e = oecm_esp_init(&pc, &port, &pal);
  if (e != ESP_OK)
    return fail("oecm_esp_init", esp_err_to_name(e));
  view_setup(&cfg, &dev);
  appliedBrightness = view_brightness();
  M5.Display.setBrightness(appliedBrightness);
  if (level >= 3) {
    cfg.refdes_buf = refdes;
    cfg.refdes_cap = sizeof refdes;
  }
  oecm_err oe = oecm_init(&dev, &cfg, &pal, tx, sizeof tx);
  if (oe == OECM_OK)
    oe = oecm_start(&dev);
  if (oe != OECM_OK)
    return fail("oecm", oecm_err_str(oe));
  Serial.printf("device %s (Level %u), broker %s\n", oecm_identification_link(&dev), level, pc.uri);
  running = true;
}

/* Next sensor (1): button A, a tap or a swipe to the left; the previous
 * one (-1): a swipe to the right. */
static int gesture() {
  if (M5.BtnA.wasClicked())
    return 1;
  if (M5.Touch.getCount() == 0)
    return 0;
  const auto &t = M5.Touch.getDetail(0);
  if (t.wasFlicked())
    return t.distanceX() < 0 ? 1 : -1;
  return t.wasClicked() ? 1 : 0;
}

void loop() {
  M5.update();
  if (!running) {
    delay(100);
    return;
  }
  uint32_t wait = oecm_poll_interval_ms(&dev);
  bool pulsing = view_now_ms() < pulseUntil + 40;  // one more frame without the ring
  if (wait > (pulsing ? 10u : 50u))  // short: buttons, touch and the pulse stay responsive
    wait = pulsing ? 10 : 50;
  oecm_esp_dispatch(port, &dev, wait);
  console();
  if (!running)
    return;
  int64_t now = view_now_ms();
  unsigned cycle = view_cycle_s();
  int g = gesture();
  if (g < 0)
    view_prev();
  else if (g > 0 || (cycle && now - shownSince >= (int64_t)cycle * 1000))
    view_next();
  if (g || (cycle && now - shownSince >= (int64_t)cycle * 1000))
    shownSince = now;
  oecm_err oe = oecm_poll(&dev);
  if (oe != OECM_OK)
    Serial.printf("oecm_poll: %s\n", oecm_err_str(oe));
  if (view_brightness() != appliedBrightness) {
    appliedBrightness = view_brightness();
    M5.Display.setBrightness(appliedBrightness);
  }
  bool on = oecm_connected(&dev);
  if (view_revision() != drawnRevision || now / 1000 != drawnSecond || on != drawnConnected ||
      (pulsing && now - drawnMs >= 40)) {
    drawnMs = now;
    drawnRevision = view_revision();
    drawnSecond = now / 1000;
    drawnConnected = on;
    draw(now);
  }
}
