"""Sensors of the OI4 OEC integration: Health, HealthScore and Data fields."""

from __future__ import annotations

from typing import Any

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorStateClass,
)
from homeassistant.const import PERCENTAGE
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from . import OecConfigEntry
from .const import HEALTH_OPTIONS, signal_new_fields, signal_new_health
from .entity import OecEntity
from .hub import OecHub
from .model import KIND_NUMBER, KIND_TEXT

PARALLEL_UPDATES = 0

_UNIT_CLASSES: dict[str, SensorDeviceClass] = {
    "°C": SensorDeviceClass.TEMPERATURE,
    "°F": SensorDeviceClass.TEMPERATURE,
    "K": SensorDeviceClass.TEMPERATURE,
    "Pa": SensorDeviceClass.PRESSURE,
    "hPa": SensorDeviceClass.PRESSURE,
    "kPa": SensorDeviceClass.PRESSURE,
    "bar": SensorDeviceClass.PRESSURE,
    "mbar": SensorDeviceClass.PRESSURE,
    "psi": SensorDeviceClass.PRESSURE,
    "W": SensorDeviceClass.POWER,
    "kW": SensorDeviceClass.POWER,
    "V": SensorDeviceClass.VOLTAGE,
    "mV": SensorDeviceClass.VOLTAGE,
    "A": SensorDeviceClass.CURRENT,
    "mA": SensorDeviceClass.CURRENT,
    "lx": SensorDeviceClass.ILLUMINANCE,
    "Hz": SensorDeviceClass.FREQUENCY,
}
_SPEED_UNITS = frozenset({"m/s", "km/h", "mph", "kn", "ft/s"})
_MAX_STATE = 255


def device_class(unit: str | None, text: str) -> SensorDeviceClass | None:
    """Device class of a numeric field from its unit (and name for ambiguous units)."""
    if unit is None:
        return None
    lowered = text.lower()
    if unit == PERCENTAGE:
        return SensorDeviceClass.HUMIDITY if "humid" in lowered else None
    if unit in _SPEED_UNITS:
        return SensorDeviceClass.WIND_SPEED if "wind" in lowered else SensorDeviceClass.SPEED
    if unit == "ppm":
        return SensorDeviceClass.CO2 if "co2" in lowered else None
    return _UNIT_CLASSES.get(unit)


async def async_setup_entry(
    hass: HomeAssistant,
    entry: OecConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    hub = entry.runtime_data

    @callback
    def add_health(il: str) -> None:
        async_add_entities([HealthSensor(hub, il), HealthScoreSensor(hub, il)])

    @callback
    def add_fields(key: tuple[str, str], names: list[str]) -> None:
        dataset = hub.directory.datasets[key]
        async_add_entities(
            DataSensor(hub, key, name)
            for name in names
            if dataset.kind(name) in (KIND_NUMBER, KIND_TEXT)
        )

    entry.async_on_unload(
        async_dispatcher_connect(hass, signal_new_health(entry.entry_id), add_health)
    )
    entry.async_on_unload(
        async_dispatcher_connect(hass, signal_new_fields(entry.entry_id), add_fields)
    )


class HealthSensor(OecEntity, SensorEntity):
    """Health state of an asset (MG 8.3.2)."""

    _attr_device_class = SensorDeviceClass.ENUM
    _attr_options = list(HEALTH_OPTIONS.values())
    _attr_translation_key = "health"

    def __init__(self, hub: OecHub, il: str) -> None:
        super().__init__(hub, il, "health")

    @property
    def native_value(self) -> str | None:
        asset = self.hub.directory.assets[self.il]
        return HEALTH_OPTIONS.get(asset.health or "")

    @property
    def extra_state_attributes(self) -> dict[str, Any]:
        asset = self.hub.directory.assets[self.il]
        return {"details": asset.health_details} if asset.health_details else {}


class HealthScoreSensor(OecEntity, SensorEntity):
    """HealthScore of an asset in percent (MG 8.3.2)."""

    _attr_native_unit_of_measurement = PERCENTAGE
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "health_score"

    def __init__(self, hub: OecHub, il: str) -> None:
        super().__init__(hub, il, "health_score")

    @property
    def native_value(self) -> int | None:
        return self.hub.directory.assets[self.il].health_score


class DataSensor(OecEntity, SensorEntity):
    """A numeric or text field of a Data DataSet."""

    def __init__(self, hub: OecHub, key: tuple[str, str], name: str) -> None:
        il, tag = key
        super().__init__(hub, il, f"data_{tag}_{name}")
        dataset = hub.directory.datasets[key]
        meta = dataset.fields.get(name)
        self._key = key
        self._field = name
        self._attr_name = (meta.label if meta and meta.label else None) or name
        self._numeric = dataset.kind(name) == KIND_NUMBER
        if self._numeric:
            unit = meta.unit if meta else None
            self._attr_native_unit_of_measurement = unit
            self._attr_device_class = device_class(unit, f"{name} {self._attr_name}")
            self._attr_state_class = SensorStateClass.MEASUREMENT

    @property
    def native_value(self) -> float | int | str | None:
        value = self.hub.directory.datasets[self._key].values.get(self._field)
        if self._numeric:
            if isinstance(value, bool) or not isinstance(value, int | float):
                return None
            return value
        return value[:_MAX_STATE] if isinstance(value, str) else None

    @property
    def extra_state_attributes(self) -> dict[str, Any]:
        dataset = self.hub.directory.datasets[self._key]
        meta = dataset.fields.get(self._field)
        attributes: dict[str, Any] = {"tag": dataset.tag, "field": self._field}
        if meta is not None and meta.description:
            attributes["description"] = meta.description
        if dataset.timestamp:
            attributes["timestamp"] = dataset.timestamp
        if dataset.status is not None:
            attributes["status"] = dataset.status
        return attributes
