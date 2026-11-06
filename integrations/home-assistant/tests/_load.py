"""Load the protocol modules of the integration without Home Assistant.

The package __init__ imports Home Assistant; the protocol core (oec.py,
model.py, bus.py) does not. This registers a bare package so that the core
modules and their relative imports load with the standard library only.
"""

from __future__ import annotations

import importlib
import sys
import types
from pathlib import Path

HERE = Path(__file__).resolve().parent


def _package_dir() -> Path:
    # Monorepo: oec/integrations/home-assistant/custom_components/oi4_oec;
    # oi4-oec-demo: <root>/custom_components/oi4_oec (tests in integrations/home-assistant).
    for base in (HERE.parent, *HERE.parents):
        candidate = base / "custom_components" / "oi4_oec"
        if (candidate / "manifest.json").is_file():
            return candidate
    raise RuntimeError("custom_components/oi4_oec not found")


PACKAGE_DIR = _package_dir()


def load(name: str) -> types.ModuleType:
    if "oi4_oec_core" not in sys.modules:
        package = types.ModuleType("oi4_oec_core")
        package.__path__ = [str(PACKAGE_DIR)]
        sys.modules["oi4_oec_core"] = package
    return importlib.import_module(f"oi4_oec_core.{name}")
