#include "decoder.hpp"

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

constexpr int kSampleRate = 48'000;
constexpr int kChannels = 2;
constexpr int kFrames = 96'000;
constexpr std::size_t kInputBytesPerFrame = 6;
constexpr std::size_t kOutputBytesPerFrame = 8;

void require(bool condition, const std::string& message) {
    if (!condition) throw std::runtime_error(message);
}

void append_u16(std::vector<std::uint8_t>& bytes, std::uint16_t value) {
    bytes.push_back(static_cast<std::uint8_t>(value));
    bytes.push_back(static_cast<std::uint8_t>(value >> 8));
}

void append_u32(std::vector<std::uint8_t>& bytes, std::uint32_t value) {
    for (int shift = 0; shift < 32; shift += 8) {
        bytes.push_back(static_cast<std::uint8_t>(value >> shift));
    }
}

void append_s24(std::vector<std::uint8_t>& bytes, std::int32_t value) {
    const std::uint32_t encoded = static_cast<std::uint32_t>(value) & 0x00ff'ffffU;
    bytes.push_back(static_cast<std::uint8_t>(encoded));
    bytes.push_back(static_cast<std::uint8_t>(encoded >> 8));
    bytes.push_back(static_cast<std::uint8_t>(encoded >> 16));
}

void append_s32(std::vector<std::uint8_t>& bytes, std::int32_t value) {
    append_u32(bytes, static_cast<std::uint32_t>(value));
}

std::int32_t sample_value(int frame, int channel) {
    if (frame == 0) return channel == 0 ? -0x0080'0000 : 0x007f'ffff;
    if (frame == 1) return channel == 0 ? -1 : 0;
    const std::uint32_t mixed =
        (static_cast<std::uint32_t>(frame) * 104'729U +
         static_cast<std::uint32_t>(channel) * 6'700'417U + 17U) &
        0x00ff'ffffU;
    return static_cast<std::int32_t>(mixed) - 0x0080'0000;
}
struct Fixture {
    std::vector<std::uint8_t> wav;
    std::vector<std::uint8_t> decoded;
};

Fixture make_pcm24_fixture() {
    const std::uint32_t data_size =
        static_cast<std::uint32_t>(kFrames * kInputBytesPerFrame);
    Fixture fixture;
    fixture.wav.reserve(68 + data_size);
    fixture.decoded.reserve(static_cast<std::size_t>(kFrames) * kOutputBytesPerFrame);

    fixture.wav.insert(fixture.wav.end(), {'R', 'I', 'F', 'F'});
    append_u32(fixture.wav, 60U + data_size);
    fixture.wav.insert(fixture.wav.end(), {'W', 'A', 'V', 'E'});
    fixture.wav.insert(fixture.wav.end(), {'f', 'm', 't', ' '});
    append_u32(fixture.wav, 40);
    append_u16(fixture.wav, 0xfffe);
    append_u16(fixture.wav, kChannels);
    append_u32(fixture.wav, kSampleRate);
    append_u32(fixture.wav, kSampleRate * kInputBytesPerFrame);
    append_u16(fixture.wav, kInputBytesPerFrame);
    append_u16(fixture.wav, 24);
    append_u16(fixture.wav, 22);
    append_u16(fixture.wav, 24);
    append_u32(fixture.wav, 0x3);
    fixture.wav.insert(fixture.wav.end(), {
        0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00,
        0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71,
    });
    fixture.wav.insert(fixture.wav.end(), {'d', 'a', 't', 'a'});
    append_u32(fixture.wav, data_size);

    for (int frame = 0; frame < kFrames; ++frame) {
        for (int channel = 0; channel < kChannels; ++channel) {
            const std::int32_t sample = sample_value(frame, channel);
            append_s24(fixture.wav, sample);
            append_s32(fixture.decoded, sample * 256);
        }
    }
    return fixture;
}

struct MemorySource {
    const std::vector<std::uint8_t>& bytes;
    std::size_t maximum_chunk = 113;
    std::size_t failure_offset = std::numeric_limits<std::size_t>::max();
    std::size_t calls = 0;
    std::size_t largest_offset = 0;

    std::int64_t read(std::int64_t offset, std::uint8_t* destination, std::size_t count) {
        ++calls;
        require(offset >= 0, "decoder requested a negative source offset");
        require(count <= 32 * 1024, "decoder issued an unbounded source read");
        const std::size_t position = static_cast<std::size_t>(offset);
        largest_offset = std::max(largest_offset, position);
        if (position >= failure_offset) return -1;
        if (position >= bytes.size()) return 0;
        count = std::min({count, maximum_chunk, bytes.size() - position,
                          failure_offset - position});
        if (count == 0) return -1;
        std::memcpy(destination, bytes.data() + position, count);
        return static_cast<std::int64_t>(count);
    }
};

jastreamer::Decoder make_decoder(MemorySource& source, bool seekable = true) {
    return jastreamer::Decoder(
        [&source](std::int64_t offset, std::uint8_t* destination, std::size_t count) {
            return source.read(offset, destination, count);
        },
        static_cast<std::int64_t>(source.bytes.size()),
        seekable);
}

void verify_full_decode(const Fixture& fixture) {
    MemorySource source{fixture.wav};
    auto decoder = make_decoder(source);
    const auto& format = decoder.format();
    require(format.sample_rate == kSampleRate, "PCM sample rate changed");
    require(format.channels == kChannels, "PCM channel count changed");
    require(format.container_bits == 32, "24-bit PCM was not retained in a 32-bit container");
    require(format.valid_bits == 24, "24-bit PCM precision was not retained");
    require(!format.floating_point, "integer PCM passed through floating point");
    require(format.channel_mask == 0x3U, "stereo channel layout was not retained");
    require(format.lossless, "PCM was not marked lossless");
    require(decoder.duration_ms() == 2'000, "PCM duration is inaccurate");
    require(decoder.position_ms() == 0, "initial decoder position is not zero");

    std::vector<std::uint8_t> actual;
    actual.reserve(fixture.decoded.size());
    std::vector<std::uint8_t> block(kOutputBytesPerFrame * 137);
    for (;;) {
        const std::size_t frames = decoder.read(block.data(), 137);
        if (frames == 0) break;
        actual.insert(actual.end(), block.begin(),
                      block.begin() + static_cast<std::ptrdiff_t>(frames * kOutputBytesPerFrame));
    }
    require(actual == fixture.decoded, "decoded integer samples changed precision or alignment");
    require(decoder.position_ms() == 2'000, "final decoder position is inaccurate");
    require(decoder.duration_ms() == 2'000, "decoded duration is inaccurate");
    require(decoder.read(block.data(), 1) == 0, "decoder did not retain EOF state");
    require(source.calls > 100, "partial source reads were not exercised");
}

void verify_seek(const Fixture& fixture) {
    MemorySource source{fixture.wav};
    auto decoder = make_decoder(source);
    decoder.seek(750);
    require(decoder.position_ms() == 750, "seek did not set the requested position");

    constexpr std::size_t frames = 333;
    std::vector<std::uint8_t> actual(frames * kOutputBytesPerFrame);
    require(decoder.read(actual.data(), frames) == frames, "seeked decode returned too few samples");
    const auto expected_begin = fixture.decoded.begin() +
                                static_cast<std::ptrdiff_t>(36'000 * kOutputBytesPerFrame);
    require(std::equal(actual.begin(), actual.end(), expected_begin),
            "seek returned samples from the wrong position");
    require(decoder.position_ms() == 757, "position did not follow delivered seeked samples");
    require(source.largest_offset > 100'000, "seek did not exercise an arbitrary source offset");
}

void verify_nonseekable(const Fixture& fixture) {
    MemorySource source{fixture.wav};
    auto decoder = make_decoder(source, false);
    try {
        decoder.seek(1);
    } catch (const jastreamer::DecodeError&) {
        return;
    }
    throw std::runtime_error("nonseekable source accepted a seek");
}

void verify_source_failure(const Fixture& fixture) {
    MemorySource source{fixture.wav};
    source.failure_offset = 70'000;
    try {
        auto decoder = make_decoder(source);
        std::vector<std::uint8_t> block(kOutputBytesPerFrame * 1024);
        while (decoder.read(block.data(), 1024) != 0) {
        }
    } catch (const jastreamer::SourceError&) {
        return;
    } catch (const jastreamer::DecodeError&) {
        throw std::runtime_error("source failure was reported as corrupt media");
    }
    throw std::runtime_error("source failure was reported as successful EOF");
}

void verify_malformed_media() {
    std::vector<std::uint8_t> malformed(4096, 0xa5);
    MemorySource source{malformed};
    try {
        auto decoder = make_decoder(source);
        (void)decoder;
    } catch (const jastreamer::DecodeError&) {
        return;
    } catch (const jastreamer::SourceError&) {
        throw std::runtime_error("malformed media was reported as a source failure");
    }
    throw std::runtime_error("malformed media was accepted");
}

void verify_companded_pcm_precision() {
    std::vector<std::uint8_t> wav{'R', 'I', 'F', 'F'};
    append_u32(wav, 42);
    wav.insert(wav.end(), {'W', 'A', 'V', 'E', 'f', 'm', 't', ' '});
    append_u32(wav, 16);
    append_u16(wav, 7);
    append_u16(wav, 1);
    append_u32(wav, 8'000);
    append_u32(wav, 8'000);
    append_u16(wav, 1);
    append_u16(wav, 8);
    wav.insert(wav.end(), {'d', 'a', 't', 'a'});
    append_u32(wav, 6);
    wav.insert(wav.end(), {0xff, 0x7f, 0x80, 0x00, 0x55, 0xd5});
    MemorySource source{wav};
    auto decoder = make_decoder(source);
    require(decoder.format().container_bits == 16 && decoder.format().valid_bits == 16,
            "companded coded width replaced decoded PCM precision");
    require(!decoder.format().lossless, "companded PCM was marked lossless");
    std::vector<std::uint8_t> actual(12);
    require(decoder.read(actual.data(), 6) == 6, "companded PCM samples were lost");
    std::vector<std::uint8_t> expected;
    for (const int sample : {0, 0, 32124, -32124, -716, 716}) {
        append_u16(expected, static_cast<std::uint16_t>(sample));
    }
    require(actual == expected, "companded PCM sample values changed");
}

} // namespace

int main() {
    try {
        const Fixture fixture = make_pcm24_fixture();
        verify_full_decode(fixture);
        verify_seek(fixture);
        verify_nonseekable(fixture);
        verify_source_failure(fixture);
        verify_malformed_media();
        verify_companded_pcm_precision();
        std::cout << "decoder_behavior=passed\n"
                  << "pcm24_frames=" << kFrames << '\n'
                  << "pcm24_output_bytes=" << fixture.decoded.size() << '\n';
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "decoder_behavior=failed\nerror=" << error.what() << '\n';
        return 1;
    }
}
