"""Constants of the OI4 OEC integration."""

from __future__ import annotations

from typing import Final

DOMAIN: Final = "oi4_oec"
NAME: Final = "OI4 Open Edge Computing"
VERSION: Final = "0.1.0"
DOCUMENTATION: Final = "https://github.com/OI4/oi4-oec-demo/tree/HEAD/integrations/home-assistant"

CONF_CA_CERT: Final = "ca_cert"
CONF_AUTH: Final = "auth"
CONF_CLIENT_CERT: Final = "client_cert"
CONF_CLIENT_KEY: Final = "client_key"
CONF_KEY_PASSWORD: Final = "key_password"
CONF_SERIAL: Final = "serial"

AUTH_PASSWORD: Final = "password"
AUTH_CERTIFICATE: Final = "certificate"

DEFAULT_PORT: Final = 8883

# Home Assistant state values of the OEC Health (MG 8.3.2).
HEALTH_OPTIONS: Final = {
    "NORMAL_0": "normal",
    "FAILURE_1": "failure",
    "CHECK_FUNCTION_2": "check_function",
    "OFF_SPEC_3": "off_spec",
    "MAINTENANCE_REQUIRED_4": "maintenance_required",
}


def signal_new_app(entry_id: str) -> str:
    return f"{DOMAIN}_{entry_id}_new_app"


def signal_new_health(entry_id: str) -> str:
    return f"{DOMAIN}_{entry_id}_new_health"


def signal_new_fields(entry_id: str) -> str:
    return f"{DOMAIN}_{entry_id}_new_fields"


def signal_asset(entry_id: str, il: str) -> str:
    return f"{DOMAIN}_{entry_id}_asset_{il}"


def signal_connection(entry_id: str) -> str:
    return f"{DOMAIN}_{entry_id}_connection"
