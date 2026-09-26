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
// Isochronous packet sizing and explicit feedback maths (USB 2.0 section
// 5.12.4.2). Pure code: no libusb, no JNI, host testable.

#ifndef USB_DIRECT_PACKET_SIZING_H_
#define USB_DIRECT_PACKET_SIZING_H_

#include <cstddef>
#include <cstdint>

namespace usb_direct {

// Q16.16 samples per (micro)frame nominal for a rate.
uint32_t NominalQ16PerServiceInterval(uint32_t sample_rate, bool high_speed,
                                      uint8_t interval);

// Decode a raw feedback packet into Q16.16 samples per (micro)frame. Returns 0
// when unusable.
uint32_t DecodeFeedbackQ16(const uint8_t* data, size_t length, bool high_speed);

// Scale a per-(micro)frame Q16 feedback value to the data endpoint's service
// interval.
uint32_t FeedbackToServiceIntervalQ16(uint32_t feedback_q16, bool high_speed,
                                      uint8_t interval);

// Clamp a feedback-derived value to +/-`tolerance_percent` of nominal.
uint32_t ClampToNominal(uint32_t value_q16, uint32_t nominal_q16,
                        uint32_t tolerance_percent);

// Sample rate in Hz implied by a Q16 per-(micro)frame feedback value.
uint32_t FeedbackRateHz(uint32_t feedback_q16, bool high_speed);

// Turns a fractional samples-per-packet figure into whole frame counts whose
// long-run average matches the requested rate.
class PacketSizer {
 public:
  // Accumulates the fractional part so the long-run average matches
  // `q16_per_packet`.
  uint32_t NextFrames(uint32_t q16_per_packet, uint32_t max_frames);
  void Reset();

 private:
  // Seeded with half a frame so each packet rounds to nearest instead of
  // always down; the long-run average then matches the requested rate exactly.
  static constexpr uint32_t kHalfFrameQ16 = 1u << 15;
  uint32_t fraction_ = kHalfFrameQ16;
};

}  // namespace usb_direct

#endif  // USB_DIRECT_PACKET_SIZING_H_
