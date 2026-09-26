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
#include "ring_buffer.h"

namespace {

using usb_direct::RingBuffer;

void TestCapacityRounding() {
  CHECK_EQ(RingBuffer(1000).Capacity(), 1024u);
  CHECK_EQ(RingBuffer(1024).Capacity(), 1024u);
  CHECK_EQ(RingBuffer(0).Capacity(), 2u);
}

void TestShortWriteAndDrain() {
  RingBuffer ring(8);
  const uint8_t source[12] = {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12};
  CHECK_EQ(ring.Write(source, sizeof(source)), 8u);
  CHECK_EQ(ring.Available(), 8u);
  CHECK_EQ(ring.Write(source, 1), 0u);

  uint8_t sink[8] = {0};
  CHECK_EQ(ring.Read(sink, sizeof(sink)), 8u);
  CHECK_EQ(ring.Available(), 0u);
  bool contents_match = true;
  for (size_t i = 0; i < sizeof(sink); ++i) {
    if (sink[i] != source[i]) {
      contents_match = false;
    }
  }
  CHECK_TRUE(contents_match);
  CHECK_EQ(ring.Read(sink, 1), 0u);
}

void TestWrapAround() {
  RingBuffer ring(8);
  std::vector<uint8_t> written;
  std::vector<uint8_t> read_back;
  uint8_t next = 0;
  uint8_t chunk[5] = {0};
  uint8_t sink[5] = {0};
  for (int round = 0; round < 20; ++round) {
    for (uint8_t& value : chunk) {
      value = next++;
    }
    const size_t accepted = ring.Write(chunk, sizeof(chunk));
    written.insert(written.end(), chunk, chunk + accepted);
    const size_t taken = ring.Read(sink, sizeof(sink));
    read_back.insert(read_back.end(), sink, sink + taken);
  }
  while (ring.Available() > 0) {
    const size_t taken = ring.Read(sink, sizeof(sink));
    read_back.insert(read_back.end(), sink, sink + taken);
  }
  CHECK_EQ(read_back.size(), written.size());
  CHECK_TRUE(read_back == written);
  CHECK_TRUE(written.size() >= 90u);
}

void TestReset() {
  RingBuffer ring(16);
  const uint8_t source[4] = {9, 9, 9, 9};
  CHECK_EQ(ring.Write(source, sizeof(source)), 4u);
  ring.Reset();
  CHECK_EQ(ring.Available(), 0u);
  CHECK_EQ(ring.Write(source, sizeof(source)), 4u);
  CHECK_EQ(ring.Available(), 4u);
}

}  // namespace


// Regression: a stale flush target that the consumer has already read past must
// not be treated as "drop everything" (unsigned wrap made SkipTo empty the ring
// on every transfer, so a flushed stream played only silence).
void TestStaleSkipTargetDropsNothing() {
  RingBuffer ring(16);
  uint8_t in[8] = {1, 2, 3, 4, 5, 6, 7, 8};
  uint8_t out[8] = {};
  CHECK_EQ(ring.Write(in, 4), size_t{4});
  const size_t target = ring.Head();
  CHECK_EQ(ring.SkipTo(target), size_t{4});
  CHECK_EQ(ring.Write(in, 8), size_t{8});
  CHECK_EQ(ring.Read(out, 2), size_t{2});
  CHECK_EQ(ring.SkipTo(target), size_t{0});
  CHECK_EQ(ring.Available(), size_t{6});
  CHECK_EQ(ring.SkipTo(0), size_t{0});
  CHECK_EQ(ring.Available(), size_t{6});
}

void RunRingBufferTests() {
  TestStaleSkipTargetDropsNothing();
  TestCapacityRounding();
  TestShortWriteAndDrain();
  TestWrapAround();
  TestReset();
}
