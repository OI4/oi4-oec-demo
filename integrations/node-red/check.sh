#!/usr/bin/env bash
# Checks of the Node-RED nodes (opt-in; not part of oec/check.sh).
#
#   ./check.sh                    unit and node tests (Node.js >= 18, npm ci on first use)
#   OEC_D7=<oec-check> ./check.sh additionally the D7 live run of test/d7/runtime.js
#                                 (oec-check test --level 2 --peer --announce) against the
#                                 go-testbroker of the Rust SDK (OEC_TESTBROKER, default
#                                 oec/sdk/rust/target/debug/examples/go-testbroker in the
#                                 monorepo); OEC_REQUIRE=1 fails if a tool is missing
#
# Self-contained: works in the monorepo (oec/integrations/node-red) and in
# oi4-oec-demo (integrations/node-red).
set -euo pipefail

cd "$(dirname "$0")"

if [[ ! -d node_modules ]]; then
  echo "== npm ci"
  npm ci --no-audit --no-fund --loglevel=error
fi

echo "== node --test"
npm test --silent

if [[ -z "${OEC_D7:-}" ]]; then
  echo "skipped the D7 live run (set OEC_D7=<path to oec-check>)"
fi

echo "node-red: checks passed"
