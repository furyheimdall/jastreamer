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
// Places decoded PCM frames into the USB subslot layout the device asked for.
// Pure logic so the widening rule is host testable.

#ifndef USB_DIRECT_SAMPLE_PACK_H_
#define USB_DIRECT_SAMPLE_PACK_H_

#include <cstddef>
#include <cstdint>

namespace usb_direct {

// True when `frames` frames of `source_sample_bytes`/`source_channels` PCM can
// be placed into `target_sample_bytes`/`target_channels` subslots without
// changing a single sample value. The target may be wider (the sample is
// left-justified and the unused low bits are zero, which is what the USB audio
// class expects when bBitResolution is below the subslot size) and a mono
// source may be duplicated onto a stereo target, but nothing is ever narrowed,
// mixed or resampled.
bool CanPack(uint32_t source_sample_bytes, uint32_t source_channels,
             uint32_t target_sample_bytes, uint32_t target_channels);

// Converts `frames` frames of interleaved little-endian integer PCM from `src`
// into `dst`, returning the number of bytes written, or 0 when the conversion
// is not one of the allowed ones. `dst` must hold
// `frames * target_channels * target_sample_bytes` bytes.
size_t PackFrames(const uint8_t* src, size_t frames,
                  uint32_t source_sample_bytes, uint32_t source_channels,
                  uint32_t target_sample_bytes, uint32_t target_channels,
                  uint8_t* dst);

// How many whole frames of a `source_length` byte buffer may be handed to the
// feeder right now, given `free_target_bytes` of room in the ring. Whole frames
// only: leaving half a frame in the ring would shift every later sample.
size_t WritableFrames(size_t source_length, uint32_t source_frame_bytes,
                      uint32_t target_frame_bytes, size_t free_target_bytes);

}  // namespace usb_direct

#endif  // USB_DIRECT_SAMPLE_PACK_H_
