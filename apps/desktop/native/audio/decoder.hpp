#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <stdexcept>

namespace jastreamer {

struct AudioFormat {
    int sample_rate;
    int channels;
    int container_bits;
    int valid_bits;
    bool floating_point;
    std::uint32_t channel_mask;
    bool lossless;
};

class SourceError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

class DecodeError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

class Decoder {
public:
    using ReadBytes = std::function<std::int64_t(
        std::int64_t offset,
        std::uint8_t* destination,
        std::size_t count)>;

    Decoder(ReadBytes read_bytes, std::int64_t size, bool seekable);
    ~Decoder();

    Decoder(const Decoder&) = delete;
    Decoder& operator=(const Decoder&) = delete;
    Decoder(Decoder&&) = delete;
    Decoder& operator=(Decoder&&) = delete;

    [[nodiscard]] const AudioFormat& format() const;
    [[nodiscard]] std::int64_t duration_ms() const;
    std::size_t read(std::uint8_t* destination, std::size_t frames);
    void seek(std::int64_t position_ms);
    [[nodiscard]] std::int64_t position_ms() const;

private:
    class Impl;
    std::unique_ptr<Impl> impl_;
};

} // namespace jastreamer
