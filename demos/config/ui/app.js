// The Web UI of the OEC config service (ADR-0028 §3, A-304): the
// applications live by the event stream, a form per Config from its
// definition, a Set Config for the changed values of a writable Config and
// the result (the Status event) of every Set. Sensitive values stay masked:
// the form shows the mask, an empty field keeps the value.
"use strict";

(() => {
  const MASK = "********";
  const $ = (id) => document.getElementById(id);

  let apps = [];
  let connected = false;
  let chosen = null;
  let sets = [];
  let nextId = 1;
  // Config forms of the chosen application by key: {json, form, notice}.
  const forms = new Map();
  // The result of the last Set from a form by key: [text, class].
  const results = new Map();

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = String(text);
    return e;
  }

  // The text of a LocalizedText (or of a plain string).
  function txt(v) {
    if (v === undefined || v === null) return "";
    if (typeof v === "object") return typeof v.Text === "string" ? v.Text : "";
    return String(v);
  }

  function appKey(a) { return a.serviceType + "\n" + a.app; }

  function appName(a) {
    const model = a.mam ? txt(a.mam.Model) : "";
    return model || a.app;
  }

  function pill(text, cls) { return el("span", "pill " + (cls || ""), text); }

  function jsonBody(body) {
    return { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  }

  async function answer(resp) {
    let data = null;
    try { data = await resp.json(); } catch (e) { data = { error: "no JSON answer" }; }
    return { status: resp.status, data };
  }

  async function postSet(body) { return answer(await fetch("/api/set", jsonBody(body))); }
  async function postRefresh(body) { return answer(await fetch("/api/refresh", jsonBody(body))); }

  // The result of a Set record (or of a refused request) as [text, good].
  function outcome(rec) {
    if (!rec) return ["no answer", false];
    if (rec.status) {
      const code = "0x" + (rec.status.code >>> 0).toString(16).toUpperCase().padStart(8, "0");
      return [rec.status.symbol + " (" + code + ")", rec.status.good];
    }
    return ["failed: " + (rec.error || "unknown"), false];
  }

  function valuesText(values) {
    const parts = [];
    for (const [group, objs] of Object.entries(values || {})) {
      for (const [name, v] of Object.entries(objs || {})) parts.push(group + "/" + name + " = " + v);
    }
    return parts.join(", ");
  }

  // ---- applications ----

  function renderHeader() {
    const s = $("state");
    s.textContent = connected ? "bus online" : "bus offline";
    s.className = "pill " + (connected ? "online" : "offline");
    $("count").textContent = "(" + apps.length + ")";
  }

  function renderApps() {
    const list = $("apps");
    list.replaceChildren();
    if (apps.length === 0) list.append(el("li", "empty", "None found yet."));
    for (const a of apps) {
      const b = el("button", appKey(a) === chosen ? "chosen" : "");
      b.type = "button";
      b.append(el("span", "name", appName(a)), el("span", "il", a.serviceType + " · " + a.app));
      const pills = el("span", "pills");
      pills.append(pill(a.state, a.state));
      if (a.health) pills.append(pill(a.health + (a.healthScore !== null ? " " + a.healthScore : ""), a.health));
      pills.append(pill(a.configs.length + " Config" + (a.configs.length === 1 ? "" : "s")));
      b.append(pills);
      b.addEventListener("click", () => {
        if (chosen !== appKey(a)) {
          forms.clear();
          results.clear();
        }
        chosen = appKey(a);
        render();
      });
      const li = el("li");
      li.append(b);
      list.append(li);
    }
  }

  function info(dl, label, value) {
    if (value === undefined || value === null || value === "") return;
    dl.append(el("dt", "", label), el("dd", "", value));
  }

  function renderDetail() {
    const panel = $("detail");
    const a = apps.find((x) => appKey(x) === chosen);
    if (!a) {
      panel.replaceChildren(el("p", "empty", chosen ? "The application is no longer listed." : "No application chosen."));
      return;
    }
    const title = el("div", "title");
    title.append(el("h2", "", appName(a)), pill(a.state, a.state));
    if (a.health) title.append(pill(a.health, a.health));
    const again = el("button", "", "Read again");
    again.type = "button";
    again.addEventListener("click", async () => {
      again.disabled = true;
      try {
        const r = await postRefresh({ serviceType: a.serviceType, app: a.app });
        again.title = r.status === 200 ? "" : txt(r.data && r.data.error);
      } catch (e) {
        again.title = String(e);
      }
      again.disabled = false;
    });
    title.append(again);

    const dl = el("dl");
    const m = a.mam || {};
    info(dl, "ServiceType", a.serviceType);
    info(dl, "Application", a.app);
    info(dl, "Manufacturer", txt(m.Manufacturer));
    info(dl, "Model", txt(m.Model));
    info(dl, "ProductCode", m.ProductCode);
    info(dl, "SerialNumber", m.SerialNumber);
    info(dl, "SoftwareRevision", m.SoftwareRevision);
    info(dl, "HardwareRevision", m.HardwareRevision);
    if (a.health) info(dl, "Health", a.health + (a.healthScore !== null ? " (score " + a.healthScore + ")" : ""));
    const res = Array.isArray(a.profile) ? a.profile : null;
    if (res) info(dl, "Profile", res.join(", "));
    info(dl, "SubscriptionList", a.subscriptionList ? "read" : "not read");
    if (a.discoveredMs) info(dl, "Discovered", new Date(a.discoveredMs).toLocaleString());

    const parts = [title, dl];
    if (a.errors && a.errors.length) {
      const ul = el("ul", "errors");
      for (const e of a.errors) ul.append(el("li", "", e));
      parts.push(ul);
    }
    parts.push(el("h2", "", "Configs"));
    if (a.configs.length === 0) parts.push(el("p", "empty", "No Config found."));
    const seen = new Set();
    const nodes = [];
    for (const c of a.configs) {
      const key = appKey(a) + "\n" + c.source + "\n" + c.context;
      seen.add(key);
      nodes.push(configForm(a, c, key));
    }
    for (const k of [...forms.keys()]) if (!seen.has(k)) forms.delete(k);

    // The forms stay in place when unchanged (an input keeps its focus).
    let head = $("detail-head");
    let list = $("detail-configs");
    if (!head || !list) {
      head = el("div");
      head.id = "detail-head";
      list = el("div");
      list.id = "detail-configs";
      panel.replaceChildren(head, list);
    }
    head.replaceChildren(...parts);
    const now = [...list.children];
    if (now.length !== nodes.length || nodes.some((n, i) => n !== now[i])) list.replaceChildren(...nodes);
  }

  // The form of a Config: kept while the Config is unchanged, and while
  // the user edits it (with a notice when the application changed it).
  function configForm(a, c, key) {
    const json = JSON.stringify(c);
    const have = forms.get(key);
    if (have && have.json === json) return have.form;
    if (have && have.form.dataset.dirty === "1") {
      have.notice.replaceChildren();
      const reload = el("button", "", "Show the new values");
      reload.type = "button";
      reload.addEventListener("click", () => {
        forms.delete(key);
        render();
      });
      have.notice.append(el("span", "", "The application changed this Config. "), reload);
      return have.form;
    }
    const entry = { json, ...buildForm(a, c, key) };
    forms.set(key, entry);
    return entry.form;
  }

  function buildForm(a, c, key) {
    const form = el("form", "config" + (c.writable ? "" : " readonly"));
    const config = c.config || {};
    const head = el("div", "head");
    const name = config.Context ? txt(config.Context.Name) : "";
    const h3 = el("h3", "", name || c.context);
    h3.append(" ", el("small", "", "context " + c.context + (c.source !== a.app ? " · source " + c.source : "")));
    head.append(h3, pill(c.writable ? "writable" : "read only", c.writable ? "writable" : ""));
    form.append(head);
    if (config.Context && config.Context.Description) form.append(el("p", "hint", txt(config.Context.Description)));

    for (const [g, group] of Object.entries(config)) {
      if (g === "Context" || !group || typeof group !== "object") continue;
      const fs = el("fieldset");
      fs.append(el("legend", "", txt(group.Name) || g));
      if (group.Description) fs.append(el("p", "hint", txt(group.Description)));
      for (const [n, obj] of Object.entries(group)) {
        if (n === "Name" || n === "Description" || !obj || typeof obj !== "object") continue;
        fs.append(field(g, n, obj, c.writable));
      }
      form.append(fs);
    }

    const notice = el("p", "notice");
    form.append(notice);
    if (!c.writable) {
      form.append(el("p", "hint", "Read only: the SubscriptionList of the application lists no Set topic of this Config."));
      return { form, notice };
    }
    const actions = el("div", "actions");
    const send = el("button", "primary", "Send Set Config");
    send.type = "submit";
    const undo = el("button", "", "Undo changes");
    undo.type = "reset";
    const result = el("span", "result");
    const show = (text, cls) => {
      result.textContent = text;
      result.className = "result " + cls;
      results.set(key, [text, cls]);
    };
    if (results.has(key)) show(...results.get(key));
    actions.append(send, undo, result);
    form.append(actions);
    form.addEventListener("input", () => { form.dataset.dirty = "1"; });
    form.addEventListener("reset", () => { form.dataset.dirty = "0"; notice.replaceChildren(); });
    form.addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const values = {};
      for (const input of form.querySelectorAll("[data-name]")) {
        const v = input.value;
        const changed = input.dataset.sensitive === "1" ? v !== "" : v !== input.dataset.original;
        if (!changed) continue;
        (values[input.dataset.group] = values[input.dataset.group] || {})[input.dataset.name] = v;
      }
      if (Object.keys(values).length === 0) {
        show("Nothing changed.", "");
        return;
      }
      send.disabled = true;
      show("sending …", "");
      try {
        const r = await postSet({ serviceType: a.serviceType, app: a.app, source: c.source, context: c.context, values });
        if (r.data && r.data.id !== undefined) {
          const [text, good] = outcome(r.data);
          show("Set " + r.data.id + ": " + text, good ? "good" : "bad");
          addSet(r.data);
        } else {
          show("Refused (" + r.status + "): " + txt(r.data && r.data.error), "bad");
        }
      } catch (e) {
        show("Failed: " + e, "bad");
      }
      send.disabled = false;
      // The next update of the Config shows its new values.
      form.dataset.dirty = "0";
    });
    return { form, notice };
  }

  function field(group, name, obj, writable) {
    const row = el("div", "object");
    const id = "f" + nextId++;
    const label = el("label", "", txt(obj.Name) || name);
    label.htmlFor = id;
    if (obj.Mandatory) label.append(" *");
    const v = typeof obj.Value === "string" ? obj.Value : "";
    const val = obj.Validation || {};
    const sensitive = obj.Sensitive === true;
    let input;
    if (!writable) {
      input = el("input");
      input.type = "text";
      input.value = v;
      input.disabled = true;
    } else if (sensitive) {
      if (obj.Type === "Boolean") {
        input = select(["", "true", "false"], "", { "": MASK + " (unchanged)" });
      } else {
        input = el("input");
        input.type = "password";
        input.autocomplete = "new-password";
        input.placeholder = MASK + " (unchanged)";
        input.value = "";
      }
      input.dataset.sensitive = "1";
    } else if (Array.isArray(val.Values) && val.Values.length) {
      input = select(val.Values, v);
    } else if (obj.Type === "Boolean") {
      input = select(["true", "false"], v);
    } else {
      input = el("input");
      input.type = obj.Type === "Number" ? "number" : "text";
      if (obj.Type === "Number") {
        input.step = "any";
        if (typeof val.Min === "number") input.min = String(val.Min);
        if (typeof val.Max === "number") input.max = String(val.Max);
      }
      if (obj.Type === "DateTime") input.placeholder = "2026-01-01T00:00:00Z";
      if (typeof val.Length === "number" && obj.Type !== "Number") input.maxLength = val.Length;
      input.value = v;
    }
    input.id = id;
    if (writable) {
      input.dataset.group = group;
      input.dataset.name = name;
      input.dataset.original = sensitive ? "" : v;
    }
    row.append(label, input);

    const about = [];
    if (obj.Description) about.push(txt(obj.Description));
    about.push(obj.Type || "?");
    if (obj.Unit) about.push("unit " + obj.Unit);
    if (typeof val.Min === "number" || typeof val.Max === "number") {
      about.push("range " + (typeof val.Min === "number" ? val.Min : "…") + " … " + (typeof val.Max === "number" ? val.Max : "…"));
    }
    if (typeof val.Length === "number") about.push("length ≤ " + val.Length);
    if (val.Pattern) about.push("pattern " + val.Pattern);
    if (typeof obj.DefaultValue === "string" && obj.DefaultValue !== "") about.push("default " + obj.DefaultValue);
    if (sensitive) about.push("sensitive: masked, empty keeps the value");
    if (obj.Mandatory) about.push("mandatory");
    row.append(el("span", "about", about.join(" · ")));
    return row;
  }

  function select(options, current, labels) {
    const s = el("select");
    const all = options.includes(current) ? options : [current, ...options];
    for (const o of all) {
      const opt = el("option", "", labels && labels[o] !== undefined ? labels[o] : o);
      opt.value = o;
      s.append(opt);
    }
    s.value = current;
    return s;
  }

  // ---- Set results ----

  function addSet(rec) {
    if (!rec || sets.some((s) => s.id === rec.id)) return;
    sets.unshift(rec);
    sets.sort((x, y) => y.id - x.id);
    sets = sets.slice(0, 100);
    renderSets();
  }

  function renderSets() {
    const body = $("sets");
    body.replaceChildren();
    if (sets.length === 0) {
      const tr = el("tr");
      const td = el("td", "empty", "No Set yet.");
      td.colSpan = 5;
      tr.append(td);
      body.append(tr);
    }
    for (const s of sets) {
      const a = apps.find((x) => x.serviceType === s.serviceType && x.app === s.app);
      const [text, good] = outcome(s);
      const result = el("td");
      result.append(pill(text, good ? "good" : "bad"));
      const tr = el("tr");
      tr.append(
        el("td", "", new Date(s.timeMs).toLocaleTimeString()),
        el("td", "", a ? appName(a) : s.app),
        el("td", "mono", s.context),
        el("td", "mono", valuesText(s.values)),
        result,
      );
      body.append(tr);
    }
  }

  async function loadSets() {
    try {
      const resp = await fetch("/api/sets");
      const data = await resp.json();
      for (const s of data.sets || []) addSet(s);
      renderSets();
    } catch (e) {
      // the event stream reconnects and loads them again
    }
  }

  function render() {
    renderHeader();
    renderApps();
    renderDetail();
  }

  // ---- live ----

  function live(on) {
    const l = $("live");
    l.textContent = on ? "live" : "reconnecting";
    l.className = "pill " + (on ? "on" : "off");
  }

  function connect() {
    const es = new EventSource("/api/events");
    es.addEventListener("open", () => { live(true); loadSets(); });
    es.addEventListener("error", () => live(false));
    es.addEventListener("message", (ev) => {
      let d;
      try { d = JSON.parse(ev.data); } catch (e) { return; }
      if (d.type === "apps") {
        apps = Array.isArray(d.apps) ? d.apps : [];
        connected = d.connected === true;
        if (chosen === null && apps.length) chosen = appKey(apps[0]);
        render();
        renderSets();
      } else if (d.type === "set") {
        addSet(d.set);
      }
    });
  }

  $("refresh-all").addEventListener("click", async (ev) => {
    const b = ev.currentTarget;
    b.disabled = true;
    try {
      const r = await postRefresh({});
      b.title = r.status === 200 ? "" : txt(r.data && r.data.error);
    } catch (e) {
      b.title = String(e);
    }
    b.disabled = false;
  });

  renderSets();
  connect();
})();
