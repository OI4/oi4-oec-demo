"""State of the OEC bus as seen by the integration (standard library only).

The Directory consumes the publications of all applications on the bus and
keeps applications, assets (MAM, Health), Data DataSets and their Metadata.
It returns the Get requests the integration sends to discover the content.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .oec import (
    BUILTIN_BOOLEAN,
    BUILTIN_STRING,
    CLASS_HEALTH,
    CLASS_MAM,
    HELPER_WRITER_IDS,
    NUMERIC_BUILTIN_TYPES,
    REQUEST_METHODS,
    Application,
    NetworkMessage,
    OecError,
    Outgoing,
    Topic,
    dnp_decode,
    localized_text,
    parse_message,
    parse_topic,
)

# Seconds to wait for the Metadata of a new Data DataSet before its fields
# are announced without units.
METADATA_WAIT = 10.0
# Minimum seconds between two discovery rounds (Get MAM/Health/Data) of an app.
DISCOVERY_INTERVAL = 30.0

KIND_NUMBER = "number"
KIND_BOOLEAN = "boolean"
KIND_TEXT = "text"


@dataclass(slots=True)
class App:
    """An application on the bus (identified by the AppId of its topics)."""

    il: str
    service_type: str
    online: bool | None = None
    last_discovery: float | None = None


@dataclass(slots=True)
class Asset:
    """An application itself or an asset it represents."""

    il: str
    app_il: str
    mam: dict[str, Any] | None = None
    health: str | None = None
    health_score: int | None = None
    health_details: list[Any] = field(default_factory=list)

    @property
    def is_app(self) -> bool:
        return self.il == self.app_il

    def text(self, key: str) -> str | None:
        return None if self.mam is None else localized_text(self.mam.get(key))

    @property
    def name(self) -> str:
        """Display name: Model and a short form of the SerialNumber.

        Until the MAM is known, both come from the Oi4Identifier (MG 4.1:
        ManufacturerUri/Model/ProductCode/SerialNumber, dnp-encoded).
        """
        model = self.text("Model")
        serial = self.text("SerialNumber")
        if not model:
            parts = self.il.split("/")
            if len(parts) < 4:
                return self.il
            model = dnp_decode(parts[-3])
            serial = serial or dnp_decode(parts[-1])
        if not serial:
            return model
        return f"{model} {serial if len(serial) <= 20 else '…' + serial[-12:]}"


@dataclass(slots=True)
class Field:
    """A field of a Data DataSet, described by Metadata if available."""

    name: str
    label: str | None = None
    unit: str | None = None
    builtin_type: int | None = None
    description: str | None = None


@dataclass(slots=True)
class DataSet:
    """A Data DataSet (one tag) of an asset."""

    asset_il: str
    tag: str
    writer_id: int
    first_seen: float
    values: dict[str, Any] = field(default_factory=dict)
    timestamp: str | None = None
    status: Any = None
    fields: dict[str, Field] = field(default_factory=dict)
    has_metadata: bool = False
    released: bool = False
    announced: set[str] = field(default_factory=set)

    @property
    def key(self) -> tuple[str, str]:
        return self.asset_il, self.tag

    def kind(self, name: str) -> str | None:
        """Entity kind of a field; None for structures and arrays."""
        meta = self.fields.get(name)
        if meta is not None and meta.builtin_type is not None:
            if meta.builtin_type == BUILTIN_BOOLEAN:
                return KIND_BOOLEAN
            if meta.builtin_type in NUMERIC_BUILTIN_TYPES:
                return KIND_NUMBER
            if meta.builtin_type == BUILTIN_STRING:
                return KIND_TEXT
            return None
        value = self.values.get(name)
        if isinstance(value, bool):
            return KIND_BOOLEAN
        if isinstance(value, int | float):
            return KIND_NUMBER
        if isinstance(value, str):
            return KIND_TEXT
        return None


@dataclass(slots=True)
class Changes:
    """What one ingested message changed."""

    new_apps: list[str] = field(default_factory=list)
    new_assets: list[str] = field(default_factory=list)
    devices: set[str] = field(default_factory=set)
    health: list[str] = field(default_factory=list)
    assets: set[str] = field(default_factory=set)
    fields: list[tuple[tuple[str, str], list[str]]] = field(default_factory=list)
    requests: list[Outgoing] = field(default_factory=list)
    request: tuple[Topic, NetworkMessage] | None = None

    def __bool__(self) -> bool:
        return bool(
            self.new_apps or self.new_assets or self.devices or self.health
            or self.assets or self.fields or self.requests or self.request
        )


class Directory:
    """Applications, assets and Data of the bus."""

    def __init__(self, application: Application) -> None:
        self.application = application
        self.apps: dict[str, App] = {}
        self.assets: dict[str, Asset] = {}
        self.datasets: dict[tuple[str, str], DataSet] = {}
        self.health_known: set[str] = set()

    @property
    def own_il(self) -> str:
        return self.application.identity.il

    def subscriptions(self) -> list[str]:
        """Topic filters of the integration (all publications it uses and its requests)."""
        return [
            "Oi4v2/+/+/Pub/MAM/#",
            "Oi4v2/+/+/Pub/Health/#",
            "Oi4v2/+/+/Pub/Data/#",
            "Oi4v2/+/+/Pub/Metadata/#",
            *self.application.request_filters(),
        ]

    def app_online(self, asset_il: str) -> bool | None:
        asset = self.assets.get(asset_il)
        app = self.apps.get(asset.app_il if asset else asset_il)
        return None if app is None else app.online

    def ingest(self, topic: str, payload: bytes | str, now: float) -> Changes:
        """Process one received message."""
        changes = Changes()
        try:
            parsed = parse_topic(topic)
            message = parse_message(payload)
            app_il = parsed.app_il
            source_il = parsed.source_il
        except OecError:
            return changes
        if app_il == self.own_il:
            if parsed.method in REQUEST_METHODS:
                changes.request = (parsed, message)
            return changes
        if parsed.method != "Pub":
            return changes
        app = self._app(app_il, parsed.service_type, now, changes)
        if parsed.resource == "MAM":
            self._mam(app, source_il, message, changes, now)
        elif parsed.resource == "Health":
            self._health(app, source_il, message, changes)
        elif parsed.resource == "Data":
            self._data(app, parsed, source_il, message, changes, now)
        elif parsed.resource == "Metadata":
            self._metadata(app, source_il, message, changes)
        return changes

    def _app(self, il: str, service_type: str, now: float, changes: Changes) -> App:
        app = self.apps.get(il)
        if app is None:
            app = self.apps[il] = App(il, service_type)
            changes.new_apps.append(il)
            asset = self._asset(il, il, changes)
            if asset.app_il != il:
                # Listed by another application before its own first message.
                asset.app_il = il
                changes.devices.add(il)
            self._discover(app, now, changes)
        return app

    def _takes(self, app: App, il: str) -> bool:
        """Whether the entries of an application about an asset are used.

        The integration never models itself. An application is the authority
        for itself: entries of other applications about it are ignored, as are
        all foreign entries of a Registry, which lists the assets of the whole
        bus (its MAM and Health answers mirror the other applications).
        """
        if il == self.own_il:
            return False
        if il == app.il:
            return True
        return app.service_type != "Registry" and il not in self.apps

    def _discover(self, app: App, now: float, changes: Changes) -> None:
        """Get MAM, Health and Data of all assets of an application."""
        if app.last_discovery is not None and now - app.last_discovery < DISCOVERY_INTERVAL:
            return
        app.last_discovery = now
        get = self.application.get
        changes.requests += [
            get(app.service_type, app.il, "MAM", class_id=CLASS_MAM),
            get(app.service_type, app.il, "Health", class_id=CLASS_HEALTH),
            get(app.service_type, app.il, "Data"),
        ]

    def _asset(self, il: str, app_il: str, changes: Changes) -> Asset:
        asset = self.assets.get(il)
        if asset is None:
            asset = self.assets[il] = Asset(il, app_il)
            changes.new_assets.append(il)
        return asset

    def _set_online(self, app: App, online: bool, changes: Changes) -> None:
        if app.online is online:
            return
        app.online = online
        changes.assets.update(il for il, asset in self.assets.items() if asset.app_il == app.il)

    def _mam(self, app: App, source_il: str | None, message: NetworkMessage,
             changes: Changes, now: float) -> None:
        for entry in message.messages:
            if entry.writer_id in HELPER_WRITER_IDS:
                continue
            il = entry.writer_name or source_il
            if not il or not entry.payload or not self._takes(app, il):
                continue
            asset = self._asset(il, app.il, changes)
            if asset.mam != entry.payload:
                asset.mam = entry.payload
                changes.devices.add(il)
                changes.assets.add(il)
        if source_il == app.il and message.reply_to == "":
            # Birth message (MG 6.3.3, REQ-LIFE-013): online again, rediscover.
            was_offline = app.online is False
            self._set_online(app, True, changes)
            if was_offline:
                self._discover(app, now, changes)

    def _health(self, app: App, source_il: str | None, message: NetworkMessage,
                changes: Changes) -> None:
        for entry in message.messages:
            if entry.writer_id in HELPER_WRITER_IDS:
                continue
            il = entry.writer_name or source_il
            health = entry.payload.get("Health")
            score = entry.payload.get("HealthScore")
            if not il or not isinstance(health, str) or not self._takes(app, il):
                continue
            score = score if isinstance(score, int) and not isinstance(score, bool) else None
            asset = self._asset(il, app.il, changes)
            if il == app.il and score == 0 and health in ("NORMAL_0", "FAILURE_1"):
                # Close or Will message (REQ-LIFE-011/012).
                self._set_online(app, False, changes)
            elif il == app.il and app.online is None:
                self._set_online(app, True, changes)
            details = entry.payload.get("Details")
            asset.health = health
            asset.health_score = score
            asset.health_details = details if isinstance(details, list) else []
            changes.assets.add(il)
            if il not in self.health_known:
                self.health_known.add(il)
                changes.health.append(il)

    def _data(self, app: App, parsed: Topic, source_il: str | None, message: NetworkMessage,
              changes: Changes, now: float) -> None:
        for entry in message.messages:
            if entry.writer_id in HELPER_WRITER_IDS:
                continue
            il = entry.writer_name or source_il
            tag = entry.writer_group or parsed.filter
            if not il or not tag or not self._takes(app, il):
                continue
            self._asset(il, app.il, changes)
            dataset = self.datasets.get((il, tag))
            if dataset is None:
                dataset = self.datasets[(il, tag)] = DataSet(il, tag, entry.writer_id, now)
                changes.requests.append(
                    self.application.get_metadata(app.service_type, app.il, il, tag, entry.writer_id)
                )
            dataset.values.update(entry.payload)
            dataset.timestamp = entry.timestamp
            dataset.status = entry.status
            changes.assets.add(il)
            self._announce(dataset, changes)

    def _metadata(self, app: App, source_il: str | None, message: NetworkMessage,
                  changes: Changes) -> None:
        if message.message_type != "ua-metadata":
            return
        raw = message.raw
        il = raw.get("DataSetWriterName") or source_il
        tag = raw.get("WriterGroupName")
        meta = raw.get("MetaData")
        dataset = self.datasets.get((il, tag)) if isinstance(il, str) and isinstance(tag, str) else None
        if dataset is None or not isinstance(meta, dict):
            return
        fields = meta.get("Fields")
        if not isinstance(fields, list):
            return
        for item in fields:
            parsed = _field(item)
            if parsed is not None:
                dataset.fields[parsed.name] = parsed
        dataset.has_metadata = True
        dataset.released = True
        changes.assets.add(dataset.asset_il)
        self._announce(dataset, changes)

    def tick(self, now: float) -> Changes:
        """Announce the fields of DataSets whose Metadata did not arrive in time."""
        changes = Changes()
        for dataset in self.datasets.values():
            if not dataset.released and now - dataset.first_seen >= METADATA_WAIT:
                dataset.released = True
                self._announce(dataset, changes)
        return changes

    def _announce(self, dataset: DataSet, changes: Changes) -> None:
        if not dataset.released:
            return
        names = [*dataset.fields, *(n for n in dataset.values if n not in dataset.fields)]
        new = [n for n in names if n not in dataset.announced and dataset.kind(n) is not None]
        if new:
            dataset.announced.update(new)
            changes.fields.append((dataset.key, new))


def _field(item: Any) -> Field | None:
    if not isinstance(item, dict) or not isinstance(item.get("Name"), str):
        return None
    builtin = item.get("BuiltInType")
    if not isinstance(builtin, int) or isinstance(builtin, bool):
        builtin = None
    if item.get("ValueRank", -1) not in (-1, None) or item.get("ArrayDimensions"):
        builtin = -1  # arrays are not mapped to entities
    unit = None
    for prop in item.get("Properties") or []:
        if isinstance(prop, dict) and prop.get("Key") == "EngineeringUnits":
            value = prop.get("Value")
            info = value.get("Value") if isinstance(value, dict) else None
            if isinstance(info, dict):
                unit = localized_text(info.get("DisplayName"))
    description = localized_text(item.get("Description"))
    label = description.split(" (", 1)[0].strip() if description else None
    return Field(
        name=item["Name"],
        label=label or None,
        unit=unit or None,
        builtin_type=builtin,
        description=description,
    )
