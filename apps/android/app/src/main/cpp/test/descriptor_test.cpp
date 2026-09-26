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
// Configuration descriptor blocks hand-written from the USB Audio Class 1.0
// and 2.0 specifications, exactly as a device would return them for a
// GET_DESCRIPTOR(CONFIGURATION) request.

#include <cstdint>
#include <vector>

#include "check.h"
#include "uac_descriptors.h"

namespace {

using usb_direct::AltSetting;
using usb_direct::ParseConfiguration;
using usb_direct::SyncType;
using usb_direct::UacConfig;
using usb_direct::UacVersion;

// A UAC1 headphone dongle: no interface association descriptor, AudioControl
// interface 0, AudioStreaming interface 1 with a zero-bandwidth alt 0 and a
// 24-bit stereo alt 1 offering 44100 and 48000 Hz.
const uint8_t kUac1Config[] = {
    // Configuration descriptor.
    0x09,        // bLength
    0x02,        // bDescriptorType (CONFIGURATION)
    0x59, 0x00,  // wTotalLength (89)
    0x02,        // bNumInterfaces
    0x01,        // bConfigurationValue
    0x00,        // iConfiguration
    0x80,        // bmAttributes (bus powered)
    0x32,        // bMaxPower (100 mA)

    // Interface 0 alt 0: AudioControl.
    0x09,  // bLength
    0x04,  // bDescriptorType (INTERFACE)
    0x00,  // bInterfaceNumber
    0x00,  // bAlternateSetting
    0x00,  // bNumEndpoints
    0x01,  // bInterfaceClass (AUDIO)
    0x01,  // bInterfaceSubClass (AUDIOCONTROL)
    0x00,  // bInterfaceProtocol
    0x00,  // iInterface

    // Class-specific AudioControl header.
    0x09,        // bLength
    0x24,        // bDescriptorType (CS_INTERFACE)
    0x01,        // bDescriptorSubtype (HEADER)
    0x00, 0x01,  // bcdADC (1.00)
    0x09, 0x00,  // wTotalLength
    0x01,        // bInCollection
    0x01,        // baInterfaceNr(1)

    // Interface 1 alt 0: AudioStreaming, zero bandwidth.
    0x09,  // bLength
    0x04,  // bDescriptorType (INTERFACE)
    0x01,  // bInterfaceNumber
    0x00,  // bAlternateSetting
    0x00,  // bNumEndpoints
    0x01,  // bInterfaceClass (AUDIO)
    0x02,  // bInterfaceSubClass (AUDIOSTREAMING)
    0x00,  // bInterfaceProtocol
    0x00,  // iInterface

    // Interface 1 alt 1: AudioStreaming, operational.
    0x09,  // bLength
    0x04,  // bDescriptorType (INTERFACE)
    0x01,  // bInterfaceNumber
    0x01,  // bAlternateSetting
    0x02,  // bNumEndpoints
    0x01,  // bInterfaceClass (AUDIO)
    0x02,  // bInterfaceSubClass (AUDIOSTREAMING)
    0x00,  // bInterfaceProtocol
    0x00,  // iInterface

    // Class-specific AS_GENERAL.
    0x07,        // bLength
    0x24,        // bDescriptorType (CS_INTERFACE)
    0x01,        // bDescriptorSubtype (AS_GENERAL)
    0x01,        // bTerminalLink
    0x01,        // bDelay
    0x01, 0x00,  // wFormatTag (PCM)

    // Class-specific FORMAT_TYPE_I.
    0x0E,              // bLength (14)
    0x24,              // bDescriptorType (CS_INTERFACE)
    0x02,              // bDescriptorSubtype (FORMAT_TYPE)
    0x01,              // bFormatType (FORMAT_TYPE_I)
    0x02,              // bNrChannels
    0x03,              // bSubframeSize
    0x18,              // bBitResolution (24)
    0x02,              // bSamFreqType (2 discrete rates)
    0x44, 0xAC, 0x00,  // tSamFreq(1) = 44100
    0x80, 0xBB, 0x00,  // tSamFreq(2) = 48000

    // Standard audio isochronous OUT endpoint (9 byte audio form).
    0x09,        // bLength
    0x05,        // bDescriptorType (ENDPOINT)
    0x01,        // bEndpointAddress (OUT 1)
    0x05,        // bmAttributes (isochronous, asynchronous, data)
    0x26, 0x01,  // wMaxPacketSize (294)
    0x01,        // bInterval
    0x03,        // bRefresh
    0x81,        // bSynchAddress

    // Isochronous IN feedback endpoint.
    0x07,        // bLength
    0x05,        // bDescriptorType (ENDPOINT)
    0x81,        // bEndpointAddress (IN 1)
    0x11,        // bmAttributes (isochronous, usage type feedback)
    0x03, 0x00,  // wMaxPacketSize (3)
    0x01,        // bInterval

    // Class-specific AS isochronous endpoint descriptor.
    0x07,        // bLength
    0x25,        // bDescriptorType (CS_ENDPOINT)
    0x01,        // bDescriptorSubtype (EP_GENERAL)
    0x01,        // bmAttributes (sampling frequency control)
    0x00,        // bLockDelayUnits
    0x00, 0x00,  // wLockDelay
};

// The same device declaring a continuous sample rate range of 8000..96000 Hz.
const uint8_t kUac1ContinuousConfig[] = {
    // Configuration descriptor.
    0x09, 0x02, 0x59, 0x00, 0x02, 0x01, 0x00, 0x80, 0x32,

    // Interface 0 alt 0: AudioControl.
    0x09, 0x04, 0x00, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00,

    // Class-specific AudioControl header, bcdADC 1.00.
    0x09, 0x24, 0x01, 0x00, 0x01, 0x09, 0x00, 0x01, 0x01,

    // Interface 1 alt 0: AudioStreaming, zero bandwidth.
    0x09, 0x04, 0x01, 0x00, 0x00, 0x01, 0x02, 0x00, 0x00,

    // Interface 1 alt 1: AudioStreaming, operational.
    0x09, 0x04, 0x01, 0x01, 0x02, 0x01, 0x02, 0x00, 0x00,

    // AS_GENERAL, PCM.
    0x07, 0x24, 0x01, 0x01, 0x01, 0x01, 0x00,

    // FORMAT_TYPE_I with a continuous rate range.
    0x0E,              // bLength (14)
    0x24,              // bDescriptorType (CS_INTERFACE)
    0x02,              // bDescriptorSubtype (FORMAT_TYPE)
    0x01,              // bFormatType (FORMAT_TYPE_I)
    0x02,              // bNrChannels
    0x03,              // bSubframeSize
    0x18,              // bBitResolution (24)
    0x00,              // bSamFreqType (continuous)
    0x40, 0x1F, 0x00,  // tLowerSamFreq = 8000
    0x00, 0x77, 0x01,  // tUpperSamFreq = 96000

    // Isochronous OUT endpoint.
    0x09, 0x05, 0x01, 0x05, 0x26, 0x01, 0x01, 0x03, 0x81,

    // Isochronous IN feedback endpoint.
    0x07, 0x05, 0x81, 0x11, 0x03, 0x00, 0x01,

    // CS_ENDPOINT with the sampling frequency control.
    0x07, 0x25, 0x01, 0x01, 0x00, 0x00, 0x00,
};

// A UAC2 high-speed DAC: interface association descriptor, clock source 0x10
// reached through the USB streaming input terminal, 16-bit and 24-bit stereo
// alt settings and an explicit feedback endpoint.
const uint8_t kUac2Config[] = {
    // Configuration descriptor.
    0x09,        // bLength
    0x02,        // bDescriptorType (CONFIGURATION)
    0xBB, 0x00,  // wTotalLength (187)
    0x02,        // bNumInterfaces
    0x01,        // bConfigurationValue
    0x00,        // iConfiguration
    0x80,        // bmAttributes
    0x32,        // bMaxPower

    // Interface association descriptor.
    0x08,  // bLength
    0x0B,  // bDescriptorType (INTERFACE_ASSOCIATION)
    0x00,  // bFirstInterface
    0x02,  // bInterfaceCount
    0x01,  // bFunctionClass (AUDIO)
    0x00,  // bFunctionSubClass (UNDEFINED)
    0x20,  // bFunctionProtocol (IP version 2.00)
    0x00,  // iFunction

    // Interface 0 alt 0: AudioControl.
    0x09,  // bLength
    0x04,  // bDescriptorType (INTERFACE)
    0x00,  // bInterfaceNumber
    0x00,  // bAlternateSetting
    0x00,  // bNumEndpoints
    0x01,  // bInterfaceClass (AUDIO)
    0x01,  // bInterfaceSubClass (AUDIOCONTROL)
    0x20,  // bInterfaceProtocol (IP version 2.00)
    0x00,  // iInterface

    // Class-specific AudioControl header.
    0x09,        // bLength
    0x24,        // bDescriptorType (CS_INTERFACE)
    0x01,        // bDescriptorSubtype (HEADER)
    0x00, 0x02,  // bcdADC (2.00)
    0x01,        // bCategory (DESKTOP_SPEAKER)
    0x2E, 0x00,  // wTotalLength (46)
    0x00,        // bmControls

    // CLOCK_SOURCE.
    0x08,  // bLength
    0x24,  // bDescriptorType (CS_INTERFACE)
    0x0A,  // bDescriptorSubtype (CLOCK_SOURCE)
    0x10,  // bClockID
    0x03,  // bmAttributes (internal programmable clock)
    0x07,  // bmControls (frequency control host programmable)
    0x00,  // bAssocTerminal
    0x00,  // iClockSource

    // INPUT_TERMINAL (USB streaming).
    0x11,                    // bLength (17)
    0x24,                    // bDescriptorType (CS_INTERFACE)
    0x02,                    // bDescriptorSubtype (INPUT_TERMINAL)
    0x01,                    // bTerminalID
    0x01, 0x01,              // wTerminalType (USB streaming)
    0x00,                    // bAssocTerminal
    0x10,                    // bCSourceID (clock 0x10)
    0x02,                    // bNrChannels
    0x03, 0x00, 0x00, 0x00,  // bmChannelConfig (front left + right)
    0x00,                    // iChannelNames
    0x00, 0x00,              // bmControls
    0x00,                    // iTerminal

    // OUTPUT_TERMINAL (speaker).
    0x0C,        // bLength (12)
    0x24,        // bDescriptorType (CS_INTERFACE)
    0x03,        // bDescriptorSubtype (OUTPUT_TERMINAL)
    0x03,        // bTerminalID
    0x01, 0x03,  // wTerminalType (speaker)
    0x00,        // bAssocTerminal
    0x01,        // bSourceID (input terminal 0x01)
    0x10,        // bCSourceID (clock 0x10)
    0x00, 0x00,  // bmControls
    0x00,        // iTerminal

    // Interface 1 alt 0: AudioStreaming, zero bandwidth.
    0x09, 0x04, 0x01, 0x00, 0x00, 0x01, 0x02, 0x20, 0x00,

    // Interface 1 alt 1: AudioStreaming, 16 bit.
    0x09, 0x04, 0x01, 0x01, 0x02, 0x01, 0x02, 0x20, 0x00,

    // Class-specific AS_GENERAL.
    0x10,                    // bLength (16)
    0x24,                    // bDescriptorType (CS_INTERFACE)
    0x01,                    // bDescriptorSubtype (AS_GENERAL)
    0x01,                    // bTerminalLink (input terminal 0x01)
    0x00,                    // bmControls
    0x01,                    // bFormatType (FORMAT_TYPE_I)
    0x01, 0x00, 0x00, 0x00,  // bmFormats (PCM)
    0x02,                    // bNrChannels
    0x03, 0x00, 0x00, 0x00,  // bmChannelConfig
    0x00,                    // iChannelNames

    // Class-specific FORMAT_TYPE_I, 16 bit in a 2 byte subslot.
    0x06,  // bLength
    0x24,  // bDescriptorType (CS_INTERFACE)
    0x02,  // bDescriptorSubtype (FORMAT_TYPE)
    0x01,  // bFormatType (FORMAT_TYPE_I)
    0x02,  // bSubslotSize
    0x10,  // bBitResolution (16)

    // Isochronous OUT endpoint, 2 transactions per microframe of 192 bytes.
    0x07,        // bLength
    0x05,        // bDescriptorType (ENDPOINT)
    0x01,        // bEndpointAddress (OUT 1)
    0x05,        // bmAttributes (isochronous, asynchronous, data)
    0xC0, 0x08,  // wMaxPacketSize (192 bytes, 1 additional transaction)
    0x01,        // bInterval

    // Class-specific AS isochronous endpoint descriptor.
    0x08,        // bLength
    0x25,        // bDescriptorType (CS_ENDPOINT)
    0x01,        // bDescriptorSubtype (EP_GENERAL)
    0x00,        // bmAttributes
    0x00,        // bmControls
    0x00,        // bLockDelayUnits
    0x00, 0x00,  // wLockDelay

    // Explicit isochronous IN feedback endpoint.
    0x07,        // bLength
    0x05,        // bDescriptorType (ENDPOINT)
    0x81,        // bEndpointAddress (IN 1)
    0x11,        // bmAttributes (isochronous, usage type feedback)
    0x04, 0x00,  // wMaxPacketSize (4)
    0x04,        // bInterval (every 8 microframes)

    // Interface 1 alt 2: AudioStreaming, 24 bit.
    0x09, 0x04, 0x01, 0x02, 0x02, 0x01, 0x02, 0x20, 0x00,

    // Class-specific AS_GENERAL.
    0x10, 0x24, 0x01, 0x01, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x02, 0x03,
    0x00, 0x00, 0x00, 0x00,

    // Class-specific FORMAT_TYPE_I, 24 bit in a 3 byte subslot.
    0x06, 0x24, 0x02, 0x01, 0x03, 0x18,

    // Isochronous OUT endpoint, 2 transactions per microframe of 288 bytes.
    0x07, 0x05, 0x01, 0x05, 0x20, 0x09, 0x01,

    // Class-specific AS isochronous endpoint descriptor.
    0x08, 0x25, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00,

    // Explicit isochronous IN feedback endpoint.
    0x07, 0x05, 0x81, 0x11, 0x04, 0x00, 0x04,
};

// A UAC1 capture-only interface: the single isochronous endpoint is an IN
// endpoint, so there is nothing to play to.
const uint8_t kInputOnlyConfig[] = {
    // Configuration descriptor.
    0x09, 0x02, 0x3F, 0x00, 0x02, 0x01, 0x00, 0x80, 0x32,

    // Interface 0 alt 0: AudioControl.
    0x09, 0x04, 0x00, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00,

    // Class-specific AudioControl header, bcdADC 1.00.
    0x09, 0x24, 0x01, 0x00, 0x01, 0x09, 0x00, 0x01, 0x01,

    // Interface 1 alt 1: AudioStreaming, operational.
    0x09, 0x04, 0x01, 0x01, 0x01, 0x01, 0x02, 0x00, 0x00,

    // AS_GENERAL, PCM.
    0x07, 0x24, 0x01, 0x01, 0x01, 0x01, 0x00,

    // FORMAT_TYPE_I, 16 bit stereo at 48000 Hz.
    0x0B, 0x24, 0x02, 0x01, 0x02, 0x02, 0x10, 0x01, 0x80, 0xBB, 0x00,

    // Isochronous IN endpoint only.
    0x09, 0x05, 0x81, 0x05, 0xC0, 0x00, 0x01, 0x00, 0x00,
};

// Offsets inside kUac1Config used by the rejection cases.
constexpr size_t kUac1Alt1InterfaceOffset = 36;
constexpr size_t kUac1AsGeneralOffset = 45;
constexpr size_t kUac1AsGeneralLength = 7;
constexpr size_t kUac1FormatTagOffset = kUac1AsGeneralOffset + 5;

std::vector<uint8_t> AsVector(const uint8_t* data, size_t length) {
  return std::vector<uint8_t>(data, data + length);
}

void CheckTotalLength(const uint8_t* data, size_t length) {
  const size_t declared =
      static_cast<size_t>(data[2]) | (static_cast<size_t>(data[3]) << 8);
  CHECK_EQ(declared, length);
}

void TestUac1Discrete() {
  CheckTotalLength(kUac1Config, sizeof(kUac1Config));
  const UacConfig config =
      ParseConfiguration(kUac1Config, sizeof(kUac1Config));
  CHECK_EQ(config.error, std::string());
  CHECK_EQ(config.version, UacVersion::kUac1);
  CHECK_EQ(config.control_interface, 0);
  CHECK_EQ(config.alts.size(), 1u);
  if (config.alts.size() != 1) {
    return;
  }
  const AltSetting& alt = config.alts.front();
  CHECK_EQ(alt.interface_number, 1);
  CHECK_EQ(alt.alt_setting, 1);
  CHECK_EQ(alt.channels, 2);
  CHECK_EQ(alt.subslot_bytes, 3);
  CHECK_EQ(alt.bit_resolution, 24);
  CHECK_EQ(alt.endpoint_address, 0x01);
  CHECK_EQ(alt.interval, 1);
  CHECK_EQ(alt.sync, SyncType::kAsync);
  CHECK_EQ(alt.feedback_endpoint, 0x81);
  CHECK_TRUE(alt.endpoint_rate_control);
  CHECK_EQ(alt.max_packet_bytes, 294);
  CHECK_EQ(alt.terminal_link, 1);
  CHECK_EQ(alt.rates.size(), 2u);
  if (alt.rates.size() == 2) {
    CHECK_EQ(alt.rates[0], 44100u);
    CHECK_EQ(alt.rates[1], 48000u);
  }
  CHECK_EQ(alt.rate_min, 0u);
  CHECK_EQ(alt.rate_max, 0u);
}

void TestUac1Continuous() {
  CheckTotalLength(kUac1ContinuousConfig, sizeof(kUac1ContinuousConfig));
  const UacConfig config = ParseConfiguration(kUac1ContinuousConfig,
                                              sizeof(kUac1ContinuousConfig));
  CHECK_EQ(config.version, UacVersion::kUac1);
  CHECK_EQ(config.alts.size(), 1u);
  if (config.alts.empty()) {
    return;
  }
  const AltSetting& alt = config.alts.front();
  CHECK_TRUE(alt.rates.empty());
  CHECK_EQ(alt.rate_min, 8000u);
  CHECK_EQ(alt.rate_max, 96000u);
  CHECK_EQ(alt.bit_resolution, 24);
  CHECK_EQ(alt.subslot_bytes, 3);
}

void TestUac2() {
  CheckTotalLength(kUac2Config, sizeof(kUac2Config));
  const UacConfig config = ParseConfiguration(kUac2Config, sizeof(kUac2Config));
  CHECK_EQ(config.error, std::string());
  CHECK_EQ(config.version, UacVersion::kUac2);
  CHECK_EQ(config.control_interface, 0);
  CHECK_EQ(config.alts.size(), 2u);
  if (config.alts.size() != 2) {
    return;
  }

  const AltSetting& sixteen_bit = config.alts[0];
  CHECK_EQ(sixteen_bit.alt_setting, 1);
  CHECK_EQ(sixteen_bit.interface_number, 1);
  CHECK_EQ(sixteen_bit.channels, 2);
  CHECK_EQ(sixteen_bit.subslot_bytes, 2);
  CHECK_EQ(sixteen_bit.bit_resolution, 16);
  CHECK_EQ(sixteen_bit.terminal_link, 1);
  CHECK_EQ(sixteen_bit.clock_source_id, 0x10);
  CHECK_EQ(sixteen_bit.feedback_endpoint, 0x81);
  CHECK_EQ(sixteen_bit.sync, SyncType::kAsync);
  CHECK_EQ(sixteen_bit.endpoint_address, 0x01);
  CHECK_EQ(sixteen_bit.interval, 1);
  // 192 bytes with one additional transaction per microframe.
  CHECK_EQ(sixteen_bit.max_packet_bytes, 384);
  CHECK_TRUE(sixteen_bit.rates.empty());
  CHECK_EQ(sixteen_bit.rate_min, 0u);
  CHECK_TRUE(!sixteen_bit.endpoint_rate_control);

  const AltSetting& twenty_four_bit = config.alts[1];
  CHECK_EQ(twenty_four_bit.alt_setting, 2);
  CHECK_EQ(twenty_four_bit.subslot_bytes, 3);
  CHECK_EQ(twenty_four_bit.bit_resolution, 24);
  CHECK_EQ(twenty_four_bit.clock_source_id, 0x10);
  CHECK_EQ(twenty_four_bit.feedback_endpoint, 0x81);
  // 288 bytes with one additional transaction per microframe.
  CHECK_EQ(twenty_four_bit.max_packet_bytes, 576);
  CHECK_TRUE(twenty_four_bit.rates.empty());
}

void TestTruncatedBuffer() {
  // Cut the block in the middle of the interface 1 alt 0 descriptor.
  const UacConfig config = ParseConfiguration(kUac1Config, 40);
  CHECK_TRUE(config.alts.empty());
  CHECK_TRUE(!config.error.empty());

  // Shorter than a configuration descriptor at all.
  const UacConfig tiny = ParseConfiguration(kUac1Config, 5);
  CHECK_TRUE(tiny.alts.empty());
  CHECK_TRUE(!tiny.error.empty());

  // No buffer at all.
  const UacConfig none = ParseConfiguration(nullptr, 0);
  CHECK_TRUE(none.alts.empty());
  CHECK_TRUE(!none.error.empty());
}

void TestZeroLengthDescriptor() {
  std::vector<uint8_t> block = AsVector(kUac1Config, sizeof(kUac1Config));
  CHECK_EQ(block[kUac1Alt1InterfaceOffset], 0x09);
  block[kUac1Alt1InterfaceOffset] = 0x00;  // bLength = 0.
  const UacConfig config = ParseConfiguration(block.data(), block.size());
  CHECK_TRUE(config.alts.empty());
  CHECK_TRUE(!config.error.empty());
}

void TestNonPcmFormatTag() {
  std::vector<uint8_t> block = AsVector(kUac1Config, sizeof(kUac1Config));
  CHECK_EQ(block[kUac1FormatTagOffset], 0x01);
  block[kUac1FormatTagOffset] = 0x02;  // wFormatTag = PCM8, not PCM.
  const UacConfig config = ParseConfiguration(block.data(), block.size());
  CHECK_EQ(config.version, UacVersion::kUac1);
  CHECK_TRUE(config.alts.empty());
  CHECK_TRUE(!config.error.empty());
}

void TestRuntAsGeneral() {
  // Replace the 7 byte AS_GENERAL with a 3 byte stub that stops before
  // bTerminalLink.
  std::vector<uint8_t> block(kUac1Config,
                             kUac1Config + kUac1AsGeneralOffset);
  block.push_back(0x03);  // bLength
  block.push_back(0x24);  // bDescriptorType (CS_INTERFACE)
  block.push_back(0x01);  // bDescriptorSubtype (AS_GENERAL)
  block.insert(block.end(), kUac1Config + kUac1AsGeneralOffset +
                                kUac1AsGeneralLength,
               kUac1Config + sizeof(kUac1Config));
  const UacConfig config = ParseConfiguration(block.data(), block.size());
  CHECK_EQ(config.version, UacVersion::kUac1);
  CHECK_TRUE(config.alts.empty());
  CHECK_TRUE(!config.error.empty());
}

void TestInputOnlyInterface() {
  CheckTotalLength(kInputOnlyConfig, sizeof(kInputOnlyConfig));
  const UacConfig config =
      ParseConfiguration(kInputOnlyConfig, sizeof(kInputOnlyConfig));
  CHECK_EQ(config.version, UacVersion::kUac1);
  CHECK_TRUE(config.alts.empty());
  CHECK_TRUE(!config.error.empty());
}

}  // namespace

void RunDescriptorTests() {
  TestUac1Discrete();
  TestUac1Continuous();
  TestUac2();
  TestTruncatedBuffer();
  TestZeroLengthDescriptor();
  TestNonPcmFormatTag();
  TestRuntAsGeneral();
  TestInputOnlyInterface();
}
