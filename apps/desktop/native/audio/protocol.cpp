#include "protocol.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <utility>

namespace jastreamer {
TransportFenceDecision TransportFence::claim_media(const std::string& play_id, std::uint64_t sequence) {
    std::lock_guard lock(mutex_);
    if (current_) return {TransportFenceResult::media_active, std::nullopt};
    MediaLease lease{play_id, ++next_generation_};
    current_ = Current{lease, sequence};
    leases_[lease.generation] = LeaseState{lease, sequence, false};
    return {TransportFenceResult::accepted, std::move(lease)};
}

TransportFenceDecision TransportFence::accept_transport(const std::string& play_id, std::uint64_t sequence,
                                                         bool releases_media) {
    std::lock_guard lock(mutex_);
    if (!current_) return {TransportFenceResult::no_media, std::nullopt};
    if (current_->lease.play_id != play_id)
        return {TransportFenceResult::wrong_media, std::nullopt};
    if (sequence < current_->sequence)
        return {TransportFenceResult::stale_sequence, std::nullopt};
    current_->sequence = sequence;
    auto lease = current_->lease;
    auto found = leases_.find(lease.generation);
    if (found == leases_.end()) return {TransportFenceResult::no_media, std::nullopt};
    found->second.sequence = sequence;
    if (releases_media) {
        found->second.stop_pending = true;
        current_.reset();
    }
    return {TransportFenceResult::accepted, std::move(lease)};
}

bool TransportFence::permits_media(const MediaLease& lease) {
    std::lock_guard lock(mutex_);
    return current_ && current_->lease.generation == lease.generation &&
           current_->lease.play_id == lease.play_id;
}

bool TransportFence::permits_transport(const MediaLease& lease, std::uint64_t sequence,
                                       bool releases_media) {
    std::lock_guard lock(mutex_);
    const auto found = leases_.find(lease.generation);
    if (found == leases_.end() || found->second.lease.play_id != lease.play_id ||
        found->second.sequence != sequence) {
        return false;
    }
    if (releases_media) return found->second.stop_pending;
    return !found->second.stop_pending && current_ &&
           current_->lease.generation == lease.generation;
}

void TransportFence::complete(const MediaLease& lease) {
    std::lock_guard lock(mutex_);
    if (current_ && current_->lease.generation == lease.generation &&
        current_->lease.play_id == lease.play_id) {
        current_.reset();
    }
    const auto found = leases_.find(lease.generation);
    if (found != leases_.end() && found->second.lease.play_id == lease.play_id)
        leases_.erase(found);
}

void TransportFence::release(const MediaLease& lease) {
    std::lock_guard lock(mutex_);
    if (current_ && current_->lease.generation == lease.generation &&
        current_->lease.play_id == lease.play_id) {
        current_.reset();
    }
    const auto found = leases_.find(lease.generation);
    if (found != leases_.end() && found->second.lease.play_id == lease.play_id &&
        !found->second.stop_pending) {
        leases_.erase(found);
    }
}


JsonLineProtocol::JsonLineProtocol(std::istream& input, std::ostream& output)
    : input_(input), output_(output) {}

JsonLineProtocol::ReadResult JsonLineProtocol::read(nlohmann::json& value) {
    std::string line;
    line.reserve(4096);
    for (;;) {
        const auto next = input_.get();
        if (next == std::char_traits<char>::eof()) {
            if (line.empty()) {
                return ReadResult::end_of_stream;
            }
            throw ProtocolError("unterminated JSON line");
        }
        if (next == '\n') {
            break;
        }
        if (next == '\r') {
            if (input_.peek() == '\n') {
                input_.get();
            }
            break;
        }
        if (line.size() == kMaximumProtocolLineBytes) {
            // Drain this record without retaining attacker-controlled data so a caller may
            // report the failure and continue with the next framed command.
            while (input_.good()) {
                const auto discarded = input_.get();
                if (discarded == '\r') {
                    if (input_.peek() == '\n') input_.get();
                    break;
                }
                if (discarded == '\n' || discarded == std::char_traits<char>::eof()) break;
            }
            throw ProtocolError("JSON line exceeds 256 KiB");
        }
        line.push_back(static_cast<char>(next));
    }
    if (line.empty()) {
        throw ProtocolError("empty JSON line");
    }
    try {
        value = nlohmann::json::parse(line, nullptr, true, false);
    } catch (const nlohmann::json::exception&) {
        throw ProtocolError("invalid JSON");
    }
    if (!value.is_object()) {
        throw ProtocolError("protocol message must be an object");
    }
    return ReadResult::message;
}

Command JsonLineProtocol::command_from(const nlohmann::json& value) {
    if (!value.is_object() || !value.contains("id") || !value["id"].is_string() ||
        !value.contains("method") || !value["method"].is_string()) {
        throw ProtocolError("command requires string id and method");
    }
    const auto id = value["id"].get<std::string>();
    const auto method = value["method"].get<std::string>();
    if (id.empty() || id.size() > 128 || method.empty() || method.size() > 64) {
        throw ProtocolError("invalid command id or method");
    }
    auto params = nlohmann::json::object();
    if (value.contains("params")) {
        if (!value["params"].is_object()) {
            throw ProtocolError("command params must be an object");
        }
        params = value["params"];
    }
    return Command{id, method, std::move(params)};
}

void JsonLineProtocol::reply(const std::string& id, const nlohmann::json& result) {
    write({{"id", id}, {"ok", true}, {"result", result}});
}

void JsonLineProtocol::fail(const std::string& id, std::string code, std::string message) {
    write({{"id", id},
           {"ok", false},
           {"error", {{"code", std::move(code)}, {"message", sanitized_protocol_message(std::move(message))}}}});
}

void JsonLineProtocol::notify(const nlohmann::json& value) {
    if (!value.is_object() || value.contains("id")) {
        throw ProtocolError("notification must be an object without id");
    }
    write(value);
}

void JsonLineProtocol::write(const nlohmann::json& value) {
    const auto encoded = value.dump(-1, ' ', false, nlohmann::json::error_handler_t::replace);
    if (encoded.size() > kMaximumProtocolLineBytes) {
        throw ProtocolError("outgoing JSON line exceeds 256 KiB");
    }
    std::lock_guard lock(output_mutex_);
    output_.write(encoded.data(), static_cast<std::streamsize>(encoded.size()));
    output_.put('\n');
    output_.flush();
    if (!output_) {
        throw ProtocolError("protocol output closed");
    }
}

std::string sanitized_protocol_message(std::string message) {
    if (message.size() > 512) {
        message.resize(512);
    }
    for (auto& ch : message) {
        const auto byte = static_cast<unsigned char>(ch);
        if (byte < 0x20 || byte == 0x7f) {
            ch = ' ';
        }
    }
    return message.empty() ? std::string("native audio operation failed") : message;
}

} // namespace jastreamer
