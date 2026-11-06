"""Base entities of the OI4 OEC integration."""

from __future__ import annotations

from homeassistant.core import callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity import Entity

from .const import DOMAIN, signal_asset, signal_connection
from .hub import OecHub


class OecEntity(Entity):
    """An entity of an OEC asset (application or represented asset)."""

    _attr_has_entity_name = True
    _attr_should_poll = False

    def __init__(self, hub: OecHub, il: str, key: str) -> None:
        self.hub = hub
        self.il = il
        self._attr_unique_id = f"{hub.entry.entry_id}_{il}_{key}"
        self._attr_device_info = DeviceInfo(identifiers={(DOMAIN, il)})

    @property
    def available(self) -> bool:
        return self.hub.available(self.il)

    async def async_added_to_hass(self) -> None:
        entry_id = self.hub.entry.entry_id
        for signal in (signal_asset(entry_id, self.il), signal_connection(entry_id)):
            self.async_on_remove(
                async_dispatcher_connect(self.hass, signal, self._handle_update)
            )

    @callback
    def _handle_update(self) -> None:
        self.async_write_ha_state()
