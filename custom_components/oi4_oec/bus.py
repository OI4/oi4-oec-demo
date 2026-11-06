"""MQTT/TLS connection of the integration to the OEC broker (no Home Assistant imports)."""

from __future__ import annotations

import asyncio
import contextlib
import logging
import os
import ssl
import tempfile
from collections.abc import Callable, Iterable
from dataclasses import dataclass

import aiomqtt

from .oec import Application, Outgoing

_LOGGER = logging.getLogger(__name__)

# Health about itself at least every 60 s (MG 7.5.1, SF-106).
HEARTBEAT_INTERVAL = 55.0
KEEPALIVE = 60
CONNECT_TIMEOUT = 15.0
MAX_BACKOFF = 60.0

# CONNACK return codes of MQTT 3.1.1 (4, 5) and their MQTT 5 equivalents in paho.
_AUTH_CODES = frozenset({4, 5, 134, 135})

MessageHandler = Callable[[str, bytes], Iterable[Outgoing]]
ConnectionHandler = Callable[[bool], None]


@dataclass(frozen=True, slots=True)
class BrokerSettings:
    """Where and how to connect (MG 6.2: MQTT 3.1.1 over TLS)."""

    host: str
    port: int = 8883
    username: str | None = None
    password: str | None = None


class ConnectError(Exception):
    """The broker could not be reached or refused the connection."""

    def __init__(self, reason: str, detail: str) -> None:
        super().__init__(detail)
        self.reason = reason


def tls_context(
    ca_pem: str | None,
    cert_pem: str | None = None,
    key_pem: str | None = None,
    key_password: str | None = None,
) -> ssl.SSLContext:
    """TLS 1.2 or higher with verification of the broker certificate (MG 6.2.2).

    Blocking (reads trust stores and key files): run it in an executor.
    Without ca_pem the system trust store is used.
    """
    context = ssl.create_default_context(cadata=ca_pem.strip() if ca_pem else None)
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    context.check_hostname = True
    context.verify_mode = ssl.CERT_REQUIRED
    if cert_pem and key_pem:
        with tempfile.TemporaryDirectory(prefix="oi4_oec_") as folder:
            os.chmod(folder, 0o700)
            cert_file = os.path.join(folder, "client.pem")
            key_file = os.path.join(folder, "client.key")
            for path, content in ((cert_file, cert_pem), (key_file, key_pem)):
                with open(os.open(path, os.O_WRONLY | os.O_CREAT, 0o600), "w", encoding="ascii") as handle:
                    handle.write(content.strip() + "\n")
            context.load_cert_chain(cert_file, key_file, key_password or None)
    return context


def _client(settings: BrokerSettings, context: ssl.SSLContext, identifier: str,
            will: Outgoing | None) -> aiomqtt.Client:
    return aiomqtt.Client(
        settings.host,
        settings.port,
        username=settings.username or None,
        password=settings.password or None,
        identifier=identifier,
        protocol=aiomqtt.ProtocolVersion.V311,
        clean_session=True,
        keepalive=KEEPALIVE,
        timeout=CONNECT_TIMEOUT,
        tls_context=context,
        will=None if will is None else aiomqtt.Will(will.topic, will.payload, qos=0, retain=False),
    )


def _reason(err: aiomqtt.MqttError) -> str:
    code = getattr(err, "rc", None)
    code = getattr(code, "value", code)
    if code in _AUTH_CODES:
        return "invalid_auth"
    cause = err.__cause__ or err.__context__
    if isinstance(cause, ssl.SSLError) or "certificate" in str(err).lower() or "ssl" in str(err).lower():
        return "tls_error"
    return "cannot_connect"


async def probe(settings: BrokerSettings, context: ssl.SSLContext, identifier: str) -> None:
    """Connect once and disconnect; raises ConnectError with a reason."""
    try:
        async with _client(settings, context, identifier, None):
            pass
    except aiomqtt.MqttError as err:
        raise ConnectError(_reason(err), str(err)) from err


class Bus:
    """Keeps the integration connected as an OEC application.

    Every connection registers a new Will, publishes the Birth first, then
    subscribes, publishes Health on start and every HEARTBEAT_INTERVAL and
    the Close message before a graceful disconnect (MG 6.3).
    """

    def __init__(
        self,
        settings: BrokerSettings,
        context: ssl.SSLContext,
        application: Application,
        subscriptions: list[str],
        on_message: MessageHandler,
        on_connection: ConnectionHandler,
    ) -> None:
        self._settings = settings
        self._context = context
        self._app = application
        self._subscriptions = subscriptions
        self._on_message = on_message
        self._on_connection = on_connection
        self._stop = asyncio.Event()
        self.connected = False
        self.last_error: str | None = None

    async def run(self) -> None:
        """Connect and reconnect with backoff until stop() is called."""
        backoff = 1.0
        while not self._stop.is_set():
            try:
                await self._session()
                backoff = 1.0
            except aiomqtt.MqttError as err:
                self.last_error = str(err)
                _LOGGER.warning("OEC broker %s:%s: %s", self._settings.host, self._settings.port, err)
            finally:
                if self.connected:
                    self.connected = False
                    self._on_connection(False)
            if self._stop.is_set():
                break
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(self._stop.wait(), backoff)
            backoff = min(backoff * 2, MAX_BACKOFF)

    def stop(self) -> None:
        """Publish the Close message and disconnect (run() returns)."""
        self._stop.set()

    async def _session(self) -> None:
        app = self._app
        async with _client(self._settings, self._context, app.identity.client_id, app.will()) as client:
            await self._publish(client, [app.birth()])
            await client.subscribe([(item, 0) for item in self._subscriptions])
            await self._publish(client, [app.heartbeat()])
            self.connected = True
            self.last_error = None
            self._on_connection(True)
            receive = asyncio.ensure_future(self._receive(client))
            heartbeat = asyncio.ensure_future(self._heartbeat(client))
            stop = asyncio.ensure_future(self._stop.wait())
            try:
                await asyncio.wait({receive, heartbeat, stop}, return_when=asyncio.FIRST_COMPLETED)
            finally:
                for task in (receive, heartbeat, stop):
                    task.cancel()
                await asyncio.gather(receive, heartbeat, stop, return_exceptions=True)
            for task in (receive, heartbeat):
                if not task.cancelled() and task.exception() is not None:
                    raise task.exception()
            if self._stop.is_set():
                await self._publish(client, [app.close()])

    async def _receive(self, client: aiomqtt.Client) -> None:
        async for message in client.messages:
            payload = message.payload
            if isinstance(payload, str):
                payload = payload.encode()
            elif not isinstance(payload, bytes | bytearray):
                continue
            try:
                outgoing = list(self._on_message(message.topic.value, bytes(payload)))
            except Exception:  # noqa: BLE001 - a bad message must not end the session
                _LOGGER.exception("Failed to process OEC message on %s", message.topic.value)
                continue
            await self._publish(client, outgoing)

    async def _heartbeat(self, client: aiomqtt.Client) -> None:
        while True:
            await asyncio.sleep(HEARTBEAT_INTERVAL)
            await self._publish(client, [self._app.heartbeat()])

    @staticmethod
    async def _publish(client: aiomqtt.Client, messages: Iterable[Outgoing]) -> None:
        for item in messages:
            await client.publish(item.topic, item.payload, qos=0, retain=False)
