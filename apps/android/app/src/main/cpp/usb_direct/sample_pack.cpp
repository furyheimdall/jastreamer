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

#include "sample_pack.h"

#include <cstring>

namespace usb_direct {

bool CanPack(const uint32_t source_sample_bytes, const uint32_t source_channels,
             const uint32_t target_sample_bytes,
             const uint32_t target_channels) {
  if (source_sample_bytes == 0 || source_channels == 0 ||
      target_sample_bytes == 0 || target_channels == 0) {
    return false;
  }
  if (target_sample_bytes < source_sample_bytes) {
    return false;
  }
  if (target_channels == source_channels) {
    return true;
  }
  // Duplicating a mono source onto both sides of a stereo-only device keeps
  // every sample value; any other layout change would mix channels.
  return source_channels == 1 && target_channels == 2;
}

namespace {

// Writes one sample left-justified into `dst`: the unused low bytes are zero,
// so a 24-bit sample handed to a 32-bit subslot keeps its original value in
// the high bits instead of being attenuated.
inline void PlaceSample(const uint8_t* src, const uint32_t source_sample_bytes,
                        const uint32_t target_sample_bytes, uint8_t* dst) {
  const uint32_t padding = target_sample_bytes - source_sample_bytes;
  if (padding != 0) {
    std::memset(dst, 0, padding);
  }
  std::memcpy(dst + padding, src, source_sample_bytes);
}

}  // namespace

size_t PackFrames(const uint8_t* src, const size_t frames,
                  const uint32_t source_sample_bytes,
                  const uint32_t source_channels,
                  const uint32_t target_sample_bytes,
                  const uint32_t target_channels, uint8_t* dst) {
  if (src == nullptr || dst == nullptr ||
      !CanPack(source_sample_bytes, source_channels, target_sample_bytes,
               target_channels)) {
    return 0;
  }
  const size_t target_frame_bytes =
      static_cast<size_t>(target_sample_bytes) * target_channels;
  if (frames == 0) {
    return 0;
  }
  if (source_sample_bytes == target_sample_bytes &&
      source_channels == target_channels) {
    const size_t bytes = frames * target_frame_bytes;
    std::memcpy(dst, src, bytes);
    return bytes;
  }

  const size_t source_frame_bytes =
      static_cast<size_t>(source_sample_bytes) * source_channels;
  for (size_t frame = 0; frame < frames; ++frame) {
    const uint8_t* source_frame = src + frame * source_frame_bytes;
    uint8_t* target_frame = dst + frame * target_frame_bytes;
    if (source_channels == target_channels) {
      for (uint32_t channel = 0; channel < source_channels; ++channel) {
        PlaceSample(source_frame + channel * source_sample_bytes,
                    source_sample_bytes, target_sample_bytes,
                    target_frame + channel * target_sample_bytes);
      }
    } else {
      // Mono onto stereo: the same sample on both sides.
      PlaceSample(source_frame, source_sample_bytes, target_sample_bytes,
                  target_frame);
      std::memcpy(target_frame + target_sample_bytes, target_frame,
                  target_sample_bytes);
    }
  }
  return frames * target_frame_bytes;
}

size_t WritableFrames(const size_t source_length,
                      const uint32_t source_frame_bytes,
                      const uint32_t target_frame_bytes,
                      const size_t free_target_bytes) {
  if (source_frame_bytes == 0 || target_frame_bytes == 0) {
    return 0;
  }
  const size_t offered = source_length / source_frame_bytes;
  const size_t room = free_target_bytes / target_frame_bytes;
  return offered < room ? offered : room;
}

}  // namespace usb_direct
