#!/usr/bin/env python3
"""Private JSON-lines AirPlay sender bridge for Jastreamer.

The process handles one pairing exchange or one playback session. Control is read
from stdin, status is written to stdout, and decoded signed 16-bit big-endian PCM is
read from fd 3. No credential is accepted on argv or written to logs.
"""

from __future__ import annotations

import asyncio
import base64
import importlib.metadata
import ipaddress
import json
import logging
import os
import re
import sys
import time
from typing import Any

PYATV_VERSION = "0.18.0"
PROTOCOL_VERSION = 1
MAX_CONTROL_LINE = 64 * 1024
MAX_ARTWORK_BYTES = 5 * 1024 * 1024
PCM_RATE = 44_100
PCM_CHANNELS = 2
PCM_SAMPLE_BYTES = 2
PCM_FRAME_BYTES = PCM_CHANNELS * PCM_SAMPLE_BYTES
TASK_CLEANUP_TIMEOUT = 5.0
PIN_RE = re.compile(r"^[0-9]{4}$")
FEATURE_WORDS_RE = re.compile(r"^0x([0-9A-Fa-f]{1,8}),0x([0-9A-Fa-f]{1,8})$")

# Public AirTunes receiver RSA parameters; this is not a private key.
RAOP_RSA_MODULUS_HEX = (
    "e7d744f2a2e2788b6c1f55a08eb70544a8fa7945aa8be6c62ce5f51cbdd4dc68"
    "42fe3d1083dd2edec1bfd4252dc02e6f398bdf0e6148ea84855e2e442da6d626"
    "64f674a1f304929ade4f6893ef2df6e711a8c77a0d91c9d980822e50d12922af"
    "ea40ea9f0e14c0f76938c5f3882fc0323dd9fe55155f51bb5921c201629fd733"
    "52d5e2efaabf9ba048d7b813a2b6767f6c3ccf1eb4ce673d037b0d2ea30c5fff"
    "eb06f8d08adde409571a9c689fef10728855dd8cfb9a8bef5c8943ef3b5faa15"
    "dde698beddf3599603eb3e6f61372bb628f6559f599a78bf500687aa7f4976c0"
    "562d412956f8989e18a6355bd81597825e0fc875343ec782117625cdbf98447b"
)


def emit(event: str, **fields: Any) -> None:
    payload = {"event": event, "protocol": PROTOCOL_VERSION, **fields}
    sys.stdout.write(json.dumps(payload, ensure_ascii=False, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def safe_message(error: BaseException) -> str:
    return str(error).replace("\r", " ").replace("\n", " ")[:512] or type(error).__name__


class AirPlayStageError(RuntimeError):
    """Adds the failed RAOP stage without changing the underlying classification."""


async def await_airplay_stage(stage: str, operation: Any) -> Any:
    try:
        return await operation
    except AirPlayStageError:
        raise
    except Exception as error:
        seen: set[int] = set()
        current: BaseException | None = error
        while current is not None and id(current) not in seen:
            if isinstance(current, AirPlayStageError):
                raise current
            seen.add(id(current))
            current = current.__cause__ or current.__context__
        detail = safe_message(error)
        raise AirPlayStageError(
            f"{stage}: {type(error).__name__}: {detail}"
        ) from error


def exception_type_names(error: BaseException) -> str:
    names: list[str] = []
    seen: set[int] = set()
    current: BaseException | None = error
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        names.append(type(current).__name__.lower())
        current = current.__cause__ or current.__context__
    return " ".join(names)


class UnsupportedAudioFormat(ValueError):
    """Raised when a receiver cannot accept pyatv's fixed RAOP wire format."""


def validate_receiver_audio_format(properties: Any) -> None:
    expected = {"sr": PCM_RATE, "ch": PCM_CHANNELS, "ss": PCM_SAMPLE_BYTES * 8}
    parsed: dict[str, int] = {}
    for key, default in expected.items():
        value = properties.get(key, str(default))
        try:
            parsed[key] = int(value)
        except (TypeError, ValueError) as error:
            raise UnsupportedAudioFormat(
                f"receiver advertises malformed {key} audio property"
            ) from error
    actual = (parsed["sr"], parsed["ch"], parsed["ss"])
    supported = (PCM_RATE, PCM_CHANNELS, PCM_SAMPLE_BYTES * 8)
    if actual != supported:
        raise UnsupportedAudioFormat(
            f"receiver audio format {actual[0]} Hz/{actual[1]} channels/"
            f"{actual[2]}-bit is unsupported; only {supported[0]} Hz/"
            f"{supported[1]} channels/{supported[2]}-bit is supported"
        )


def validate_initialized_audio_format(context: Any) -> None:
    actual = (context.sample_rate, context.channels, context.bytes_per_channel)
    supported = (PCM_RATE, PCM_CHANNELS, PCM_SAMPLE_BYTES)
    if actual != supported:
        raise UnsupportedAudioFormat(
            f"pyatv initialized audio format {actual[0]} Hz/{actual[1]} channels/"
            f"{actual[2] * 8}-bit instead of {supported[0]} Hz/"
            f"{supported[1]} channels/{supported[2] * 8}-bit"
        )


async def read_message(reader: asyncio.StreamReader) -> dict[str, Any]:
    try:
        raw = await reader.readline()
    except ValueError as error:
        raise ValueError("control message is too large") from error
    if not raw:
        raise EOFError("control input closed")
    if len(raw) > MAX_CONTROL_LINE or not raw.endswith(b"\n"):
        raise ValueError("control message is too large or incomplete")
    value = json.loads(raw)
    if not isinstance(value, dict):
        raise ValueError("control message must be an object")
    return value


def device_config(message: dict[str, Any]):
    from pyatv.conf import AppleTV, ManualService
    from pyatv.const import PairingRequirement, Protocol

    device = message.get("device")
    if not isinstance(device, dict):
        raise ValueError("device configuration is required")
    address = ipaddress.IPv4Address(str(device.get("address", "")))
    name = str(device.get("name", "AirPlay"))[:255]
    identifier = str(device.get("id", ""))[:255] or None
    port = int(device.get("port", 0))
    if port < 1 or port > 65535:
        raise ValueError("device port is invalid")
    properties = device.get("properties", {})
    if not isinstance(properties, dict):
        raise ValueError("device properties must be an object")
    clean_properties = {
        str(key)[:128]: str(value)[:4096]
        for key, value in properties.items()
        if "\x00" not in str(key) and "\x00" not in str(value)
    }
    # pyatv 0.18.0 concatenates feature words without padding the low 32 bits.
    for key in ("ft", "features"):
        words = FEATURE_WORDS_RE.fullmatch(clean_properties.get(key, ""))
        if words:
            lower, upper = words.groups()
            clean_properties[key] = f"0x{int(lower, 16):08x},0x{upper}"
    credentials = message.get("credentials") or None
    password = message.get("password") or None
    if credentials is not None:
        credentials = str(credentials)
    if password is not None:
        password = str(password)
    flags = int(clean_properties.get("sf") or clean_properties.get("flags") or "0", 16)
    required = bool(device.get("pairing_required", flags & (0x8 | 0x200)))
    password_required = bool(
        device.get(
            "password_required",
            clean_properties.get("pw", "false").lower() == "true" or flags & 0x80,
        )
    )
    config = AppleTV(address, name)
    service = ManualService(
        identifier,
        Protocol.RAOP,
        port,
        clean_properties,
        credentials=credentials,
        password=password,
        requires_password=password_required,
        pairing_requirement=(
            PairingRequirement.Mandatory if required else PairingRequirement.NotNeeded
        ),
    )
    config.add_service(service)
    return config, service


def uncompressed_alac_packet(pcm: bytes) -> bytes:
    """Frame one stereo PCM packet as an uncompressed ALAC channel pair."""
    if len(pcm) % PCM_FRAME_BYTES:
        raise ValueError("ALAC input is not a whole stereo PCM frame")
    # ALAC's CPE tag, element tag, reserved/partial/shift fields and escape bit
    # occupy 23 bits. Samples stay signed big-endian and interleaved L/R.
    header = 0x100001
    end = 0b111
    return (
        (header << (len(pcm) * 8 + 9))
        | (int.from_bytes(pcm, "big") << 9)
        | (end << 6)
    ).to_bytes(len(pcm) + 4, "big")


def configure_utf8_metadata(client: Any) -> None:
    """Correct pyatv 0.18.0 DMAP lengths without dropping Unicode metadata."""
    rtsp = client.rtsp

    async def utf8_metadata(
        rtsp_session: int,
        rtpseq: int,
        rtptime: int,
        metadata: Any,
    ) -> Any:
        payload = bytearray()
        for name, value in (
            ("minm", metadata.title),
            ("asal", metadata.album),
            ("asar", metadata.artist),
        ):
            if value:
                encoded = value.encode("utf-8")
                payload.extend(name.encode("ascii"))
                payload.extend(len(encoded).to_bytes(4, "big"))
                payload.extend(encoded)
        body = b"mlit" + len(payload).to_bytes(4, "big") + payload
        return await rtsp.exchange(
            "SET_PARAMETER",
            content_type="application/x-dmap-tagged",
            headers={
                "Session": rtsp_session,
                "RTP-Info": f"seq={rtpseq};rtptime={rtptime}",
            },
            body=body,
        )

    rtsp.set_metadata = utf8_metadata


def configure_rtsp_stage_errors(client: Any) -> None:
    """Identify pyatv's otherwise opaque send_audio RTSP failure stage."""
    rtsp = client.rtsp

    def wrap(method_name: str, stage: str) -> None:
        operation = getattr(rtsp, method_name)

        async def staged_operation(*args: Any, **kwargs: Any) -> Any:
            try:
                return await operation(*args, **kwargs)
            except AirPlayStageError:
                raise
            except Exception as error:
                # Do not include request payloads, credentials or receiver responses.
                raise AirPlayStageError(
                    f"{stage}: {type(error).__name__}"
                ) from error

        setattr(rtsp, method_name, staged_operation)

    wrap("set_parameter", "send progress metadata")
    wrap("set_metadata", "send text metadata")
    wrap("set_artwork", "send artwork")
    wrap("record", "start RAOP RECORD")
    wrap("flush", "flush RAOP session")
    wrap("teardown", "tear down RAOP session")


def configure_legacy_rsa(client: Any, properties: Any) -> None:
    """Negotiate advertised AirTunes RSA/AES without changing AirPlay 2."""
    from pyatv.protocols.raop.protocols.airplayv1 import AirPlayV1

    protocol = client._protocol  # Pinned pyatv 0.18.0 protocol instance.
    encryption_types = {
        value.strip() for value in str(properties.get("et", "")).split(",")
    }
    if not isinstance(protocol, AirPlayV1) or "1" not in encryption_types:
        return

    from cryptography.hazmat.primitives import hashes
    from cryptography.hazmat.primitives.asymmetric import padding, rsa
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

    codecs = {value.strip() for value in str(properties.get("cn", "")).split(",")}
    use_alac = "1" in codecs
    key, iv = os.urandom(16), os.urandom(16)
    public_key = rsa.RSAPublicNumbers(65537, int(RAOP_RSA_MODULUS_HEX, 16)).public_key()
    encrypted_key = public_key.encrypt(
        key,
        padding.OAEP(mgf=padding.MGF1(hashes.SHA1()), algorithm=hashes.SHA1(), label=None),
    )
    key_lines = (
        "a=rsaaeskey:" + base64.b64encode(encrypted_key).decode("ascii").rstrip("=")
        + "\r\na=aesiv:" + base64.b64encode(iv).decode("ascii").rstrip("=") + "\r\n"
    )
    cipher = Cipher(algorithms.AES(key), modes.CBC(iv))
    exchange = client.rtsp.exchange
    send_packet = protocol.send_audio_packet


    async def encrypted_exchange(method: str, *args: Any, **kwargs: Any) -> Any:
        if method == "ANNOUNCE":
            body = kwargs.get("body")
            if not isinstance(body, str):
                raise ValueError("RAOP ANNOUNCE SDP is missing")
            if use_alac:
                l16 = "a=rtpmap:96 L16/44100/2\r\n"
                if body.count(l16) != 1:
                    raise ValueError("RAOP ANNOUNCE SDP has an unexpected audio format")
                body = body.replace(l16, "a=rtpmap:96 AppleLossless\r\n")
            kwargs["body"] = body + key_lines
        return await exchange(method, *args, **kwargs)

    async def encrypted_packet(transport: Any, rtp_header: bytes, audio: bytes) -> Any:
        if use_alac:
            audio = uncompressed_alac_packet(audio)
        # AirTunes resets CBC for every packet and leaves its partial block clear.
        block_end = len(audio) & ~15
        encryptor = cipher.encryptor()
        payload = encryptor.update(audio[:block_end]) + encryptor.finalize()
        if block_end != len(audio):
            payload += audio[block_end:]
        return await send_packet(transport, rtp_header, payload)

    # Preserve pyatv's password challenge, timing and encrypted retransmit buffer.
    client.rtsp.exchange = encrypted_exchange
    protocol.send_audio_packet = encrypted_packet




async def run_pair(message: dict[str, Any], reader: asyncio.StreamReader) -> int:
    import pyatv
    from pyatv.const import Protocol

    config, service = device_config(message)
    pairing = await pyatv.pair(
        config, Protocol.RAOP, asyncio.get_running_loop(), name="Jastreamer"
    )
    try:
        await pairing.begin()
        emit("pairing", prompt="Enter the PIN shown on the AirPlay receiver.")
        finish = await read_message(reader)
        if finish.get("op") != "pair_finish":
            raise ValueError("pair_finish was required")
        pin = str(finish.get("pin", ""))
        if not PIN_RE.fullmatch(pin):
            raise ValueError("PIN must contain exactly four digits")
        password = finish.get("password")
        if password:
            service.password = str(password)
        pairing.pin(int(pin))
        await pairing.finish()
        if not pairing.has_paired or not service.credentials:
            raise RuntimeError("receiver did not return pairing credentials")
        emit("paired", credentials=service.credentials)
        return 0
    finally:
        await pairing.close()


class RawPCMSource:
    """Pinned pyatv AudioSource contract over the private PCM descriptor."""

    NO_FRAMES = b""

    def __init__(
        self,
        reader: asyncio.StreamReader,
        transport: asyncio.ReadTransport,
        duration_ms: int,
    ) -> None:
        self._reader = reader
        self._transport = transport
        self._remainder = bytearray()
        self._closed = False
        self._eof = False
        self._duration_ms = max(0, duration_ms)
        self.content_frames = 0
        self.first_frames = asyncio.Event()

    @classmethod
    async def open(cls, descriptor: int, duration_ms: int) -> RawPCMSource:
        reader = asyncio.StreamReader()
        protocol = asyncio.StreamReaderProtocol(reader)
        pipe = os.fdopen(descriptor, "rb", buffering=0, closefd=True)
        try:
            transport, _ = await asyncio.get_running_loop().connect_read_pipe(
                lambda: protocol, pipe
            )
        except BaseException:
            pipe.close()
            raise
        return cls(reader, transport, duration_ms)

    async def _fill(self, requested: int) -> None:
        while len(self._remainder) < requested and not self._closed and not self._eof:
            chunk = await self._reader.read(requested - len(self._remainder))
            if self._closed:
                return
            if not chunk:
                self._eof = True
                return
            self._remainder.extend(chunk)

    async def prime(self, nframes: int) -> bool:
        """Buffer one complete packet without advancing the stream clock."""
        requested = nframes * PCM_FRAME_BYTES
        await self._fill(requested)
        return len(self._remainder) >= requested

    async def close(self) -> None:
        if not self._closed:
            self._closed = True
            self._remainder.clear()
            self._transport.close()
            await asyncio.sleep(0)

    async def readframes(self, nframes: int) -> bytes:
        requested = nframes * PCM_FRAME_BYTES
        await self._fill(requested)
        available = min(requested, len(self._remainder))
        available -= available % PCM_FRAME_BYTES
        if available == 0:
            self._remainder.clear()
            return self.NO_FRAMES
        frames = bytes(self._remainder[:available])
        del self._remainder[:available]
        self.content_frames += available // PCM_FRAME_BYTES
        self.first_frames.set()
        return frames

    async def get_metadata(self):
        from pyatv.interface import MediaMetadata

        return MediaMetadata(duration=self.duration)

    @property
    def sample_rate(self) -> int:
        return PCM_RATE

    @property
    def channels(self) -> int:
        return PCM_CHANNELS

    @property
    def sample_size(self) -> int:
        return PCM_SAMPLE_BYTES

    @property
    def duration(self) -> int:
        return (self._duration_ms + 999) // 1000




def read_artwork(message: dict[str, Any]) -> bytes:
    descriptor = message.get("artwork_fd")
    if descriptor is None:
        # An explicit empty JPEG parameter clears artwork retained by a receiver
        # from its preceding session.
        return b""
    fd = int(descriptor)
    with os.fdopen(fd, "rb", buffering=0, closefd=True) as artwork:
        data = artwork.read(MAX_ARTWORK_BYTES + 1)
    if len(data) > MAX_ARTWORK_BYTES:
        raise ValueError("artwork exceeds the 5 MiB sender limit")
    if len(data) < 4 or not data.startswith(b"\xff\xd8") or not data.endswith(b"\xff\xd9"):
        raise ValueError("artwork is not a complete JPEG image")
    return data


def media_metadata(message: dict[str, Any], artwork: bytes):
    from pyatv.interface import MediaMetadata

    metadata = message.get("metadata")
    if not isinstance(metadata, dict):
        raise ValueError("metadata is required")
    duration_ms = max(0, int(metadata.get("duration_ms", 0)))
    return MediaMetadata(
        title=str(metadata.get("title", ""))[:2048],
        artist=str(metadata.get("artist", ""))[:2048],
        album=str(metadata.get("album", ""))[:2048],
        artwork=artwork,
        duration=duration_ms / 1000.0,
    )


async def control_reader(
    reader: asyncio.StreamReader,
    client: Any,
    stop_requested: asyncio.Event,
    stop_times: list[float],
) -> None:
    while True:
        message = await read_message(reader)
        if message.get("op") != "stop":
            emit("error", kind="protocol", message="unsupported control operation")
            continue
        stop_times.append(time.monotonic())
        stop_requested.set()
        client.stop()
        return


async def position_reporter(
    source: RawPCMSource,
    context: Any,
    started_at: float,
    finished: asyncio.Event,
) -> None:
    latency_ms = max(0, int(context.latency * 1000 / context.sample_rate))
    while not finished.is_set():
        elapsed_ms = max(0, int((time.monotonic() - started_at) * 1000) - latency_ms)
        content_ms = int(source.content_frames * 1000 / source.sample_rate)
        position_ms = min(elapsed_ms, content_ms)
        emit("position", position_ms=position_ms)
        try:
            await asyncio.wait_for(finished.wait(), timeout=0.5)
        except TimeoutError:
            pass


async def run_stream(message: dict[str, Any], reader: asyncio.StreamReader) -> int:
    import pyatv
    from pyatv.const import Protocol
    from pyatv.protocols.airplay.auth import extract_credentials
    from pyatv.support.rtsp import FRAMES_PER_PACKET

    config, service = device_config(message)
    validate_receiver_audio_format(service.properties)
    artwork = read_artwork(message)
    metadata = media_metadata(message, artwork)
    duration_ms = max(0, int(message["metadata"].get("duration_ms", 0)))
    source = await RawPCMSource.open(3, duration_ms)
    atv = None
    manager = None
    stop_requested = asyncio.Event()
    stop_times: list[float] = []
    finished = asyncio.Event()
    prime_task: asyncio.Task[bool] | None = None
    send_task: asyncio.Task[None] | None = None
    first_frame_task: asyncio.Task[bool] | None = None
    control_task: asyncio.Task[None] | None = None
    reporter_task: asyncio.Task[None] | None = None
    position_ms = 0
    try:
        atv = await await_airplay_stage(
            "create RAOP client",
            pyatv.connect(config, asyncio.get_running_loop(), protocol=Protocol.RAOP),
        )
        stream = atv.stream.main_instance
        manager = stream.playback_manager
        manager.acquire()
        client, context = await await_airplay_stage(
            f"connect to RAOP receiver port {service.port}",
            manager.setup(stream.core.service),
        )
        client_id = str(message.get("client_id", ""))
        if not re.fullmatch(r"[0-9A-F]{16}", client_id):
            raise ValueError("client identity is invalid")
        client.rtsp.dacp_id = client_id
        context.credentials = extract_credentials(stream.core.service)
        context.password = stream.core.service.password
        configure_legacy_rsa(client, stream.core.service.properties)
        await await_airplay_stage(
            f"initialize RAOP session on port {service.port}",
            client.initialize(stream.core.service.properties),
        )
        validate_initialized_audio_format(context)
        configure_utf8_metadata(client)
        configure_rtsp_stage_errors(client)
        route = type(client._protocol).__name__  # Pinned pyatv 0.18.0 internal API.

        control_task = asyncio.create_task(
            control_reader(reader, client, stop_requested, stop_times)
        )
        prime_task = asyncio.create_task(source.prime(FRAMES_PER_PACKET))
        done, _ = await asyncio.wait(
            (prime_task, control_task), return_when=asyncio.FIRST_COMPLETED
        )
        if control_task in done:
            await control_task
            emit("stopped", position_ms=0)
            return 0
        if not await prime_task:
            raise RuntimeError("audio source ended before playback started")

        send_task = asyncio.create_task(
            await_airplay_stage(
                f"stream audio over RAOP port {service.port}",
                client.send_audio(source, metadata),
            )
        )
        first_frame_task = asyncio.create_task(source.first_frames.wait())
        done, _ = await asyncio.wait(
            (send_task, first_frame_task, control_task),
            return_when=asyncio.FIRST_COMPLETED,
        )
        if send_task in done:
            await send_task
            raise RuntimeError("audio source ended before playback started")
        if control_task in done:
            await control_task
            emit("stopped", position_ms=0)
            return 0
        await first_frame_task

        started_at = time.monotonic()
        emit(
            "playing",
            position_ms=0,
            latency_ms=max(0, int(context.latency * 1000 / context.sample_rate)),
            route="airplay2" if route == "AirPlayV2" else "airplay1",
        )
        reporter_task = asyncio.create_task(
            position_reporter(source, context, started_at, finished)
        )
        done, _ = await asyncio.wait(
            (send_task, control_task), return_when=asyncio.FIRST_COMPLETED
        )
        if send_task in done:
            await send_task
        else:
            await control_task
            await source.close()
            send_task.cancel()
            await asyncio.wait_for(
                asyncio.gather(send_task, return_exceptions=True),
                timeout=TASK_CLEANUP_TIMEOUT,
            )

        latency_ms = max(0, int(context.latency * 1000 / context.sample_rate))
        finished_at = stop_times[0] if stop_times else time.monotonic()
        position_ms = min(
            duration_ms,
            max(0, int((finished_at - started_at) * 1000) - latency_ms),
        )
        finished.set()
        if reporter_task:
            await reporter_task
        if stop_requested.is_set():
            emit("stopped", position_ms=position_ms)
        else:
            # StreamClient returns only after its receiver-latency padding and RTSP
            # teardown complete, so this is audible EOF rather than source EOF.
            emit("eof", position_ms=duration_ms or position_ms)
        return 0
    finally:
        finished.set()
        await source.close()
        tasks = [
            task
            for task in (
                prime_task,
                send_task,
                first_frame_task,
                control_task,
                reporter_task,
            )
            if task is not None
        ]
        for task in tasks:
            if not task.done():
                task.cancel()
        if tasks:
            try:
                await asyncio.wait_for(
                    asyncio.gather(*tasks, return_exceptions=True),
                    timeout=TASK_CLEANUP_TIMEOUT,
                )
            except TimeoutError:
                logging.warning("timed out cancelling AirPlay stream tasks")
        if manager is not None:
            await manager.teardown()
        if atv is not None:
            pending = atv.close()
            if pending:
                await asyncio.gather(*pending, return_exceptions=True)


async def async_main() -> int:
    installed = importlib.metadata.version("pyatv")
    if installed != PYATV_VERSION:
        raise RuntimeError(f"pyatv {PYATV_VERSION} is required (found {installed})")
    reader = asyncio.StreamReader(limit=MAX_CONTROL_LINE)
    protocol = asyncio.StreamReaderProtocol(reader)
    transport, _ = await asyncio.get_running_loop().connect_read_pipe(
        lambda: protocol, sys.stdin.buffer
    )
    try:
        message = await read_message(reader)
        if message.get("protocol") != PROTOCOL_VERSION:
            raise ValueError("helper protocol version is incompatible")
        operation = message.get("op")
        if operation == "pair_begin":
            return await run_pair(message, reader)
        if operation == "stream":
            return await run_stream(message, reader)
        raise ValueError("first operation must be pair_begin or stream")
    finally:
        transport.close()


def main() -> int:
    if sys.argv[1:] == ["--check"]:
        try:
            installed = importlib.metadata.version("pyatv")
        except importlib.metadata.PackageNotFoundError:
            installed = "missing"
        print(
            f"jastreamer-airplay protocol={PROTOCOL_VERSION} "
            f"pyatv={installed} required={PYATV_VERSION}"
        )
        return 0 if installed == PYATV_VERSION else 1
    if len(sys.argv) != 1:
        print("usage: jastreamer-airplay [--check]", file=sys.stderr)
        return 2
    logging.basicConfig(level=logging.WARNING, stream=sys.stderr)
    try:
        return asyncio.run(async_main())
    except EOFError as error:
        emit("error", kind="cancelled", message=safe_message(error))
    except UnsupportedAudioFormat as error:
        emit("error", kind="unsupported", message=safe_message(error))
    except (ValueError, json.JSONDecodeError) as error:
        emit("error", kind="protocol", message=safe_message(error))
    except Exception as error:  # pyatv exception subclasses are not API-stable.
        names = exception_type_names(error)
        if "auth" in names or "credential" in names or "pair" in names:
            kind = "auth"
        elif "timeout" in names:
            kind = "timeout"
        elif "valueerror" in names or "jsondecodeerror" in names:
            kind = "protocol"
        else:
            kind = "transport"
        message = "receiver rejected authorization" if kind == "auth" else safe_message(error)
        emit("error", kind=kind, message=message)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
