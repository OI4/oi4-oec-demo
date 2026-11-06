'use strict';
// An OEC 2.0 application of Messaging Level 2 (sans-IO): the application
// itself and the assets it represents as Sources, each with MAM, Health,
// Profile, Config, Data, Metadata, Event and PublicationList; the
// SubscriptionList of the application. Every method returns the
// publications to send ({topic, payload}); the caller does the MQTT I/O.

const oec = require('./oec');

const { CLASS, DSW, STATUS, OecError, TAG } = oec;

const PROFILE_ASSET = Object.freeze(['MAM', 'Health', 'Config', 'Data', 'Metadata', 'Event', 'Profile', 'PublicationList']);
const PROFILE_APP = Object.freeze([...PROFILE_ASSET, 'SubscriptionList']);
const SERVED = new Set(['MAM', 'Health', 'Profile', 'Config', 'Data', 'Metadata', 'PublicationList', 'SubscriptionList']);
const STATUS_LEVELS = Object.freeze(['Good', 'Uncertain', 'Bad']);
const DEFAULT_MAX_PACKET_SIZE = 262144;
const MAX_U32 = 4294967295;

function nonEmpty(value, what) {
  if (typeof value !== 'string' || value.trim() === '') throw new OecError(`${what} is mandatory`);
  return value;
}

/**
 * The Master Asset Model of the application or an asset (MG 5) from the
 * settings of a node: manufacturer, manufacturerUri, model, productCode,
 * serialNumber and the optional revisions, deviceManual, deviceClass,
 * description, revisionCounter and identificationLink (an Identification
 * Link the asset already has; otherwise the Oi4Identifier is generated).
 */
function masterAssetModel(s, defaults = {}) {
  const v = { ...defaults, ...Object.fromEntries(Object.entries(s || {}).filter(([, x]) => x !== '' && x != null)) };
  nonEmpty(v.manufacturer, 'Manufacturer');
  nonEmpty(v.manufacturerUri, 'ManufacturerUri');
  if (/[:/\s]/.test(v.manufacturerUri)) throw new OecError('ManufacturerUri: a domain name without scheme, e.g. example.com');
  nonEmpty(v.model, 'Model');
  nonEmpty(v.productCode, 'ProductCode');
  nonEmpty(v.serialNumber, 'SerialNumber');
  const il = v.identificationLink || oec.identificationLink(v.manufacturerUri, v.model, v.productCode, v.serialNumber);
  if ([...il].length > 255) throw new OecError('the Identification Link exceeds 255 characters');
  const counter = v.revisionCounter == null ? -1 : Number(v.revisionCounter);
  if (!Number.isInteger(counter) || counter < -1 || counter > 2147483647) throw new OecError('RevisionCounter: an Int32 from -1 on');
  return {
    Manufacturer: oec.localizedText(v.manufacturer),
    ManufacturerUri: v.manufacturerUri,
    Model: oec.localizedText(v.model),
    ProductCode: v.productCode,
    HardwareRevision: v.hardwareRevision ?? '',
    SoftwareRevision: v.softwareRevision ?? '',
    DeviceRevision: v.deviceRevision ?? '',
    DeviceManual: v.deviceManual ?? '',
    DeviceClass: v.deviceClass ?? '',
    SerialNumber: v.serialNumber,
    ProductInstanceUri: il,
    RevisionCounter: counter,
    Description: oec.localizedText(v.description ?? ''),
  };
}

function checkName(value, what) {
  if (typeof value !== 'string' || !TAG.test(value) || value.length > 128) {
    throw new OecError(`${what} ${JSON.stringify(value)}: letters, digits, "_" and "-" (at most 128)`);
  }
  return value;
}

function checkHealth(state, score) {
  if (!oec.HEALTH_STATES.includes(state)) throw new OecError(`Health: one of ${oec.HEALTH_STATES.join(', ')}`);
  if (!Number.isInteger(score) || score < 0 || score > 100) throw new OecError('HealthScore: an integer 0 … 100');
}

/** The group of a SubscriptionList entry (oec-core sublist.rs group_of). */
function groupOf(topicPath) {
  const lv = topicPath.split('/');
  const resource = lv[4];
  if (!resource || resource === '+' || resource === '#' || lv.slice(0, 4).includes('#')) return null;
  const filter = lv.slice(6);
  const wild = lv[5] === '#' || filter.some((l) => l === '+' || l === '#');
  return filter.length === 0 || wild ? resource : `${resource}/${filter.join('/')}`;
}

class Provider {
  /**
   * @param {object} o
   * @param {string} o.serviceType  ServiceType of the application (MG 7.2)
   * @param {object} o.mam          the MAM settings (see masterAssetModel)
   * @param {number} [o.maxPacketSize] MaxPacketSize of broker.json
   * @param {string} [o.syslogLevel] the most verbose Syslog level published
   *   by default (REQ-TOP-024/025: Debug never, Informational better not)
   * @param {Function} [o.clock]   ms since the epoch
   */
  constructor({ serviceType, mam, maxPacketSize = DEFAULT_MAX_PACKET_SIZE, syslogLevel = 'Notice', clock = Date.now }) {
    if (!oec.SERVICE_TYPES.includes(serviceType)) throw new OecError(`ServiceType: one of ${oec.SERVICE_TYPES.join(', ')}`);
    const threshold = oec.SYSLOG_LEVELS.indexOf(syslogLevel);
    if (threshold < 0 || syslogLevel === 'Debug') {
      throw new OecError(`Syslog level: one of ${oec.SYSLOG_LEVELS.filter((l) => l !== 'Debug').join(', ')} (Debug is off by default)`);
    }
    this.syslogThreshold = threshold;
    this.syslogModes = new Map();
    this.serviceType = serviceType;
    this.clock = clock;
    this.maxPacketSize = maxPacketSize;
    const appMam = masterAssetModel(mam, { deviceClass: `Oi4.${serviceType}` });
    this.il = appMam.ProductInstanceUri;
    this.appId = oec.encodeId(this.il);
    this.publisherId = `${serviceType}/${this.il}`;
    this.ids = new oec.MessageIds(this.publisherId, clock);
    this.writerIds = new oec.WriterIds();
    this.sources = new Map();
    this.sources.set(this.il, this._newSource(this.il, 'app', appMam));
    this.consumers = new Map();
    this.online = false;
    this.listSnapshot = null;
    this.subSnapshot = null;
  }

  _newSource(il, kind, mam) {
    return {
      il, kind, mam,
      health: { state: 'NORMAL_0', score: 100 },
      data: new Map(), configs: new Map(), events: new Set(),
    };
  }

  // --- Sources -------------------------------------------------------------

  get app() {
    return this.sources.get(this.il);
  }

  source(il) {
    const s = this.sources.get(il ?? this.il);
    if (!s) throw new OecError(`unknown Source ${il}`);
    return s;
  }

  hasSource(il) {
    return this.sources.has(il);
  }

  /** Adds or replaces an asset the application represents; returns its IL. */
  addAsset(settings) {
    const mam = masterAssetModel(settings);
    const il = mam.ProductInstanceUri;
    if (il === this.il) throw new OecError('an asset needs another Identification Link than the application');
    const existing = this.sources.get(il);
    if (existing) {
      const changed = JSON.stringify(existing.mam) !== JSON.stringify(mam);
      existing.mam = mam;
      return { il, out: changed ? [this._pub('MAM', il, [this._mamItem(existing)], CLASS.MAM)].filter(Boolean) : [] };
    }
    const s = this._newSource(il, 'asset', mam);
    this.sources.set(il, s);
    if (!this.online) return { il, out: [] };
    return { il, out: [...this._sourceBirth(s), ...this.flushLists()] };
  }

  // Removals publish nothing: the caller flushes the lists (flushLists)
  // a little later, so that a redeploy that removes and adds again
  // publishes no intermediate lists.

  removeAsset(il) {
    const s = this.sources.get(il);
    if (s && s.kind === 'asset') this.sources.delete(il);
  }

  // --- Health (MG 9.1.2) ---------------------------------------------------

  setHealth(il, state, score) {
    checkHealth(state, score);
    const s = this.source(il);
    if (s.kind === 'app' && state === 'NORMAL_0' && score === 0) {
      throw new OecError('NORMAL_0 with HealthScore 0 is reserved for the graceful disconnect of the application');
    }
    const recovered = s.kind === 'app' && s.health.state === 'FAILURE_1' && s.health.score === 0;
    s.health = { state, score };
    if (!this.online) return [];
    const out = [];
    if (recovered) out.push(this._pub('MAM', s.il, [this._mamItem(s)], CLASS.MAM));
    out.push(this._pub('Health', s.il, [this._healthItem(s)], CLASS.Health));
    return out;
  }

  /** Health of the application, at least every 60 s (MG 7.5.1). */
  heartbeat() {
    if (!this.online) return [];
    return [this._pub('Health', this.il, [this._healthItem(this.app)], CLASS.Health)];
  }

  // --- Data and Metadata (MG 9.1.4, 9.1.5) ---------------------------------

  /**
   * Defines a Data DataSet of a Source: `fields` (see oec.normalizeFields)
   * or null to derive them from the first value, `writable` for `Set
   * Data`. The DataSet exists from its first value on.
   */
  defineData(il, tag, { fields = null, writable = false, description = '' } = {}) {
    const s = this.source(il);
    checkName(tag, 'Tag');
    if (s.data.has(tag)) throw new OecError(`the Tag ${tag} is already used for this Source`);
    const d = {
      tag, writable: !!writable, description: description || '',
      fields: fields ? oec.normalizeFields(fields) : null,
      defined: !!fields, values: null, ts: 0, classId: null, version: 0, metaTs: 0,
      writerId: this.writerIds.get(`Data|${s.il}|${tag}`),
    };
    if (d.fields && d.fields.length === 0) throw new OecError(`Data ${tag}: at least one field`);
    if (d.fields) this._classify(d);
    s.data.set(tag, d);
  }

  removeData(il, tag) {
    const s = this.sources.get(il);
    if (s) s.data.delete(tag);
  }

  _classify(d) {
    d.classId = oec.dataClassId(d.tag, d.fields);
    d.version = oec.dataVersion(d.classId);
    d.metaTs = 0;
  }

  /** The values of a Data DataSet merged with the last ones; throws OecError. */
  _merge(d, values) {
    if (!values || typeof values !== 'object' || Array.isArray(values)) throw new OecError(`Data ${d.tag}: the values are no object`);
    if (!d.defined) {
      const merged = { ...(d.values || {}), ...values };
      const fields = oec.deriveFields(merged);
      return { fields, values: Object.fromEntries(fields.map((f) => [f.name, merged[f.name]])) };
    }
    for (const k of Object.keys(values)) {
      if (!d.fields.some((f) => f.name === k)) throw new OecError(`Data ${d.tag}: unknown field ${k}`);
    }
    const out = {};
    for (const f of d.fields) {
      const v = f.name in values ? values[f.name] : d.values?.[f.name];
      if (v === undefined) throw new OecError(`Data ${d.tag}: no value for field ${f.name}`);
      out[f.name] = oec.fieldValue(f, v);
    }
    return { fields: d.fields, values: out };
  }

  /** Publishes new values of a Data DataSet; throws OecError. */
  publishData(il, tag, values, ts) {
    const s = this.source(il);
    const d = s.data.get(tag);
    if (!d) throw new OecError(`Data ${tag} is not defined`);
    const merged = this._merge(d, values);
    return this._applyData(s, d, merged, ts, '');
  }

  _applyData(s, d, merged, ts, replyTo) {
    const created = d.values == null;
    if (!d.defined && JSON.stringify(merged.fields) !== JSON.stringify(d.fields)) {
      d.fields = merged.fields;
      this._classify(d);
    }
    d.values = merged.values;
    d.ts = ts ?? this.clock();
    if (!this.online) return [];
    const out = created ? this.flushLists() : [];
    if (!d.metaTs) out.push(this._metadata(s, d, ''));
    out.push(this._pub('Data', s.il, [this._dataItem(s, d)], d.classId, d.tag, replyTo));
    return out;
  }

  // --- Config (MG 9.1.3) ---------------------------------------------------

  /**
   * Defines a Config DataSet: `definition` as oec.normalizeConfig accepts,
   * optionally with `Context: {Name, Description}`; `values` are stored
   * values `{group: {name: value}}` that replace the defined ones.
   */
  defineConfig(il, name, definition, values = null) {
    const s = this.source(il);
    checkName(name, 'context name');
    if (s.configs.has(name)) throw new OecError(`the context name ${name} is already used for this Source`);
    const config = oec.normalizeConfig(definition);
    if (![...oec.configObjects(config)].length) throw new OecError(`Config ${name}: at least one config element`);
    const ctx = definition.Context || {};
    config.Context = { Name: oec.lt(ctx.Name ?? name) };
    if (ctx.Description != null) config.Context.Description = oec.lt(ctx.Description);
    for (const [g, n, o] of oec.configObjects(config)) {
      const v = values?.[g]?.[n];
      if (v != null && !oec.checkConfigValue(o, oec.configString(v))) o.Value = oec.configString(v);
    }
    s.configs.set(name, { name, config, changedAt: this.clock(), writerId: this.writerIds.get(`Config|${s.il}|${name}`) });
    if (!this.online) return [];
    return [...this.flushLists(), this._configPub(s, s.configs.get(name), '')];
  }

  removeConfig(il, name) {
    const s = this.sources.get(il);
    if (s) s.configs.delete(name);
  }

  /** The values of a Config: {group: {name: value string}}. */
  configValues(il, name) {
    const c = this.source(il).configs.get(name);
    if (!c) throw new OecError(`Config ${name} is not defined`);
    const out = {};
    for (const [g, n, o] of oec.configObjects(c.config)) (out[g] ??= {})[n] = o.Value;
    return out;
  }

  /** The values of a Config as JS values (Boolean, Number, else String; '' → null). */
  configTypedValues(il, name) {
    const c = this.source(il).configs.get(name);
    const out = {};
    for (const [g, n, o] of oec.configObjects(c.config)) (out[g] ??= {})[n] = oec.configTyped(o, o.Value);
    return out;
  }

  /** Changes values locally `{group: {name: value}}`; throws OecError. */
  setConfigValues(il, name, values) {
    const s = this.source(il);
    const c = s.configs.get(name);
    if (!c) throw new OecError(`Config ${name} is not defined`);
    const changes = [];
    for (const [g, group] of Object.entries(values || {})) {
      for (const [n, v] of Object.entries(group || {})) {
        const o = c.config[g]?.[n];
        if (!o || n === 'Name' || n === 'Description') throw new OecError(`Config ${name}: unknown config element ${g}/${n}`);
        const value = oec.configString(v);
        const bad = oec.checkConfigValue(o, value);
        if (bad) throw new OecError(`Config ${name}: ${g}/${n} = ${JSON.stringify(value)} is invalid (${bad})`);
        changes.push([g, n, value]);
      }
    }
    return this._applyConfig(s, c, changes, '');
  }

  _applyConfig(s, c, changes, replyTo) {
    for (const [g, n, v] of changes) c.config[g][n].Value = v;
    c.changedAt = this.clock();
    if (!this.online) return [];
    return [this._configPub(s, c, replyTo)];
  }

  _configPub(s, c, replyTo) {
    return this._pub('Config', s.il, [this._configItem(s, c)], CLASS.Config, c.name, replyTo);
  }

  // --- Event (MG 9.1.6) ----------------------------------------------------

  /**
   * Whether the application publishes its Syslog events of `level`: as
   * switched by `Set PublicationList` (Config MODE_1, REQ-TOP-027), else
   * the levels down to the default threshold.
   */
  syslogEnabled(level) {
    if (this.syslogModes.has(level)) return this.syslogModes.get(level);
    const i = oec.SYSLOG_LEVELS.indexOf(level);
    return i >= 0 && i <= this.syslogThreshold;
  }

  /**
   * Publishes an Event: {category: 'Syslog'|'Generic', level, number,
   * description, details}. Syslog: level is the Severity name or number
   * from the PRI `number`; MSG/HEADER in details.
   */
  publishEvent(il, ev) {
    const s = this.source(il);
    const { level, payload } = Provider.eventPayload(ev);
    const filter = `${ev.category}/${level}`;
    // The Syslog entries of the application are always listed.
    const appSyslog = s.kind === 'app' && ev.category === 'Syslog';
    if (appSyslog && !this.syslogEnabled(level)) return [];
    const fresh = !appSyslog && !s.events.has(filter);
    s.events.add(filter);
    if (!this.online) return [];
    const out = fresh ? this.flushLists() : [];
    const item = oec.dsm(this._eventWriter(s, filter), filter, s.il, payload, { timestamp: this.clock(), messageType: 'ua-event' });
    out.push(this._message(oec.topic(this.serviceType, this.il, 'Pub', 'Event', s.il, filter.split('/')), [item], CLASS.Event, ''));
    return out;
  }

  static eventPayload(ev) {
    if (ev.category === 'Syslog') {
      const n = Number(ev.number);
      if (!Number.isInteger(n) || n < 0 || n > 191) throw new OecError('Syslog: Number is the PRI 0 … 191 (Facility × 8 + Severity)');
      const level = oec.SYSLOG_LEVELS[n % 8];
      const payload = { Number: n, Category: 'CAT_SYSLOG_0' };
      const d = ev.details || {};
      const details = {};
      if (d.MSG != null) details.MSG = String(d.MSG);
      if (d.HEADER != null) details.HEADER = String(d.HEADER);
      if (Object.keys(details).length) payload.Details = details;
      return { level, payload };
    }
    if (ev.category === 'Generic') {
      checkName(ev.level, 'Generic level');
      const n = Number(ev.number ?? 0);
      if (!Number.isInteger(n) || n < 0 || n > MAX_U32) throw new OecError('Generic: Number is a UInt32');
      const payload = { Number: n };
      if (ev.description != null && ev.description !== '') payload.Description = String(ev.description);
      payload.Category = 'CAT_GENERIC_99';
      if (ev.details != null) payload.Details = ev.details;
      return { level: ev.level, payload };
    }
    throw new OecError('Event category: Syslog or Generic');
  }

  _eventWriter(s, filter) {
    if (s.kind === 'app') {
      const fixed = { 'Status/Good': DSW.StatusGood, 'Status/Uncertain': DSW.StatusUncertain, 'Status/Bad': DSW.StatusBad }[filter];
      if (fixed) return fixed;
    }
    return this.writerIds.get(`Event|${s.il}|${filter}`);
  }

  _statusEvent(il, name, replyTo) {
    const s = this.sources.get(il) || this.app;
    const level = oec.statusLevel(name);
    const filter = `Status/${level}`;
    const item = oec.dsm(this._eventWriter(s, filter), filter, s.il, oec.statusPayload(name), { timestamp: this.clock(), messageType: 'ua-event' });
    return this._message(oec.topic(this.serviceType, this.il, 'Pub', 'Event', s.il, ['Status', level]), [item], CLASS.Event, replyTo);
  }

  // --- Consumer subscriptions (SubscriptionList) ---------------------------

  addConsumer(filter) {
    this.consumers.set(filter, (this.consumers.get(filter) || 0) + 1);
    return this.flushLists();
  }

  removeConsumer(filter) {
    const n = (this.consumers.get(filter) || 0) - 1;
    if (n > 0) this.consumers.set(filter, n);
    else this.consumers.delete(filter);
  }

  // --- Lifecycle (MG 6.3) --------------------------------------------------

  /** The Last Will: FAILURE_1/0 about the application (REQ-LIFE-016). */
  will() {
    const item = oec.dsm(DSW.Health, '', this.il, { Health: 'FAILURE_1', HealthScore: 0 });
    return this._message(this._topic('Pub', 'Health', this.il), [item], CLASS.Health, '');
  }

  /** NORMAL_0/0 before a graceful disconnect (MG 6.3.4). */
  close() {
    this.online = false;
    const item = oec.dsm(DSW.Health, '', this.il, { Health: 'NORMAL_0', HealthScore: 0 }, { timestamp: this.clock() });
    return this._message(this._topic('Pub', 'Health', this.il), [item], CLASS.Health, '');
  }

  /** Topic filters of the requests to the application. */
  requestFilters() {
    return oec.REQUEST_METHODS.map((m) => `${oec.TOPIC_ROOT}/${this.serviceType}/${this.appId}/${m}/#`);
  }

  /** Everything to publish after a connect, the Birth first (MG 6.3.3). */
  connected() {
    this.online = true;
    const out = [];
    for (const s of this.sources.values()) out.push(...this._sourceBirth(s));
    this.listSnapshot = null;
    this.subSnapshot = null;
    out.push(...this.flushLists(true));
    for (const s of this.sources.values()) {
      for (const c of s.configs.values()) out.push(this._configPub(s, c, ''));
    }
    for (const s of this.sources.values()) {
      for (const d of s.data.values()) {
        if (d.values == null) continue;
        d.metaTs = 0;
        out.push(this._metadata(s, d, ''));
      }
    }
    for (const s of this.sources.values()) {
      for (const d of s.data.values()) {
        if (d.values != null) out.push(this._pub('Data', s.il, [this._dataItem(s, d)], d.classId, d.tag, ''));
      }
    }
    return out;
  }

  disconnected() {
    this.online = false;
  }

  _sourceBirth(s) {
    return [
      this._pub('MAM', s.il, [this._mamItem(s)], CLASS.MAM),
      this._pub('Health', s.il, [this._healthItem(s)], CLASS.Health),
      this._pub('Profile', s.il, [this._profileItem(s)], CLASS.Profile),
    ];
  }

  // --- PublicationList and SubscriptionList (MG 9.1.8, 9.1.9) --------------

  _publicationEntries(s) {
    const out = [];
    const entry = (resource, filter, writerId, mode, config) => {
      const payload = { Resource: resource, Source: s.il };
      if (filter != null) payload.Filter = filter;
      payload.DataSetWriterId = writerId;
      payload.Mode = mode;
      if (config) payload.Config = config;
      out.push(oec.dsm(this._listWriter(s), filter == null ? resource : `${resource}/${filter}`, s.il, payload, { messageType: 'ua-keyframe' }));
    };
    entry('MAM', null, this._writer(s, 'MAM'), 'ON_REQUEST_SOURCE_10');
    entry('Health', null, this._writer(s, 'Health'), 'ON_REQUEST_SOURCE_10');
    entry('Profile', null, this._writer(s, 'Profile'), 'ON_REQUEST_SOURCE_10');
    for (const level of STATUS_LEVELS) entry('Event', `Status/${level}`, this._eventWriter(s, `Status/${level}`), 'FILTER_4');
    if (s.kind === 'app') {
      for (const level of oec.SYSLOG_LEVELS) {
        const f = `Syslog/${level}`;
        entry('Event', f, this._eventWriter(s, f), this.syslogEnabled(level) ? 'FILTER_4' : 'OFF_0', 'MODE_1');
      }
    }
    for (const f of [...s.events].sort()) {
      if (s.kind === 'app' && f.startsWith('Syslog/')) continue;
      entry('Event', f, this._eventWriter(s, f), 'FILTER_4');
    }
    for (const c of s.configs.values()) entry('Config', c.name, c.writerId, 'ON_REQUEST_FILTER_9');
    for (const d of s.data.values()) {
      if (d.values == null) continue;
      entry('Data', d.tag, d.writerId, 'ON_REQUEST_FILTER_9');
      entry('Metadata', d.tag, d.writerId, 'ON_REQUEST_FILTER_9');
    }
    return out;
  }

  _listItems(il, filter) {
    const sources = il == null ? [...this.sources.values()] : [this.sources.get(il)];
    const want = filter ? filter.join('/') : null;
    const out = [];
    for (const s of sources) {
      for (const m of this._publicationEntries(s)) {
        if (want == null || m.WriterGroupName === want || m.WriterGroupName.startsWith(`${want}/`)) out.push(m);
      }
    }
    return out;
  }

  _subscriptionEntries() {
    const out = [];
    for (const path of [...this.consumers.keys()].sort()) {
      const group = groupOf(path);
      if (group) out.push({ path, group, configurable: false });
    }
    for (const s of this.sources.values()) {
      for (const c of s.configs.values()) {
        out.push({ path: oec.topic(this.serviceType, this.il, 'Set', 'Config', s.il, c.name), group: `Config/${c.name}`, configurable: false });
      }
    }
    for (const s of this.sources.values()) {
      for (const d of s.data.values()) {
        if (d.writable && d.values != null) {
          out.push({ path: oec.topic(this.serviceType, this.il, 'Set', 'Data', s.il, d.tag), group: `Data/${d.tag}`, configurable: false });
        }
      }
    }
    return out;
  }

  _subscriptionItems(entries) {
    return entries.map((e) => oec.dsm(DSW.SubscriptionList, e.group, this.il, { TopicPath: e.path, Config: 'NONE_0' }, { messageType: 'ua-keyframe' }));
  }

  _sublistAddressed(path, filter) {
    const lv = path.split('/');
    if (lv[4] !== filter[0]) return false;
    if (filter.length === 1) return true;
    if (lv[5] === '#') return true;
    return lv.length > 6 && oec.topicMatches(lv.slice(6).join('/'), filter.slice(1).join('/'));
  }

  /**
   * The PublicationList and the SubscriptionList as a whole if they changed
   * since their last publication (REQ-RES-PUBLIST-…, REQ-RES-SUBLIST-005);
   * the SubscriptionList after a connect only if it is not empty.
   */
  flushLists(connect = false) {
    if (!this.online) return [];
    const out = [];
    const list = this._listItems(null, null);
    const listJson = JSON.stringify(list);
    if (listJson !== this.listSnapshot) {
      this.listSnapshot = listJson;
      out.push(...this._series(this._topic('Pub', 'PublicationList'), list, CLASS.PublicationList, null, ''));
    }
    const subs = this._subscriptionItems(this._subscriptionEntries());
    const subJson = JSON.stringify(subs);
    if (subJson !== this.subSnapshot && !(connect && subs.length === 0)) {
      this.subSnapshot = subJson;
      out.push(...this._series(this._topic('Pub', 'SubscriptionList'), subs, CLASS.SubscriptionList, null, ''));
    } else if (connect) {
      this.subSnapshot = subJson;
    }
    return out;
  }

  // --- Items ----------------------------------------------------------------

  _writer(s, resource) {
    if (s.kind === 'app') return DSW[resource];
    return this.writerIds.get(`${resource}|${s.il}`);
  }

  _listWriter() {
    // One DataSetWriterId for the PublicationList of every Source (REQ-MSG-021).
    return DSW.PublicationList;
  }

  _mamItem(s) {
    return oec.dsm(this._writer(s, 'MAM'), '', s.il, s.mam);
  }

  _healthItem(s) {
    return oec.dsm(this._writer(s, 'Health'), '', s.il, { Health: s.health.state, HealthScore: s.health.score }, { timestamp: this.clock() });
  }

  _profileItem(s) {
    return oec.dsm(this._writer(s, 'Profile'), '', s.il, { Resources: [...(s.kind === 'app' ? PROFILE_APP : PROFILE_ASSET)] }, { messageType: 'ua-keyframe' });
  }

  _configItem(s, c) {
    return oec.dsm(c.writerId, c.name, s.il, oec.maskedConfig(c.config), { timestamp: c.changedAt, messageType: 'ua-keyframe' });
  }

  _dataItem(s, d) {
    return oec.dsm(d.writerId, d.tag, s.il, d.values, { timestamp: d.ts, version: d.version, messageType: 'ua-keyframe' });
  }

  _metadata(s, d, replyTo) {
    if (!d.metaTs) d.metaTs = this.clock();
    const body = {
      MessageId: this.ids.next(),
      MessageType: 'ua-metadata',
      PublisherId: this.publisherId,
      DataSetWriterId: d.writerId,
      WriterGroupName: d.tag,
      DataSetWriterName: s.il,
      Timestamp: oec.timestamp(d.metaTs),
      ReplyTo: replyTo || '',
      MetaData: oec.metaData(d.tag, d.fields, d.classId, d.version, d.description),
    };
    return { topic: oec.topic(this.serviceType, this.il, 'Pub', 'Metadata', s.il, d.tag), payload: JSON.stringify(body) };
  }

  // --- Messages -------------------------------------------------------------

  _topic(method, resource, il, filter) {
    return oec.topic(this.serviceType, this.il, method, resource, il, filter);
  }

  _message(topic, items, classId, replyTo) {
    return { topic, payload: oec.networkMessage(this.ids.next(), this.publisherId, items, replyTo, classId) };
  }

  _pub(resource, il, items, classId, filter, replyTo = '') {
    return this._message(this._topic('Pub', resource, il, filter), items, classId, replyTo);
  }

  /**
   * An answer as one or more NetworkMessages (MG 8.2.3): `classOf` is a
   * DataSetClassId or a function of the items of a page.
   */
  _series(topic, items, classOf, request, replyTo) {
    const cls = (page) => (typeof classOf === 'function' ? classOf(page) : classOf);
    const longest = this.ids.longest();
    const single = Buffer.byteLength(oec.networkMessage(longest, this.publisherId, [], replyTo, cls(items) || CLASS.MAM));
    const pagination = Buffer.byteLength(JSON.stringify(oec.dsm(oec.DSW_PAGINATION, 'Pagination', this.il, {
      TotalCount: MAX_U32, PerPage: MAX_U32, Page: MAX_U32, HasNext: false, PaginationId: longest,
    }))) + 1;
    const pages = oec.plan(items, request, this.maxPacketSize, { single, paged: single + pagination });
    let first = null;
    return pages.map((p) => {
      const id = this.ids.next();
      first ??= id;
      const msgs = [...p.items];
      if (p.pagination) msgs.push(oec.dsm(oec.DSW_PAGINATION, 'Pagination', this.il, { ...p.pagination, PaginationId: first }));
      return { topic, payload: oec.networkMessage(id, this.publisherId, msgs, replyTo, cls(p.items)) };
    });
  }

  _dataClass(items) {
    let cls = null;
    for (const m of items) {
      const d = this.sources.get(m.DataSetWriterName)?.data.get(m.WriterGroupName);
      if (!d) return null;
      if (cls && cls !== d.classId) return null;
      cls = d.classId;
    }
    return cls;
  }

  // --- Requests to the application -----------------------------------------

  /**
   * Handles a message on one of the requestFilters(): returns
   * {out, events}; events tell the flow what `Set` changed:
   * {type: 'data'|'config', il, name, values, messageId}.
   */
  handle(topicName, payload) {
    const none = { out: [], events: [] };
    const t = oec.parseTopic(topicName);
    if (!t || t.serviceType !== this.serviceType || t.appId !== this.appId || !oec.REQUEST_METHODS.includes(t.method)) return none;
    let message;
    try {
      message = oec.parseMessage(payload);
    } catch {
      return none;
    }
    if (t.method === 'Get') return { out: this._get(topicName, t, message), events: [] };
    if (t.sourceIl == null) return none;
    if (!this.sources.has(t.sourceIl)) return { out: [this._statusEvent(this.il, 'BadNotFound', message.MessageId)], events: [] };
    const result = this._write(t, message);
    const out = [...(result.out || [])];
    out.push(this._statusEvent(t.sourceIl, result.status || 'Good', message.MessageId));
    return { out, events: result.events || [] };
  }

  _get(topicName, t, message) {
    if (!SERVED.has(t.resource)) return [];
    if (t.sourceIl != null && !this.sources.has(t.sourceIl)) return [this._statusEvent(this.il, 'BadNotFound', message.MessageId)];
    const replyTo = message.MessageId;
    if (t.resource === 'Metadata') {
      const d = t.sourceIl != null && t.filter?.length === 1 ? this.sources.get(t.sourceIl).data.get(t.filter[0]) : null;
      return d && d.values != null ? [this._metadata(this.sources.get(t.sourceIl), d, replyTo)] : [];
    }
    const levels = topicName.split('/');
    levels[3] = 'Pub';
    const reply = levels.join('/');
    const request = oec.paginationRequest(message);
    const sources = t.sourceIl == null ? [...this.sources.values()] : [this.sources.get(t.sourceIl)];
    const one = t.filter?.length === 1 ? t.filter[0] : null;
    const filtered = t.filter != null;
    switch (t.resource) {
      case 'MAM':
      case 'Health':
      case 'Profile': {
        if (filtered) return [];
        const make = { MAM: (s) => this._mamItem(s), Health: (s) => this._healthItem(s), Profile: (s) => this._profileItem(s) }[t.resource];
        return this._series(reply, sources.map(make), CLASS[t.resource], request, replyTo);
      }
      case 'Config': {
        const items = [];
        for (const s of sources) {
          for (const c of s.configs.values()) {
            if (!filtered || (one != null && c.name === one)) items.push(this._configItem(s, c));
          }
        }
        return this._series(reply, items, CLASS.Config, request, replyTo);
      }
      case 'Data': {
        const items = [];
        for (const s of sources) {
          for (const d of s.data.values()) {
            if (d.values != null && (!filtered || (one != null && d.tag === one))) items.push(this._dataItem(s, d));
          }
        }
        return this._series(reply, items, (page) => this._dataClass(page), request, replyTo);
      }
      case 'PublicationList':
        return this._series(reply, this._listItems(t.sourceIl, t.filter), CLASS.PublicationList, request, replyTo);
      case 'SubscriptionList': {
        let entries = t.sourceIl != null && t.sourceIl !== this.il ? [] : this._subscriptionEntries();
        if (filtered) entries = entries.filter((e) => this._sublistAddressed(e.path, t.filter));
        return this._series(reply, this._subscriptionItems(entries), CLASS.SubscriptionList, request, replyTo);
      }
      default:
        return [];
    }
  }

  /** The one DataSetMessage of a Set/Del, helper DataSets aside (oec-core WriteWire::single). */
  static single(message) {
    if (!Array.isArray(message.Messages)) return { status: 'BadDecodingError' };
    const res = message.Messages.filter((m) => !(m && Number.isInteger(m.DataSetWriterId) && m.DataSetWriterId >= 1 && m.DataSetWriterId <= 9));
    if (res.length !== 1) return { status: 'BadInvalidArgument' };
    if (!res[0] || typeof res[0] !== 'object' || Array.isArray(res[0])) return { status: 'BadDecodingError' };
    return { dsm: res[0] };
  }

  _write(t, message) {
    const s = this.sources.get(t.sourceIl);
    const key = `${t.method} ${t.resource}`;
    const one = t.filter?.length === 1 ? t.filter[0] : null;
    switch (key) {
      case 'Set Data': return this._setData(s, one, message);
      case 'Set Config': return this._setConfig(s, one, message);
      case 'Set Metadata': {
        const d = one != null ? s.data.get(one) : null;
        return { status: d && d.values != null ? 'BadNotWritable' : 'BadNotFound' };
      }
      case 'Set PublicationList': return this._setPublicationList(s, t, message);
      case 'Set SubscriptionList':
      case 'Del SubscriptionList': return this._writeSubscriptionList(s, t, message);
      default: return { status: 'BadNotFound' };
    }
  }

  _setData(s, tag, message) {
    const d = tag != null ? s.data.get(tag) : null;
    if (!d || d.values == null) return { status: 'BadNotFound' };
    if (!d.writable) return { status: 'BadNotWritable' };
    const { dsm, status } = Provider.single(message);
    if (status) return { status };
    const p = dsm.Payload;
    if (!p || typeof p !== 'object' || Array.isArray(p)) return { status: 'BadDecodingError' };
    const values = {};
    for (const f of d.fields) {
      if (!(f.name in p)) return { status: 'BadInvalidArgument' };
      try {
        values[f.name] = oec.fieldValue(f, p[f.name]);
      } catch {
        return { status: 'BadTypeMismatch' };
      }
    }
    const out = this._applyData(s, d, { fields: d.fields, values }, this.clock(), message.MessageId);
    return { out, events: [{ type: 'data', il: s.il, name: d.tag, values: { ...values }, messageId: message.MessageId }] };
  }

  _setConfig(s, name, message) {
    const c = name != null ? s.configs.get(name) : null;
    if (!c) return { status: 'BadNotFound' };
    const { dsm, status } = Provider.single(message);
    if (status) return { status };
    const r = oec.parseConfigSet(c.config, dsm.Payload);
    if (r.status) return { status: r.status };
    const out = this._applyConfig(s, c, r.changes, message.MessageId);
    return { out, events: [{ type: 'config', il: s.il, name: c.name, values: this.configTypedValues(s.il, c.name), messageId: message.MessageId }] };
  }

  _setPublicationList(s, t, message) {
    if (!t.filter) return { status: 'BadNotFound' };
    const want = t.filter.join('/');
    const current = this._publicationEntries(s).filter((m) => m.WriterGroupName === want);
    if (current.length === 0) return { status: 'BadNotFound' };
    const { dsm, status } = Provider.single(message);
    if (status) return { status };
    const r = dsm.Payload;
    if (!r || typeof r !== 'object' || Array.isArray(r) || typeof r.Resource !== 'string' || typeof r.Source !== 'string'
      || !Number.isInteger(r.DataSetWriterId) || (r.Filter != null && typeof r.Filter !== 'string')) {
      return { status: 'BadDecodingError' };
    }
    const c = current[0].Payload;
    const fixed = r.Resource !== c.Resource || r.Source !== c.Source || (r.Filter ?? null) !== (c.Filter ?? null)
      || r.DataSetWriterId !== c.DataSetWriterId || (r.Config ?? 'NONE_0') !== (c.Config ?? 'NONE_0')
      || (r.Interval ?? 0) !== 0 || Object.values(r.Precisions || {}).some((x) => x !== 0);
    // Only the Mode of a Syslog level of the application can be switched (Config MODE_1).
    const switchable = c.Config === 'MODE_1' && ['FILTER_4', 'OFF_0'].includes(r.Mode);
    if (fixed || (r.Mode !== c.Mode && !switchable)) return { status: 'BadNotWritable' };
    const reply = this._topic('Pub', 'PublicationList', s.il, t.filter);
    if (r.Mode === c.Mode) return { out: this._series(reply, current, CLASS.PublicationList, null, message.MessageId) };
    const level = c.Filter.slice('Syslog/'.length);
    this.syslogModes.set(level, r.Mode === 'FILTER_4');
    const updated = this._publicationEntries(s).filter((m) => m.WriterGroupName === want);
    const out = this._series(reply, updated, CLASS.PublicationList, null, message.MessageId);
    out.push(...this.flushLists());
    return { out, events: [{ type: 'syslog', il: s.il, name: level, values: { enabled: r.Mode === 'FILTER_4' }, messageId: message.MessageId }] };
  }

  _writeSubscriptionList(s, t, message) {
    if (!t.filter || s.kind !== 'app') return { status: 'BadNotFound' };
    const { dsm, status } = Provider.single(message);
    if (status) return { status };
    const r = dsm.Payload;
    if (!r || typeof r !== 'object' || Array.isArray(r) || typeof r.TopicPath !== 'string') return { status: 'BadDecodingError' };
    if (groupOf(r.TopicPath) !== t.filter.join('/')) return { status: 'BadInvalidArgument' };
    const current = this._subscriptionEntries().find((e) => e.path === r.TopicPath);
    if (t.method === 'Set') return { status: current ? 'BadNotWritable' : 'BadNotSupported' };
    return { status: current ? 'BadNotWritable' : 'BadNotFound' };
  }

  // --- Requests of the application as a consumer ---------------------------

  /** A Get request: {topic, payload, messageId}. */
  getRequest({ serviceType, appIl, resource, sourceIl = null, filter = null, perPage = 0, page = 0 }) {
    if (!oec.SERVICE_TYPES.includes(serviceType)) throw new OecError(`ServiceType: one of ${oec.SERVICE_TYPES.join(', ')}`);
    nonEmpty(appIl, 'AppId');
    nonEmpty(resource, 'Resource');
    if (filter != null && sourceIl == null) throw new OecError('a Filter needs a Source');
    const messageId = this.ids.next();
    const topicName = oec.topic(serviceType, appIl, 'Get', resource, sourceIl, filter == null ? null : String(filter).split('/'));
    if (resource === 'Metadata') {
      if (sourceIl == null || filter == null) throw new OecError('Get Metadata needs Source and Tag');
      const body = {
        MessageId: messageId, MessageType: 'ua-metadata', PublisherId: this.publisherId,
        DataSetWriterId: 0, WriterGroupName: String(filter), DataSetWriterName: sourceIl,
        Timestamp: oec.timestamp(this.clock()), ReplyTo: '', MetaData: {},
      };
      return { topic: topicName, payload: JSON.stringify(body), messageId };
    }
    const messages = [];
    if (perPage > 0) {
      messages.push(oec.dsm(oec.DSW_PAGINATION_REQUEST, 'PaginationRequest', this.il, { PerPage: perPage, Page: page }));
    }
    return { topic: topicName, payload: oec.networkMessage(messageId, this.publisherId, messages, ''), messageId };
  }
}

module.exports = { Provider, masterAssetModel, groupOf, PROFILE_APP, PROFILE_ASSET };
