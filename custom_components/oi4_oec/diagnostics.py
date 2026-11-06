"""Diagnostics of the OI4 OEC integration."""

from __future__ import annotations

from typing import Any

from homeassistant.components.diagnostics import async_redact_data
from homeassistant.const import CONF_PASSWORD, CONF_USERNAME
from homeassistant.core import HomeAssistant

from . import OecConfigEntry
from .const import CONF_CLIENT_KEY, CONF_KEY_PASSWORD

TO_REDACT = {CONF_PASSWORD, CONF_USERNAME, CONF_CLIENT_KEY, CONF_KEY_PASSWORD}


async def async_get_config_entry_diagnostics(
    hass: HomeAssistant, entry: OecConfigEntry
) -> dict[str, Any]:
    hub = entry.runtime_data
    directory = hub.directory
    return {
        "entry": async_redact_data(dict(entry.data), TO_REDACT),
        "application": hub.application.identity.publisher_id,
        "connected": hub.connected,
        "last_error": hub.bus.last_error if hub.bus else None,
        "apps": [
            {"il": app.il, "service_type": app.service_type, "online": app.online}
            for app in directory.apps.values()
        ],
        "assets": [
            {
                "il": asset.il,
                "app": asset.app_il,
                "mam": asset.mam,
                "health": asset.health,
                "health_score": asset.health_score,
            }
            for asset in directory.assets.values()
        ],
        "datasets": [
            {
                "asset": dataset.asset_il,
                "tag": dataset.tag,
                "writer_id": dataset.writer_id,
                "has_metadata": dataset.has_metadata,
                "fields": sorted(dataset.announced),
                "values": dataset.values,
            }
            for dataset in directory.datasets.values()
        ],
    }
