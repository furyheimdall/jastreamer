#include "audio_engine.hpp"

#include <array>
#include <cstdint>
#include <cstring>
#include <iostream>

using jastreamer::AudioFormat;
using jastreamer::EngineError;

namespace {

bool require(bool condition, const char* message) {
    if (!condition) std::cerr << message << '\n';
    return condition;
}

template <typename T, std::size_t N>
std::array<std::uint8_t, sizeof(T) * N> bytes_of(const std::array<T, N>& values) {
    std::array<std::uint8_t, sizeof(T) * N> bytes{};
    std::memcpy(bytes.data(), values.data(), bytes.size());
    return bytes;
}

} // namespace

int main() {
    bool ok = true;
    const AudioFormat signed16{48000, 2, 16, 16, false, 3, true};
    auto unity = bytes_of<std::int16_t, 4>({-32768, -1, 1, 32767});
    const auto original = unity;
    jastreamer::pcm::apply_gain(unity.data(), 2, signed16, 1.0);
    ok &= require(unity == original, "unity gain changed integer words");

    auto half = bytes_of<std::int16_t, 4>({-32768, -3, 3, 32767});
    jastreamer::pcm::apply_gain(half.data(), 2, signed16, 0.5);
    std::array<std::int16_t, 4> half_values{};
    std::memcpy(half_values.data(), half.data(), half.size());
    ok &= require(half_values[0] == -16384 && half_values[1] == -2 &&
                  half_values[2] == 2 && half_values[3] == 16384,
                  "signed integer gain did not round and preserve polarity at boundaries");
    const AudioFormat signed24In32{48000, 1, 32, 24, false, 4, true};
    auto aligned = bytes_of<std::int32_t, 1>({0x7fffff00});
    jastreamer::pcm::apply_gain(aligned.data(), 1, signed24In32, 0.5);
    std::int32_t aligned_value = 0;
    std::memcpy(&aligned_value, aligned.data(), sizeof(aligned_value));
    ok &= require(aligned_value == 0x3fffff00,
                  "gain failed to keep 24 valid bits MSB-aligned in a 32-bit container");

    const AudioFormat signed24{48000, 2, 24, 24, false, 3, true};
    std::array<std::uint8_t, 12> packed24{
        0x00, 0x00, 0x80,
        0xfd, 0xff, 0xff,
        0x03, 0x00, 0x00,
        0xff, 0xff, 0x7f,
    };
    jastreamer::pcm::apply_gain(packed24.data(), 2, signed24, 0.5);
    ok &= require(packed24 == std::array<std::uint8_t, 12>{
                      0x00, 0x00, 0xc0,
                      0xfe, 0xff, 0xff,
                      0x02, 0x00, 0x00,
                      0x00, 0x00, 0x40,
                  },
                  "packed signed 24-bit gain did not preserve polarity and rounding");


    const AudioFormat unsigned8{44100, 1, 8, 8, false, 4, false};
    std::array<std::uint8_t, 3> unsigned_values{0, 128, 255};
    jastreamer::pcm::apply_gain(unsigned_values.data(), 3, unsigned8, 0.0);
    ok &= require(unsigned_values == std::array<std::uint8_t, 3>{128, 128, 128},
                  "unsigned 8-bit silence was not centered at 128");

    const AudioFormat float32{96000, 1, 32, 32, true, 4, true};
    auto floats = bytes_of<float, 3>({-1.0F, 0.25F, 1.0F});
    jastreamer::pcm::apply_gain(floats.data(), 3, float32, 0.5);
    std::array<float, 3> float_values{};
    std::memcpy(float_values.data(), floats.data(), floats.size());
    ok &= require(float_values == std::array<float, 3>{-0.5F, 0.125F, 0.5F},
                  "floating-point local gain produced unexpected samples");

    try {
        jastreamer::pcm::validate_exclusive_source_format(
            AudioFormat{192000, 2, 64, 32, false, 3, true});
        jastreamer::pcm::validate_exclusive_source_format(
            AudioFormat{192000, 2, 32, 24, false, 3, true});
    } catch (...) {
        ok &= require(false, "Representable exact PCM was rejected before endpoint negotiation");
    }

    ok &= require(jastreamer::pcm::default_channel_mask(2) == 3 &&
                  jastreamer::pcm::default_channel_mask(6) == 0x60f &&
                  jastreamer::pcm::default_channel_mask(9) == 0,
                  "channel mask boundary mapping is incorrect");
    ok &= require(jastreamer::pcm::exclusive_buffer_duration_100ns(480, 48000) == 100000 &&
                  jastreamer::pcm::exclusive_buffer_duration_100ns(1, 44100) == 227,
                  "exclusive aligned buffer duration did not round upward at the frame boundary");
    try {
        (void)jastreamer::pcm::exclusive_buffer_duration_100ns(0, 48000);
        ok &= require(false, "zero-frame exclusive buffer was accepted");
    } catch (const EngineError& error) {
        ok &= require(error.code() == "exclusive_unsupported",
                      "invalid aligned buffer used the wrong error class");
    }
    return ok ? 0 : 1;
}
