"""Config flow of the OI4 OEC integration."""

from __future__ import annotations

from unittest.mock import AsyncMock, patch

import pytest
from homeassistant import config_entries
from homeassistant.const import CONF_HOST, CONF_PASSWORD, CONF_PORT, CONF_USERNAME
from homeassistant.core import HomeAssistant
from homeassistant.data_entry_flow import FlowResultType
from pytest_homeassistant_custom_component.common import MockConfigEntry

from custom_components.oi4_oec.bus import ConnectError
from custom_components.oi4_oec.const import (
    CONF_AUTH,
    CONF_CA_CERT,
    CONF_CLIENT_CERT,
    CONF_CLIENT_KEY,
    CONF_SERIAL,
    DOMAIN,
)

USER = {CONF_HOST: "oec.example", CONF_PORT: 8883, CONF_AUTH: "password"}
CREDENTIALS = {CONF_USERNAME: "ha", CONF_PASSWORD: "secret"}


@pytest.fixture
def probe():
    with patch("custom_components.oi4_oec.config_flow.probe", new_callable=AsyncMock) as mock:
        yield mock


@pytest.fixture(autouse=True)
def no_setup():
    with patch("custom_components.oi4_oec.async_setup_entry", return_value=True):
        yield


async def _start(hass: HomeAssistant, user: dict) -> dict:
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": config_entries.SOURCE_USER})
    assert result["type"] is FlowResultType.FORM
    assert result["step_id"] == "user"
    return await hass.config_entries.flow.async_configure(result["flow_id"], user)


async def test_password(hass: HomeAssistant, probe: AsyncMock) -> None:
    result = await _start(hass, USER)
    assert result["step_id"] == "password"
    result = await hass.config_entries.flow.async_configure(result["flow_id"], CREDENTIALS)
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert result["title"] == "OEC oec.example"
    assert result["result"].unique_id == "oec.example:8883"
    data = result["data"]
    assert data[CONF_USERNAME] == "ha"
    assert data[CONF_SERIAL]  # the Home Assistant instance id
    settings, _context, client_id = probe.await_args.args
    assert (settings.host, settings.port, settings.username) == ("oec.example", 8883, "ha")
    assert client_id.startswith("oec-ha-") and client_id.endswith("-setup")


@pytest.mark.parametrize(("reason"), ["cannot_connect", "invalid_auth", "tls_error"])
async def test_connect_errors(hass: HomeAssistant, probe: AsyncMock, reason: str) -> None:
    probe.side_effect = ConnectError(reason, "failed")
    result = await _start(hass, USER)
    result = await hass.config_entries.flow.async_configure(result["flow_id"], CREDENTIALS)
    assert result["type"] is FlowResultType.FORM
    assert result["errors"] == {"base": reason}
    probe.side_effect = None
    result = await hass.config_entries.flow.async_configure(result["flow_id"], CREDENTIALS)
    assert result["type"] is FlowResultType.CREATE_ENTRY


async def test_invalid_ca(hass: HomeAssistant, probe: AsyncMock) -> None:
    result = await _start(hass, {**USER, CONF_CA_CERT: "-----BEGIN CERTIFICATE-----\nnope\n-----END CERTIFICATE-----"})
    result = await hass.config_entries.flow.async_configure(result["flow_id"], CREDENTIALS)
    assert result["errors"] == {"base": "invalid_tls"}
    probe.assert_not_awaited()


async def test_invalid_client_certificate(hass: HomeAssistant, probe: AsyncMock) -> None:
    result = await _start(hass, {**USER, CONF_AUTH: "certificate"})
    assert result["step_id"] == "certificate"
    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_CLIENT_CERT: "not a certificate", CONF_CLIENT_KEY: "not a key"}
    )
    assert result["errors"] == {"base": "invalid_tls"}
    probe.assert_not_awaited()


async def test_already_configured(hass: HomeAssistant, probe: AsyncMock) -> None:
    MockConfigEntry(domain=DOMAIN, unique_id="oec.example:8883", data={**USER, **CREDENTIALS}).add_to_hass(hass)
    result = await _start(hass, USER)
    assert result["type"] is FlowResultType.ABORT
    assert result["reason"] == "already_configured"
