// The Web UI of the OEC logger demo: the records live by SSE, filtered like
// GET /api/records, the values of the series as SVG lines and the settings
// of the OEC Config (read only). No library, no build step.
"use strict";

const LEVELS = ["Emergency", "Alert", "Critical", "Error", "Warning", "Notice", "Informational", "Debug"];
const SVG = "http://www.w3.org/2000/svg";
const $ = (id) => document.getElementById(id);

let rows = []; // {seq, record}, the newest first
let paused = false;
let queued = []; // records received while paused
let seriesTimer = null;

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined && text !== null) e.textContent = text;
  return e;
}

function filter() {
  const f = new FormData($("filter"));
  return {
    resource: f.get("resource"),
    level: f.get("level"),
    q: f.get("q").trim(),
    limit: Number(f.get("limit")),
  };
}

// The same selection as the server (ui.c): resource, Events of the level
// or more severe, a part of the record JSON in any case.
function matches(record, f) {
  if (f.resource && record.resource !== f.resource) return false;
  if (f.level) {
    const max = LEVELS.indexOf(f.level);
    if (record.resource !== "Event" || LEVELS.indexOf(record.level) > max) return false;
  }
  if (f.q && !JSON.stringify(record).toLowerCase().includes(f.q.toLowerCase())) return false;
  return true;
}

function query(f) {
  const p = new URLSearchParams();
  p.set("limit", String(f.limit));
  if (f.resource) p.set("resource", f.resource);
  if (f.level) p.set("level", f.level);
  if (f.q) p.set("q", f.q);
  return p.toString();
}

function payloadText(p) {
  const s = typeof p === "string" ? p : JSON.stringify(p);
  return s.length > 600 ? s.slice(0, 600) + " …" : s;
}

function row(e, fresh) {
  const r = e.record;
  const tr = el("tr", fresh ? "new" : "");
  const lv = r.level ? "lv-" + LEVELS.indexOf(r.level) : "";
  tr.append(
    el("td", "", e.seq),
    el("td", "", r.time),
    el("td", "", r.resource),
    el("td", lv, r.level || "–"),
    el("td", "", r.app),
    el("td", "", r.source || "–"),
    el("td", "", r.filter || "–"),
    el("td", "payload", payloadText(r.payload)),
  );
  return tr;
}

function render() {
  const body = $("records");
  body.replaceChildren(...rows.map((e) => row(e, false)));
  if (rows.length === 0) {
    const tr = el("tr");
    const td = el("td", "empty", "No records match.");
    td.colSpan = 8;
    tr.append(td);
    body.append(tr);
  }
  $("count").textContent = rows.length + " shown" + (paused && queued.length ? ", " + queued.length + " new while paused" : "");
}

function add(e) {
  const f = filter();
  if (!matches(e.record, f)) return;
  rows.unshift(e);
  if (rows.length > f.limit) rows.length = f.limit;
  const body = $("records");
  if (body.querySelector(".empty")) body.replaceChildren();
  body.prepend(row(e, true));
  while (body.children.length > f.limit) body.lastChild.remove();
  $("count").textContent = rows.length + " shown";
}

async function getJSON(path) {
  const resp = await fetch(path, { headers: { Accept: "application/json" } });
  if (!resp.ok) throw new Error(path + ": " + resp.status);
  return resp.json();
}

async function reload() {
  try {
    const data = await getJSON("/api/records?" + query(filter()));
    rows = data.records.reverse();
    queued = [];
    render();
  } catch (err) {
    $("count").textContent = String(err);
  }
}

function showStatus(s) {
  $("version").textContent = "version " + s.version;
  const state = $("state");
  state.textContent = s.state;
  state.className = "pill " + s.state;
  const list = $("settings-list");
  list.replaceChildren();
  const put = (k, v) => list.append(el("dt", "", k), el("dd", "", v));
  if (s.settings) {
    const st = s.settings;
    const kinds = ["event", "health", "data"].filter((k) => st[k]).map((k) => k[0].toUpperCase() + k.slice(1));
    put("Logs", kinds.length ? kinds.join(", ") : "nothing");
    put("Sources", st.sources.length ? st.sources.join(", ") : "every source");
    put("Events from", st.minEventLevel);
    put("Format", st.format);
    put("File", st.file || "standard output only");
    put("History", st.historySize + " records");
  }
  put("Held", s.records + " records, " + s.series + " series");
}

function sparkline(points) {
  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("viewBox", "0 0 200 48");
  svg.setAttribute("preserveAspectRatio", "none");
  const pts = points.filter((p) => p[1] !== null);
  if (pts.length === 0) return svg;
  const t0 = pts[0][0], t1 = pts[pts.length - 1][0];
  let lo = Math.min(...pts.map((p) => p[1])), hi = Math.max(...pts.map((p) => p[1]));
  if (hi === lo) { hi += 1; lo -= 1; }
  const x = (t) => (t1 === t0 ? 100 : ((t - t0) / (t1 - t0)) * 198 + 1);
  const y = (v) => 46 - ((v - lo) / (hi - lo)) * 44;
  const line = document.createElementNS(SVG, "polyline");
  line.setAttribute("points", pts.map((p) => x(p[0]).toFixed(1) + "," + y(p[1]).toFixed(1)).join(" "));
  svg.append(line);
  const title = document.createElementNS(SVG, "title");
  title.textContent = pts.length + " values, " + lo + " … " + hi;
  svg.append(title);
  return svg;
}

async function loadSeries() {
  seriesTimer = null;
  try {
    const data = await getJSON("/api/series");
    const box = $("series");
    box.replaceChildren();
    if (data.series.length === 0) box.append(el("p", "empty", "No values yet."));
    for (const s of data.series) {
      const card = el("div", "series");
      const last = s.points.length ? s.points[s.points.length - 1][1] : null;
      card.append(
        el("div", "name", [s.resource, s.app, s.source, s.filter].filter(Boolean).join(" · ") + " · " + s.field),
        el("div", "value", last === null ? "–" : String(last)),
        sparkline(s.points),
      );
      box.append(card);
    }
  } catch (err) {
    $("series").replaceChildren(el("p", "empty", String(err)));
  }
}

function seriesSoon() {
  if (!seriesTimer) seriesTimer = setTimeout(loadSeries, 1000);
}

function connect() {
  const live = $("live");
  const es = new EventSource("/api/events");
  es.onopen = () => {
    live.textContent = "live";
    live.className = "pill on";
    reload();
    loadSeries();
  };
  es.onerror = () => {
    live.textContent = "reconnecting";
    live.className = "pill off";
  };
  es.onmessage = (m) => {
    const ev = JSON.parse(m.data);
    if (ev.type === "status") showStatus(ev);
    else if (ev.type === "reset") reload();
    else if (ev.type === "record") {
      const e = { seq: ev.seq, record: ev.record };
      if (paused) {
        queued.push(e);
        $("count").textContent = rows.length + " shown, " + queued.length + " new while paused";
      } else add(e);
      if (ev.record.resource !== "Event") seriesSoon();
    }
  };
}

$("filter").addEventListener("submit", (e) => e.preventDefault());
$("filter").addEventListener("change", (e) => {
  if (e.target.name === "pause") {
    paused = e.target.checked;
    if (!paused) reload();
    return;
  }
  reload();
});
let typing = null;
$("filter").elements.namedItem("q").addEventListener("input", () => {
  clearTimeout(typing);
  typing = setTimeout(reload, 300);
});
connect();
