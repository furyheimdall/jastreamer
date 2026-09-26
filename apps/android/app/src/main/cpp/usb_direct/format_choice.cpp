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

#include "format_choice.h"

#include <cstdarg>
#include <cstdio>

namespace usb_direct {
namespace {

bool SupportsRate(const AltSetting& alt, const std::vector<uint32_t>& rates,
                  uint32_t sample_rate) {
  if (rates.empty()) {
    // UAC2 devices do not list their rates in the descriptors; when the clock
    // source query came back empty the rate can only be tried.
    if (alt.rate_min > 0 && alt.rate_max >= alt.rate_min) {
      return sample_rate >= alt.rate_min && sample_rate <= alt.rate_max;
    }
    return true;
  }
  for (const uint32_t rate : rates) {
    if (rate == sample_rate) {
      return true;
    }
  }
  if (alt.rate_min > 0 && alt.rate_max >= alt.rate_min) {
    return sample_rate >= alt.rate_min && sample_rate <= alt.rate_max;
  }
  return false;
}

std::string Format(const char* format, ...) __attribute__((format(printf, 1, 2)));

std::string Format(const char* format, ...) {
  char buffer[192];
  va_list args;
  va_start(args, format);
  const int written = std::vsnprintf(buffer, sizeof(buffer), format, args);
  va_end(args);
  if (written <= 0) {
    return std::string();
  }
  const size_t length = static_cast<size_t>(written) < sizeof(buffer)
                            ? static_cast<size_t>(written)
                            : sizeof(buffer) - 1;
  return std::string(buffer, length);
}

void SetError(std::string* error, const std::string& message) {
  if (error != nullptr) {
    *error = message;
  }
}

}  // namespace

int ChooseAltSetting(const std::vector<AltSetting>& alts,
                     const std::vector<std::vector<uint32_t>>& rates,
                     uint32_t sample_rate, uint32_t channels, uint32_t bits,
                     std::string* error) {
  bool channels_seen = false;
  bool width_seen = false;
  int best = -1;
  uint32_t best_container = 0;
  uint32_t best_resolution = 0;

  for (size_t i = 0; i < alts.size(); ++i) {
    const AltSetting& alt = alts[i];
    if (alt.channels != channels) {
      continue;
    }
    channels_seen = true;
    // The device must carry at least the source's valid bits: a narrower
    // resolution would throw samples away. A wider one is fine because the
    // feeder left-justifies the sample and zero-fills the extra low bits.
    if (alt.bit_resolution < bits) {
      continue;
    }
    const uint32_t container_bits = static_cast<uint32_t>(alt.subslot_bytes) * 8u;
    if (container_bits < alt.bit_resolution) {
      continue;
    }
    width_seen = true;

    static const std::vector<uint32_t> kNoRates;
    const std::vector<uint32_t>& alt_rates = i < rates.size() ? rates[i] : kNoRates;
    if (!SupportsRate(alt, alt_rates, sample_rate)) {
      continue;
    }

    // Least padding wins: the narrowest container first, then the smallest
    // declared resolution inside it. An exact match therefore always wins,
    // because nothing can be narrower than the source itself.
    if (best < 0 || container_bits < best_container ||
        (container_bits == best_container &&
         alt.bit_resolution < best_resolution)) {
      best = static_cast<int>(i);
      best_container = container_bits;
      best_resolution = alt.bit_resolution;
    }
  }

  if (best >= 0) {
    return best;
  }
  if (!channels_seen) {
    SetError(error, Format("device has no %u-channel output alternate setting",
                           static_cast<unsigned>(channels)));
  } else if (!width_seen) {
    SetError(error, Format("device does not offer %u-bit %u-channel output",
                           static_cast<unsigned>(bits),
                           static_cast<unsigned>(channels)));
  } else {
    SetError(error,
             Format("device does not support %u Hz at %u-bit %u-channel",
                    static_cast<unsigned>(sample_rate),
                    static_cast<unsigned>(bits),
                    static_cast<unsigned>(channels)));
  }
  return -1;
}

}  // namespace usb_direct
