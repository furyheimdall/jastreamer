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

#include <cstdint>

#include "check.h"
#include "packet_sizing.h"

namespace {

using usb_direct::ClampToNominal;
using usb_direct::DecodeFeedbackQ16;
using usb_direct::FeedbackRateHz;
using usb_direct::FeedbackToServiceIntervalQ16;
using usb_direct::NominalQ16PerServiceInterval;
using usb_direct::PacketSizer;

void TestNominal() {
  // 48000 / 8000 = 6 samples per microframe, exactly representable.
  CHECK_EQ(NominalQ16PerServiceInterval(48000, true, 1), 6u << 16);
  // 44100 / 8000 = 5.5125 samples per microframe.
  CHECK_EQ(NominalQ16PerServiceInterval(44100, true, 1),
           static_cast<uint32_t>((static_cast<uint64_t>(44100) << 16) / 8000));
  CHECK_EQ(NominalQ16PerServiceInterval(44100, true, 1), 361267u);
  // bInterval 2 means one service interval every 2 microframes.
  CHECK_EQ(NominalQ16PerServiceInterval(96000, true, 2), 24u << 16);
  // Full speed counts samples per 1 ms frame.
  CHECK_EQ(NominalQ16PerServiceInterval(48000, false, 1), 48u << 16);
  CHECK_EQ(NominalQ16PerServiceInterval(0, true, 1), 0u);
}

void TestServiceIntervalScaling() {
  const uint32_t per_microframe = NominalQ16PerServiceInterval(96000, true, 1);
  CHECK_EQ(FeedbackToServiceIntervalQ16(per_microframe, true, 2), 24u << 16);
  CHECK_EQ(FeedbackToServiceIntervalQ16(per_microframe, true, 1), 12u << 16);
  const uint32_t per_frame = NominalQ16PerServiceInterval(48000, false, 1);
  CHECK_EQ(FeedbackToServiceIntervalQ16(per_frame, false, 2), 96u << 16);
  CHECK_EQ(FeedbackToServiceIntervalQ16(0, true, 1), 0u);
}

void TestDecodeFeedback() {
  // High speed: 4 bytes little endian in 16.16 format, here 5.5125 samples.
  const uint8_t high_speed[4] = {0x33, 0x83, 0x05, 0x00};
  CHECK_EQ(DecodeFeedbackQ16(high_speed, sizeof(high_speed), true), 361267u);

  const uint8_t high_speed_six[4] = {0x00, 0x00, 0x06, 0x00};
  CHECK_EQ(DecodeFeedbackQ16(high_speed_six, sizeof(high_speed_six), true),
           6u << 16);

  // Full speed: 3 bytes little endian in 10.14 format, here 48 samples.
  const uint8_t full_speed[3] = {0x00, 0x00, 0x0C};
  CHECK_EQ(DecodeFeedbackQ16(full_speed, sizeof(full_speed), false), 48u << 16);

  // 44.1 kHz full speed: 44.1 samples per frame = 44.1 * 2^14 = 722534.
  const uint8_t full_speed_441[3] = {0x66, 0x06, 0x0B};
  CHECK_EQ(DecodeFeedbackQ16(full_speed_441, sizeof(full_speed_441), false),
           722534u << 2);

  // Wrong payload sizes are ignored rather than misread.
  CHECK_EQ(DecodeFeedbackQ16(high_speed, 3, true), 0u);
  CHECK_EQ(DecodeFeedbackQ16(high_speed, 0, true), 0u);
  CHECK_EQ(DecodeFeedbackQ16(full_speed, 2, false), 0u);
  CHECK_EQ(DecodeFeedbackQ16(high_speed, 4, false), 0u);
  CHECK_EQ(DecodeFeedbackQ16(nullptr, 4, true), 0u);
}

void TestFeedbackRate() {
  const uint32_t rates[] = {44100, 48000, 96000, 192000, 384000};
  for (const uint32_t rate : rates) {
    const uint32_t q16 = NominalQ16PerServiceInterval(rate, true, 1);
    const uint32_t decoded = FeedbackRateHz(q16, true);
    const uint32_t difference = decoded > rate ? decoded - rate : rate - decoded;
    CHECK_TRUE(difference <= 1);
  }
  const uint32_t full_speed_q16 = NominalQ16PerServiceInterval(48000, false, 1);
  CHECK_EQ(FeedbackRateHz(full_speed_q16, false), 48000u);
  CHECK_EQ(FeedbackRateHz(0, true), 0u);
}

void TestClampToNominal() {
  const uint32_t nominal = 6u << 16;
  const uint32_t low = nominal - (nominal * 20 / 100);
  const uint32_t high = nominal + (nominal * 20 / 100);
  CHECK_EQ(ClampToNominal(1u << 16, nominal, 20), low);
  CHECK_EQ(ClampToNominal(12u << 16, nominal, 20), high);
  CHECK_EQ(ClampToNominal(nominal + 1024, nominal, 20), nominal + 1024);
  CHECK_EQ(ClampToNominal(nominal, 0, 20), nominal);
}

void TestPacketSizerAverage() {
  PacketSizer sizer;
  const uint32_t q16 = NominalQ16PerServiceInterval(44100, true, 1);
  uint64_t total = 0;
  bool sizes_plausible = true;
  for (int i = 0; i < 8000; ++i) {
    const uint32_t frames = sizer.NextFrames(q16, 64);
    if (frames != 5 && frames != 6) {
      sizes_plausible = false;
    }
    total += frames;
  }
  CHECK_EQ(total, 44100u);
  CHECK_TRUE(sizes_plausible);

  // A rate that divides evenly produces a constant packet size.
  sizer.Reset();
  const uint32_t even_q16 = NominalQ16PerServiceInterval(48000, true, 1);
  bool constant = true;
  uint64_t even_total = 0;
  for (int i = 0; i < 8000; ++i) {
    const uint32_t frames = sizer.NextFrames(even_q16, 64);
    if (frames != 6) {
      constant = false;
    }
    even_total += frames;
  }
  CHECK_TRUE(constant);
  CHECK_EQ(even_total, 48000u);
}

void TestPacketSizerLimits() {
  PacketSizer sizer;
  // The endpoint cannot carry more than max_frames, so the value is truncated.
  CHECK_EQ(sizer.NextFrames(20u << 16, 8), 8u);
  CHECK_EQ(sizer.NextFrames(20u << 16, 8), 8u);

  // Never an empty packet while there is something to send.
  sizer.Reset();
  CHECK_EQ(sizer.NextFrames(1024, 8), 1u);
  CHECK_EQ(sizer.NextFrames(0, 8), 0u);
  CHECK_EQ(sizer.NextFrames(6u << 16, 0), 0u);
}

}  // namespace

void RunPacketSizingTests() {
  TestNominal();
  TestServiceIntervalScaling();
  TestDecodeFeedback();
  TestFeedbackRate();
  TestClampToNominal();
  TestPacketSizerAverage();
  TestPacketSizerLimits();
}
