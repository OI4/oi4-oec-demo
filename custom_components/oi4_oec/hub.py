"""Connection of a config entry to one OEC bus."""

from __future__ import annotations

import asyncio
import contextlib
import logging
import ssl
import time
from datetime import timedelta
from typing import TYPE_CHECKING

from homeassistant.const import CONF_HOST, CONF_PASSWORD, CONF_PORT, CONF_USERNAME
from homeassistant.core import CALLBACK_TYPE, HomeAssistant, callback
from homeassistant.helpers import device_registry as dr
from homeassistant.helpers.dispatcher import async_dispatcher_send
from homeassistant.helpers.event import async_track_time_interval

from .bus import BrokerSettings, Bus, tls_context
from .const import (
    AUTH_CERTIFICATE,
    CONF_AUTH,
    CONF_CA_CERT,
    CONF_CLIENT_CERT,
    CONF_CLIENT_KEY,
    CONF_KEY_PASSWORD,
    CONF_SERIAL,
    DOCUMENTATION,
    DOMAIN,
    VERSION,
    signal_asset,
    signal_connection,
    signal_new_app,
    signal_new_fields,
    signal_new_health,
)
from .model import Asset, Changes, Directory
from .oec import Application, Identity, Outgoing

if TYPE_CHECKING:
    from homeassistant.config_entries import ConfigEntry

_LOGGER = logging.getLogger(__name__)

TICK = timedelta(seconds=5)
STOP_TIMEOUT = 5.0


def identity_for(entry: ConfigEntry) -> Identity:
    return Identity(
        serial_number=entry.data[CONF_SERIAL],
        software_revision=VERSION,
        device_manual=DOCUMENTATION,
    )


async def async_tls_context(hass: HomeAssistant, data: dict) -> ssl.SSLContext:
    """Build the TLS context of a config entry in the executor."""
    certificate = data.get(CONF_AUTH) == AUTH_CERTIFICATE
    return await hass.async_add_executor_job(
        tls_context,
        data.get(CONF_CA_CERT),
        data.get(CONF_CLIENT_CERT) if certificate else None,
        data.get(CONF_CLIENT_KEY) if certificate else None,
        data.get(CONF_KEY_PASSWORD) if certificate else None,
    )


def broker_settings(data: dict) -> BrokerSettings:
    password_auth = data.get(CONF_AUTH) != AUTH_CERTIFICATE
    return BrokerSettings(
        host=data[CONF_HOST],
        port=data[CONF_PORT],
        username=data.get(CONF_USERNAME) if password_auth else None,
        password=data.get(CONF_PASSWORD) if password_auth else None,
    )


class OecHub:
    """The integration as an OEC application on one broker."""

    def __init__(self, hass: HomeAssistant, entry: ConfigEntry) -> None:
        self.hass = hass
        self.entry = entry
        self.application = Application(identity_for(entry))
        self.directory = Directory(self.application)
        self.bus: Bus | None = None
        self._task: asyncio.Task | None = None
        self._unsub_tick: CALLBACK_TYPE | None = None
        self._device_ids: dict[str, str] = {}

    @property
    def own_il(self) -> str:
        return self.application.identity.il

    @property
    def connected(self) -> bool:
        return self.bus is not None and self.bus.connected

    async def async_prepare(self) -> None:
        """Build the TLS context (raises ssl.SSLError) and the hub device."""
        context = await async_tls_context(self.hass, self.entry.data)
        self.bus = Bus(
            broker_settings(self.entry.data),
            context,
            self.application,
            self.directory.subscriptions(),
            self._on_message,
            self._on_connection,
        )
        device = dr.async_get(self.hass).async_get_or_create(
            config_entry_id=self.entry.entry_id,
            identifiers={(DOMAIN, self.own_il)},
            name=self.entry.title,
            manufacturer="Open Industry 4.0 Alliance",
            model="OEC Home Assistant",
            model_id="OEC-HOME-ASSISTANT",
            serial_number=self.application.identity.serial_number,
            sw_version=VERSION,
            entry_type=dr.DeviceEntryType.SERVICE,
        )
        self._device_ids[self.own_il] = device.id

    @callback
    def async_start(self) -> None:
        """Start the bus connection and the timer for pending Metadata."""
        assert self.bus is not None
        self._task = self.entry.async_create_background_task(
            self.hass, self.bus.run(), f"{DOMAIN} bus {self.entry.title}"
        )
        self._unsub_tick = async_track_time_interval(self.hass, self._tick, TICK)

    async def async_stop(self) -> None:
        """Publish the Close message and disconnect."""
        if self._unsub_tick is not None:
            self._unsub_tick()
            self._unsub_tick = None
        if self.bus is not None:
            self.bus.stop()
        if self._task is not None:
            with contextlib.suppress(TimeoutError, asyncio.CancelledError):
                await asyncio.wait_for(asyncio.shield(self._task), STOP_TIMEOUT)
            if not self._task.done():
                self._task.cancel()
            self._task = None

    @callback
    def _on_connection(self, connected: bool) -> None:
        async_dispatcher_send(self.hass, signal_connection(self.entry.entry_id))

    @callback
    def _on_message(self, topic: str, payload: bytes) -> list[Outgoing]:
        changes = self.directory.ingest(topic, payload, time.time())
        outgoing = list(changes.requests)
        if changes.request is not None:
            outgoing += self.application.handle_request(*changes.request)
        self._apply(changes)
        return outgoing

    @callback
    def _tick(self, _now) -> None:
        self._apply(self.directory.tick(time.time()))

    @callback
    def _apply(self, changes: Changes) -> None:
        entry_id = self.entry.entry_id
        for il in dict.fromkeys([*changes.new_assets, *changes.devices]):
            self._update_device(self.directory.assets[il])
        for il in changes.new_apps:
            async_dispatcher_send(self.hass, signal_new_app(entry_id), il)
        for il in changes.health:
            async_dispatcher_send(self.hass, signal_new_health(entry_id), il)
        for key, names in changes.fields:
            async_dispatcher_send(self.hass, signal_new_fields(entry_id), key, names)
        for il in changes.assets:
            async_dispatcher_send(self.hass, signal_asset(entry_id, il))

    @callback
    def _update_device(self, asset: Asset) -> None:
        def revision(key: str) -> str | None:
            value = asset.text(key)
            return value if value and value != "undefined" else None

        via = self.own_il if asset.is_app else asset.app_il
        device = dr.async_get(self.hass).async_get_or_create(
            config_entry_id=self.entry.entry_id,
            identifiers={(DOMAIN, asset.il)},
            name=asset.name,
            manufacturer=asset.text("Manufacturer"),
            model=asset.text("Model"),
            model_id=asset.text("ProductCode"),
            serial_number=asset.text("SerialNumber"),
            sw_version=revision("SoftwareRevision"),
            hw_version=revision("HardwareRevision"),
            via_device_id=self._device_ids.get(via),
        )
        self._device_ids[asset.il] = device.id

    @callback
    def available(self, il: str) -> bool:
        """Entities of an asset are available while the bus and its application are online."""
        return self.connected and self.directory.app_online(il) is not False
