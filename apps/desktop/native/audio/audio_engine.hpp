#pragma once

#include "decoder.hpp"

#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <stdexcept>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace jastreamer {

struct EndpointInfo {
    std::string id;
    std::string name;
    bool is_default = false;
};

struct SourceCallbacks {
    Decoder::ReadBytes read;
    std::function<void()> cancel;
    std::function<void()> rearm;
};

struct MediaSpec {
    std::string play_id;
    std::uint64_t sequence = 0;
    std::int64_t size = -1;
    bool seekable = false;
    std::string mime;
    std::int64_t declared_duration_ms = 0;
    bool transformed_known = false;
    bool transformed = false;
    std::string device_id = "default";
    bool exclusive = false;
    double volume = 1.0;
    SourceCallbacks source;
};

class EngineError final : public std::runtime_error {
public:
    EngineError(std::string code, std::string message);
    const std::string& code() const noexcept;

private:
    std::string code_;
};

namespace pcm {

// Decoder integer words are little-endian and valid bits are MSB-aligned in their
// container. At unity this function deliberately does not touch the buffer.
void apply_gain(std::uint8_t* data, std::size_t frames, const AudioFormat& format, double gain);

// Validate that a decoded source can be expressed as WAVEFORMATEXTENSIBLE.
// Endpoint acceptance remains an exact IsFormatSupported negotiation.
void validate_exclusive_source_format(const AudioFormat& format);

std::uint32_t default_channel_mask(int channels);
std::int64_t exclusive_buffer_duration_100ns(std::uint32_t frames, int sample_rate);


} // namespace pcm

class AudioEngine final {
public:
    using Emit = std::function<void(nlohmann::json)>;

    explicit AudioEngine(Emit emit);
    ~AudioEngine();
    AudioEngine(const AudioEngine&) = delete;
    AudioEngine& operator=(const AudioEngine&) = delete;

    static std::vector<EndpointInfo> enumerate_endpoints();

    nlohmann::json set_uri(MediaSpec spec);
    nlohmann::json play(const std::string& play_id, std::uint64_t sequence);
    nlohmann::json pause(const std::string& play_id, std::uint64_t sequence);
    nlohmann::json stop(const std::string& play_id, std::uint64_t sequence);
    nlohmann::json seek(const std::string& play_id, std::uint64_t sequence, std::int64_t position_ms);
    nlohmann::json set_volume(double volume);
    nlohmann::json status() const;
    void shutdown();

private:
    class Impl;
    std::unique_ptr<Impl> impl_;
};

} // namespace jastreamer
