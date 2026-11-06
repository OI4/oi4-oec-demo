"""Setup, entities and unload of the OI4 OEC integration with a simulated bus."""

from __future__ import annotations

import asyncio
import json
from unittest.mock import patch

import pytest
from homeassistant.config_entries import ConfigEntryState
from homeassistant.const import CONF_HOST, CONF_PASSWORD, CONF_PORT, CONF_USERNAME, STATE_UNAVAILABLE
from homeassistant.core import HomeAssistant
from homeassistant.helpers import device_registry as dr
from homeassistant.helpers import entity_registry as er
from pytest_homeassistant_custom_component.common import MockConfigEntry

from custom_components.oi4_oec.const import CONF_AUTH, CONF_SERIAL, DOMAIN
from custom_components.oi4_oec.oec import encode_id, topic

WEATHER = "oi4.org/OEC,20Weather/OEC-WEATHER/w1"
STATION = "oi4.org/OEC,20Weather,20Station/OEC-WEATHER-STATION/w1-station-1"
PUBLISHER = "OTConnector/" + WEATHER
OWN = "oi4.org/OEC,20Home,20Assistant/OEC-HOME-ASSISTANT/ha-test"


class FakeBus:
    """Stands in for bus.Bus: connects at once, delivers fed messages."""

    instance: FakeBus | None = None

    def __init__(self, settings, context, application, subscriptions, on_message, on_connection) -> None:
        self.settings = settings
        self.application = application
        self.subscriptions = subscriptions
        self.on_message = on_message
        self.on_connection = on_connection
        self.connected = False
        self.last_error = None
        self.stopped = asyncio.Event()
        FakeBus.instance = self

    async def run(self) -> None:
        self.connected = True
        self.on_connection(True)
        await self.stopped.wait()
        self.connected = False
        self.on_connection(False)

    def stop(self) -> None:
        self.stopped.set()


def nm(messages: list[dict], reply_to: str = "") -> bytes:
    return json.dumps({"MessageId": "1-" + PUBLISHER, "MessageType": "ua-data", "PublisherId": PUBLISHER,
                       "ReplyTo": reply_to, "Messages": messages}).encode()


def dsm(writer_id: int, il: str, payload: dict, group: str = "") -> dict:
    return {"DataSetWriterId": writer_id, "WriterGroupName": group, "DataSetWriterName": il, "Payload": payload}


def mam(il: str, model: str, serial: str) -> dict:
    return {"Manufacturer": {"Locale": "en-US", "Text": "Open Industry 4.0 Alliance"}, "ManufacturerUri": "oi4.org",
            "Model": {"Locale": "en-US", "Text": model}, "ProductCode": "OEC-WEATHER", "HardwareRevision": "undefined",
            "SoftwareRevision": "1.2.0", "SerialNumber": serial, "ProductInstanceUri": il, "RevisionCounter": 1}


METADATA = json.dumps({
    "MessageId": "2-" + PUBLISHER, "MessageType": "ua-metadata", "PublisherId": PUBLISHER, "ReplyTo": "x",
    "DataSetWriterId": 36, "WriterGroupName": "Oi4Pv", "DataSetWriterName": STATION,
    "MetaData": {"Name": "Oi4Pv", "Fields": [
        {"Name": "Pv", "BuiltInType": 11, "ValueRank": -1, "Description": {"Locale": "en-US", "Text": "Air temperature"},
         "Properties": [{"Key": "EngineeringUnits", "Value": {"UaType": 22, "Value": {"DisplayName": {"Text": "°C"}}}}]},
        {"Name": "Sv1", "BuiltInType": 11, "ValueRank": -1, "Description": {"Locale": "en-US", "Text": "Relative humidity"},
         "Properties": [{"Key": "EngineeringUnits", "Value": {"UaType": 22, "Value": {"DisplayName": {"Text": "%"}}}}]},
        {"Name": "Ok", "BuiltInType": 1, "ValueRank": -1, "Description": {"Locale": "en-US", "Text": "Station ok"}},
    ]},
}).encode()


def wtopic(method: str, resource: str, source: str | None = None, filter_: str | None = None) -> str:
    return topic("OTConnector", WEATHER, method, resource, source, filter_)


@pytest.fixture
async def entry(hass: HomeAssistant):
    entry = MockConfigEntry(
        domain=DOMAIN, unique_id="oec.example:8883", title="OEC oec.example",
        data={CONF_HOST: "oec.example", CONF_PORT: 8883, CONF_AUTH: "password", CONF_USERNAME: "ha",
              CONF_PASSWORD: "secret", CONF_SERIAL: "ha-test"},
    )
    entry.add_to_hass(hass)
    with patch("custom_components.oi4_oec.hub.Bus", FakeBus):
        assert await hass.config_entries.async_setup(entry.entry_id)
        await hass.async_block_till_done()
    yield entry


def state(hass: HomeAssistant, entry, il: str, key: str, platform: str = "sensor"):
    entity_id = er.async_get(hass).async_get_entity_id(platform, DOMAIN, f"{entry.entry_id}_{il}_{key}")
    assert entity_id is not None, key
    return hass.states.get(entity_id)


async def feed(hass: HomeAssistant, topic_: str, payload: bytes) -> list:
    out = FakeBus.instance.on_message(topic_, payload)
    await hass.async_block_till_done()
    return out


async def test_setup_and_discovery(hass: HomeAssistant, entry) -> None:
    bus = FakeBus.instance
    assert entry.state is ConfigEntryState.LOADED
    assert bus.settings.username == "ha"
    assert f"Oi4v2/ITConnector/{encode_id(OWN)}/Get/#" in bus.subscriptions
    devices = dr.async_get(hass)
    hub_device = devices.async_get_device_by_identifier((DOMAIN, OWN), entry.entry_id)
    assert hub_device is not None and hub_device.entry_type is dr.DeviceEntryType.SERVICE
    bus_id = er.async_get(hass).async_get_entity_id("binary_sensor", DOMAIN, f"{entry.entry_id}_bus")
    assert hass.states.get(bus_id).state == "on"

    # First message of an unknown application: discovery and Health entities.
    out = await feed(hass, wtopic("Pub", "Health", WEATHER), nm([dsm(11, WEATHER, {"Health": "NORMAL_0", "HealthScore": 100})]))
    assert [o.topic for o in out] == [wtopic("Get", "MAM"), wtopic("Get", "Health"), wtopic("Get", "Data")]
    assert state(hass, entry, WEATHER, "online", "binary_sensor").state == "on"
    assert state(hass, entry, WEATHER, "health").state == "normal"
    assert state(hass, entry, WEATHER, "health_score").state == "100"

    # MAM answer with a represented asset: devices below the application.
    await feed(hass, wtopic("Pub", "MAM"), nm([dsm(10, WEATHER, mam(WEATHER, "OEC Weather", "w1")),
                                               dsm(28, STATION, mam(STATION, "OEC Weather Station", "w1-station-1"))], "1-x"))
    app_device = devices.async_get_device_by_identifier((DOMAIN, WEATHER), entry.entry_id)
    station_device = devices.async_get_device_by_identifier((DOMAIN, STATION), entry.entry_id)
    assert (app_device.name, app_device.sw_version, app_device.hw_version) == ("OEC Weather w1", "1.2.0", None)
    assert app_device.via_device_id == hub_device.id
    assert station_device.name == "OEC Weather Station w1-station-1"
    assert station_device.via_device_id == app_device.id
    await feed(hass, wtopic("Pub", "Health", STATION), nm([dsm(29, STATION, {"Health": "OFF_SPEC_3", "HealthScore": 60})]))
    assert state(hass, entry, STATION, "health").state == "off_spec"

    # Data, then Metadata: entities with names, units and device classes.
    out = await feed(hass, wtopic("Pub", "Data"), nm([dsm(36, STATION, {"Pv": 6.2, "Sv1": 80.5, "Ok": True}, "Oi4Pv")]))
    assert [o.topic for o in out] == [wtopic("Get", "Metadata", STATION, "Oi4Pv")]
    await feed(hass, wtopic("Pub", "Metadata", STATION, "Oi4Pv"), METADATA)
    temperature = state(hass, entry, STATION, "data_Oi4Pv_Pv")
    assert temperature.state == "6.2"
    assert temperature.attributes["unit_of_measurement"] == "°C"
    assert temperature.attributes["device_class"] == "temperature"
    assert temperature.attributes["friendly_name"] == "OEC Weather Station w1-station-1 Air temperature"
    assert temperature.attributes["tag"] == "Oi4Pv"
    assert state(hass, entry, STATION, "data_Oi4Pv_Sv1").attributes["device_class"] == "humidity"
    assert state(hass, entry, STATION, "data_Oi4Pv_Ok", "binary_sensor").state == "on"
    await feed(hass, wtopic("Pub", "Data", STATION, "Oi4Pv"), nm([dsm(36, STATION, {"Pv": 7.5}, "Oi4Pv")]))
    assert state(hass, entry, STATION, "data_Oi4Pv_Pv").state == "7.5"

    # Requests to the integration itself are answered.
    own_get = topic("ITConnector", OWN, "Get", "MAM")
    [answer] = await feed(hass, own_get, nm([]))
    assert answer.topic == topic("ITConnector", OWN, "Pub", "MAM")

    # Close message: the application is offline, its entities unavailable.
    await feed(hass, wtopic("Pub", "Health", WEATHER), nm([dsm(11, WEATHER, {"Health": "NORMAL_0", "HealthScore": 0})]))
    assert state(hass, entry, WEATHER, "online", "binary_sensor").state == "off"
    assert state(hass, entry, STATION, "data_Oi4Pv_Pv").state == STATE_UNAVAILABLE

    # Birth: online again.
    await feed(hass, wtopic("Pub", "MAM", WEATHER), nm([dsm(10, WEATHER, mam(WEATHER, "OEC Weather", "w1"))]))
    assert state(hass, entry, STATION, "data_Oi4Pv_Pv").state == "7.5"


async def test_unload(hass: HomeAssistant, entry) -> None:
    bus = FakeBus.instance
    assert await hass.config_entries.async_unload(entry.entry_id)
    await hass.async_block_till_done()
    assert entry.state is ConfigEntryState.NOT_LOADED
    assert bus.stopped.is_set()


async def test_connection_lost(hass: HomeAssistant, entry) -> None:
    await feed(hass, wtopic("Pub", "Health", WEATHER), nm([dsm(11, WEATHER, {"Health": "NORMAL_0", "HealthScore": 100})]))
    bus = FakeBus.instance
    bus.connected = False
    bus.on_connection(False)
    await hass.async_block_till_done()
    assert state(hass, entry, WEATHER, "online", "binary_sensor").state == STATE_UNAVAILABLE
    bus_id = er.async_get(hass).async_get_entity_id("binary_sensor", DOMAIN, f"{entry.entry_id}_bus")
    assert hass.states.get(bus_id).state == "off"


async def test_shutdown_publishes_close(hass: HomeAssistant, entry) -> None:
    # The Close goes out in a shutdown job, before Home Assistant cancels background tasks.
    bus = FakeBus.instance
    await hass.async_stop()
    assert bus.stopped.is_set()
