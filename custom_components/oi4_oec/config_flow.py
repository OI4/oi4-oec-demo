"""Config flow of the OI4 OEC integration."""

from __future__ import annotations

import ssl
from typing import Any

import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigFlowResult
from homeassistant.const import CONF_HOST, CONF_PASSWORD, CONF_PORT, CONF_USERNAME
from homeassistant.helpers import config_validation as cv
from homeassistant.helpers import instance_id
from homeassistant.helpers.selector import (
    SelectSelector,
    SelectSelectorConfig,
    SelectSelectorMode,
    TextSelector,
    TextSelectorConfig,
    TextSelectorType,
)

from .bus import ConnectError, probe
from .const import (
    AUTH_CERTIFICATE,
    AUTH_PASSWORD,
    CONF_AUTH,
    CONF_CA_CERT,
    CONF_CLIENT_CERT,
    CONF_CLIENT_KEY,
    CONF_KEY_PASSWORD,
    CONF_SERIAL,
    DEFAULT_PORT,
    DOMAIN,
    VERSION,
)
from .hub import async_tls_context, broker_settings
from .oec import Identity

_MULTILINE = TextSelector(TextSelectorConfig(multiline=True))
_PASSWORD = TextSelector(TextSelectorConfig(type=TextSelectorType.PASSWORD))

STEP_USER = vol.Schema(
    {
        vol.Required(CONF_HOST): str,
        vol.Required(CONF_PORT, default=DEFAULT_PORT): cv.port,
        vol.Optional(CONF_CA_CERT): _MULTILINE,
        vol.Required(CONF_AUTH, default=AUTH_PASSWORD): SelectSelector(
            SelectSelectorConfig(
                options=[AUTH_PASSWORD, AUTH_CERTIFICATE],
                mode=SelectSelectorMode.LIST,
                translation_key=CONF_AUTH,
            )
        ),
    }
)
STEP_PASSWORD = vol.Schema(
    {
        vol.Required(CONF_USERNAME): str,
        vol.Required(CONF_PASSWORD): _PASSWORD,
    }
)
STEP_CERTIFICATE = vol.Schema(
    {
        vol.Required(CONF_CLIENT_CERT): _MULTILINE,
        vol.Required(CONF_CLIENT_KEY): _MULTILINE,
        vol.Optional(CONF_KEY_PASSWORD): _PASSWORD,
    }
)


class OecConfigFlow(ConfigFlow, domain=DOMAIN):
    """Set up the connection to an OEC broker."""

    VERSION = 1

    def __init__(self) -> None:
        self._data: dict[str, Any] = {}

    async def async_step_user(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        if user_input is not None:
            await self.async_set_unique_id(f"{user_input[CONF_HOST]}:{user_input[CONF_PORT]}")
            self._abort_if_unique_id_configured()
            self._data = dict(user_input)
            if user_input[CONF_AUTH] == AUTH_CERTIFICATE:
                return await self.async_step_certificate()
            return await self.async_step_password()
        return self.async_show_form(step_id="user", data_schema=STEP_USER)

    async def async_step_password(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        return await self._credentials("password", STEP_PASSWORD, user_input)

    async def async_step_certificate(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        return await self._credentials("certificate", STEP_CERTIFICATE, user_input)

    async def _credentials(
        self, step_id: str, schema: vol.Schema, user_input: dict[str, Any] | None
    ) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            data = {**self._data, **user_input}
            if not data.get(CONF_SERIAL):
                data[CONF_SERIAL] = await instance_id.async_get(self.hass)
            errors = await self._validate(data)
            if not errors:
                return self.async_create_entry(title=f"OEC {data[CONF_HOST]}", data=data)
        return self.async_show_form(
            step_id=step_id,
            data_schema=self.add_suggested_values_to_schema(schema, user_input or {}),
            errors=errors,
        )

    async def _validate(self, data: dict[str, Any]) -> dict[str, str]:
        try:
            context = await async_tls_context(self.hass, data)
        except (ssl.SSLError, ValueError, OSError):
            return {"base": "invalid_tls"}
        identity = Identity(serial_number=data[CONF_SERIAL], software_revision=VERSION)
        try:
            await probe(broker_settings(data), context, f"{identity.client_id}-setup")
        except ConnectError as err:
            return {"base": err.reason}
        return {}
