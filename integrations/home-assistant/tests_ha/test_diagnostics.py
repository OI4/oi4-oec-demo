"""Diagnostics of the OI4 OEC integration."""

from __future__ import annotations

from homeassistant.core import HomeAssistant
from homeassistant.setup import async_setup_component
from pytest_homeassistant_custom_component.components.diagnostics import get_diagnostics_for_config_entry
from pytest_homeassistant_custom_component.typing import ClientSessionGenerator

from .test_init import STATION, WEATHER, dsm, entry, feed, mam, nm, wtopic  # noqa: F401

REDACTED = "**REDACTED**"


async def test_diagnostics(hass: HomeAssistant, hass_client: ClientSessionGenerator, entry) -> None:  # noqa: F811
    assert await async_setup_component(hass, "diagnostics", {})
    await feed(hass, wtopic("Pub", "MAM"), nm([dsm(28, STATION, mam(STATION, "OEC Weather Station", "s1"))], "1-x"))
    await feed(hass, wtopic("Pub", "Data"), nm([dsm(36, STATION, {"Pv": 6.2}, "Oi4Pv")]))
    result = await get_diagnostics_for_config_entry(hass, hass_client, entry)
    assert result["entry"]["password"] == REDACTED
    assert result["entry"]["username"] == REDACTED
    assert result["entry"]["host"] == "oec.example"
    assert result["connected"] is True
    assert result["application"].startswith("ITConnector/oi4.org/OEC,20Home,20Assistant/")
    assert [app["il"] for app in result["apps"]] == [WEATHER]
    assert {asset["il"] for asset in result["assets"]} == {WEATHER, STATION}
    assert result["datasets"] == [{"asset": STATION, "tag": "Oi4Pv", "writer_id": 36, "has_metadata": False,
                                   "fields": [], "values": {"Pv": 6.2}}]
