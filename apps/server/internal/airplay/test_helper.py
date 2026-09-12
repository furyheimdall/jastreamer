"""AirPlay adapter contracts exercised with the pinned container dependency."""

import base64
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import padding, rsa
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

from pyatv.protocols.airplay.utils import AirPlayMajorVersion, get_protocol_version
from pyatv.protocols.raop.protocols.airplayv1 import AirPlayV1
from pyatv.settings import AirPlayVersion

import helper


class ReceiverFeatureTests(unittest.TestCase):
    def test_short_low_feature_word_keeps_airplay2_authentication(self):
        _, service = helper.device_config(
            {
                "device": {
                    "address": "192.0.2.1",
                    "port": 7000,
                    "properties": {"ft": "0x0,0x10000"},
                    "pairing_required": True,
                }
            }
        )
        self.assertEqual(
            get_protocol_version(service, AirPlayVersion.Auto),
            AirPlayMajorVersion.AirPlayV2,
        )


class MetadataEncodingTests(unittest.IsolatedAsyncioTestCase):
    async def test_unicode_dmap_uses_utf8_byte_lengths_for_every_protocol(self):
        requests = []

        async def exchange(method, **kwargs):
            requests.append((method, kwargs))

        rtsp = SimpleNamespace(exchange=exchange)
        helper.configure_utf8_metadata(SimpleNamespace(rtsp=rtsp))
        metadata = SimpleNamespace(title="日本語", album="アルバム", artist="歌手")
        await rtsp.set_metadata(7, 11, 13, metadata)

        method, request = requests[0]
        self.assertEqual(method, "SET_PARAMETER")
        body = request["body"]
        self.assertEqual(body[:4], b"mlit")
        self.assertEqual(int.from_bytes(body[4:8], "big"), len(body) - 8)
        offset = 8
        for name, value in (
            (b"minm", metadata.title),
            (b"asal", metadata.album),
            (b"asar", metadata.artist),
        ):
            encoded = value.encode("utf-8")
            self.assertEqual(body[offset : offset + 4], name)
            size = int.from_bytes(body[offset + 4 : offset + 8], "big")
            self.assertEqual(size, len(encoded))
            self.assertEqual(body[offset + 8 : offset + 8 + size], encoded)
            offset += 8 + size
        self.assertEqual(offset, len(body))


class LegacyEncryptionTests(unittest.IsolatedAsyncioTestCase):
    @staticmethod
    def _bits(packet, offset, width):
        shift = len(packet) * 8 - offset - width
        return (int.from_bytes(packet, "big") >> shift) & ((1 << width) - 1)

    async def test_receiver_decodes_alac_after_packet_decryption(self):
        private_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)

        class Receiver:
            def __init__(self):
                self.cipher = None
                self.announce_body = ""
                self.encrypted_audio = []
                self.audio = []

            async def announce(self, method, *, body, **kwargs):
                self.announce_body = body
                attributes = dict(
                    line[2:].split(":", 1)
                    for line in body.splitlines()
                    if line.startswith("a=")
                )
                encrypted_key = base64.b64decode(attributes["rsaaeskey"] + "===")
                key = private_key.decrypt(
                    encrypted_key,
                    padding.OAEP(
                        mgf=padding.MGF1(hashes.SHA1()),
                        algorithm=hashes.SHA1(),
                        label=None,
                    ),
                )
                iv = base64.b64decode(attributes["aesiv"] + "===")
                self.cipher = Cipher(algorithms.AES(key), modes.CBC(iv))

            def sendto(self, packet):
                audio = packet[12:]
                self.encrypted_audio.append(audio)
                block_end = len(audio) & ~15
                decryptor = self.cipher.decryptor()
                self.audio.append(
                    decryptor.update(audio[:block_end])
                    + decryptor.finalize()
                    + audio[block_end:]
                )

        receiver = Receiver()
        rtsp = SimpleNamespace(exchange=receiver.announce)
        protocol = AirPlayV1(SimpleNamespace(rtpseq=1), rtsp)
        client = SimpleNamespace(rtsp=rtsp, _protocol=protocol)
        modulus = format(private_key.public_key().public_numbers().n, "x")
        with patch.object(helper, "RAOP_RSA_MODULUS_HEX", modulus):
            helper.configure_legacy_rsa(client, {"et": "0,1", "cn": "0,1"})
        await rtsp.exchange(
            "ANNOUNCE",
            body=(
                "v=0\r\n"
                "a=rtpmap:96 L16/44100/2\r\n"
                "a=fmtp:96 352 0 16 40 10 14 2 255 0 0 44100\r\n"
            ),
        )

        pcm = bytes((index * 73 + 19) & 0xFF for index in range(352 * 4))
        await protocol.send_audio_packet(receiver, bytes(12), pcm)
        alac = receiver.audio[0]

        self.assertIn("a=rtpmap:96 AppleLossless\r\n", receiver.announce_body)
        self.assertNotIn("L16/44100/2", receiver.announce_body)
        self.assertEqual(len(alac), len(pcm) + 4)
        self.assertEqual(self._bits(alac, 0, 3), 1)  # channel-pair element
        self.assertEqual(self._bits(alac, 3, 4), 0)  # element instance
        self.assertEqual(self._bits(alac, 7, 12), 0)  # reserved
        self.assertEqual(self._bits(alac, 19, 1), 0)  # full frame
        self.assertEqual(self._bits(alac, 20, 2), 0)  # no shifted bytes
        self.assertEqual(self._bits(alac, 22, 1), 1)  # uncompressed escape
        self.assertEqual(
            self._bits(alac, 23, len(pcm) * 8).to_bytes(len(pcm), "big"),
            pcm,
        )
        self.assertEqual(self._bits(alac, 23 + len(pcm) * 8, 3), 7)
        self.assertEqual(alac[-1] & 0x3F, 0)

        encrypted = receiver.encrypted_audio[0]
        clear_tail = len(alac) & 15
        self.assertEqual(clear_tail, 4)
        self.assertNotEqual(encrypted[:-clear_tail], alac[:-clear_tail])
        self.assertEqual(encrypted[-clear_tail:], alac[-clear_tail:])

    async def test_pcm_only_rsa_receiver_keeps_l16_packets(self):
        rtsp_calls = []

        async def exchange(method, *args, **kwargs):
            rtsp_calls.append((method, kwargs))

        class Transport:
            def __init__(self):
                self.packet = None

            def sendto(self, packet):
                self.packet = packet

        rtsp = SimpleNamespace(exchange=exchange)
        protocol = AirPlayV1(SimpleNamespace(rtpseq=1), rtsp)
        client = SimpleNamespace(rtsp=rtsp, _protocol=protocol)
        helper.configure_legacy_rsa(client, {"et": "1", "cn": "0"})
        body = "v=0\r\na=rtpmap:96 L16/44100/2\r\n"
        await rtsp.exchange("ANNOUNCE", body=body)
        transport = Transport()
        pcm = bytes(range(32))
        await protocol.send_audio_packet(transport, bytes(12), pcm)

        self.assertIn("a=rtpmap:96 L16/44100/2\r\n", rtsp_calls[0][1]["body"])
        self.assertEqual(len(transport.packet), 12 + len(pcm))

    def test_airplay2_protocol_is_not_wrapped(self):
        async def exchange(*args, **kwargs):
            return None

        async def send_audio_packet(*args, **kwargs):
            return None

        protocol = SimpleNamespace(send_audio_packet=send_audio_packet)
        rtsp = SimpleNamespace(exchange=exchange)
        client = SimpleNamespace(rtsp=rtsp, _protocol=protocol)
        helper.configure_legacy_rsa(client, {"et": "1", "cn": "1"})
        self.assertIs(rtsp.exchange, exchange)
        self.assertIs(protocol.send_audio_packet, send_audio_packet)


class StageErrorTests(unittest.IsolatedAsyncioTestCase):
    async def test_send_audio_rtsp_errors_name_stage_without_payload(self):
        async def rejected(*args, **kwargs):
            raise RuntimeError("credential-and-payload-must-not-escape")

        rtsp = SimpleNamespace(
            set_parameter=rejected,
            set_metadata=rejected,
            set_artwork=rejected,
            record=rejected,
            flush=rejected,
            teardown=rejected,
        )
        helper.configure_rtsp_stage_errors(SimpleNamespace(rtsp=rtsp))
        expected = {
            "set_parameter": "send progress metadata: RuntimeError",
            "set_metadata": "send text metadata: RuntimeError",
            "set_artwork": "send artwork: RuntimeError",
            "record": "start RAOP RECORD: RuntimeError",
            "flush": "flush RAOP session: RuntimeError",
            "teardown": "tear down RAOP session: RuntimeError",
        }
        for method_name, message in expected.items():
            with self.subTest(method_name=method_name):
                with self.assertRaisesRegex(helper.AirPlayStageError, f"^{message}$"):
                    await getattr(rtsp, method_name)(b"sensitive")

    async def test_pyatv_wrapper_does_not_hide_specific_rtsp_stage(self):
        async def wrapped_failure():
            try:
                raise helper.AirPlayStageError(
                    "send artwork: RuntimeError"
                ) from RuntimeError("sensitive")
            except helper.AirPlayStageError as error:
                raise RuntimeError("an error occurred during streaming") from error

        with self.assertRaisesRegex(
            helper.AirPlayStageError, "^send artwork: RuntimeError$"
        ):
            await helper.await_airplay_stage("stream audio", wrapped_failure())


if __name__ == "__main__":
    unittest.main()
