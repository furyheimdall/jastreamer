#include "coreaudio_pcm.hpp"

#include <array>
#include <atomic>
#include <cstdint>
#include <iostream>
#include <stdexcept>
#include <thread>
#include <vector>

namespace {
void require(bool condition, const char* message) {
    if (!condition) throw std::runtime_error(message);
}
void packing() {
    // 24 valid bits in a decoder's 32-bit word, including negative extrema.
    const std::array<std::uint8_t, 16> source{
        0, 0xff, 0xff, 0x7f, 0, 0, 0, 0x80, 0, 0xff, 0xff, 0xff, 0, 1, 0, 0};
    std::array<std::uint8_t, 12> packed{};
    jastreamer::coreaudio::pack_integer(source.data(), packed.data(), 4, 4, 3, 24, 24, false);
    require(packed == std::array<std::uint8_t, 12>{0xff, 0xff, 0x7f, 0, 0, 0x80, 0xff, 0xff, 0xff, 1, 0, 0},
            "Packed 24-bit output must preserve signed valid words");
    std::array<std::uint8_t, 16> high{};
    jastreamer::coreaudio::pack_integer(packed.data(), high.data(), 4, 3, 4, 24, 24, true);
    require(high == source, "High-aligned output must preserve valid bits with zero low padding");
    std::array<std::uint8_t, 16> low{};
    jastreamer::coreaudio::pack_integer(source.data(), low.data(), 4, 4, 4, 24, 24, false);
    require(low == std::array<std::uint8_t, 16>{0xff, 0xff, 0x7f, 0, 0, 0, 0x80, 0, 0xff, 0xff, 0xff, 0, 1, 0, 0, 0},
            "Low-aligned output must zero high padding, not sign-extend it");
    const std::array<std::uint8_t, 4> unsigned_pcm{0, 128, 255, 127};
    std::array<std::uint8_t, 4> signed_pcm{};
    jastreamer::coreaudio::pack_integer(unsigned_pcm.data(), signed_pcm.data(), 4, 1, 1, 8, 8, false);
    require(signed_pcm == std::array<std::uint8_t, 4>{128, 0, 127, 255}, "Unsigned decoder PCM must become signed HAL PCM");
    const std::array<std::uint8_t, 4> dirty_padding{0xab, 0x34, 0x12, 0x80};
    std::array<std::uint8_t, 4> clean{};
    jastreamer::coreaudio::pack_integer(dirty_padding.data(), clean.data(), 1, 4, 4, 24, 24, true);
    require(clean == std::array<std::uint8_t, 4>{0, 0x34, 0x12, 0x80}, "Invalid decoder padding cannot reach the DAC");
    const std::array<std::uint8_t, 8> widest{1, 2, 3, 4, 5, 6, 7, 0x80};
    std::array<std::uint8_t, 8> copied{};
    jastreamer::coreaudio::pack_integer(widest.data(), copied.data(), 1, 8, 8, 64, 64, true);
    require(copied == widest, "64-bit integer PCM must not pass through float or overflow a shift");
    const std::array<std::uint8_t, 8> sixteen{0xff, 0x7f, 0, 0x80, 0xff, 0xff, 1, 0};
    std::array<std::uint8_t, 12> widened24{};
    jastreamer::coreaudio::pack_integer(sixteen.data(), widened24.data(), 4, 2, 3, 16, 24, false);
    require(widened24 == std::array<std::uint8_t, 12>{0, 0xff, 0x7f, 0, 0, 0x80, 0, 0xff, 0xff, 0, 1, 0},
            "16-to-24 widening must append zero LSBs and preserve normalized signed amplitude");
    std::array<std::uint8_t, 16> widened32{};
    jastreamer::coreaudio::pack_integer(source.data(), widened32.data(), 4, 4, 4, 24, 32, false);
    require(widened32 == source, "24-to-32 widening must preserve decoder MSB-aligned words exactly");
    std::array<std::uint8_t, 16> padded24{};
    jastreamer::coreaudio::pack_integer(sixteen.data(), padded24.data(), 4, 2, 4, 16, 24, false);
    require(padded24 == std::array<std::uint8_t, 16>{0, 0xff, 0x7f, 0, 0, 0, 0x80, 0, 0, 0xff, 0xff, 0, 0, 1, 0, 0},
            "Low-aligned widened words must zero both precision-extension and container-padding bits");
}
void ring_boundaries() {
    jastreamer::coreaudio::PcmRing ring(8);
    const std::array<std::uint8_t, 12> source{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11};
    std::array<std::uint8_t, 12> output{};
    require(ring.push(source.data(), 12) == 8, "Producer must not overwrite unread PCM");
    require(ring.push(source.data(), 1) == 0, "A full ring must reject another byte");
    require(ring.pop(output.data(), 5) == 5, "Consumer must read its requested prefix");
    require(ring.push(source.data() + 8, 4) == 4, "Producer must reuse only consumed space");
    require(ring.pop(output.data() + 5, 7) == 7, "Consumer must join the wrapped PCM blocks");
    require(output == source, "Ring wrap must not reorder, omit or repeat samples");
    output.fill(0x55);
    require(ring.pop(output.data(), 12) == 0 && output[0] == 0x55, "Underrun must not invent PCM");
    ring.push(source.data(), 8);
    ring.clear();
    require(ring.readable() == 0 && ring.pop(output.data(), 12) == 0, "Seek reset must discard the old media segment");
}
void eof_publication_race() {
    jastreamer::coreaudio::PcmRing ring(16);
    const std::array<std::uint8_t, 8> source{11, 22, 33, 44, 55, 66, 77, 88};
    std::array<std::uint8_t, 8> output{};
    std::atomic<bool> eof{false};
    ring.push(source.data(), 4);
    const auto first = ring.pop(output.data(), output.size());
    // Deterministically schedule the producer's final bytes+EOF between the
    // callback's first read and its acquire of EOF. No silence may split them.
    ring.push(source.data() + 4, 4);
    eof.store(true, std::memory_order_release);
    auto result = jastreamer::coreaudio::complete_pcm_block(
        ring, output.data(), output.size(), first, eof.load(std::memory_order_acquire));
    require(result.bytes == 8 && !result.underrun && output == source && ring.readable() == 0,
            "Final PCM published with EOF must fill the same callback, not follow an unreported silence gap");
    ring.push(source.data(), 4);
    output.fill(0);
    const auto partial = ring.pop(output.data(), output.size());
    result = jastreamer::coreaudio::complete_pcm_block(ring, output.data(), output.size(), partial, true);
    require(result.bytes == 4 && !result.underrun && output[3] == 44 && output[4] == 0,
            "A drained short final block must retain normal trailing silence without reporting starvation");
    ring.push(source.data(), 4);
    const auto starved = ring.pop(output.data(), output.size());
    result = jastreamer::coreaudio::complete_pcm_block(ring, output.data(), output.size(), starved, false);
    require(result.bytes == 4 && result.underrun, "Missing frames before EOF must invalidate transparency");
}
void concurrent_ring() {
    jastreamer::coreaudio::PcmRing ring(4096);
    std::vector<std::uint8_t> source(1024 * 1024), output(source.size());
    for (std::size_t i = 0; i != source.size(); ++i) source[i] = static_cast<std::uint8_t>((i * 37) ^ (i >> 8));
    std::thread producer([&] {
        std::size_t offset = 0;
        while (offset < source.size()) {
            const auto count = std::min<std::size_t>(997, source.size() - offset);
            const auto written = ring.push(source.data() + offset, count);
            offset += written;
            if (!written) std::this_thread::yield();
        }
    });
    std::size_t offset = 0;
    while (offset < output.size()) {
        const auto count = ring.pop(output.data() + offset, std::min<std::size_t>(509, output.size() - offset));
        offset += count;
        if (!count) std::this_thread::yield();
    }
    producer.join();
    require(source == output && ring.readable() == 0, "Concurrent HAL/decode transfer must preserve all PCM exactly");
}
}
int main() {
    try {
        packing();
        ring_boundaries();
        eof_publication_race();
        concurrent_ring();
        std::cout << "CoreAudio PCM packing and bounded SPSC transport passed\n";
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
