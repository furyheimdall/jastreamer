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
#include <vector>

#include "check.h"
#include "sample_pack.h"

namespace {

using usb_direct::CanPack;
using usb_direct::PackFrames;

void TestPassThroughCopiesExactly() {
  const std::vector<uint8_t> source = {0x11, 0x22, 0x33, 0x44,
                                       0x55, 0x66, 0x77, 0x88};
  std::vector<uint8_t> target(source.size(), 0xAA);
  CHECK_EQ(PackFrames(source.data(), 2, 2, 2, 2, 2, target.data()),
           static_cast<size_t>(8));
  for (size_t i = 0; i < source.size(); ++i) {
    CHECK_EQ(target[i], source[i]);
  }
}

void TestWidens24Into32LeftJustified() {
  // Two stereo frames of packed little-endian 24-bit samples.
  const std::vector<uint8_t> source = {0x01, 0x02, 0x03, 0x04, 0x05, 0x06,
                                       0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C};
  std::vector<uint8_t> target(16, 0xFF);
  CHECK_EQ(PackFrames(source.data(), 2, 3, 2, 4, 2, target.data()),
           static_cast<size_t>(16));
  // The sample keeps its value in the high bytes; the new low byte is zero, so
  // the level is unchanged rather than attenuated by 256.
  const uint8_t expected[16] = {0x00, 0x01, 0x02, 0x03, 0x00, 0x04, 0x05, 0x06,
                                0x00, 0x07, 0x08, 0x09, 0x00, 0x0A, 0x0B, 0x0C};
  for (size_t i = 0; i < target.size(); ++i) {
    CHECK_EQ(target[i], expected[i]);
  }
}

void TestWidens16Into24And32() {
  const std::vector<uint8_t> source = {0x34, 0x12, 0x78, 0x56};
  std::vector<uint8_t> three(6, 0xFF);
  CHECK_EQ(PackFrames(source.data(), 1, 2, 2, 3, 2, three.data()),
           static_cast<size_t>(6));
  const uint8_t expected_three[6] = {0x00, 0x34, 0x12, 0x00, 0x78, 0x56};
  for (size_t i = 0; i < three.size(); ++i) {
    CHECK_EQ(three[i], expected_three[i]);
  }

  std::vector<uint8_t> four(8, 0xFF);
  CHECK_EQ(PackFrames(source.data(), 1, 2, 2, 4, 2, four.data()),
           static_cast<size_t>(8));
  const uint8_t expected_four[8] = {0x00, 0x00, 0x34, 0x12,
                                    0x00, 0x00, 0x78, 0x56};
  for (size_t i = 0; i < four.size(); ++i) {
    CHECK_EQ(four[i], expected_four[i]);
  }
}

void TestDuplicatesMonoOntoStereo() {
  const std::vector<uint8_t> source = {0x34, 0x12, 0x78, 0x56};
  std::vector<uint8_t> target(8, 0xFF);
  CHECK_EQ(PackFrames(source.data(), 2, 2, 1, 2, 2, target.data()),
           static_cast<size_t>(8));
  const uint8_t expected[8] = {0x34, 0x12, 0x34, 0x12, 0x78, 0x56, 0x78, 0x56};
  for (size_t i = 0; i < target.size(); ++i) {
    CHECK_EQ(target[i], expected[i]);
  }

  // Mono widened onto a stereo 32-bit device at the same time.
  const std::vector<uint8_t> packed24 = {0x01, 0x02, 0x03};
  std::vector<uint8_t> wide(8, 0xFF);
  CHECK_EQ(PackFrames(packed24.data(), 1, 3, 1, 4, 2, wide.data()),
           static_cast<size_t>(8));
  const uint8_t expected_wide[8] = {0x00, 0x01, 0x02, 0x03,
                                    0x00, 0x01, 0x02, 0x03};
  for (size_t i = 0; i < wide.size(); ++i) {
    CHECK_EQ(wide[i], expected_wide[i]);
  }
}

void TestRejectsLossyConversions() {
  // Narrowing, downmixing and upmixing beyond mono duplication are refused, so
  // the sink can never quietly change the samples.
  CHECK_TRUE(!CanPack(3, 2, 2, 2));
  CHECK_TRUE(!CanPack(2, 2, 2, 1));
  CHECK_TRUE(!CanPack(2, 6, 2, 2));
  CHECK_TRUE(!CanPack(2, 1, 2, 6));
  CHECK_TRUE(!CanPack(0, 2, 2, 2));
  CHECK_TRUE(CanPack(3, 2, 4, 2));
  CHECK_TRUE(CanPack(2, 1, 4, 2));

  const std::vector<uint8_t> source = {0x01, 0x02, 0x03, 0x04};
  std::vector<uint8_t> target(8, 0xFF);
  CHECK_EQ(PackFrames(source.data(), 1, 3, 2, 2, 2, target.data()),
           static_cast<size_t>(0));
  CHECK_EQ(target[0], static_cast<uint8_t>(0xFF));
}

void TestWritableFramesNeverSplitsAFrame() {
  using usb_direct::WritableFrames;
  // Source 24-bit stereo (6 byte frames) into 32-bit stereo (8 byte frames).
  CHECK_EQ(WritableFrames(600, 6, 8, 8000), static_cast<size_t>(100));
  // The ring only has room for 10 target frames.
  CHECK_EQ(WritableFrames(600, 6, 8, 87), static_cast<size_t>(10));
  // A trailing partial source frame is never offered.
  CHECK_EQ(WritableFrames(605, 6, 8, 8000), static_cast<size_t>(100));
  CHECK_EQ(WritableFrames(5, 6, 8, 8000), static_cast<size_t>(0));
  CHECK_EQ(WritableFrames(600, 6, 8, 7), static_cast<size_t>(0));
  CHECK_EQ(WritableFrames(600, 0, 8, 8000), static_cast<size_t>(0));
}

}  // namespace

void RunSamplePackTests() {
  TestPassThroughCopiesExactly();
  TestWidens24Into32LeftJustified();
  TestWidens16Into24And32();
  TestDuplicatesMonoOntoStereo();
  TestRejectsLossyConversions();
  TestWritableFramesNeverSplitsAFrame();
}
