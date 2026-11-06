"""OEC 2.0 protocol core (sans-IO, standard library only).

Topics, Identification Links, NetworkMessages and the Level 2 responder of
the integration as an OEC application (OEC Messaging Guideline, "MG").
Nothing in this module does I/O or imports Home Assistant.
"""

from __future__ import annotations

import base64
import binascii
import json
import time
from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any

TOPIC_ROOT = "Oi4v2"
SERVICE_TYPES = (
    "Registry",
    "OTConnector",
    "ITConnector",
    "CloudConnector",
    "Utility",
    "Persistence",
    "Aggregation",
)
METHODS = ("Get", "Pub", "Set", "Del", "Call", "Reply")
REQUEST_METHODS = ("Get", "Set", "Del")

CLASS_MAM = "360ca8f3-5e66-42a2-8f10-9cdf45f4bf58"
CLASS_HEALTH = "d8e7b6df-42ba-448a-975a-199f59e8ffeb"
CLASS_PROFILE = "48017c6a-05c8-48d7-9d85-4b08bbb707f3"
CLASS_EVENT = "543ae05e-b6d9-4161-a0a3-350a0fac5976"

# Helper DataSets (MG 8.3.11, REQ-MSG-019).
DSW_PAGINATION_REQUEST = 1
DSW_PAGINATION = 2
DSW_LOCALE = 3
HELPER_WRITER_IDS = frozenset({1, 2, 3, 9})

# DataSetWriterIds of the own resources (REQ-MSG-020: from 10 on).
DSW_MAM = 10
DSW_HEALTH = 11
DSW_PROFILE = 12
DSW_STATUS_BAD = 13

# Resources answered on Get; Event carries the Status events (REQ-CMP-023).
RESOURCES = ("MAM", "Health", "Profile")
PROFILE = (*RESOURCES, "Event")

# OPC UA StatusCodes of the Status events (MG 8.3.6.1).
STATUS_BAD_NOT_SUPPORTED = (0x803D0000, "BadNotSupported", "The requested operation is not supported.")
STATUS_BAD_NOT_FOUND = (
    0x803E0000,
    "BadNotFound",
    "A requested item was not found or a search operation ended without success.",
)

HEALTH_STATES = (
    "NORMAL_0",
    "FAILURE_1",
    "CHECK_FUNCTION_2",
    "OFF_SPEC_3",
    "MAINTENANCE_REQUIRED_4",
)

# OPC UA built-in types (Part 6): 2..11 are the numeric ones.
NUMERIC_BUILTIN_TYPES = frozenset(range(2, 12))
BUILTIN_BOOLEAN = 1
BUILTIN_STRING = 12

_DNP_UNRESERVED = frozenset(
    "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
)
_FILTER_ESCAPES = {"%": "%25", "+": "%2B", "#": "%23", "/": "%2F"}

Clock = Callable[[], float]


class OecError(ValueError):
    """A topic or message that is no valid OEC message."""


def dnp_encode(value: str) -> str:
    """Encode one Oi4Identifier element with the dnp-encoding (MG A3)."""
    out = []
    for char in value:
        if char in _DNP_UNRESERVED:
            out.append(char)
        else:
            out.extend(f",{octet:02X}" for octet in char.encode())
    return "".join(out)


def dnp_decode(value: str) -> str:
    """Inverse of dnp_encode; invalid escapes are kept as they are."""
    out = bytearray()
    index = 0
    while index < len(value):
        escape = value[index + 1 : index + 3]
        if value[index] == "," and len(escape) == 2 and all(c in "0123456789ABCDEFabcdef" for c in escape):
            out.append(int(escape, 16))
            index += 3
        else:
            out += value[index].encode()
            index += 1
    return out.decode(errors="replace")


def identification_link(
    manufacturer_uri: str, model: str, product_code: str, serial_number: str
) -> str:
    """Build the Oi4Identifier of an asset (MG 4.1)."""
    return "/".join(
        (
            manufacturer_uri,
            dnp_encode(model),
            dnp_encode(product_code),
            dnp_encode(serial_number),
        )
    )


def encode_id(identification_link: str) -> str:
    """AppId/Source topic level: base64url without padding of the IL."""
    raw = base64.urlsafe_b64encode(identification_link.encode())
    return raw.decode().rstrip("=")


def decode_id(level: str) -> str:
    """Inverse of encode_id."""
    if not level or "=" in level:
        raise OecError(f"invalid topic id level {level!r}")
    try:
        raw = base64.urlsafe_b64decode(level + "=" * (-len(level) % 4))
        return raw.decode()
    except (binascii.Error, UnicodeDecodeError, ValueError) as err:
        raise OecError(f"invalid topic id level {level!r}") from err


def encode_filter(value: str) -> str:
    """Percent-encode the characters a Filter level must not contain."""
    return "".join(_FILTER_ESCAPES.get(char, char) for char in value)


@dataclass(frozen=True, slots=True)
class Topic:
    """A parsed OEC topic (MG 7)."""

    service_type: str
    app_id: str
    method: str
    resource: str
    source: str | None = None
    filter: str | None = None

    @property
    def app_il(self) -> str:
        return decode_id(self.app_id)

    @property
    def source_il(self) -> str | None:
        return None if self.source is None else decode_id(self.source)

    def __str__(self) -> str:
        levels = [TOPIC_ROOT, self.service_type, self.app_id, self.method, self.resource]
        if self.source is not None:
            levels.append(self.source)
            if self.filter is not None:
                levels.append(self.filter)
        return "/".join(levels)


def parse_topic(value: str) -> Topic:
    """Parse an OEC topic; raises OecError for foreign topics."""
    levels = value.split("/")
    if len(levels) < 5 or levels[0] != TOPIC_ROOT:
        raise OecError(f"not an OEC topic: {value!r}")
    service_type, app_id, method, resource = levels[1:5]
    if service_type not in SERVICE_TYPES or method not in METHODS or not resource:
        raise OecError(f"not an OEC topic: {value!r}")
    decode_id(app_id)
    source = levels[5] if len(levels) > 5 else None
    rest = "/".join(levels[6:]) if len(levels) > 6 else None
    if source is not None:
        decode_id(source)
    return Topic(service_type, app_id, method, resource, source, rest)


def topic(
    service_type: str,
    app_il: str,
    method: str,
    resource: str,
    source_il: str | None = None,
    filter_: str | None = None,
) -> str:
    """Build a topic from Identification Links."""
    return str(
        Topic(
            service_type,
            encode_id(app_il),
            method,
            resource,
            None if source_il is None else encode_id(source_il),
            None if filter_ is None else encode_filter(filter_),
        )
    )


def timestamp(now: float) -> str:
    """ISO 8601 UTC timestamp with milliseconds."""
    moment = datetime.fromtimestamp(now, UTC)
    return moment.strftime("%Y-%m-%dT%H:%M:%S.") + f"{moment.microsecond // 1000:03d}Z"


@dataclass(slots=True)
class DataSetMessage:
    """One entry of Messages of a ua-data NetworkMessage."""

    writer_id: int
    writer_group: str
    writer_name: str
    payload: dict[str, Any]
    timestamp: str | None = None
    status: Any = None
    message_type: str | None = None

    def to_json(self) -> dict[str, Any]:
        out: dict[str, Any] = {
            "DataSetWriterId": self.writer_id,
            "WriterGroupName": self.writer_group,
            "DataSetWriterName": self.writer_name,
        }
        if self.timestamp is not None:
            out["Timestamp"] = self.timestamp
        out["Payload"] = self.payload
        return out


@dataclass(slots=True)
class NetworkMessage:
    """A parsed NetworkMessage (ua-data or ua-metadata)."""

    message_id: str
    message_type: str
    publisher_id: str
    reply_to: str
    class_id: str | None = None
    messages: list[DataSetMessage] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict)

    @property
    def publisher_il(self) -> str:
        return self.publisher_id.partition("/")[2]


def localized_text(value: Any) -> str | None:
    """Text of a LocalizedText or of a list of them (en-US preferred)."""
    if isinstance(value, list):
        texts = [item for item in value if isinstance(item, dict)]
        preferred = [item for item in texts if item.get("Locale") == "en-US"]
        return localized_text((preferred or texts or [None])[0])
    if isinstance(value, dict):
        value = value.get("Text")
    return value if isinstance(value, str) else None


def _str(value: Any, default: str = "") -> str:
    return value if isinstance(value, str) else default


def _int(value: Any) -> int | None:
    return value if isinstance(value, int) and not isinstance(value, bool) else None


def parse_message(payload: bytes | str) -> NetworkMessage:
    """Parse a NetworkMessage; unknown keys are ignored (REQ-MSG-046)."""
    try:
        data = json.loads(payload)
    except (ValueError, UnicodeDecodeError) as err:
        raise OecError("payload is no JSON") from err
    if not isinstance(data, dict):
        raise OecError("payload is no JSON object")
    message_id = data.get("MessageId")
    message_type = data.get("MessageType")
    publisher_id = data.get("PublisherId")
    if not all(isinstance(v, str) for v in (message_id, message_type, publisher_id)):
        raise OecError("MessageId, MessageType or PublisherId missing")
    message = NetworkMessage(
        message_id,
        message_type,
        publisher_id,
        _str(data.get("ReplyTo")),
        data.get("DataSetClassId") if isinstance(data.get("DataSetClassId"), str) else None,
        raw=data,
    )
    if message_type != "ua-data":
        return message
    entries = data.get("Messages")
    if not isinstance(entries, list):
        raise OecError("Messages is no array")
    for entry in entries:
        if not isinstance(entry, dict) or _int(entry.get("DataSetWriterId")) is None:
            continue
        body = entry.get("Payload")
        message.messages.append(
            DataSetMessage(
                entry["DataSetWriterId"],
                _str(entry.get("WriterGroupName")),
                _str(entry.get("DataSetWriterName")),
                body if isinstance(body, dict) else {},
                entry.get("Timestamp") if isinstance(entry.get("Timestamp"), str) else None,
                entry.get("Status"),
                entry.get("MessageType") if isinstance(entry.get("MessageType"), str) else None,
            )
        )
    return message


class MessageIds:
    """MessageId generator `<unixMs>-<PublisherId>` (MG 8.2.1), strictly increasing."""

    def __init__(self, publisher_id: str, clock: Clock = time.time) -> None:
        self._publisher_id = publisher_id
        self._clock = clock
        self._last = 0

    def next(self) -> str:
        millis = max(int(self._clock() * 1000), self._last + 1)
        self._last = millis
        return f"{millis}-{self._publisher_id}"


def network_message(
    message_id: str,
    publisher_id: str,
    messages: list[dict[str, Any]],
    reply_to: str = "",
    class_id: str | None = None,
) -> str:
    """Serialize a ua-data NetworkMessage."""
    out: dict[str, Any] = {
        "MessageId": message_id,
        "MessageType": "ua-data",
        "PublisherId": publisher_id,
    }
    if class_id is not None:
        out["DataSetClassId"] = class_id
    out["ReplyTo"] = reply_to
    out["Messages"] = messages
    return json.dumps(out, separators=(",", ":"), ensure_ascii=False)


@dataclass(frozen=True, slots=True)
class Identity:
    """The integration as an OEC application (ServiceType ITConnector)."""

    serial_number: str
    software_revision: str
    service_type: str = "ITConnector"
    manufacturer: str = "Open Industry 4.0 Alliance"
    manufacturer_uri: str = "oi4.org"
    model: str = "OEC Home Assistant"
    product_code: str = "OEC-HOME-ASSISTANT"
    description: str = "Home Assistant integration of OI4 Open Edge Computing"
    device_manual: str = ""

    @property
    def il(self) -> str:
        return identification_link(
            self.manufacturer_uri, self.model, self.product_code, self.serial_number
        )

    @property
    def app_id(self) -> str:
        return encode_id(self.il)

    @property
    def publisher_id(self) -> str:
        return f"{self.service_type}/{self.il}"

    @property
    def client_id(self) -> str:
        """Stable MQTT ClientId derived from the serial number (REQ-MQTT-013)."""
        tail = "".join(c for c in self.serial_number if c.isalnum())[-16:]
        return f"oec-ha-{tail.lower()}"

    def mam(self) -> dict[str, Any]:
        return {
            "Manufacturer": {"Locale": "en-US", "Text": self.manufacturer},
            "ManufacturerUri": self.manufacturer_uri,
            "Model": {"Locale": "en-US", "Text": self.model},
            "ProductCode": self.product_code,
            "HardwareRevision": "",
            "SoftwareRevision": self.software_revision,
            "DeviceRevision": "",
            "DeviceManual": self.device_manual,
            "DeviceClass": f"Oi4.{self.service_type}",
            "SerialNumber": self.serial_number,
            "ProductInstanceUri": self.il,
            "RevisionCounter": -1,
            "Description": {"Locale": "en-US", "Text": self.description},
        }


@dataclass(frozen=True, slots=True)
class Outgoing:
    """A message to publish (QoS 0, no retain)."""

    topic: str
    payload: str


class Application:
    """The integration as a Messaging Level 2 application (MG 1.3).

    Builds Birth, Will, Close and Health messages, answers Get requests to the
    own AppId for MAM, Health and Profile (REQ-CMP-015/017/018), answers Set
    and Del with Status events and builds the Get requests of the integration
    as a consumer.
    """

    def __init__(self, identity: Identity, clock: Clock = time.time) -> None:
        self.identity = identity
        self.clock = clock
        self.ids = MessageIds(identity.publisher_id, clock)
        self.health = "NORMAL_0"
        self.health_score = 100

    def _topic(self, method: str, resource: str, with_source: bool = True) -> str:
        il = self.identity.il
        return topic(self.identity.service_type, il, method, resource, il if with_source else None)

    def _dsm(self, writer_id: int, payload: dict[str, Any], stamp: str | None = None) -> dict[str, Any]:
        return DataSetMessage(writer_id, "", self.identity.il, payload, stamp).to_json()

    def _health_payload(self, health: str | None = None, score: int | None = None) -> dict[str, Any]:
        return {
            "Health": self.health if health is None else health,
            "HealthScore": self.health_score if score is None else score,
        }

    def _resource(self, resource: str) -> tuple[str, list[dict[str, Any]]]:
        if resource == "MAM":
            return CLASS_MAM, [self._dsm(DSW_MAM, self.identity.mam())]
        if resource == "Health":
            return CLASS_HEALTH, [self._dsm(DSW_HEALTH, self._health_payload())]
        return CLASS_PROFILE, [self._dsm(DSW_PROFILE, {"Resources": list(PROFILE)})]

    def _publish(self, resource: str, health: str | None = None, score: int | None = None,
                 stamp: str | None = None) -> Outgoing:
        if resource == "Health":
            class_id = CLASS_HEALTH
            entries = [self._dsm(DSW_HEALTH, self._health_payload(health, score), stamp)]
        else:
            class_id, entries = self._resource(resource)
        return Outgoing(
            self._topic("Pub", resource),
            network_message(self.ids.next(), self.identity.publisher_id, entries, class_id=class_id),
        )

    def birth(self) -> Outgoing:
        """Pub MAM about itself, the first message after every connect (MG 6.3.3)."""
        return self._publish("MAM")

    def will(self) -> Outgoing:
        """FAILURE_1/0 about itself, created anew for every connection (REQ-LIFE-016)."""
        return self._publish("Health", "FAILURE_1", 0)

    def close(self) -> Outgoing:
        """NORMAL_0/0 about itself before a graceful disconnect (MG 6.3.4)."""
        return self._publish("Health", "NORMAL_0", 0)

    def heartbeat(self) -> Outgoing:
        """Health about itself: on start, on change and at least every 60 s (MG 7.5.1)."""
        return self._publish("Health", stamp=timestamp(self.clock()))

    def request_filters(self) -> list[str]:
        """Topic filters of the requests addressed to the application."""
        base = f"{TOPIC_ROOT}/{self.identity.service_type}/{self.identity.app_id}"
        return [f"{base}/{method}/#" for method in REQUEST_METHODS]

    def status_bad(self, status: tuple[int, str, str], reply_to: str) -> Outgoing:
        """Status event under the own AppId (MG 8.3.6, REQ-RES-EVENT-005, SF-108)."""
        number, symbol, description = status
        entry = DataSetMessage(
            DSW_STATUS_BAD, "Status/Bad", self.identity.il,
            {
                "Number": number,
                "Description": description,
                "Category": "CAT_STATUS_1",
                "Details": {"SymbolicId": symbol},
            },
            timestamp(self.clock()),
        ).to_json()
        # The Event Filter <EventCategory>/<EventLevel> spans two levels (REQ-TOP-023).
        event_topic = Topic(self.identity.service_type, self.identity.app_id, "Pub", "Event",
                            self.identity.app_id, "Status/Bad")
        return Outgoing(
            str(event_topic),
            network_message(self.ids.next(), self.identity.publisher_id, [entry], reply_to, CLASS_EVENT),
        )

    def handle_request(self, parsed: Topic, message: NetworkMessage) -> list[Outgoing]:
        """Answer a request to the own AppId.

        Get MAM, Health and Profile are answered with the resource. The
        integration offers nothing for modification: every Set and Del is
        answered with the Status event BadNotSupported, or BadNotFound for a
        Source it neither is nor represents (REQ-CMP-043, REQ-RES-EVENT-005).
        A Get for such a Source gets no publication but BadNotFound
        (REQ-CMP-047). Everything else is not answered.
        """
        if (
            parsed.method not in REQUEST_METHODS
            or parsed.app_id != self.identity.app_id
            or parsed.service_type != self.identity.service_type
        ):
            return []
        foreign = parsed.source is not None and parsed.source != self.identity.app_id
        if parsed.method != "Get":
            status = STATUS_BAD_NOT_FOUND if foreign else STATUS_BAD_NOT_SUPPORTED
            return [self.status_bad(status, message.message_id)]
        if message.message_type != "ua-data":
            return []
        if foreign:
            return [self.status_bad(STATUS_BAD_NOT_FOUND, message.message_id)]
        if parsed.resource not in RESOURCES or parsed.filter is not None:
            return []
        class_id, entries = self._resource(parsed.resource)
        pub_topic = self._topic("Pub", parsed.resource, parsed.source is not None)
        paging = _pagination_request(message)
        if paging is None:
            return [
                Outgoing(
                    pub_topic,
                    network_message(
                        self.ids.next(), self.identity.publisher_id, entries,
                        message.message_id, class_id,
                    ),
                )
            ]
        per_page, page = paging
        return self._paginate(pub_topic, class_id, entries, message.message_id, per_page, page)

    def _paginate(
        self, pub_topic: str, class_id: str, entries: list[dict[str, Any]],
        reply_to: str, per_page: int, page: int,
    ) -> list[Outgoing]:
        """Answer a PaginationRequest (REQ-PAG-002/004)."""
        total = len(entries)
        last = max(1, -(-total // per_page))
        out: list[Outgoing] = []
        series_id: str | None = None
        for number in range(1, last + 1) if page == 0 else [page]:
            message_id = self.ids.next()
            if series_id is None:
                series_id = message_id
            chunk = entries[(number - 1) * per_page : number * per_page]
            pagination = DataSetMessage(
                DSW_PAGINATION, "Pagination", self.identity.il,
                {
                    "TotalCount": total,
                    "PerPage": per_page,
                    "Page": number,
                    "HasNext": number * per_page < total,
                    "PaginationId": series_id,
                },
            ).to_json()
            out.append(
                Outgoing(
                    pub_topic,
                    network_message(
                        message_id, self.identity.publisher_id, [*chunk, pagination],
                        reply_to, class_id,
                    ),
                )
            )
        return out

    def get(self, service_type: str, app_il: str, resource: str,
            source_il: str | None = None, class_id: str | None = None) -> Outgoing:
        """A Get request of the integration as a consumer."""
        return Outgoing(
            topic(service_type, app_il, "Get", resource, source_il),
            network_message(self.ids.next(), self.identity.publisher_id, [], class_id=class_id),
        )

    def get_metadata(self, service_type: str, app_il: str, source_il: str, tag: str,
                     writer_id: int) -> Outgoing:
        """Get Metadata of one Data DataSet (MG 9.1.5: empty MetaData)."""
        body = {
            "MessageId": self.ids.next(),
            "MessageType": "ua-metadata",
            "PublisherId": self.identity.publisher_id,
            "DataSetWriterId": writer_id,
            "WriterGroupName": tag,
            "DataSetWriterName": source_il,
            "Timestamp": timestamp(self.clock()),
            "ReplyTo": "",
            "MetaData": {},
        }
        return Outgoing(
            topic(service_type, app_il, "Get", "Metadata", source_il, tag),
            json.dumps(body, separators=(",", ":"), ensure_ascii=False),
        )


def _pagination_request(message: NetworkMessage) -> tuple[int, int] | None:
    for entry in message.messages:
        if entry.writer_id != DSW_PAGINATION_REQUEST:
            continue
        per_page = _int(entry.payload.get("PerPage"))
        page = _int(entry.payload.get("Page", 0))
        if per_page is not None and per_page > 0 and page is not None and page >= 0:
            return per_page, page
    return None
