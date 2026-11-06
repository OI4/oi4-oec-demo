"""Binary sensors of the OI4 OEC integration: bus connection, application online, Boolean Data fields."""

from __future__ import annotations

from typing import Any

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
)
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from . import OecConfigEntry
from .const import DOMAIN, signal_connection, signal_new_app, signal_new_fields
from .entity import OecEntity
from .hub import OecHub
from .model import KIND_BOOLEAN

PARALLEL_UPDATES = 0


async def async_setup_entry(
    hass: HomeAssistant,
    entry: OecConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    hub = entry.runtime_data
    async_add_entities([BusConnectedSensor(hub)])

    @callback
    def add_app(il: str) -> None:
        async_add_entities([OnlineSensor(hub, il)])

    @callback
    def add_fields(key: tuple[str, str], names: list[str]) -> None:
        dataset = hub.directory.datasets[key]
        async_add_entities(
            DataBinarySensor(hub, key, name)
            for name in names
            if dataset.kind(name) == KIND_BOOLEAN
        )

    entry.async_on_unload(
        async_dispatcher_connect(hass, signal_new_app(entry.entry_id), add_app)
    )
    entry.async_on_unload(
        async_dispatcher_connect(hass, signal_new_fields(entry.entry_id), add_fields)
    )


class BusConnectedSensor(BinarySensorEntity):
    """Connection of the integration to the OEC broker."""

    _attr_has_entity_name = True
    _attr_should_poll = False
    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_translation_key = "bus"

    def __init__(self, hub: OecHub) -> None:
        self.hub = hub
        self._attr_unique_id = f"{hub.entry.entry_id}_bus"
        self._attr_device_info = DeviceInfo(identifiers={(DOMAIN, hub.own_il)})

    @property
    def is_on(self) -> bool:
        return self.hub.connected

    async def async_added_to_hass(self) -> None:
        self.async_on_remove(
            async_dispatcher_connect(
                self.hass, signal_connection(self.hub.entry.entry_id), self._handle_update
            )
        )

    @callback
    def _handle_update(self) -> None:
        self.async_write_ha_state()


class OnlineSensor(OecEntity, BinarySensorEntity):
    """Online state of an application from its Birth, Close and Will messages (MG 6.3)."""

    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY
    _attr_translation_key = "online"

    def __init__(self, hub: OecHub, il: str) -> None:
        super().__init__(hub, il, "online")

    @property
    def available(self) -> bool:
        return self.hub.connected

    @property
    def is_on(self) -> bool | None:
        return self.hub.directory.app_online(self.il)


class DataBinarySensor(OecEntity, BinarySensorEntity):
    """A Boolean field of a Data DataSet."""

    def __init__(self, hub: OecHub, key: tuple[str, str], name: str) -> None:
        il, tag = key
        super().__init__(hub, il, f"data_{tag}_{name}")
        meta = hub.directory.datasets[key].fields.get(name)
        self._key = key
        self._field = name
        self._attr_name = (meta.label if meta and meta.label else None) or name

    @property
    def is_on(self) -> bool | None:
        value = self.hub.directory.datasets[self._key].values.get(self._field)
        return value if isinstance(value, bool) else None

    @property
    def extra_state_attributes(self) -> dict[str, Any]:
        dataset = self.hub.directory.datasets[self._key]
        return {"tag": dataset.tag, "field": self._field}
