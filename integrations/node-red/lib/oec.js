'use strict';
// OEC 2.0 protocol core (sans-IO): topics, Identification Links,
// NetworkMessages, DataSetClassIds, Config checks and pagination of the OEC
// Messaging Guideline ("MG"). Nothing in this module does I/O or knows
// Node-RED.

const crypto = require('node:crypto');

const TOPIC_ROOT = 'Oi4v2';
const SERVICE_TYPES = Object.freeze([
  'Registry', 'OTConnector', 'ITConnector', 'CloudConnector', 'Utility', 'Persistence', 'Aggregation',
]);
const METHODS = Object.freeze(['Get', 'Pub', 'Set', 'Del', 'Call', 'Reply']);
const REQUEST_METHODS = Object.freeze(['Get', 'Set', 'Del']);

const CLASS = Object.freeze({
  MAM: '360ca8f3-5e66-42a2-8f10-9cdf45f4bf58',
  Health: 'd8e7b6df-42ba-448a-975a-199f59e8ffeb',
  Profile: '48017c6a-05c8-48d7-9d85-4b08bbb707f3',
  Event: '543ae05e-b6d9-4161-a0a3-350a0fac5976',
  Config: '9d5983db-440d-4474-9fd7-1cd7a6c8b6c2',
  PublicationList: '217434d6-6e1e-4230-b907-f52bc9ffe152',
  SubscriptionList: 'e5d68c47-c276-4929-8ab9-4c1090cac785',
});
// UUIDv5 namespace of the DataSetClassIds of Data DataSets (oec-core data.rs).
const DATA_CLASS_NAMESPACE = 'e7954113-fb31-5334-8b17-76075c4eb311';

// Helper DataSets (MG 8.3.11, REQ-MSG-019).
const DSW_PAGINATION_REQUEST = 1;
const DSW_PAGINATION = 2;
// DataSetWriterIds of the application's own DataSets (REQ-MSG-020: from 10 on).
const DSW = Object.freeze({
  MAM: 10, Health: 11, Profile: 12, PublicationList: 13,
  StatusGood: 14, StatusUncertain: 15, StatusBad: 16, SubscriptionList: 17,
});
const DSW_FIRST_FREE = 18;
const DSW_MAX = 65535;

const HEALTH_STATES = Object.freeze([
  'NORMAL_0', 'FAILURE_1', 'CHECK_FUNCTION_2', 'OFF_SPEC_3', 'MAINTENANCE_REQUIRED_4',
]);

const SYSLOG_LEVELS = Object.freeze([
  'Emergency', 'Alert', 'Critical', 'Error', 'Warning', 'Notice', 'Informational', 'Debug',
]);

// OPC UA StatusCodes of the Status events (MG 8.3.6.1, oec-core status.rs).
const STATUS = Object.freeze({
  Good: [0x00000000, 'The operation succeeded.'],
  Bad: [0x80000000, 'The operation failed.'],
  BadInternalError: [0x80020000, 'An internal error occurred as a result of a programming or configuration error.'],
  BadDecodingError: [0x80070000, 'Decoding halted because of invalid data in the stream.'],
  BadNotWritable: [0x803B0000, 'The access level does not allow writing to the Node.'],
  BadOutOfRange: [0x803C0000, 'The value was out of range.'],
  BadNotSupported: [0x803D0000, 'The requested operation is not supported.'],
  BadNotFound: [0x803E0000, 'A requested item was not found or a search operation ended without success.'],
  BadTypeMismatch: [0x80740000, "The value supplied for the attribute is not of the same type as the attribute's value."],
  BadInvalidArgument: [0x80AB0000, 'One or more arguments are invalid.'],
});

// OPC UA built-in types (Part 6) of Data fields with their JSON form.
const BUILTIN = Object.freeze({
  Boolean: 1, SByte: 2, Byte: 3, Int16: 4, UInt16: 5, Int32: 6, UInt32: 7,
  Float: 10, Double: 11, String: 12, DateTime: 13,
});
const INTEGER_RANGES = Object.freeze({
  SByte: [-128, 127], Byte: [0, 255], Int16: [-32768, 32767], UInt16: [0, 65535],
  Int32: [-2147483648, 2147483647], UInt32: [0, 4294967295],
});

const SENSITIVE_MASK = '********';
const CONFIG_TYPES = Object.freeze(['Boolean', 'ByteString', 'DateTime', 'Number', 'String']);

class OecError extends Error {}

// --- Identification Links and topics ---------------------------------------

const UNRESERVED = /^[A-Za-z0-9\-._~]$/;

/** dnp-encoding of one Oi4Identifier element (MG A3). */
function dnpEncode(value) {
  let out = '';
  for (const ch of String(value)) {
    if (UNRESERVED.test(ch)) {
      out += ch;
    } else {
      for (const octet of Buffer.from(ch, 'utf8')) {
        out += ',' + octet.toString(16).toUpperCase().padStart(2, '0');
      }
    }
  }
  return out;
}

/** Oi4Identifier `ManufacturerUri/Model/ProductCode/SerialNumber` (MG 4.1). */
function identificationLink(manufacturerUri, model, productCode, serialNumber) {
  return [manufacturerUri, dnpEncode(model), dnpEncode(productCode), dnpEncode(serialNumber)].join('/');
}

/** AppId/Source topic level: base64url without padding of the IL (MG 7.4). */
function encodeId(il) {
  return Buffer.from(il, 'utf8').toString('base64url');
}

function decodeId(level) {
  if (!level || !/^[A-Za-z0-9_-]+$/.test(level) || level.length % 4 === 1) {
    throw new OecError(`invalid topic id level ${JSON.stringify(level)}`);
  }
  const il = Buffer.from(level, 'base64url').toString('utf8');
  if (encodeId(il) !== level) {
    throw new OecError(`invalid topic id level ${JSON.stringify(level)}`);
  }
  return il;
}

const FILTER_ESCAPES = { '%': '%25', '+': '%2B', '#': '%23', '/': '%2F' };

/** One Filter level as written on the topic (REQ-TOP-019 … 022). */
function encodeLevel(value) {
  return String(value).replace(/[%+#/]/g, (c) => FILTER_ESCAPES[c]);
}

function decodeLevel(level) {
  return level.replace(/%(25|2B|23|2F)/gi, (_, h) => String.fromCharCode(parseInt(h, 16)));
}

/** Checks a free Filter value (Tag, context name, Generic level). */
function checkFilterValue(value, what) {
  if (typeof value !== 'string' || value === '' || value.includes('\0')) {
    throw new OecError(`${what}: a non-empty string without U+0000`);
  }
  return value;
}

/** Parses an OEC topic; null for a foreign topic. */
function parseTopic(topic) {
  const levels = String(topic).split('/');
  if (levels.length < 5 || levels[0] !== TOPIC_ROOT) return null;
  const [, serviceType, appId, method, resource] = levels;
  if (!SERVICE_TYPES.includes(serviceType) || !METHODS.includes(method) || !resource) return null;
  try {
    const out = { serviceType, appId, appIl: decodeId(appId), method, resource, source: null, sourceIl: null, filter: null };
    if (levels.length > 5) {
      out.source = levels[5];
      out.sourceIl = decodeId(levels[5]);
    }
    if (levels.length > 6) out.filter = levels.slice(6).map(decodeLevel);
    return out;
  } catch {
    return null;
  }
}

/** A topic; `filter` is an array of decoded levels or a single value. */
function topic(serviceType, appIl, method, resource, sourceIl, filter) {
  const levels = [TOPIC_ROOT, serviceType, encodeId(appIl), method, resource];
  if (sourceIl != null) {
    levels.push(encodeId(sourceIl));
    if (filter != null) {
      for (const f of Array.isArray(filter) ? filter : [filter]) levels.push(encodeLevel(f));
    }
  }
  return levels.join('/');
}

/** Whether an MQTT topic matches a topic filter with `+` and `#`. */
function topicMatches(filter, name) {
  const f = filter.split('/');
  const n = name.split('/');
  for (let i = 0; i < f.length; i++) {
    if (f[i] === '#') return true;
    if (i >= n.length) return false;
    if (f[i] !== '+' && f[i] !== n[i]) return false;
  }
  return f.length === n.length;
}

// --- Time and MessageIds ---------------------------------------------------

/** ISO 8601 UTC timestamp with milliseconds. */
function timestamp(ms) {
  return new Date(ms).toISOString();
}

/** MessageIds `<unixMs>-<PublisherId>` (MG 8.2.1), unique and increasing. */
class MessageIds {
  constructor(publisherId, clock = Date.now) {
    this.publisherId = publisherId;
    this.clock = clock;
    this.last = 0;
  }

  next() {
    const ms = Math.max(Math.floor(this.clock()), this.last + 1);
    this.last = ms;
    return `${ms}-${this.publisherId}`;
  }

  /** The longest MessageId this generator can produce soon (for sizes). */
  longest() {
    return `${'9'.repeat(16)}-${this.publisherId}`;
  }
}

// --- Messages --------------------------------------------------------------

/**
 * One DataSetMessage (MG 8.2.2): DataSetWriterId, WriterGroupName,
 * DataSetWriterName, then the optional members in a fixed order.
 */
function dsm(writerId, group, writerName, payload, opts = {}) {
  const out = { DataSetWriterId: writerId, WriterGroupName: group, DataSetWriterName: writerName };
  if (opts.timestamp != null) out.Timestamp = timestamp(opts.timestamp);
  if (opts.version != null) out.MetaDataVersion = { MajorVersion: opts.version, MinorVersion: opts.version };
  if (opts.messageType) out.MessageType = opts.messageType;
  out.Payload = payload;
  return out;
}

/** A ua-data NetworkMessage as JSON text (MG 8.2.1). */
function networkMessage(messageId, publisherId, messages, replyTo = '', classId = null) {
  const out = { MessageId: messageId, MessageType: 'ua-data', PublisherId: publisherId };
  if (classId) out.DataSetClassId = classId;
  out.ReplyTo = replyTo || '';
  out.Messages = messages;
  return JSON.stringify(out);
}

/** Parses a NetworkMessage; throws OecError. Unknown keys are ignored (REQ-MSG-046). */
function parseMessage(payload) {
  let data;
  try {
    data = JSON.parse(Buffer.isBuffer(payload) ? payload.toString('utf8') : String(payload));
  } catch {
    throw new OecError('payload is no JSON');
  }
  if (!data || typeof data !== 'object' || Array.isArray(data)) throw new OecError('payload is no JSON object');
  for (const k of ['MessageId', 'MessageType', 'PublisherId']) {
    if (typeof data[k] !== 'string' || data[k] === '') throw new OecError(`${k} missing`);
  }
  return data;
}

function localizedText(text) {
  return { Locale: 'en-US', Text: String(text ?? '') };
}

function lt(value) {
  if (value && typeof value === 'object' && !Array.isArray(value) && 'Text' in value) return value;
  return localizedText(value);
}

/** The Status event payload of a StatusCode name (MG 8.3.6.1). */
function statusPayload(name) {
  const [number, description] = STATUS[name] || STATUS.Bad;
  return { Number: number, Description: description, Category: 'CAT_STATUS_1', Details: { SymbolicId: STATUS[name] ? name : 'Bad' } };
}

function statusLevel(name) {
  return name === 'Good' ? 'Good' : 'Bad';
}

// --- UUIDs and Data DataSets -----------------------------------------------

function uuidBytes(uuid) {
  return Buffer.from(uuid.replace(/-/g, ''), 'hex');
}

function uuidString(bytes) {
  const h = bytes.toString('hex');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20, 32)}`;
}

/** UUIDv5 (RFC 9562) of `name` in `namespace`. */
function uuidV5(namespace, name) {
  const hash = crypto.createHash('sha1').update(uuidBytes(namespace)).update(Buffer.from(name, 'utf8')).digest();
  const b = Buffer.from(hash.subarray(0, 16));
  b[6] = (b[6] & 0x0f) | 0x50;
  b[8] = (b[8] & 0x3f) | 0x80;
  return uuidString(b);
}

/** DataSetClassId of a Data DataSet: UUIDv5 of `["<Tag>",[["<Name>",<BuiltInType>,-1],…]]`. */
function dataClassId(tag, fields) {
  const shape = [tag, fields.map((f) => [f.name, BUILTIN[f.type], -1])];
  return uuidV5(DATA_CLASS_NAMESPACE, JSON.stringify(shape));
}

/** MetaDataVersion: the first four octets of the DataSetClassId (as the micro SDK). */
function dataVersion(classId) {
  const v = uuidBytes(classId).readUInt32BE(0);
  return v === 0 ? 1 : v;
}

const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;
const TAG = /^[A-Za-z0-9_-]+$/;

/** The field type of a JS value for derived fields. */
function typeOf(value) {
  if (typeof value === 'boolean') return 'Boolean';
  if (typeof value === 'number' && Number.isFinite(value)) return 'Double';
  if (typeof value === 'string') return 'String';
  return null;
}

/**
 * Normalizes the field definitions of a Data DataSet: an array or an
 * object `{name: {type, unit, description, low, high}}`. Throws OecError.
 */
function normalizeFields(defs) {
  const list = Array.isArray(defs)
    ? defs
    : Object.entries(defs || {}).map(([name, d]) => ({ name, ...(typeof d === 'string' ? { type: d } : d) }));
  const seen = new Set();
  return list.map((x) => {
    // Lower case keys or the PascalCase of the guideline.
    const d = x && typeof x === 'object' ? {
      name: x.name ?? x.Name, type: x.type ?? x.Type, unit: x.unit ?? x.Unit,
      description: x.description ?? x.Description, low: x.low ?? x.Low, high: x.high ?? x.High,
    } : x;
    if (!d || !NAME.test(d.name || '') || d.name.length > 64) {
      throw new OecError(`field name ${JSON.stringify(d && d.name)}: letters, digits and "_", not starting with a digit`);
    }
    if (seen.has(d.name)) throw new OecError(`field ${d.name} defined twice`);
    seen.add(d.name);
    const type = d.type || 'Double';
    if (!(type in BUILTIN)) throw new OecError(`field ${d.name}: unknown type ${type} (${Object.keys(BUILTIN).join(', ')})`);
    const f = { name: d.name, type };
    if (d.unit) f.unit = String(d.unit);
    if (d.description) f.description = String(d.description);
    if (d.low != null || d.high != null) {
      if (!Number.isFinite(Number(d.low)) || !Number.isFinite(Number(d.high))) {
        throw new OecError(`field ${d.name}: low and high are both numbers`);
      }
      f.low = Number(d.low);
      f.high = Number(d.high);
    }
    return f;
  });
}

/** Fields derived from a value object (numbers as Double). */
function deriveFields(values) {
  return Object.entries(values).map(([name, v]) => {
    const type = typeOf(v);
    if (!type) throw new OecError(`field ${name}: only numbers, booleans and strings can be derived`);
    return { name, type };
  });
}

/** A value of a field in its JSON form; throws OecError for a mismatch. */
function fieldValue(field, value) {
  const t = field.type;
  if (t === 'Boolean') {
    if (typeof value !== 'boolean') throw new OecError(`${field.name}: no Boolean`);
    return value;
  }
  if (t === 'String') {
    if (typeof value !== 'string') throw new OecError(`${field.name}: no String`);
    return value;
  }
  if (t === 'DateTime') {
    const ms = value instanceof Date ? value.getTime() : typeof value === 'string' ? Date.parse(value) : NaN;
    if (!Number.isFinite(ms)) throw new OecError(`${field.name}: no DateTime`);
    return value instanceof Date ? value.toISOString() : value;
  }
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new OecError(`${field.name}: no number`);
  const range = INTEGER_RANGES[t];
  if (range && (!Number.isInteger(value) || value < range[0] || value > range[1])) {
    throw new OecError(`${field.name}: no ${t}`);
  }
  return value;
}

/** The DataSetMetaData of a Data DataSet (MG 9.1.5). */
function metaData(tag, fields, classId, version, description) {
  const out = {
    ConfigurationVersion: { MajorVersion: version, MinorVersion: version },
    DataSetClassId: classId,
    EnumDataTypes: [],
    Fields: fields.map((f) => {
      const properties = [];
      if (f.low != null) {
        properties.push({ Key: 'EURange', Value: { UaType: 22, Value: { High: f.high, Low: f.low, UaTypeId: 'i=884' } } });
      }
      if (f.unit) {
        properties.push({
          Key: 'EngineeringUnits',
          Value: {
            UaType: 22,
            Value: {
              DisplayName: { Text: f.unit },
              NamespaceUri: 'http://www.opcfoundation.org/UA/units/un/cefact',
              UaTypeId: 'i=887',
              UnitId: -1,
            },
          },
        });
      }
      return {
        ArrayDimensions: [],
        BuiltInType: BUILTIN[f.type],
        DataSetFieldId: uuidV5(classId, f.name),
        DataType: `i=${BUILTIN[f.type]}`,
        Description: localizedText(f.description || ''),
        FieldFlags: 0,
        MaxStringLength: 0,
        Name: f.name,
        Properties: properties,
        ValueRank: -1,
      };
    }),
    Name: tag,
    Namespaces: [],
    SimpleDataTypes: [],
    StructureDataTypes: [],
  };
  if (description) out.Description = localizedText(description);
  return out;
}

// --- Config (MG 9.1.3) -----------------------------------------------------

const CONFIG_NAME = /^[A-Za-z_][A-Za-z0-9_-]*$/;
const JSON_NUMBER = /^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$/;
const DATE_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
const BASE64 = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;
const OBJECT_KEYS = ['Type', 'Value', 'DefaultValue', 'Unit', 'Name', 'Description', 'Validation', 'Mandatory', 'Sensitive'];

function compilePattern(p) {
  try {
    return new RegExp(p, 'u');
  } catch {
    return null;
  }
}

/** Checks a Config value; returns null or the StatusCode name of the violation. */
function checkConfigValue(o, value) {
  if (value === '') return null;
  if (o.Sensitive === true && value === SENSITIVE_MASK) return 'BadOutOfRange';
  let typed;
  switch (o.Type) {
    case 'Boolean': typed = value === 'true' || value === 'false'; break;
    case 'Number': typed = JSON_NUMBER.test(value); break;
    case 'DateTime': typed = DATE_TIME.test(value) && Number.isFinite(Date.parse(value)); break;
    case 'ByteString': typed = BASE64.test(value); break;
    default: typed = true;
  }
  if (!typed) return 'BadTypeMismatch';
  const v = o.Validation;
  if (!v) return null;
  if (v.Length != null && [...value].length > v.Length) return 'BadOutOfRange';
  if (o.Type === 'Number') {
    const x = Number(value);
    if ((v.Min != null && x < Number(v.Min)) || (v.Max != null && x > Number(v.Max))) return 'BadOutOfRange';
  }
  if (v.Pattern != null) {
    const re = compilePattern(v.Pattern);
    if (re && !re.test(value)) return 'BadOutOfRange';
  }
  if (Array.isArray(v.Values) && !v.Values.includes(value)) return 'BadOutOfRange';
  return null;
}

/** Iterates the groups and objects of a Config payload: [group, name, object]. */
function* configObjects(config) {
  for (const [g, group] of Object.entries(config)) {
    if (g === 'Context' || !group || typeof group !== 'object') continue;
    for (const [n, o] of Object.entries(group)) {
      if (n === 'Name' || n === 'Description' || !o || typeof o !== 'object') continue;
      yield [g, n, o];
    }
  }
}

/**
 * Normalizes and checks a Config definition `{group: {Name, Description?,
 * obj: {Type, Value, …}}}` (REQ-RES-CONFIG-004 … 008); Name values may be
 * plain strings. Returns a new object; throws OecError.
 */
function normalizeConfig(def) {
  if (!def || typeof def !== 'object' || Array.isArray(def)) throw new OecError('the Config definition is no object');
  const out = {};
  for (const [g, group] of Object.entries(def)) {
    if (g === 'Context') continue;
    if (!CONFIG_NAME.test(g)) throw new OecError(`GroupName ${JSON.stringify(g)}: letters, digits, "-" and "_", not starting with a digit or "-"`);
    if (!group || typeof group !== 'object' || Array.isArray(group)) throw new OecError(`group ${g} is no object`);
    const ng = { Name: lt(group.Name ?? g) };
    if (group.Description != null) ng.Description = lt(group.Description);
    for (const [n, o] of Object.entries(group)) {
      if (n === 'Name' || n === 'Description') continue;
      if (!CONFIG_NAME.test(n)) throw new OecError(`ConfigName ${JSON.stringify(n)} in group ${g}: letters, digits, "-" and "_", not starting with a digit or "-"`);
      if (!o || typeof o !== 'object' || Array.isArray(o)) throw new OecError(`${g}/${n} is no object`);
      const type = o.Type || 'String';
      if (!CONFIG_TYPES.includes(type)) throw new OecError(`${g}/${n}: Type is one of ${CONFIG_TYPES.join(', ')}`);
      const no = { Type: type, Value: o.Value == null ? '' : String(o.Value) };
      if (o.DefaultValue != null) no.DefaultValue = String(o.DefaultValue);
      if (o.Unit != null) no.Unit = String(o.Unit);
      no.Name = lt(o.Name ?? n);
      if (o.Description != null) no.Description = lt(o.Description);
      if (o.Validation != null) {
        const v = o.Validation;
        const nv = {};
        if (v.Length != null) {
          if (!Number.isInteger(v.Length) || v.Length < 0) throw new OecError(`${g}/${n}: Length is a non-negative integer`);
          nv.Length = v.Length;
        }
        for (const k of ['Min', 'Max']) {
          if (v[k] != null) {
            if (type !== 'Number') throw new OecError(`${g}/${n}: Min and Max apply to Type Number only`);
            if (typeof v[k] !== 'number') throw new OecError(`${g}/${n}: ${k} is a number`);
            nv[k] = v[k];
          }
        }
        if (nv.Min != null && nv.Max != null && nv.Min > nv.Max) throw new OecError(`${g}/${n}: Min is greater than Max`);
        if (v.Pattern != null) {
          if (!compilePattern(String(v.Pattern))) throw new OecError(`${g}/${n}: the Pattern does not compile`);
          nv.Pattern = String(v.Pattern);
        }
        if (v.Values != null) {
          if (!Array.isArray(v.Values)) throw new OecError(`${g}/${n}: Values is an array`);
          nv.Values = v.Values.map(String);
        }
        no.Validation = nv;
      }
      if (o.Mandatory != null) no.Mandatory = o.Mandatory === true;
      if (o.Sensitive != null) no.Sensitive = o.Sensitive === true;
      for (const [what, value] of [['Value', no.Value], ['DefaultValue', no.DefaultValue]]) {
        if (value != null) {
          const bad = checkConfigValue(no, value);
          if (bad) throw new OecError(`${g}/${n}: the ${what} is no valid ${type} value (${bad})`);
        }
      }
      ng[n] = no;
    }
    out[g] = ng;
  }
  return out;
}

/** The Config as published: non-empty sensitive values masked (REQ-RES-CONFIG-024). */
function maskedConfig(config) {
  const out = structuredClone(config);
  for (const [, , o] of configObjects(out)) {
    if (o.Sensitive === true) {
      if (o.Value) o.Value = SENSITIVE_MASK;
      if (o.DefaultValue) o.DefaultValue = SENSITIVE_MASK;
    }
  }
  return out;
}

function sameJson(a, b) {
  return JSON.stringify(canonical(a)) === JSON.stringify(canonical(b));
}

function canonical(v) {
  if (Array.isArray(v)) return v.map(canonical);
  if (v && typeof v === 'object') {
    return Object.fromEntries(Object.keys(v).sort().map((k) => [k, canonical(v[k])]));
  }
  return v;
}

function sameLt(requested, current) {
  if (current == null) return requested == null;
  return sameJson(requested, current);
}

function sameValidation(requested, current) {
  const r = requested || {};
  const c = current || {};
  const num = (x) => (x == null ? null : Number(x));
  return (r.Length ?? null) === (c.Length ?? null) && num(r.Min) === num(c.Min) && num(r.Max) === num(c.Max)
    && (r.Pattern ?? null) === (c.Pattern ?? null) && sameJson(r.Values ?? null, c.Values ?? null);
}

/**
 * Checks the payload of a `Set Config` against the current Config (MG
 * 9.1.3, REQ-RES-CONFIG-003/005/011/023/025, oec-core config.rs parse_set):
 * `{ changes: [[group, name, value]] }` or `{ status }`.
 */
function parseConfigSet(current, payload) {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return { status: 'BadDecodingError' };
  const changes = [];
  let objects = 0;
  for (const [g, gj] of Object.entries(payload)) {
    if (g === 'Context') {
      if (!sameLt(gj, current.Context)) return { status: 'BadNotWritable' };
      continue;
    }
    const group = current[g];
    if (!group) return { status: 'BadNotFound' };
    if (!gj || typeof gj !== 'object' || Array.isArray(gj)) return { status: 'BadDecodingError' };
    for (const [n, oj] of Object.entries(gj)) {
      if (n === 'Name' || n === 'Description') {
        if (!sameLt(oj, group[n])) return { status: 'BadNotWritable' };
        continue;
      }
      const o = group[n];
      if (!o) return { status: 'BadNotFound' };
      if (!oj || typeof oj !== 'object' || Array.isArray(oj)) return { status: 'BadDecodingError' };
      objects++;
      if (!('Value' in oj)) return { status: 'BadInvalidArgument' };
      if (typeof oj.Value !== 'string') return { status: 'BadTypeMismatch' };
      if ('Type' in oj && oj.Type !== o.Type) return { status: 'BadTypeMismatch' };
      const sensitive = o.Sensitive === true;
      for (const [key, j] of Object.entries(oj)) {
        let unchanged = true;
        switch (key) {
          case 'Name': unchanged = sameLt(j, o.Name); break;
          case 'Description': unchanged = sameLt(j, o.Description); break;
          case 'Unit': unchanged = sameLt(j, o.Unit); break;
          case 'DefaultValue':
            unchanged = sameLt(j, o.DefaultValue) || (sensitive && j === SENSITIVE_MASK && !!o.DefaultValue);
            break;
          case 'Mandatory': unchanged = typeof j === 'boolean' && j === (o.Mandatory === true); break;
          case 'Sensitive': unchanged = typeof j === 'boolean' && j === sensitive; break;
          case 'Validation': unchanged = sameValidation(j, o.Validation); break;
          default: unchanged = true;
        }
        if (!unchanged) return { status: 'BadNotWritable' };
      }
      if (sensitive && oj.Value === SENSITIVE_MASK) continue;
      const bad = checkConfigValue(o, oj.Value);
      if (bad) return { status: bad };
      changes.push([g, n, oj.Value]);
    }
    for (const [n, o] of Object.entries(group)) {
      if (n !== 'Name' && n !== 'Description' && o.Mandatory === true && !(n in gj)) return { status: 'BadInvalidArgument' };
    }
  }
  if (objects === 0) return { status: 'BadInvalidArgument' };
  return { changes };
}

/** A typed JS value of a Config value string (for flows). */
function configTyped(o, value) {
  if (value === '') return null;
  switch (o.Type) {
    case 'Boolean': return value === 'true';
    case 'Number': return Number(value);
    default: return value;
  }
}

/** The string form of a JS value for a Config object. */
function configString(value) {
  if (value == null) return '';
  if (value instanceof Date) return value.toISOString();
  return typeof value === 'string' ? value : String(value);
}

// --- DataSetWriterIds ------------------------------------------------------

/**
 * Deterministic DataSetWriterIds: FNV-1a of the key, mapped into
 * 18 … 65535 with linear probing. The same key gets the same id after a
 * restart unless another key took it first.
 */
class WriterIds {
  constructor() {
    this.byKey = new Map();
    this.used = new Set(Object.values(DSW));
  }

  static hash(key) {
    let h = 0x811c9dc5;
    for (const b of Buffer.from(key, 'utf8')) {
      h ^= b;
      h = Math.imul(h, 0x01000193) >>> 0;
    }
    return h;
  }

  get(key) {
    let id = this.byKey.get(key);
    if (id != null) return id;
    const span = DSW_MAX - DSW_FIRST_FREE + 1;
    let slot = WriterIds.hash(key) % span;
    for (let i = 0; i < span; i++, slot = (slot + 1) % span) {
      if (!this.used.has(DSW_FIRST_FREE + slot)) {
        id = DSW_FIRST_FREE + slot;
        this.used.add(id);
        this.byKey.set(key, id);
        return id;
      }
    }
    throw new OecError('no free DataSetWriterId');
  }
}

// --- Pagination (MG 8.2.3, REQ-PAG-002 … 008) ------------------------------

/** The PaginationRequest of a Get: {perPage, page} or null. */
function paginationRequest(message) {
  const entries = Array.isArray(message.Messages) ? message.Messages : [];
  for (const e of entries) {
    if (!e || e.DataSetWriterId !== DSW_PAGINATION_REQUEST) continue;
    const p = e.Payload || {};
    const perPage = p.PerPage;
    const page = p.Page ?? 0;
    if (Number.isInteger(perPage) && perPage > 0 && Number.isInteger(page) && page >= 0) return { perPage, page };
  }
  return null;
}

function itemsFit(sizes, k, limit, overhead) {
  for (let i = 0; i < sizes.length; i += k) {
    let sum = overhead;
    for (let j = i; j < sizes.length && j < i + k; j++) sum += sizes[j] + 1;
    if (sum > limit) return false;
  }
  return true;
}

function largestFit(sizes, bound, limit, overhead) {
  for (let k = Math.max(1, bound); k >= 1; k--) {
    if (itemsFit(sizes, k, limit, overhead)) return k;
  }
  return 1;
}

/**
 * The pages of an answer: [{items, pagination?}] where pagination is
 * {TotalCount, PerPage, Page, HasNext}. A requested pagination is honored;
 * without one the answer is split only if it exceeds `limit` octets.
 */
function plan(items, request, limit, overhead) {
  const n = items.length;
  const sizes = items.map((i) => Buffer.byteLength(JSON.stringify(i)));
  const total = sizes.reduce((s, x) => s + x + 1, 0);
  let perPage;
  let from;
  let to;
  if (!request) {
    if (total + overhead.single <= limit) return [{ items }];
    perPage = largestFit(sizes, n, limit, overhead.paged);
    from = 1;
    to = Math.max(1, Math.ceil(n / perPage));
  } else {
    const bounded = Math.min(request.perPage, Math.max(n, 1));
    perPage = itemsFit(sizes, bounded, limit, overhead.paged) ? request.perPage : largestFit(sizes, bounded, limit, overhead.paged);
    from = request.page === 0 ? 1 : request.page;
    to = request.page === 0 ? Math.max(1, Math.ceil(n / perPage)) : request.page;
  }
  const pages = [];
  for (let page = from; page <= to; page++) {
    pages.push({
      items: items.slice((page - 1) * perPage, page * perPage),
      pagination: { TotalCount: n, PerPage: perPage, Page: page, HasNext: page * perPage < n },
    });
  }
  return pages;
}

module.exports = {
  TOPIC_ROOT, SERVICE_TYPES, METHODS, REQUEST_METHODS, CLASS, DATA_CLASS_NAMESPACE,
  DSW_PAGINATION_REQUEST, DSW_PAGINATION, DSW, HEALTH_STATES, SYSLOG_LEVELS, STATUS, BUILTIN,
  SENSITIVE_MASK, CONFIG_TYPES, OBJECT_KEYS, NAME, TAG,
  OecError,
  dnpEncode, identificationLink, encodeId, decodeId, encodeLevel, decodeLevel, checkFilterValue,
  parseTopic, topic, topicMatches,
  timestamp, MessageIds,
  dsm, networkMessage, parseMessage, localizedText, lt, statusPayload, statusLevel,
  uuidV5, dataClassId, dataVersion, typeOf, normalizeFields, deriveFields, fieldValue, metaData,
  checkConfigValue, configObjects, normalizeConfig, maskedConfig, parseConfigSet, configTyped, configString,
  WriterIds, paginationRequest, plan,
};
