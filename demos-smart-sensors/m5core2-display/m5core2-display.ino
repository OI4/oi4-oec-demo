/*
 * The climate display demo: an M5Stack Core2 as an OEC smart device that
 * shows the Temperature, Humidity, Label and Health of the climate sensors
 * of other OTConnector applications (e.g. the ESP32 DHT22 demo), received
 * through consumer subscriptions of the OEC Micro SDK (climate_view.c).
 * The temperature has the colour of a scale from blue (cold) over green and
 * yellow to red (hot). Button A or a tap on the screen shows the next
 * sensor, the Config Cycle also after a number of seconds. Title, stale
 * time, brightness, cycle and the ends of the scale are set through the
 * Config context "Display".
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

/* The last part of an Identification Link (the SerialNumber). */
static const char *tail(const char *il) {
  const char *t = strrchr(il, '/');
  return t ? t + 1 : il;
}

/* A value in the 7-segment font with its unit in a smaller one. */
static void value(double v, const char *unit, bool degree, int y, uint16_t color) {
  char text[16];
  snprintf(text, sizeof text, "%.1f", v);
  canvas.setTextColor(color);
  canvas.setTextDatum(top_right);
  canvas.setFont(&fonts::Font7);
  canvas.drawString(text, 220, y);
  canvas.setTextDatum(top_left);
  canvas.setFont(&fonts::FreeSansBold18pt7b);
  int x = 232;
  if (degree) {
    canvas.drawCircle(x + 5, y + 6, 5, color);
    canvas.drawCircle(x + 5, y + 6, 4, color);
    x += 14;
  }
  canvas.drawString(unit, x, y);
}

static uint16_t hueColor(int h) {
  uint8_t rgb[3];
  view_hue(h, rgb);
  return M5.Display.color565(rgb[0], rgb[1], rgb[2]);
}

/* A temperature with one decimal, without it if whole. */
static String scaleEnd(int t10) {
  char text[24];
  if (t10 % 10 == 0)
    snprintf(text, sizeof text, "%d", t10 / 10);
  else
    snprintf(text, sizeof text, "%s%d.%d", t10 < 0 ? "-" : "", abs(t10) / 10, abs(t10) % 10);
  return text;
}

/* The colour scale with the ends and a marker at the temperature. */
static void scale(double t, bool stale, int y) {
  const int x0 = 48, w = 224, h = 10;
  for (int x = 0; x < w; x++)
    canvas.drawFastVLine(x0 + x, y, h, hueColor(2400 - 2400 * x / (w - 1)));
  if (!stale) {
    int mx = x0 + view_heat_position(t) * (w - 1) / 1000;
    canvas.fillRect(mx - 2, y - 5, 5, h + 10, TFT_BLACK);
    canvas.fillRect(mx - 1, y - 4, 3, h + 8, TFT_WHITE);
  }
  int cold, hot;
  view_scale(&cold, &hot);
  canvas.setFont(&fonts::Font2);
  canvas.setTextColor(TFT_LIGHTGREY);
  canvas.setTextDatum(middle_right);
  canvas.drawString(scaleEnd(cold), x0 - 12, y + h / 2);
  canvas.drawCircle(x0 - 8, y, 2, TFT_LIGHTGREY);  // the degree sign: Font2 has none
  canvas.setTextDatum(middle_left);
  String high = scaleEnd(hot);
  canvas.drawString(high, x0 + w + 6, y + h / 2);
  canvas.drawCircle(x0 + w + 6 + canvas.textWidth(high) + 4, y, 2, TFT_LIGHTGREY);
}

static void draw(int64_t now) {
  canvas.fillSprite(TFT_BLACK);
  canvas.fillRect(0, 0, 320, 30, 0x18E3);
  canvas.setTextColor(TFT_WHITE);
  canvas.setTextDatum(middle_left);
  canvas.setFont(&fonts::FreeSansBold12pt7b);
  canvas.drawString(view_title(), 8, 15);
  bool on = oecm_connected(&dev);
  canvas.fillCircle(306, 15, 7, on ? TFT_GREEN : TFT_RED);
  size_t n = view_count();
  canvas.setFont(&fonts::Font2);
  canvas.setTextDatum(middle_right);
  if (n)
    canvas.drawString(String(view_shown() + 1) + "/" + n, 290, 15);
  if (n == 0) {
    canvas.setTextDatum(middle_center);
    canvas.setFont(&fonts::FreeSans9pt7b);
    canvas.drawString(on ? "waiting for climate data ..." : "connecting to the broker ...", 160, 110);
    canvas.setFont(&fonts::Font2);
    canvas.drawString(oecm_identification_link(&dev), 160, 150);
    canvas.pushSprite(0, 0);
    return;
  }
  const view_sensor *s = view_at(view_shown());
  int64_t age = (now - s->data_ms) / 1000;
  bool stale = age > (int64_t)view_stale_s();
  canvas.setTextDatum(top_left);
  canvas.setTextColor(TFT_WHITE);
  canvas.setFont(&fonts::FreeSans12pt7b);
  canvas.drawString(s->label[0] ? s->label : tail(s->source), 10, 38);
  int heat = 2400 - 2400 * view_heat_position(s->temperature) / 1000;
  value(s->temperature, "C", true, 66, stale ? TFT_DARKGREY : hueColor(heat));
  scale(s->temperature, stale, 121);
  value(s->humidity, "%", false, 142, stale ? TFT_DARKGREY : TFT_SKYBLUE);
  const char *h = s->health[0] ? s->health : "Health unknown";
  canvas.fillCircle(16, 204, 7, healthColor(s->health));
  canvas.setFont(&fonts::FreeSans9pt7b);
  canvas.setTextColor(TFT_WHITE);
  canvas.setTextDatum(middle_left);
  String line = h;
  if (s->health[0] && s->health_score >= 0)
    line += String("  (") + s->health_score + ")";
  canvas.drawString(line, 30, 204);
  canvas.setFont(&fonts::Font2);
  canvas.setTextColor(stale ? TFT_RED : TFT_LIGHTGREY);
  canvas.drawString(String(tail(s->source)) + "   " + (stale ? "stale, " : "") + (long)age + " s ago", 10, 228);
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

static bool clicked() {
  if (M5.BtnA.wasClicked())
    return true;
  return M5.Touch.getCount() > 0 && M5.Touch.getDetail(0).wasClicked();
}

void loop() {
  M5.update();
  if (!running) {
    delay(100);
    return;
  }
  uint32_t wait = oecm_poll_interval_ms(&dev);
  oecm_esp_dispatch(port, &dev, wait > 50 ? 50 : wait);  // short: buttons and touch stay responsive
  console();
  if (!running)
    return;
  int64_t now = view_now_ms();
  unsigned cycle = view_cycle_s();
  if (clicked() || (cycle && now - shownSince >= (int64_t)cycle * 1000)) {
    view_next();
    shownSince = now;
  }
  oecm_err oe = oecm_poll(&dev);
  if (oe != OECM_OK)
    Serial.printf("oecm_poll: %s\n", oecm_err_str(oe));
  if (view_brightness() != appliedBrightness) {
    appliedBrightness = view_brightness();
    M5.Display.setBrightness(appliedBrightness);
  }
  bool on = oecm_connected(&dev);
  if (view_revision() != drawnRevision || now / 1000 != drawnSecond || on != drawnConnected) {
    drawnRevision = view_revision();
    drawnSecond = now / 1000;
    drawnConnected = on;
    draw(now);
  }
}
