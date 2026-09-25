#include "decoder.hpp"

#include <algorithm>
#include <array>
#include <charconv>
#include <cstdint>
#include <filesystem>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <limits>
#include <optional>
#include <sstream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace {

class Sha256 {
public:
    void update(const std::uint8_t* data, std::size_t size) {
        total_bytes_ += size;
        while (size != 0) {
            const std::size_t count = std::min(size, block_.size() - block_size_);
            std::copy_n(data, count, block_.begin() + static_cast<std::ptrdiff_t>(block_size_));
            block_size_ += count;
            data += count;
            size -= count;
            if (block_size_ == block_.size()) {
                transform(block_.data());
                block_size_ = 0;
            }
        }
    }

    std::string finish() {
        const std::uint64_t bit_count = total_bytes_ * 8U;
        block_[block_size_++] = 0x80;
        if (block_size_ > 56) {
            std::fill(block_.begin() + static_cast<std::ptrdiff_t>(block_size_), block_.end(), 0);
            transform(block_.data());
            block_size_ = 0;
        }
        std::fill(block_.begin() + static_cast<std::ptrdiff_t>(block_size_), block_.begin() + 56, 0);
        for (int index = 0; index < 8; ++index) {
            block_[63 - index] = static_cast<std::uint8_t>(bit_count >> (index * 8));
        }
        transform(block_.data());

        std::ostringstream result;
        result << std::hex << std::setfill('0');
        for (std::uint32_t word : state_) {
            result << std::setw(8) << word;
        }
        return result.str();
    }

private:
    static constexpr std::uint32_t rotate_right(std::uint32_t value, int count) {
        return (value >> count) | (value << (32 - count));
    }

    void transform(const std::uint8_t* block) {
        static constexpr std::array<std::uint32_t, 64> constants{
            0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU, 0x59f111f1U,
            0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U,
            0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U, 0xc19bf174U, 0xe49b69c1U, 0xefbe4786U,
            0x0fc19dc6U, 0x240ca1ccU, 0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU,
            0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
            0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU, 0x53380d13U,
            0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U, 0xa2bfe8a1U, 0xa81a664bU,
            0xc24b8b70U, 0xc76c51a3U, 0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U,
            0x19a4c116U, 0x1e376c08U, 0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU,
            0x5b9cca4fU, 0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
            0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
        };
        std::array<std::uint32_t, 64> words{};
        for (int index = 0; index < 16; ++index) {
            words[index] = (static_cast<std::uint32_t>(block[index * 4]) << 24) |
                           (static_cast<std::uint32_t>(block[index * 4 + 1]) << 16) |
                           (static_cast<std::uint32_t>(block[index * 4 + 2]) << 8) |
                           static_cast<std::uint32_t>(block[index * 4 + 3]);
        }
        for (int index = 16; index < 64; ++index) {
            const std::uint32_t s0 = rotate_right(words[index - 15], 7) ^
                                     rotate_right(words[index - 15], 18) ^
                                     (words[index - 15] >> 3);
            const std::uint32_t s1 = rotate_right(words[index - 2], 17) ^
                                     rotate_right(words[index - 2], 19) ^
                                     (words[index - 2] >> 10);
            words[index] = words[index - 16] + s0 + words[index - 7] + s1;
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];
        std::uint32_t e = state_[4];
        std::uint32_t f = state_[5];
        std::uint32_t g = state_[6];
        std::uint32_t h = state_[7];
        for (int index = 0; index < 64; ++index) {
            const std::uint32_t sum1 = rotate_right(e, 6) ^ rotate_right(e, 11) ^ rotate_right(e, 25);
            const std::uint32_t choice = (e & f) ^ (~e & g);
            const std::uint32_t temporary1 = h + sum1 + choice + constants[index] + words[index];
            const std::uint32_t sum0 = rotate_right(a, 2) ^ rotate_right(a, 13) ^ rotate_right(a, 22);
            const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temporary2 = sum0 + majority;
            h = g;
            g = f;
            f = e;
            e = d + temporary1;
            d = c;
            c = b;
            b = a;
            a = temporary1 + temporary2;
        }
        state_[0] += a;
        state_[1] += b;
        state_[2] += c;
        state_[3] += d;
        state_[4] += e;
        state_[5] += f;
        state_[6] += g;
        state_[7] += h;
    }

    std::array<std::uint32_t, 8> state_{
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
    };
    std::array<std::uint8_t, 64> block_{};
    std::size_t block_size_ = 0;
    std::uint64_t total_bytes_ = 0;
};

struct Options {
    std::filesystem::path path;
    std::int64_t seek_ms = 0;
    std::uint64_t limit_frames = std::numeric_limits<std::uint64_t>::max();
    std::size_t source_chunk = 997;
    std::optional<std::uint64_t> source_fail_at;
    bool unknown_size = false;
    bool nonseekable = false;
    bool expect_source_error = false;
    std::optional<std::string> expect_sha256;
    std::optional<std::uint64_t> expect_frames;
    std::optional<int> expect_container_bits;
    std::optional<int> expect_valid_bits;
    std::optional<bool> expect_floating;
    std::optional<bool> expect_lossless;
};

[[noreturn]] void usage_error(const std::string& message) {
    throw std::invalid_argument(
        message +
        "\nusage: jastreamer-decoder-smoke [--seek-ms N] [--limit-frames N] "
        "[--source-chunk N] [--source-fail-at N] [--unknown-size] [--nonseekable] "
        "[--expect-source-error] [--expect-sha256 HEX] [--expect-frames N] "
        "[--expect-container-bits N] [--expect-valid-bits N] "
        "[--expect-floating 0|1] [--expect-lossless 0|1] FILE");
}

template <typename Integer>
Integer parse_integer(std::string_view text, const char* option) {
    Integer value{};
    const auto [end, error] = std::from_chars(text.data(), text.data() + text.size(), value);
    if (error != std::errc{} || end != text.data() + text.size()) {
        usage_error(std::string("invalid value for ") + option);
    }
    return value;
}

Options parse_options(int argc, char** argv) {
    Options options;
    for (int index = 1; index < argc; ++index) {
        const std::string_view argument(argv[index]);
        auto value = [&](const char* name) -> std::string_view {
            if (++index >= argc) {
                usage_error(std::string("missing value for ") + name);
            }
            return argv[index];
        };

        if (argument == "--seek-ms") {
            options.seek_ms = parse_integer<std::int64_t>(value("--seek-ms"), "--seek-ms");
        } else if (argument == "--limit-frames") {
            options.limit_frames = parse_integer<std::uint64_t>(value("--limit-frames"), "--limit-frames");
        } else if (argument == "--source-chunk") {
            options.source_chunk = parse_integer<std::size_t>(value("--source-chunk"), "--source-chunk");
            if (options.source_chunk == 0 || options.source_chunk > 65'536) {
                usage_error("--source-chunk must be in 1..65536");
            }
        } else if (argument == "--source-fail-at") {
            options.source_fail_at = parse_integer<std::uint64_t>(value("--source-fail-at"), "--source-fail-at");
        } else if (argument == "--unknown-size") {
            options.unknown_size = true;
        } else if (argument == "--nonseekable") {
            options.nonseekable = true;
        } else if (argument == "--expect-source-error") {
            options.expect_source_error = true;
        } else if (argument == "--expect-sha256") {
            options.expect_sha256 = std::string(value("--expect-sha256"));
        } else if (argument == "--expect-frames") {
            options.expect_frames = parse_integer<std::uint64_t>(value("--expect-frames"), "--expect-frames");
        } else if (argument == "--expect-container-bits") {
            options.expect_container_bits = parse_integer<int>(value("--expect-container-bits"), "--expect-container-bits");
        } else if (argument == "--expect-valid-bits") {
            options.expect_valid_bits = parse_integer<int>(value("--expect-valid-bits"), "--expect-valid-bits");
        } else if (argument == "--expect-floating") {
            const int parsed = parse_integer<int>(value("--expect-floating"), "--expect-floating");
            if (parsed != 0 && parsed != 1) usage_error("--expect-floating must be 0 or 1");
            options.expect_floating = parsed != 0;
        } else if (argument == "--expect-lossless") {
            const int parsed = parse_integer<int>(value("--expect-lossless"), "--expect-lossless");
            if (parsed != 0 && parsed != 1) usage_error("--expect-lossless must be 0 or 1");
            options.expect_lossless = parsed != 0;
        } else if (!argument.empty() && argument.front() == '-') {
            usage_error("unknown option: " + std::string(argument));
        } else if (!options.path.empty()) {
            usage_error("more than one input file was supplied");
        } else {
            options.path = std::filesystem::path(argument);
        }
    }
    if (options.path.empty()) usage_error("an input file is required");
    if (options.seek_ms < 0) usage_error("--seek-ms cannot be negative");
    if (options.nonseekable && options.seek_ms != 0) {
        usage_error("--seek-ms cannot be used with --nonseekable");
    }
    return options;
}

void require(bool condition, const std::string& message) {
    if (!condition) throw std::runtime_error(message);
}

int run(const Options& options) {
    const std::uintmax_t unsigned_size = std::filesystem::file_size(options.path);
    if (unsigned_size > static_cast<std::uintmax_t>(std::numeric_limits<std::int64_t>::max())) {
        throw std::runtime_error("input file is too large");
    }
    const std::int64_t file_size = static_cast<std::int64_t>(unsigned_size);
    std::ifstream input(options.path, std::ios::binary);
    if (!input) throw std::runtime_error("could not open input file");

    auto reader = [&](std::int64_t offset, std::uint8_t* destination, std::size_t count) -> std::int64_t {
        if (offset < 0 || offset > file_size) throw jastreamer::SourceError("source offset is invalid");
        if (options.source_fail_at.has_value()) {
            const std::uint64_t failure = *options.source_fail_at;
            if (static_cast<std::uint64_t>(offset) >= failure) {
                throw jastreamer::SourceError("injected source failure");
            }
            count = std::min<std::size_t>(count, static_cast<std::size_t>(failure - offset));
        }
        count = std::min(count, options.source_chunk);
        count = std::min<std::size_t>(count, static_cast<std::size_t>(file_size - offset));
        if (count == 0) return 0;

        input.clear();
        input.seekg(offset, std::ios::beg);
        if (!input) throw jastreamer::SourceError("source seek failed");
        input.read(reinterpret_cast<char*>(destination), static_cast<std::streamsize>(count));
        const std::streamsize read = input.gcount();
        if (read <= 0 && !input.eof()) throw jastreamer::SourceError("source read failed");
        return static_cast<std::int64_t>(read);
    };

    try {
        jastreamer::Decoder decoder(
            std::move(reader), options.unknown_size ? -1 : file_size, !options.nonseekable);
        if (options.seek_ms != 0) decoder.seek(options.seek_ms);

        const jastreamer::AudioFormat format = decoder.format();
        require(format.channels > 0 && format.container_bits > 0 && format.container_bits % 8 == 0,
                "decoder returned an invalid sample container");
        const std::size_t bytes_per_frame = static_cast<std::size_t>(format.channels) *
                                            static_cast<std::size_t>(format.container_bits / 8);
        require(bytes_per_frame <= std::numeric_limits<std::size_t>::max() / 4096,
                "decoded frame size is too large");

        std::vector<std::uint8_t> buffer(bytes_per_frame * 4096);
        Sha256 hash;
        std::uint64_t frames = 0;
        while (frames < options.limit_frames) {
            const std::size_t request = static_cast<std::size_t>(std::min<std::uint64_t>(
                4096, options.limit_frames - frames));
            const std::size_t received = decoder.read(buffer.data(), request);
            if (received == 0) break;
            hash.update(buffer.data(), received * bytes_per_frame);
            frames += received;
        }
        const std::string digest = hash.finish();

        std::cout << "sample_rate=" << format.sample_rate << '\n'
                  << "channels=" << format.channels << '\n'
                  << "container_bits=" << format.container_bits << '\n'
                  << "valid_bits=" << format.valid_bits << '\n'
                  << "floating_point=" << (format.floating_point ? 1 : 0) << '\n'
                  << "channel_mask=0x" << std::hex << format.channel_mask << std::dec << '\n'
                  << "lossless=" << (format.lossless ? 1 : 0) << '\n'
                  << "duration_ms=" << decoder.duration_ms() << '\n'
                  << "position_ms=" << decoder.position_ms() << '\n'
                  << "decoded_frames=" << frames << '\n'
                  << "decoded_bytes=" << frames * bytes_per_frame << '\n'
                  << "sha256=" << digest << '\n';

        if (options.expect_source_error) throw std::runtime_error("expected a source error but decoding succeeded");
        if (options.expect_sha256) require(digest == *options.expect_sha256, "decoded SHA-256 did not match");
        if (options.expect_frames) require(frames == *options.expect_frames, "decoded frame count did not match");
        if (options.expect_container_bits) require(format.container_bits == *options.expect_container_bits, "container bits did not match");
        if (options.expect_valid_bits) require(format.valid_bits == *options.expect_valid_bits, "valid bits did not match");
        if (options.expect_floating) require(format.floating_point == *options.expect_floating, "floating-point flag did not match");
        if (options.expect_lossless) require(format.lossless == *options.expect_lossless, "lossless flag did not match");
        return 0;
    } catch (const jastreamer::SourceError& error) {
        std::cerr << "error_kind=source\nerror=" << error.what() << '\n';
        return options.expect_source_error ? 0 : 3;
    } catch (const jastreamer::DecodeError& error) {
        std::cerr << "error_kind=decode\nerror=" << error.what() << '\n';
        return 4;
    }
}

} // namespace

int main(int argc, char** argv) {
    try {
        return run(parse_options(argc, argv));
    } catch (const std::invalid_argument& error) {
        std::cerr << error.what() << '\n';
        return 2;
    } catch (const std::exception& error) {
        std::cerr << "error_kind=assertion\nerror=" << error.what() << '\n';
        return 5;
    }
}
