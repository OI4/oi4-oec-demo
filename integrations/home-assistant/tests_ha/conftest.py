"""Fixtures of the Home Assistant tests."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

# custom_components/ is next to tests_ha/ (monorepo) or at the repository root (oi4-oec-demo).
for base in Path(__file__).resolve().parents:
    if (base / "custom_components" / "oi4_oec" / "manifest.json").is_file():
        sys.path.insert(0, str(base))
        break

pytest_plugins = ["pytest_homeassistant_custom_component"]


@pytest.fixture(autouse=True)
def auto_enable_custom_integrations(enable_custom_integrations):
    """Load custom_components/oi4_oec in every test."""
    return
