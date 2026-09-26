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
#include <string>
#include <vector>

#include "check.h"
#include "format_choice.h"

namespace {

using usb_direct::AltSetting;
using usb_direct::ChooseAltSetting;

AltSetting MakeAlt(uint8_t alt_setting, uint8_t channels, uint8_t subslot_bytes,
                   uint8_t bit_resolution) {
  AltSetting alt;
  alt.interface_number = 1;
  alt.alt_setting = alt_setting;
  alt.channels = channels;
  alt.subslot_bytes = subslot_bytes;
  alt.bit_resolution = bit_resolution;
  alt.endpoint_address = 0x01;
  return alt;
}

void TestExactMatchWins() {
  std::vector<AltSetting> alts;
  // A 24-bit stream is offered in a 4 byte and in a 3 byte subslot.
  alts.push_back(MakeAlt(1, 2, 4, 24));
  alts.push_back(MakeAlt(2, 2, 3, 24));
  const std::vector<std::vector<uint32_t>> rates(alts.size());

  std::string error = "unset";
  const int chosen = ChooseAltSetting(alts, rates, 96000, 2, 24, &error);
  CHECK_EQ(chosen, 1);
  CHECK_EQ(error, std::string("unset"));

  // Order must not matter.
  std::vector<AltSetting> reversed;
  reversed.push_back(MakeAlt(2, 2, 3, 24));
  reversed.push_back(MakeAlt(1, 2, 4, 24));
  CHECK_EQ(ChooseAltSetting(reversed, rates, 96000, 2, 24, &error), 0);
}

void TestNarrowestFallback() {
  std::vector<AltSetting> alts;
  // No 3 byte subslot: the narrowest container that still fits wins.
  alts.push_back(MakeAlt(1, 2, 8, 24));
  alts.push_back(MakeAlt(2, 2, 4, 24));
  const std::vector<std::vector<uint32_t>> rates(alts.size());

  std::string error;
  const int chosen = ChooseAltSetting(alts, rates, 48000, 2, 24, &error);
  CHECK_EQ(chosen, 1);
  CHECK_EQ(alts[static_cast<size_t>(chosen)].subslot_bytes, 4);
}

void TestNeverSubstitutesFormat() {
  std::vector<AltSetting> alts;
  alts.push_back(MakeAlt(1, 2, 2, 16));
  const std::vector<std::vector<uint32_t>> rates(alts.size());

  // A 16-bit only device must not be used for a 24-bit request.
  std::string error;
  CHECK_EQ(ChooseAltSetting(alts, rates, 48000, 2, 24, &error), -1);
  CHECK_TRUE(!error.empty());

  // Nor may the channel count be changed.
  error.clear();
  CHECK_EQ(ChooseAltSetting(alts, rates, 48000, 6, 16, &error), -1);
  CHECK_TRUE(error.find("6-channel") != std::string::npos);

  // A subslot narrower than the bit depth is unusable.
  std::vector<AltSetting> narrow;
  narrow.push_back(MakeAlt(1, 2, 2, 24));
  error.clear();
  CHECK_EQ(ChooseAltSetting(narrow, {std::vector<uint32_t>()}, 48000, 2, 24,
                            &error),
           -1);
  CHECK_TRUE(!error.empty());
}

void TestRateFiltering() {
  std::vector<AltSetting> alts;
  alts.push_back(MakeAlt(1, 2, 3, 24));
  alts.push_back(MakeAlt(2, 2, 3, 24));
  std::vector<std::vector<uint32_t>> rates(2);
  rates[0] = {44100, 48000};
  rates[1] = {88200, 96000};

  std::string error;
  CHECK_EQ(ChooseAltSetting(alts, rates, 96000, 2, 24, &error), 1);
  CHECK_EQ(ChooseAltSetting(alts, rates, 44100, 2, 24, &error), 0);

  error.clear();
  CHECK_EQ(ChooseAltSetting(alts, rates, 192000, 2, 24, &error), -1);
  CHECK_TRUE(error.find("192000 Hz") != std::string::npos);
}

void TestContinuousAndUnknownRates() {
  // A UAC1 device with a continuous range covers everything inside it.
  std::vector<AltSetting> alts;
  AltSetting continuous = MakeAlt(1, 2, 3, 24);
  continuous.rate_min = 8000;
  continuous.rate_max = 96000;
  alts.push_back(continuous);
  const std::vector<std::vector<uint32_t>> no_rates(alts.size());

  std::string error;
  CHECK_EQ(ChooseAltSetting(alts, no_rates, 44100, 2, 24, &error), 0);
  error.clear();
  CHECK_EQ(ChooseAltSetting(alts, no_rates, 192000, 2, 24, &error), -1);
  CHECK_TRUE(!error.empty());

  // A UAC2 device whose clock query returned nothing is assumed able to run the
  // requested rate; the device itself rejects it otherwise.
  std::vector<AltSetting> unknown;
  unknown.push_back(MakeAlt(1, 2, 4, 32));
  CHECK_EQ(ChooseAltSetting(unknown, no_rates, 384000, 2, 32, &error), 0);
}

void TestEmptyDeviceList() {
  const std::vector<AltSetting> alts;
  const std::vector<std::vector<uint32_t>> rates;
  std::string error;
  CHECK_EQ(ChooseAltSetting(alts, rates, 48000, 2, 16, &error), -1);
  CHECK_TRUE(!error.empty());
}

}  // namespace

void RunFormatChoiceTests() {
  TestExactMatchWins();
  TestNarrowestFallback();
  TestNeverSubstitutesFormat();
  TestRateFiltering();
  TestContinuousAndUnknownRates();
  TestEmptyDeviceList();
}
