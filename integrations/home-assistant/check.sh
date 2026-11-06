#!/usr/bin/env bash
# Checks of the Home Assistant integration (opt-in; not part of oec/check.sh).
#
#   ./check.sh                 protocol core: standard library unittests (python3 >= 3.12)
#   OEC_HA_FULL=1 ./check.sh   additionally, in docker: the Home Assistant tests
#                              (pytest-homeassistant-custom-component, Python 3.14)
#                              and hassfest of the pinned Home Assistant version
#
# Works in the monorepo (custom_components/ next to this script) and in
# oi4-oec-demo (custom_components/ at the repository root, this script in
# integrations/home-assistant/).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
if [[ -f "$here/custom_components/oi4_oec/manifest.json" ]]; then
  root="$here"
elif [[ -f "$here/../../custom_components/oi4_oec/manifest.json" ]]; then
  root="$(cd "$here/../.." && pwd)"
else
  echo "custom_components/oi4_oec not found" >&2
  exit 1
fi
rel="${here#"$root"}"
rel="${rel#/}"

# Home Assistant version of the test harness pin (requirements_test.txt) and its Python.
HA_VERSION=2026.10.4
PYTHON_IMAGE=python:3.14

echo "== unittests (protocol core)"
(cd "$here/tests" && python3 -m unittest discover -s . -p 'test_*.py')

if [[ "${OEC_HA_FULL:-}" != "1" ]]; then
  echo "skipped Home Assistant tests and hassfest (set OEC_HA_FULL=1, needs docker)"
  echo "home-assistant: checks passed"
  exit 0
fi

echo "== Home Assistant tests (docker $PYTHON_IMAGE)"
docker run --rm -v "$root":/src -w "/src/$rel" "$PYTHON_IMAGE" bash -c '
  set -eo pipefail
  pip install --quiet --disable-pip-version-check --root-user-action=ignore -r requirements_test.txt
  python -m pytest -q -p no:cacheprovider'

echo "== hassfest (Home Assistant $HA_VERSION)"
docker run --rm -v "$root/custom_components":/github/workspace/custom_components:ro "$PYTHON_IMAGE" bash -c "
  set -eo pipefail
  git clone --quiet --depth 1 --branch $HA_VERSION https://github.com/home-assistant/core /usr/src/homeassistant 2>/dev/null
  cd /usr/src/homeassistant
  pip install --quiet --disable-pip-version-check --root-user-action=ignore uv
  uv pip install --quiet --system -c homeassistant/package_constraints.txt -r requirements.txt
  pin() { awk -F'==' -v name=\"\$1\" '\$1 == name {print \$2}' \"\$2\"; }
  uv pip install --quiet --system \"pipdeptree==\$(pin pipdeptree requirements_test.txt)\" \\
    \"tqdm==\$(pin tqdm requirements_test.txt)\" \"ruff==\$(pin ruff requirements_test_pre_commit.txt)\"
  cd /github/workspace
  sh /usr/src/homeassistant/script/hassfest/docker/entrypoint.sh | tail -3"

echo "home-assistant: checks passed"
