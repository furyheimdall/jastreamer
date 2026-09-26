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

#include "packet_sizing.h"

namespace usb_direct {
namespace {

constexpr uint64_t kOne = 1ull << 16;
constexpr uint64_t kMax32 = 0xFFFFFFFFull;

uint32_t Saturate(uint64_t value) {
  return static_cast<uint32_t>(value > kMax32 ? kMax32 : value);
}

// Number of (micro)frames covered by one service interval.
uint32_t ServiceIntervalUnits(bool high_speed, uint8_t interval) {
  const uint8_t effective = interval == 0 ? 1 : interval;
  if (!high_speed) {
    return effective;
  }
  // bInterval on a high-speed isochronous endpoint encodes a service interval
  // of 2^(bInterval - 1) microframes.
  const uint8_t shift = static_cast<uint8_t>(effective - 1);
  if (shift >= 31) {
    return 1u << 31;
  }
  return 1u << shift;
}

}  // namespace

uint32_t NominalQ16PerServiceInterval(uint32_t sample_rate, bool high_speed,
                                      uint8_t interval) {
  if (sample_rate == 0) {
    return 0;
  }
  const uint64_t divisor = high_speed ? 8000ull : 1000ull;
  const uint64_t per_unit = (static_cast<uint64_t>(sample_rate) << 16) / divisor;
  return Saturate(per_unit * ServiceIntervalUnits(high_speed, interval));
}

uint32_t DecodeFeedbackQ16(const uint8_t* data, size_t length,
                           bool high_speed) {
  if (data == nullptr) {
    return 0;
  }
  if (high_speed) {
    // High speed: 4 bytes, 16.16 samples per microframe.
    if (length != 4) {
      return 0;
    }
    return static_cast<uint32_t>(data[0]) |
           (static_cast<uint32_t>(data[1]) << 8) |
           (static_cast<uint32_t>(data[2]) << 16) |
           (static_cast<uint32_t>(data[3]) << 24);
  }
  // Full speed: 3 bytes, 10.14 samples per frame.
  if (length != 3) {
    return 0;
  }
  const uint32_t raw = static_cast<uint32_t>(data[0]) |
                       (static_cast<uint32_t>(data[1]) << 8) |
                       (static_cast<uint32_t>(data[2]) << 16);
  return Saturate(static_cast<uint64_t>(raw) << 2);
}

uint32_t FeedbackToServiceIntervalQ16(uint32_t feedback_q16, bool high_speed,
                                      uint8_t interval) {
  if (feedback_q16 == 0) {
    return 0;
  }
  return Saturate(static_cast<uint64_t>(feedback_q16) *
                  ServiceIntervalUnits(high_speed, interval));
}

uint32_t ClampToNominal(uint32_t value_q16, uint32_t nominal_q16,
                        uint32_t tolerance_percent) {
  if (nominal_q16 == 0) {
    return value_q16;
  }
  const uint64_t nominal = nominal_q16;
  const uint64_t slack = (nominal * tolerance_percent) / 100ull;
  const uint64_t low = nominal > slack ? nominal - slack : 0ull;
  const uint64_t high = nominal + slack;
  if (value_q16 < low) {
    return Saturate(low);
  }
  if (value_q16 > high) {
    return Saturate(high);
  }
  return value_q16;
}

uint32_t FeedbackRateHz(uint32_t feedback_q16, bool high_speed) {
  if (feedback_q16 == 0) {
    return 0;
  }
  const uint64_t units = high_speed ? 8000ull : 1000ull;
  return Saturate(((static_cast<uint64_t>(feedback_q16) * units) + (kOne / 2)) >>
                  16);
}

uint32_t PacketSizer::NextFrames(uint32_t q16_per_packet, uint32_t max_frames) {
  if (q16_per_packet == 0 || max_frames == 0) {
    return 0;
  }
  const uint64_t accumulated =
      static_cast<uint64_t>(fraction_) + static_cast<uint64_t>(q16_per_packet);
  uint64_t frames = accumulated >> 16;
  if (frames == 0) {
    // Fewer than one frame per packet: emit one frame and drop the borrowed
    // fraction rather than ever returning an empty packet.
    fraction_ = kHalfFrameQ16;
    return 1;
  }
  fraction_ = static_cast<uint32_t>(accumulated & (kOne - 1));
  if (frames > max_frames) {
    // The endpoint cannot carry the backlog, so the surplus whole frames are
    // dropped; only the sub-frame remainder is carried forward.
    frames = max_frames;
  }
  return static_cast<uint32_t>(frames);
}

void PacketSizer::Reset() { fraction_ = kHalfFrameQ16; }

}  // namespace usb_direct
