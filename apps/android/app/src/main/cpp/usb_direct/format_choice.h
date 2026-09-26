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
// Alternate setting selection. Pure logic so the rule that decides whether the
// feeder has to widen samples is host testable.

#ifndef USB_DIRECT_FORMAT_CHOICE_H_
#define USB_DIRECT_FORMAT_CHOICE_H_

#include <cstdint>
#include <string>
#include <vector>

#include "uac_descriptors.h"

namespace usb_direct {

// Picks the alternate setting to stream `sample_rate`/`channels`/`bits` with.
//
// `rates[i]` is the effective rate list of `alts[i]`: the descriptor rates for
// UAC1 and the rates reported by the clock source for UAC2. An empty list means
// the rates are not enumerable, in which case the alt setting is considered
// able to run any rate.
//
// An exact match (`bit_resolution == bits` and `subslot_bytes * 8 == bits`)
// always wins because it needs no sample conversion. Otherwise the narrowest
// container that still holds the samples is used. The bit depth and the channel
// count are never substituted: when nothing matches, -1 is returned and `error`
// explains why.
int ChooseAltSetting(const std::vector<AltSetting>& alts,
                     const std::vector<std::vector<uint32_t>>& rates,
                     uint32_t sample_rate, uint32_t channels, uint32_t bits,
                     std::string* error);

}  // namespace usb_direct

#endif  // USB_DIRECT_FORMAT_CHOICE_H_
