# Home Assistant integration for OI4 Open Edge Computing 2.0

A custom integration for [Home Assistant](https://www.home-assistant.io/)
that joins an OEC 2.0 message bus and shows its applications and assets
with their Health and Data. It is installed with [HACS](https://hacs.xyz/).

| | |
|---|---|
| Domain | `oi4_oec` |
| Home Assistant | 2026.10 or newer |
| OEC role | an OEC application: ServiceType `ITConnector`, Messaging Level 2, Oi4Identifier `oi4.org/OEC,20Home,20Assistant/OEC-HOME-ASSISTANT/<instance id>` |
| Connection | MQTT 5 over TLS to the OEC broker (own client, [aiomqtt](https://github.com/empicano/aiomqtt)); independent of the Home Assistant MQTT integration |
| Data flow | read only, push (`local_push`) |

## Install with HACS

1. HACS → ⋮ → *Custom repositories* → repository
   `https://github.com/OI4/oi4-oec-demo`, type *Integration*.
2. Install *OI4 Open Edge Computing*, restart Home Assistant.
3. *Settings → Devices & services → Add integration →
   OI4 Open Edge Computing*.

HACS installs `custom_components/oi4_oec/` of the repository root.
Without HACS, copy that folder into `<config>/custom_components/` and
restart.

## Configuration

Step 1, the broker (the content of `broker.json`, MG 6.2.2):

| Field | |
|---|---|
| Host | DNS name or IP address of the broker; must match the broker certificate (SAN) |
| Port | secure port, default `8883` |
| CA certificate | PEM of the CA that issued the broker certificate; empty = system trust store |
| Authentication | `password` or `certificate` |

Step 2, the credentials (RIG 4.1.3; the integration uses the means the
platform gives it):

- *password*: user name and password (on an OEC platform the content of
  `/run/secrets/oi4/credentials`, base64 of `user:password`, decoded);
- *certificate*: client certificate and key (PEM), optional key password.

The flow checks the connection before it creates the entry: wrong
credentials, a certificate that does not match the host and unreadable PEM
are reported in the form. One entry per broker (`host:port`).

## What appears in Home Assistant

- A service device for the integration itself (the entry), with the
  diagnostic binary sensor *Bus connection*.
- A device per OEC application on the bus (from its first message) and per
  asset it represents (from its `MAM`, linked *via* its application).
  Name, manufacturer, model, product code, serial number and revisions come
  from the `MAM`; until it arrives the name comes from the Oi4Identifier.
- Per application: binary sensor *Online* from its Birth, Close and Will
  messages (MG 6.3).
- Per asset: sensors *Health* (`normal`, `failure`, `check_function`,
  `off_spec`, `maintenance_required`; `Details` as attribute) and
  *Health score* (%).
- Per field of a `Data` DataSet: a sensor (numbers, text) or a binary
  sensor (Boolean). Name and unit come from the `Metadata` (field
  description, `EngineeringUnits`); the device class follows from the unit
  (°C, hPa, %, km/h, W, V, A, lx, …). Attributes: tag, field, description,
  timestamp and status of the DataSet. Fields without `Metadata` appear
  after 10 s without unit.

When an application goes offline its entities become unavailable; when the
broker connection is lost all of them do. The integration reconnects with
backoff.

## Behaviour on the bus

- Birth (`MAM`, then `Health`), Health heartbeat every 55 s, Close on
  unload and on shutdown of Home Assistant, Will at the broker (MG 6.3,
  7.5.1).
- Discovery: on the first message of an application `Get` `MAM`, `Health`
  and `Data`; for every new Data DataSet `Get` `Metadata`; again after a
  Birth.
- An application is the authority for itself: entries about it published
  by other applications are not used, and of a `Registry` only its own
  entries (the OEC Registry answers `Get MAM`/`Health` with the entries of
  the whole bus).
- Level 2 responder: answers `Get` `MAM`, `Health` and `Profile` (with
  pagination and `Locale` `en-US`); `Get` for an unknown `Source` gets the
  `Event` `Status`/`Bad` `BadNotFound`; every `Set` and `Del` gets
  `BadNotSupported` (or `BadNotFound`) – the integration offers nothing for
  modification. `Profile`: `MAM`, `Health`, `Profile`, `Event`.
- Checked with the OEC compliance tool D7 (`oec-check test --level 2` with
  proxy, restart hook and peer): **PASS**, 33 pass, 4 warnings for SHOULD
  requirements (no `Data`, `Config`, `PublicationList`,
  `SubscriptionList`, `ReferenceDesignation`; no `NewDataSetWriterId`).

## Limitations

- Read only: no `Set` of `Config` or `Data`, no services.
- Arrays and structures in `Data` are not shown.
- No reauthentication or reconfiguration flow yet: remove and add the
  entry to change the broker or the credentials.

## Development

The integration is developed in the OEC source tree
(`oec/integrations/home-assistant/`, with `custom_components/oi4_oec/`
next to this README) and copied unmodified into `OI4/oi4-oec-demo`
(`custom_components/oi4_oec/` and `hacs.json` at the repository root for
HACS, the rest in `integrations/home-assistant/`). Change it in the OEC
source tree.

Layout of the integration:

| File | |
|---|---|
| `oec.py` | OEC protocol: Oi4Identifier, topics, messages, Birth/Close/Will, Level 2 responder (standard library only) |
| `model.py` | state of the bus: applications, assets, DataSets, discovery (standard library only) |
| `bus.py` | MQTT/TLS session with aiomqtt: Will, Birth, subscriptions, heartbeat, Close, reconnect |
| `hub.py`, `__init__.py`, `config_flow.py`, `sensor.py`, `binary_sensor.py`, `entity.py`, `diagnostics.py` | Home Assistant glue |

Checks (`check.sh` finds `custom_components/` in both layouts):

```sh
./check.sh                  # unittests of oec.py and model.py (python3 >= 3.12)
OEC_HA_FULL=1 ./check.sh    # + Home Assistant tests (pytest, Python 3.14) and hassfest, in docker
```

`tools/run_bus.py` runs the bus part without Home Assistant (needs
`pip install aiomqtt==2.5.1`) and prints what it discovers – useful
against the local development environment `oec/dev` of the OEC source
tree:

```sh
python3 tools/run_bus.py --host localhost --ca ../../dev/generated/broker/ca.pem \
    --credentials ../../dev/generated/roots/oec-dev-app/run/secrets/oi4/credentials
```

Notes:

- Run long-lived bus clients on macOS in a container (Linux): App Nap
  delays the timers of a background Python process by minutes, which
  breaks the 60 s Health heartbeat.
- Two clients with the same serial number share the MQTT ClientId and take
  over each other's session.

## License

MIT.
