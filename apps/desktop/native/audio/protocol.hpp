#pragma once

#include <cstddef>
#include <istream>
#include <map>
#include <mutex>
#include <optional>
#include <ostream>
#include <stdexcept>
#include <string>
#include <cstdint>

#include <nlohmann/json.hpp>

namespace jastreamer {

inline constexpr std::size_t kMaximumProtocolLineBytes = 256U * 1024U;
inline constexpr int kNativeAudioProtocolVersion = 1;

class ProtocolError final : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

struct Command {
    std::string id;
    std::string method;
    nlohmann::json params;
};

struct MediaLease {
    std::string play_id;
    std::uint64_t generation = 0;
};

enum class TransportFenceResult {
    accepted,
    media_active,
    no_media,
    wrong_media,
    stale_sequence,
};

struct TransportFenceDecision {
    TransportFenceResult result = TransportFenceResult::no_media;
    std::optional<MediaLease> lease;
};

class TransportFence final {
public:
    TransportFenceDecision claim_media(const std::string& play_id, std::uint64_t sequence);
    TransportFenceDecision accept_transport(const std::string& play_id, std::uint64_t sequence,
                                             bool releases_media);
    bool permits_media(const MediaLease& lease);
    bool permits_transport(const MediaLease& lease, std::uint64_t sequence, bool releases_media);
    void complete(const MediaLease& lease);
    void release(const MediaLease& lease);

private:
    struct Current {
        MediaLease lease;
        std::uint64_t sequence = 0;
    };
    struct LeaseState {
        MediaLease lease;
        std::uint64_t sequence = 0;
        bool stop_pending = false;
    };


    std::mutex mutex_;
    std::optional<Current> current_;
    std::map<std::uint64_t, LeaseState> leases_;
    std::uint64_t next_generation_ = 0;
};

class JsonLineProtocol final {
public:
    enum class ReadResult { message, end_of_stream };

    JsonLineProtocol(std::istream& input, std::ostream& output);

    ReadResult read(nlohmann::json& value);
    static Command command_from(const nlohmann::json& value);

    void reply(const std::string& id, const nlohmann::json& result);
    void fail(const std::string& id, std::string code, std::string message);
    void notify(const nlohmann::json& value);

private:
    void write(const nlohmann::json& value);

    std::istream& input_;
    std::ostream& output_;
    std::mutex output_mutex_;
};

std::string sanitized_protocol_message(std::string message);

} // namespace jastreamer
