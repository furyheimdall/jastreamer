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
// Lock-free single-producer/single-consumer byte ring. The producer is the
// Kotlin feeder thread, the consumer is the libusb transfer callback, so the
// implementation never allocates or blocks after construction.

#ifndef USB_DIRECT_RING_BUFFER_H_
#define USB_DIRECT_RING_BUFFER_H_

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <vector>

namespace usb_direct {

class RingBuffer {
 public:
  // Rounds `capacity_bytes` up to a power of two (minimum 2 bytes).
  explicit RingBuffer(size_t capacity_bytes);

  RingBuffer(const RingBuffer&) = delete;
  RingBuffer& operator=(const RingBuffer&) = delete;

  // Producer side. Returns the number of bytes accepted (may be short).
  size_t Write(const uint8_t* data, size_t length);

  // Consumer side. Returns the number of bytes copied out.
  size_t Read(uint8_t* data, size_t length);

  // Bytes currently readable.
  size_t Available() const;

  // Producer side: bytes that can be accepted right now.
  size_t Free() const;

  // Producer side: the current write cursor. Hand it to [SkipTo] to drop
  // everything written so far without touching the consumer's cursor.
  size_t Head() const;

  // Consumer side: discards every byte written before `target` and returns how
  // many bytes were dropped. Only the consumer moves the read cursor, so a
  // flush issued by the producer is applied here, not behind the feeder's back.
  size_t SkipTo(size_t target);

  // Total capacity in bytes.
  size_t Capacity() const { return capacity_; }

  // Drops all buffered bytes. Only safe while neither side is running.
  void Reset();

 private:
  const size_t capacity_;  // Power of two.
  const size_t mask_;
  std::vector<uint8_t> storage_;
  std::atomic<size_t> head_{0};  // Write cursor, monotonically increasing.
  std::atomic<size_t> tail_{0};  // Read cursor, monotonically increasing.
};

}  // namespace usb_direct

#endif  // USB_DIRECT_RING_BUFFER_H_
