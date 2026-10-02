#pragma once

#include <algorithm>
#include <atomic>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <vector>

namespace jastreamer::coreaudio {

// Single producer (decode worker), single consumer (HAL). Reset only when both
// are stopped. The HAL side performs at most two copies and never waits.
class PcmRing final {
public:
    explicit PcmRing(std::size_t capacity) : bytes_(capacity) {}
    std::size_t readable() const noexcept {
        const auto read = read_.load(std::memory_order_acquire);
        return write_.load(std::memory_order_acquire) - read;
    }
    std::size_t push(const std::uint8_t* source, std::size_t size) noexcept {
        const auto write = write_.load(std::memory_order_relaxed);
        const auto read = read_.load(std::memory_order_acquire);
        size = std::min(size, bytes_.size() - (write - read));
        const auto offset = write % bytes_.size();
        const auto first = std::min(size, bytes_.size() - offset);
        std::memcpy(bytes_.data() + offset, source, first);
        std::memcpy(bytes_.data(), source + first, size - first);
        write_.store(write + size, std::memory_order_release);
        return size;
    }
    std::size_t pop(std::uint8_t* destination, std::size_t size) noexcept {
        const auto read = read_.load(std::memory_order_relaxed);
        const auto write = write_.load(std::memory_order_acquire);
        size = std::min(size, write - read);
        const auto offset = read % bytes_.size();
        const auto first = std::min(size, bytes_.size() - offset);
        std::memcpy(destination, bytes_.data() + offset, first);
        std::memcpy(destination + first, bytes_.data(), size - first);
        read_.store(read + size, std::memory_order_release);
        return size;
    }
    void clear() noexcept { read_.store(0); write_.store(0); }
private:
    std::vector<std::uint8_t> bytes_;
    alignas(64) std::atomic<std::size_t> read_{0};
    alignas(64) std::atomic<std::size_t> write_{0};
};

struct PcmBlockResult {
    std::size_t bytes;
    bool underrun;
};

// Complete a first ring read after acquiring the producer's EOF flag. Seeing
// EOF publishes all final PCM, including bytes published after that first read;
// take one bounded second read before deciding that missing frames are only the
// normal trailing silence after EOF. No allocation, waiting or unbounded retry.
inline PcmBlockResult complete_pcm_block(PcmRing& ring, std::uint8_t* destination,
                                        std::size_t requested, std::size_t copied,
                                        bool source_complete) noexcept {
    if (source_complete && copied < requested)
        copied += ring.pop(destination + copied, requested - copied);
    return {copied, copied < requested && !source_complete};
}

// Preserve the signed valid word, not its numeric float approximation. Input is
// decoder MSB-aligned LE PCM. Output is signed LE, high- or low-aligned as the
// negotiated ASBD specifies; unused container bits are always zero. A wider
// hardware valid word is formed by appending zero LSBs, preserving normalized
// amplitude and every source bit (output_valid_bits >= input_valid_bits).
inline void pack_integer(const std::uint8_t* input, std::uint8_t* output,
                         std::size_t samples, unsigned input_bytes,
                         unsigned output_bytes, unsigned input_valid_bits,
                         unsigned output_valid_bits, bool aligned_high) noexcept {
    const auto mask = input_valid_bits == 64 ? ~std::uint64_t{0} : (std::uint64_t{1} << input_valid_bits) - 1;
    for (std::size_t sample = 0; sample < samples; ++sample) {
        std::uint64_t word = 0;
        for (unsigned byte = 0; byte < input_bytes; ++byte)
            word |= std::uint64_t{input[sample * input_bytes + byte]} << (byte * 8);
        // FFmpeg unsigned eight-bit PCM is offset binary.
        if (input_bytes == 1) word ^= 0x80;
        word = (word >> (input_bytes * 8 - input_valid_bits)) & mask;
        word <<= output_valid_bits - input_valid_bits;
        if (aligned_high) word <<= output_bytes * 8 - output_valid_bits;
        for (unsigned byte = 0; byte < output_bytes; ++byte)
            output[sample * output_bytes + byte] = static_cast<std::uint8_t>(word >> (byte * 8));
    }
}

} // namespace jastreamer::coreaudio
