#!/usr/bin/env python3
"""Run the OEC bus core of the integration without Home Assistant.

Connects like the integration (Birth, Will, Health, Level 2 responder,
discovery) and prints the discovered applications, assets and Data fields.
Needs aiomqtt (pip install aiomqtt==2.5.1). Example against oec/dev:

  python tools/run_bus.py --host localhost --ca ../../dev/generated/broker/ca.pem \\
      --credentials ../../dev/generated/roots/oec-dev-app/run/secrets/oi4/credentials
"""

from __future__ import annotations

import argparse
import asyncio
import base64
import contextlib
import logging
import signal
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "tests"))
from _load import load  # noqa: E402

oec = load("oec")
model = load("model")
bus = load("bus")


def _read(path: str | None) -> str | None:
    return None if path is None else Path(path).read_text(encoding="utf-8")


def _credentials(args: argparse.Namespace) -> tuple[str | None, str | None]:
    if args.credentials:
        # Credentials file of an OEC container (MG 6.2.3): base64 of username:password.
        raw = base64.b64decode(Path(args.credentials).read_text(encoding="ascii").strip()).decode()
        username, _, password = raw.partition(":")
        return username, password
    return args.username, (_read(args.password_file) or "").strip() or None


def _print(directory: model.Directory, bus_: bus.Bus) -> None:
    print(f"--- connected={bus_.connected} apps={len(directory.apps)} assets={len(directory.assets)}")
    for app in directory.apps.values():
        print(f"app {app.service_type}/{app.il} online={app.online}")
        for asset in (a for a in directory.assets.values() if a.app_il == app.il):
            print(f"  asset {asset.name!r} health={asset.health}/{asset.health_score}")
            for dataset in (d for d in directory.datasets.values() if d.asset_il == asset.il):
                print(f"    {dataset.tag} metadata={dataset.has_metadata}")
                for name in sorted(dataset.announced):
                    meta = dataset.fields.get(name)
                    label = meta.label if meta else None
                    unit = meta.unit if meta else None
                    print(f"      {name} [{dataset.kind(name)}] {label or ''} = {dataset.values.get(name)!r} {unit or ''}")
    sys.stdout.flush()


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--host", required=True)
    parser.add_argument("--port", type=int, default=8883)
    parser.add_argument("--ca", help="CA certificate (PEM) of the broker; default: system trust store")
    parser.add_argument("--credentials", help="file with base64(username:password)")
    parser.add_argument("--username")
    parser.add_argument("--password-file")
    parser.add_argument("--cert", help="client certificate (PEM)")
    parser.add_argument("--key", help="client key (PEM)")
    parser.add_argument("--serial", default="oec-ha-dev", help="SerialNumber of the integration")
    parser.add_argument("--interval", type=float, default=15.0, help="seconds between two prints")
    parser.add_argument("--duration", type=float, default=0.0, help="stop after seconds (0: never)")
    args = parser.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")

    username, password = _credentials(args)
    context = bus.tls_context(_read(args.ca), _read(args.cert), _read(args.key))
    application = oec.Application(oec.Identity(args.serial, "dev"))
    directory = model.Directory(application)
    print(f"AppId {application.identity.publisher_id}")

    def on_message(topic: str, payload: bytes) -> list:
        changes = directory.ingest(topic, payload, time.monotonic())
        out = list(changes.requests)
        if changes.request is not None:
            out += application.handle_request(*changes.request)
        return out

    runner = bus.Bus(
        bus.BrokerSettings(args.host, args.port, username, password),
        context, application, directory.subscriptions(), on_message, lambda up: print(f"connected={up}"),
    )
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, runner.stop)
    task = asyncio.create_task(runner.run())
    started = time.monotonic()
    last_print = started
    while not task.done():
        with contextlib.suppress(TimeoutError):
            await asyncio.wait_for(asyncio.shield(task), 1.0)
        now = time.monotonic()
        for request in directory.tick(now).requests:
            print("unexpected request", request.topic)
        if now - last_print >= args.interval:
            last_print = now
            _print(directory, runner)
        if args.duration and now - started >= args.duration:
            runner.stop()
            await task
    _print(directory, runner)


if __name__ == "__main__":
    asyncio.run(main())
