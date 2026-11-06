// The Web UI of the OEC weather service demo: the stations with their
// current values, a temperature and wind line of the last observations,
// their Health and warnings, the provider and the settings of the OEC
// Config (read only), live by SSE. MET Norway is credited while it
// provides the data (CC BY 4.0). No library, no build step.
"use strict";

const SVG = "http://www.w3.org/2000/svg";
const $ = (id) => document.getElementById(id);

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined && text !== null) e.textContent = text;
  return e;
}

function num(v, digits, unit) {
  return v === null || v === undefined ? "–" : v.toFixed(digits) + (unit ? " " + unit : "");
}

function pill(cls, text) {
  return el("span", "pill " + cls, text);
}

// A link from the state (the addresses come from the service, the page
// has none of its own).
function link(href, text) {
  const a = el("a", "", text);
  a.href = href;
  a.rel = "noopener noreferrer";
  a.target = "_blank";
  return a;
}

function showAttribution(a) {
  const box = $("attribution");
  const credit = $("credit");
  if (!a) {
    box.hidden = true;
    box.replaceChildren();
    credit.replaceChildren();
    return;
  }
  box.hidden = false;
  box.replaceChildren(
    el("strong", "", "Weather data: "),
    document.createTextNode(a.text + " "),
    link(a.sourceUrl, a.source),
    document.createTextNode(" · "),
    link(a.licenseUrl, a.license),
  );
  credit.replaceChildren(document.createTextNode(" · data: MET Norway, "), link(a.licenseUrl, a.license));
}

function showService(s) {
  $("version").textContent = "version " + s.version;
  const state = $("state");
  state.textContent = s.state;
  state.className = "pill " + s.state;
  const health = $("health");
  health.textContent = s.health + " · " + s.healthScore;
  health.className = "pill " + s.health;
  const list = $("service-list");
  list.replaceChildren();
  const put = (k, v) => list.append(el("dt", "", k), el("dd", "", v));
  put("Provider", s.provider);
  put("Poll interval", s.pollIntervalSeconds + " s");
  put("Frost at or below", num(s.frostThreshold, 1, "°C"));
  put("Storm at or above", num(s.stormThreshold, 1, "m/s"));
  put("Health", s.health + ", score " + s.healthScore);
  put("Last poll", s.updated ? new Date(s.updated).toLocaleString() : "not yet");
  showAttribution(s.attribution);
}

// chart draws the temperature and the wind speed of the history, each
// scaled to its own range.
function chart(history) {
  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("viewBox", "0 0 200 60");
  svg.setAttribute("preserveAspectRatio", "none");
  const times = history.map((o) => Date.parse(o.time));
  const t0 = Math.min(...times), t1 = Math.max(...times);
  const x = (t) => (t1 === t0 ? 100 : ((t - t0) / (t1 - t0)) * 198 + 1);
  const ranges = [];
  for (const [field, cls] of [["temperature", "temperature"], ["windSpeed", "wind"]]) {
    const pts = history.map((o, i) => [times[i], o[field]]).filter((p) => p[1] !== null && p[1] !== undefined);
    if (pts.length === 0) continue;
    let lo = Math.min(...pts.map((p) => p[1])), hi = Math.max(...pts.map((p) => p[1]));
    ranges.push(field + " " + lo.toFixed(1) + " … " + hi.toFixed(1));
    if (hi === lo) { hi += 1; lo -= 1; }
    const y = (v) => 58 - ((v - lo) / (hi - lo)) * 56;
    const line = document.createElementNS(SVG, "polyline");
    line.setAttribute("class", cls);
    line.setAttribute("points", pts.map((p) => x(p[0]).toFixed(1) + "," + y(p[1]).toFixed(1)).join(" "));
    svg.append(line);
  }
  const title = document.createElementNS(SVG, "title");
  title.textContent = history.length + " observations: " + ranges.join(", ");
  svg.append(title);
  return svg;
}

function value(label, text) {
  const d = el("div", "value", text);
  d.prepend(el("span", "label", label));
  return d;
}

function station(st) {
  const card = el("div", "station" + (st.failed ? " failed" : ""));
  const title = el("div", "title");
  title.append(el("strong", "", st.index + " · " + st.name), pill(st.health, st.health + " · " + st.healthScore));
  if (st.frost) title.append(pill("frost", "Frost"));
  if (st.storm) title.append(pill("storm", "Storm"));
  card.append(title);
  card.append(el("div", "where", st.latitude + ", " + st.longitude + ", " + st.altitude + " m · " + st.serialNumber));
  if (st.failed) card.append(el("div", "error", "No weather data: " + (st.error || "unknown error")));
  const o = st.observation || {};
  const values = el("div", "values");
  values.append(
    value("Temperature", num(o.temperature, 1, "°C")),
    value("Humidity", num(o.humidity, 0, "%")),
    value("Pressure", num(o.pressure, 1, "hPa")),
    value("Wind", num(o.windSpeed, 1, "m/s")),
    value("from", num(o.windDirection, 0, "°")),
    value("at", o.time ? new Date(o.time).toLocaleTimeString() : "–"),
  );
  card.append(values);
  if (st.history.length > 1) {
    card.append(chart(st.history));
    const legend = el("div", "legend");
    legend.append(el("span", "temperature", "— temperature"), document.createTextNode("  "), el("span", "wind", "— wind speed"),
      document.createTextNode(" · last " + st.history.length + " observations"));
    card.append(legend);
  }
  return card;
}

function show(s) {
  showService(s);
  const box = $("stations");
  box.replaceChildren(...s.stations.map(station));
  if (s.stations.length === 0) box.append(el("p", "empty", "No stations."));
  $("count").textContent = s.stations.length + (s.stations.length === 1 ? " station" : " stations");
}

function connect() {
  const live = $("live");
  const es = new EventSource("/api/events");
  es.onopen = () => {
    live.textContent = "live";
    live.className = "pill on";
  };
  es.onerror = () => {
    live.textContent = "reconnecting";
    live.className = "pill off";
  };
  es.onmessage = (m) => {
    const ev = JSON.parse(m.data);
    if (ev.type === "state") show(ev);
  };
}

connect();
