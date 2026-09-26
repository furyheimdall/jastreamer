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

#include "uac_descriptors.h"

namespace usb_direct {
namespace {

constexpr uint8_t kDtInterface = 0x04;
constexpr uint8_t kDtEndpoint = 0x05;
constexpr uint8_t kDtInterfaceAssociation = 0x0B;
constexpr uint8_t kDtCsInterface = 0x24;
constexpr uint8_t kDtCsEndpoint = 0x25;

constexpr uint8_t kClassAudio = 0x01;
constexpr uint8_t kSubclassAudioControl = 0x01;
constexpr uint8_t kSubclassAudioStreaming = 0x02;
constexpr uint8_t kProtocolUac2 = 0x20;

constexpr uint8_t kAcHeader = 0x01;
constexpr uint8_t kAcInputTerminal = 0x02;
constexpr uint8_t kAcOutputTerminal = 0x03;
constexpr uint8_t kAcClockSource = 0x0A;

constexpr uint8_t kAsGeneral = 0x01;
constexpr uint8_t kAsFormatType = 0x02;

constexpr uint16_t kFormatTagPcm = 0x0001;
constexpr uint8_t kFormatTypeI = 0x01;

uint16_t Read16(const uint8_t* p) {
  return static_cast<uint16_t>(p[0] | (static_cast<uint16_t>(p[1]) << 8));
}

uint32_t Read24(const uint8_t* p) {
  return static_cast<uint32_t>(p[0]) | (static_cast<uint32_t>(p[1]) << 8) |
         (static_cast<uint32_t>(p[2]) << 16);
}

SyncType SyncFromAttributes(uint8_t attributes) {
  switch ((attributes >> 2) & 0x03) {
    case 0x01:
      return SyncType::kAsync;
    case 0x02:
      return SyncType::kAdaptive;
    case 0x03:
      return SyncType::kSync;
    default:
      return SyncType::kNone;
  }
}

// Everything collected for one AudioStreaming alternate setting while walking
// the configuration block. Class-specific descriptors are kept as raw bytes so
// they can be interpreted once the UAC version is known for certain.
struct PendingAlt {
  uint8_t interface_number = 0;
  uint8_t alt_setting = 0;
  std::vector<uint8_t> as_general;
  std::vector<uint8_t> format_type;
  bool has_data_endpoint = false;
  uint8_t endpoint_address = 0;
  uint8_t interval = 1;
  uint16_t max_packet_bytes = 0;
  SyncType sync = SyncType::kNone;
  uint8_t synch_address = 0;
  uint8_t feedback_endpoint = 0;
  bool endpoint_rate_control = false;
};

struct TerminalClock {
  uint8_t terminal_id = 0;
  uint8_t clock_source_id = 0;
};

}  // namespace

UacConfig ParseConfiguration(const uint8_t* data, size_t length) {
  UacConfig config;
  if (data == nullptr || length < 9) {
    config.error = "configuration descriptor is too short";
    return config;
  }

  bool iad_says_uac2 = false;
  bool have_bcd_adc = false;
  uint16_t bcd_adc = 0;
  bool have_control_interface = false;
  uint8_t first_clock_source = 0;
  std::vector<TerminalClock> terminals;
  std::vector<PendingAlt> pending;

  bool in_audio_control = false;
  bool in_audio_streaming = false;
  bool truncated = false;

  size_t offset = 0;
  while (offset + 2 <= length) {
    const uint8_t descriptor_length = data[offset];
    const uint8_t descriptor_type = data[offset + 1];
    if (descriptor_length < 2 || offset + descriptor_length > length) {
      truncated = true;
      break;
    }
    const uint8_t* d = data + offset;

    switch (descriptor_type) {
      case kDtInterfaceAssociation:
        // bLength, bDescriptorType, bFirstInterface, bInterfaceCount,
        // bFunctionClass, bFunctionSubClass, bFunctionProtocol, iFunction.
        if (descriptor_length >= 8 && d[4] == kClassAudio &&
            d[6] == kProtocolUac2) {
          iad_says_uac2 = true;
        }
        break;

      case kDtInterface: {
        // bLength, bDescriptorType, bInterfaceNumber, bAlternateSetting,
        // bNumEndpoints, bInterfaceClass, bInterfaceSubClass,
        // bInterfaceProtocol, iInterface.
        if (descriptor_length < 9) {
          break;
        }
        const uint8_t interface_number = d[2];
        const uint8_t alt_setting = d[3];
        const uint8_t interface_class = d[5];
        const uint8_t interface_subclass = d[6];
        in_audio_control = interface_class == kClassAudio &&
                           interface_subclass == kSubclassAudioControl;
        in_audio_streaming = interface_class == kClassAudio &&
                             interface_subclass == kSubclassAudioStreaming;
        if (in_audio_control && !have_control_interface) {
          config.control_interface = interface_number;
          have_control_interface = true;
        }
        if (in_audio_streaming && alt_setting != 0) {
          PendingAlt alt;
          alt.interface_number = interface_number;
          alt.alt_setting = alt_setting;
          pending.push_back(alt);
        }
        break;
      }

      case kDtCsInterface: {
        if (descriptor_length < 3) {
          break;
        }
        const uint8_t subtype = d[2];
        if (in_audio_control) {
          if (subtype == kAcHeader && descriptor_length >= 5) {
            // bLength, 0x24, 0x01, bcdADC(2), ...
            bcd_adc = Read16(d + 3);
            have_bcd_adc = true;
          } else if (subtype == kAcClockSource && descriptor_length >= 4) {
            // bLength, 0x24, 0x0A, bClockID, ...
            if (first_clock_source == 0) {
              first_clock_source = d[3];
            }
          } else if (subtype == kAcOutputTerminal && descriptor_length >= 9) {
            // bLength, 0x24, 0x03, bTerminalID, wTerminalType(2),
            // bAssocTerminal, bSourceID, bCSourceID, bmControls(2), iTerminal.
            terminals.push_back({d[3], d[8]});
          } else if (subtype == kAcInputTerminal && descriptor_length >= 8) {
            // bLength, 0x24, 0x02, bTerminalID, wTerminalType(2),
            // bAssocTerminal, bCSourceID, bNrChannels, bmChannelConfig(4),
            // iChannelNames, bmControls(2), iTerminal.
            terminals.push_back({d[3], d[7]});
          }
        } else if (in_audio_streaming && !pending.empty()) {
          PendingAlt& alt = pending.back();
          if (subtype == kAsGeneral) {
            alt.as_general.assign(d, d + descriptor_length);
          } else if (subtype == kAsFormatType) {
            alt.format_type.assign(d, d + descriptor_length);
          }
        }
        break;
      }

      case kDtCsEndpoint: {
        // bLength, 0x25, bDescriptorSubtype(EP_GENERAL = 0x01), bmAttributes.
        if (in_audio_streaming && !pending.empty() && descriptor_length >= 4 &&
            d[2] == kAsGeneral && (d[3] & 0x01) != 0) {
          pending.back().endpoint_rate_control = true;
        }
        break;
      }

      case kDtEndpoint: {
        // bLength, bDescriptorType, bEndpointAddress, bmAttributes,
        // wMaxPacketSize(2), bInterval[, bRefresh, bSynchAddress].
        if (!in_audio_streaming || pending.empty() || descriptor_length < 7) {
          break;
        }
        PendingAlt& alt = pending.back();
        const uint8_t address = d[2];
        const uint8_t attributes = d[3];
        const bool isochronous = (attributes & 0x03) == 0x01;
        if (!isochronous) {
          break;
        }
        const uint16_t max_packet_size = Read16(d + 4);
        if ((address & 0x80) == 0) {
          if (!alt.has_data_endpoint) {
            alt.has_data_endpoint = true;
            alt.endpoint_address = address;
            alt.interval = d[6] == 0 ? 1 : d[6];
            alt.max_packet_bytes = static_cast<uint16_t>(
                (max_packet_size & 0x07FF) *
                (1 + ((max_packet_size >> 11) & 0x03)));
            alt.sync = SyncFromAttributes(attributes);
            if (descriptor_length >= 9) {
              alt.synch_address = d[8];
            }
          }
        } else if (((attributes >> 4) & 0x03) == 0x01 &&
                   alt.feedback_endpoint == 0) {
          // Usage type 01 on an isochronous IN endpoint: explicit feedback.
          alt.feedback_endpoint = address;
        }
        break;
      }

      default:
        break;
    }

    offset += descriptor_length;
  }

  if (iad_says_uac2) {
    config.version = UacVersion::kUac2;
  } else if (have_bcd_adc) {
    if (bcd_adc == 0x0200) {
      config.version = UacVersion::kUac2;
    } else if (bcd_adc == 0x0100) {
      config.version = UacVersion::kUac1;
    }
  }
  if (config.version == UacVersion::kUnknown) {
    // No association descriptor and no usable bcdADC: fall back to the shape of
    // the first AS_GENERAL descriptor (UAC1 is 7 bytes, UAC2 is 16).
    for (const PendingAlt& alt : pending) {
      if (alt.as_general.size() >= 16) {
        config.version = UacVersion::kUac2;
        break;
      }
      if (alt.as_general.size() == 7) {
        config.version = UacVersion::kUac1;
        break;
      }
    }
  }

  const bool uac2 = config.version == UacVersion::kUac2;
  for (const PendingAlt& pending_alt : pending) {
    if (!pending_alt.has_data_endpoint || pending_alt.as_general.empty() ||
        pending_alt.format_type.empty()) {
      continue;
    }
    const std::vector<uint8_t>& general = pending_alt.as_general;
    const std::vector<uint8_t>& format = pending_alt.format_type;

    AltSetting alt;
    alt.interface_number = pending_alt.interface_number;
    alt.alt_setting = pending_alt.alt_setting;
    alt.endpoint_address = pending_alt.endpoint_address;
    alt.interval = pending_alt.interval;
    alt.max_packet_bytes = pending_alt.max_packet_bytes;
    alt.sync = pending_alt.sync;
    alt.endpoint_rate_control = pending_alt.endpoint_rate_control;
    alt.feedback_endpoint = pending_alt.feedback_endpoint;
    // bTerminalLink is byte 3, so a runt AS_GENERAL is unusable.
    if (general.size() < 4) {
      continue;
    }
    alt.terminal_link = general[3];

    if (uac2) {
      // AS_GENERAL: bLength, 0x24, 0x01, bTerminalLink, bmControls,
      // bFormatType, bmFormats(4), bNrChannels, bmChannelConfig(4),
      // iChannelNames.
      if (general.size() < 16) {
        continue;
      }
      if (general[5] != kFormatTypeI) {
        continue;
      }
      const uint32_t formats = static_cast<uint32_t>(general[6]) |
                               (static_cast<uint32_t>(general[7]) << 8) |
                               (static_cast<uint32_t>(general[8]) << 16) |
                               (static_cast<uint32_t>(general[9]) << 24);
      if ((formats & 0x01) == 0) {  // PCM.
        continue;
      }
      alt.channels = general[10];
      // FORMAT_TYPE_I: bLength, 0x24, 0x02, bFormatType, bSubslotSize,
      // bBitResolution.
      if (format.size() < 6 || format[3] != kFormatTypeI) {
        continue;
      }
      alt.subslot_bytes = format[4];
      alt.bit_resolution = format[5];
    } else {
      // AS_GENERAL: bLength, 0x24, 0x01, bTerminalLink, bDelay, wFormatTag(2).
      if (general.size() < 7) {
        continue;
      }
      if (Read16(general.data() + 5) != kFormatTagPcm) {
        continue;
      }
      // FORMAT_TYPE_I: bLength, 0x24, 0x02, bFormatType, bNrChannels,
      // bSubframeSize, bBitResolution, bSamFreqType, then either
      // tLowerSamFreq + tUpperSamFreq or bSamFreqType discrete 3-byte rates.
      if (format.size() < 8 || format[3] != kFormatTypeI) {
        continue;
      }
      alt.channels = format[4];
      alt.subslot_bytes = format[5];
      alt.bit_resolution = format[6];
      const uint8_t rate_count = format[7];
      if (rate_count == 0) {
        if (format.size() < 14) {
          continue;
        }
        alt.rate_min = Read24(format.data() + 8);
        alt.rate_max = Read24(format.data() + 11);
      } else {
        if (format.size() < static_cast<size_t>(8) + rate_count * 3u) {
          continue;
        }
        alt.rates.reserve(rate_count);
        for (uint8_t i = 0; i < rate_count; ++i) {
          alt.rates.push_back(Read24(format.data() + 8 + i * 3));
        }
      }
    }

    if (alt.channels == 0 || alt.subslot_bytes == 0 ||
        alt.bit_resolution == 0) {
      continue;
    }
    if (alt.feedback_endpoint == 0 && !uac2 && pending_alt.synch_address != 0) {
      alt.feedback_endpoint = pending_alt.synch_address;
    }
    if (uac2) {
      for (const TerminalClock& terminal : terminals) {
        if (terminal.terminal_id == alt.terminal_link) {
          alt.clock_source_id = terminal.clock_source_id;
          break;
        }
      }
      if (alt.clock_source_id == 0) {
        alt.clock_source_id = first_clock_source;
      }
    }
    config.alts.push_back(std::move(alt));
  }

  if (config.alts.empty()) {
    if (truncated) {
      config.error = "configuration descriptor is malformed or truncated";
    } else if (config.version == UacVersion::kUnknown) {
      config.error = "device does not expose a USB Audio Class interface";
    } else {
      config.error = "no PCM type-I isochronous OUT alternate setting found";
    }
  }
  return config;
}

}  // namespace usb_direct
