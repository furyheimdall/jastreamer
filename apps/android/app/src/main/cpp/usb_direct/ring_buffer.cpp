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

#include "ring_buffer.h"

#include <cstring>

namespace usb_direct {
namespace {

size_t RoundUpPowerOfTwo(size_t value) {
  size_t capacity = 2;
  while (capacity < value && capacity < (static_cast<size_t>(1) << 31)) {
    capacity <<= 1;
  }
  return capacity;
}

}  // namespace

RingBuffer::RingBuffer(size_t capacity_bytes)
    : capacity_(RoundUpPowerOfTwo(capacity_bytes)),
      mask_(capacity_ - 1),
      storage_(capacity_, 0) {}

size_t RingBuffer::Write(const uint8_t* data, size_t length) {
  if (data == nullptr || length == 0) {
    return 0;
  }
  const size_t head = head_.load(std::memory_order_relaxed);
  const size_t tail = tail_.load(std::memory_order_acquire);
  const size_t used = head - tail;
  const size_t free_bytes = capacity_ - used;
  const size_t to_write = length < free_bytes ? length : free_bytes;
  if (to_write == 0) {
    return 0;
  }
  const size_t offset = head & mask_;
  const size_t first = (capacity_ - offset) < to_write ? (capacity_ - offset)
                                                       : to_write;
  std::memcpy(storage_.data() + offset, data, first);
  if (to_write > first) {
    std::memcpy(storage_.data(), data + first, to_write - first);
  }
  head_.store(head + to_write, std::memory_order_release);
  return to_write;
}

size_t RingBuffer::Read(uint8_t* data, size_t length) {
  if (data == nullptr || length == 0) {
    return 0;
  }
  const size_t tail = tail_.load(std::memory_order_relaxed);
  const size_t head = head_.load(std::memory_order_acquire);
  const size_t used = head - tail;
  const size_t to_read = length < used ? length : used;
  if (to_read == 0) {
    return 0;
  }
  const size_t offset = tail & mask_;
  const size_t first =
      (capacity_ - offset) < to_read ? (capacity_ - offset) : to_read;
  std::memcpy(data, storage_.data() + offset, first);
  if (to_read > first) {
    std::memcpy(data + first, storage_.data(), to_read - first);
  }
  tail_.store(tail + to_read, std::memory_order_release);
  return to_read;
}

size_t RingBuffer::Available() const {
  const size_t head = head_.load(std::memory_order_acquire);
  const size_t tail = tail_.load(std::memory_order_acquire);
  return head - tail;
}

size_t RingBuffer::Free() const {
  const size_t head = head_.load(std::memory_order_relaxed);
  const size_t tail = tail_.load(std::memory_order_acquire);
  return capacity_ - (head - tail);
}

size_t RingBuffer::Head() const {
  return head_.load(std::memory_order_relaxed);
}

size_t RingBuffer::SkipTo(size_t target) {
  const size_t tail = tail_.load(std::memory_order_relaxed);
  const size_t head = head_.load(std::memory_order_acquire);
  const size_t limit = target - tail > head - tail ? head : target;
  const size_t dropped = limit - tail;
  if (dropped == 0) {
    return 0;
  }
  tail_.store(limit, std::memory_order_release);
  return dropped;
}

void RingBuffer::Reset() {
  head_.store(0, std::memory_order_relaxed);
  tail_.store(0, std::memory_order_release);
}

}  // namespace usb_direct
