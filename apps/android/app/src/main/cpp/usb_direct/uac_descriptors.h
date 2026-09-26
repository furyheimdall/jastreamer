// Copyright 2026 The Jastreamer Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Pure parser for USB Audio Class configuration descriptors. This translation
// unit must not depend on libusb, JNI or Android so that it stays host
// testable.

#ifndef USB_DIRECT_UAC_DESCRIPTORS_H_
#define USB_DIRECT_UAC_DESCRIPTORS_H_

#include <cstdint>
#include <string>
#include <vector>

namespace usb_direct {

enum class UacVersion { kUnknown = 0, kUac1 = 1, kUac2 = 2 };

enum class SyncType { kNone = 0, kAsync, kAdaptive, kSync };

// One PCM type-I isochronous OUT alternate setting of an AudioStreaming
// interface.
struct AltSetting {
  uint8_t interface_number = 0;
  uint8_t alt_setting = 0;
  uint8_t channels = 0;
  uint8_t subslot_bytes = 0;      // UAC1 bSubframeSize / UAC2 bSubslotSize.
  uint8_t bit_resolution = 0;
  uint8_t endpoint_address = 0;   // Data OUT endpoint.
  uint8_t feedback_endpoint = 0;  // 0 when absent.
  // wMaxPacketSize bits 10:0 multiplied by the high-speed transaction count.
  uint16_t max_packet_bytes = 0;
  uint8_t interval = 1;  // bInterval of the data endpoint.
  SyncType sync = SyncType::kNone;
  // UAC1 AS endpoint advertises the sampling-frequency control.
  bool endpoint_rate_control = false;
  uint8_t terminal_link = 0;
  uint8_t clock_source_id = 0;  // UAC2 only, 0 when unknown.
  std::vector<uint32_t> rates;  // UAC1 discrete rates (bSamFreqType > 0).
  uint32_t rate_min = 0;        // UAC1 continuous range (bSamFreqType == 0).
  uint32_t rate_max = 0;
};

struct UacConfig {
  UacVersion version = UacVersion::kUnknown;
  uint8_t control_interface = 0;
  // PCM type-I isochronous OUT alt settings only, in descriptor order.
  std::vector<AltSetting> alts;
  std::string error;  // Empty on success.
};

// Parses a complete configuration descriptor block, i.e. the bytes returned by
// GET_DESCRIPTOR(CONFIGURATION): the configuration descriptor followed by every
// interface, class-specific and endpoint descriptor.
UacConfig ParseConfiguration(const uint8_t* data, size_t length);

}  // namespace usb_direct

#endif  // USB_DIRECT_UAC_DESCRIPTORS_H_
