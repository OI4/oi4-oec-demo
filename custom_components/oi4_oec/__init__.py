"""The OI4 Open Edge Computing (OEC) integration.

Joins an OEC 2.0 Message Bus as an OEC application (ServiceType ITConnector,
Messaging Level 2) and shows the applications and assets on the bus with
their Health and Data in Home Assistant.
"""

from __future__ import annotations

import ssl

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import HassJob, HomeAssistant
from homeassistant.exceptions import ConfigEntryError

from .hub import OecHub

PLATFORMS: list[Platform] = [Platform.BINARY_SENSOR, Platform.SENSOR]

type OecConfigEntry = ConfigEntry[OecHub]


async def async_setup_entry(hass: HomeAssistant, entry: OecConfigEntry) -> bool:
    """Connect to the OEC broker of the entry."""
    hub = OecHub(hass, entry)
    try:
        await hub.async_prepare()
    except (ssl.SSLError, ValueError) as err:
        raise ConfigEntryError(f"Invalid TLS configuration: {err}") from err
    entry.runtime_data = hub
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    hub.async_start()
    # A shutdown job runs before Home Assistant cancels the background tasks
    # (and so before EVENT_HOMEASSISTANT_STOP): the Close message still goes out.
    entry.async_on_unload(hass.async_add_shutdown_job(HassJob(hub.async_stop)))
    return True


async def async_unload_entry(hass: HomeAssistant, entry: OecConfigEntry) -> bool:
    """Publish the Close message and disconnect."""
    unloaded = await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
    if unloaded:
        await entry.runtime_data.async_stop()
    return unloaded
