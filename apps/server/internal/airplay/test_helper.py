"""AirPlay adapter contracts exercised with the pinned container dependency."""

import unittest

from pyatv.protocols.airplay.utils import AirPlayMajorVersion, get_protocol_version
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


if __name__ == "__main__":
    unittest.main()
