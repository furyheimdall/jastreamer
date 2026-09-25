#include "audio_engine.hpp"
#include "protocol.hpp"
#include "smtc.hpp"

#include <windows.h>
#include <objbase.h>

#include <algorithm>
#include <array>
#include <atomic>
#include <cmath>
#include <cstring>
#include <condition_variable>
#include <cstdint>
#include <cstdlib>
#include <deque>
#include <iostream>
#include <limits>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <thread>
#include <utility>
#include <vector>

namespace jastreamer {
namespace {

std::uint64_t unsigned_integer(const nlohmann::json& object, const char* name, bool nonzero = false) {
    if (!object.contains(name) || (!object[name].is_number_unsigned() && !object[name].is_number_integer()))
        throw EngineError("invalid_argument", std::string("Missing integer parameter: ") + name);
    const auto signed_value = object[name].get<std::int64_t>();
    if (signed_value < 0 || (nonzero && signed_value == 0))
        throw EngineError("invalid_argument", std::string("Invalid integer parameter: ") + name);
    return static_cast<std::uint64_t>(signed_value);
}

std::int64_t signed_integer(const nlohmann::json& object, const char* name) {
    if (!object.contains(name) || (!object[name].is_number_integer() && !object[name].is_number_unsigned()))
        throw EngineError("invalid_argument", std::string("Missing integer parameter: ") + name);
    if (object[name].is_number_unsigned()) {
        const auto value = object[name].get<std::uint64_t>();
        if (value > static_cast<std::uint64_t>(std::numeric_limits<std::int64_t>::max()))
            throw EngineError("invalid_argument", std::string("Integer parameter is too large: ") + name);
        return static_cast<std::int64_t>(value);
    }
    return object[name].get<std::int64_t>();
}

std::string required_string(const nlohmann::json& object, const char* name, std::size_t maximum = 4096) {
    if (!object.contains(name) || !object[name].is_string())
        throw EngineError("invalid_argument", std::string("Missing string parameter: ") + name);
    auto value = object[name].get<std::string>();
    if (value.empty() || value.size() > maximum)
        throw EngineError("invalid_argument", std::string("Invalid string parameter: ") + name);
    return value;
}
void require_bounded_string(const nlohmann::json& object, const char* name, std::size_t maximum = 4096) {
    if (!object.contains(name) || !object[name].is_string() ||
        object[name].get_ref<const std::string&>().size() > maximum) {
        throw EngineError("invalid_argument", std::string("Invalid string parameter: ") + name);
    }
}


bool required_bool(const nlohmann::json& object, const char* name) {
    if (!object.contains(name) || !object[name].is_boolean())
        throw EngineError("invalid_argument", std::string("Missing boolean parameter: ") + name);
    return object[name].get<bool>();
}

double required_volume(const nlohmann::json& object) {
    if (!object.contains("volume") || !object["volume"].is_number())
        throw EngineError("invalid_argument", "Missing numeric volume");
    const auto value = object["volume"].get<double>();
    if (!std::isfinite(value) || value < 0.0 || value > 1.0)
        throw EngineError("invalid_argument", "Volume must be between zero and one");
    return value;
}

std::vector<std::uint8_t> decode_base64(const std::string& encoded, std::size_t maximum) {
    if (encoded.size() > ((maximum + 2) / 3) * 4 + 4 || encoded.size() % 4 != 0)
        throw EngineError("invalid_read_result", "Media read result is too large or malformed");
    static constexpr std::array<std::int8_t, 256> table = [] {
        std::array<std::int8_t, 256> value{};
        value.fill(-1);
        for (int index = 0; index < 26; ++index) {
            value[static_cast<unsigned>('A' + index)] = static_cast<std::int8_t>(index);
            value[static_cast<unsigned>('a' + index)] = static_cast<std::int8_t>(26 + index);
        }
        for (int index = 0; index < 10; ++index) value[static_cast<unsigned>('0' + index)] = static_cast<std::int8_t>(52 + index);
        value[static_cast<unsigned>('+')] = 62;
        value[static_cast<unsigned>('/')] = 63;
        return value;
    }();
    std::vector<std::uint8_t> result;
    result.reserve(encoded.size() / 4 * 3);
    for (std::size_t offset = 0; offset < encoded.size(); offset += 4) {
        std::uint32_t bits = 0;
        int padding = 0;
        for (int index = 0; index < 4; ++index) {
            const auto byte = static_cast<unsigned char>(encoded[offset + index]);
            if (byte == '=') {
                if (index < 2 || offset + 4 != encoded.size())
                    throw EngineError("invalid_read_result", "Media read result has invalid base64 padding");
                ++padding;
                bits <<= 6;
            } else {
                if (padding != 0 || table[byte] < 0)
                    throw EngineError("invalid_read_result", "Media read result has invalid base64 data");
                bits = (bits << 6) | static_cast<std::uint32_t>(table[byte]);
            }
        }
        result.push_back(static_cast<std::uint8_t>(bits >> 16));
        if (padding < 2) result.push_back(static_cast<std::uint8_t>(bits >> 8));
        if (padding < 1) result.push_back(static_cast<std::uint8_t>(bits));
    }
    if (result.size() > maximum) throw EngineError("invalid_read_result", "Media read result exceeds requested length");
    return result;
}

class MediaReads final {
public:
    explicit MediaReads(JsonLineProtocol& wire) : protocol(wire) {}

    SourceCallbacks begin(std::string play_id, std::int64_t size, const MediaLease& lease) {
        auto generation = std::make_shared<Generation>();
        generation->play_id = std::move(play_id);
        generation->lease_generation = lease.generation;
        generation->size = size;
        std::shared_ptr<Generation> previous;
        {
            std::lock_guard lock(mutex);
            previous = current;
            current.reset();
            if (closed || permanently_cancelled.contains(lease.generation)) {
                generation->active = false;
            } else {
                current = generation;
            }
        }
        if (previous) cancel_generation(previous);
        return {
            [this, weak = std::weak_ptr<Generation>(generation)](std::int64_t offset, std::uint8_t* destination,
                                                                 std::size_t count) -> std::int64_t {
                auto active = weak.lock();
                if (!active || offset < 0 || count == 0 || count > 65536) return -1;
                const auto known_size = active->size.load(std::memory_order_acquire);
                if (known_size >= 0 && offset >= known_size) return 0;
                if (known_size >= 0)
                    count = std::min<std::size_t>(count, static_cast<std::size_t>(known_size - offset));
                auto pending = std::make_shared<Pending>();
                {
                    std::lock_guard lock(mutex);
                    if (!active->active || current != active || active->interrupted ||
                        requests.size() >= 8) return -1;
                    pending->generation = active;
                    pending->maximum = count;
                    pending->offset = offset;
                    pending->id = std::to_string(++next_id);
                    requests.emplace(pending->id, pending);
                }
                try {
                    protocol.notify({{"event", "read"}, {"read_id", pending->id}, {"play_id", active->play_id},
                                     {"offset", offset}, {"length", count}});
                } catch (...) {
                    finish_cancel(pending);
                    return -1;
                }
                std::unique_lock lock(pending->mutex);
                pending->ready.wait(lock, [&] { return pending->complete; });
                const auto failed = pending->cancelled || !pending->error.empty();
                const auto data = std::move(pending->data);
                lock.unlock();
                {
                    std::lock_guard map_lock(mutex);
                    requests.erase(pending->id);
                }
                if (failed) return -1;
                if (!data.empty()) std::memcpy(destination, data.data(), data.size());
                return static_cast<std::int64_t>(data.size());
            },
            [this, weak = std::weak_ptr<Generation>(generation)] {
                if (auto active = weak.lock()) interrupt_generation(active);
            },
            [this, weak = std::weak_ptr<Generation>(generation)] {
                if (auto active = weak.lock()) rearm_generation(active);
            },
        };
    }

    void resolve(const nlohmann::json& params) {
        const auto id = required_string(params, "read_id", 64);
        const auto play_id = required_string(params, "play_id", 256);
        std::shared_ptr<Pending> pending;
        {
            std::lock_guard lock(mutex);
            const auto found = requests.find(id);
            if (found == requests.end() || found->second->generation->play_id != play_id ||
                found->second->generation != current || !found->second->generation->active) return;
            pending = found->second;
        }
        std::vector<std::uint8_t> data;
        std::string error;
        if (params.contains("error")) {
            if (!params["error"].is_object() ||
                !params["error"].contains("code") || !params["error"]["code"].is_string() ||
                !params["error"].contains("message") || !params["error"]["message"].is_string()) {
                error = "Invalid media source error response";
            } else {
                error = "Media source read failed";
            }
        } else if (!params.contains("data") || !params["data"].is_string() ||
                   !params.contains("eof") || !params["eof"].is_boolean()) {
            error = "Invalid media source response";
        } else {
            try {
                data = decode_base64(params["data"].get<std::string>(), pending->maximum);
            } catch (const EngineError&) {
                error = "Invalid media source response";
            }
            const auto eof = params["eof"].get<bool>();
            if (data.empty() && !eof && error.empty())
                error = "Empty non-terminal media source response";
            std::int64_t learned = -1;
            if (params.contains("size")) {
                try {
                    learned = signed_integer(params, "size");
                    const auto previous = pending->generation->size.load(std::memory_order_acquire);
                    if (learned < 0 || (previous >= 0 && previous != learned))
                        error = "Invalid media source size";
                } catch (...) {
                    error = "Invalid media source size";
                }
            } else if (eof) {
                if (pending->offset > std::numeric_limits<std::int64_t>::max() -
                                          static_cast<std::int64_t>(data.size())) {
                    error = "Invalid media source size";
                } else {
                    learned = pending->offset + static_cast<std::int64_t>(data.size());
                }
            }
            if (learned >= 0) {
                if (pending->offset > learned ||
                    data.size() > static_cast<std::uint64_t>(learned - pending->offset)) {
                    error = "Media source response exceeds its declared size";
                } else if (error.empty()) {
                    pending->generation->size.store(learned, std::memory_order_release);
                }
            }
        }
        {
            std::lock_guard lock(pending->mutex);
            if (pending->complete) return;
            pending->data = std::move(data);
            pending->error = std::move(error);
            pending->complete = true;
        }
        pending->ready.notify_all();
    }

    void cancel(const MediaLease& lease) noexcept {
        std::shared_ptr<Generation> active;
        {
            std::lock_guard lock(mutex);
            permanently_cancelled.emplace(lease.generation, true);
            if (current && current->lease_generation == lease.generation &&
                current->play_id == lease.play_id) {
                active = current;
                current.reset();
            }
        }
        if (active) cancel_generation(active);
    }

    void finish(const MediaLease& lease) noexcept {
        std::shared_ptr<Generation> active;
        {
            std::lock_guard lock(mutex);
            permanently_cancelled.erase(lease.generation);
            if (current && current->lease_generation == lease.generation &&
                current->play_id == lease.play_id) {
                active = current;
                current.reset();
            }
        }
        if (active) cancel_generation(active);
    }

    void close() noexcept {
        std::vector<std::shared_ptr<Pending>> wake;
        {
            std::lock_guard lock(mutex);
            closed = true;
            if (current) {
                current->active = false;
                current.reset();
            }
            wake.reserve(requests.size());
            for (const auto& [id, pending] : requests) {
                pending->generation->active = false;
                wake.push_back(pending);
            }
        }
        for (const auto& pending : wake) finish_cancel(pending);
    }

private:
    struct Generation {
        std::string play_id;
        std::uint64_t lease_generation = 0;
        std::atomic<bool> active{true};
        std::atomic<std::int64_t> size{-1};
        bool interrupted = false;
    };
    struct Pending {
        std::string id;
        std::shared_ptr<Generation> generation;
        std::size_t maximum = 0;
        std::int64_t offset = 0;
        std::mutex mutex;
        std::condition_variable ready;
        std::vector<std::uint8_t> data;
        std::string error;
        bool complete = false;
        bool cancelled = false;
    };

    void finish_cancel(const std::shared_ptr<Pending>& pending) noexcept {
        {
            std::lock_guard lock(pending->mutex);
            pending->cancelled = true;
            pending->complete = true;
        }
        pending->ready.notify_all();
        std::lock_guard lock(mutex);
        requests.erase(pending->id);
    }

    void interrupt_generation(const std::shared_ptr<Generation>& generation) noexcept {
        std::vector<std::shared_ptr<Pending>> wake;
        {
            std::lock_guard lock(mutex);
            if (!generation->active || current != generation) return;
            generation->interrupted = true;
            for (const auto& [id, pending] : requests)
                if (pending->generation == generation) wake.push_back(pending);
        }
        for (const auto& pending : wake) finish_cancel(pending);
    }

    void rearm_generation(const std::shared_ptr<Generation>& generation) noexcept {
        std::lock_guard lock(mutex);
        if (generation->active && current == generation)
            generation->interrupted = false;
    }

    void cancel_generation(const std::shared_ptr<Generation>& generation) noexcept {
        generation->active = false;
        std::vector<std::shared_ptr<Pending>> wake;
        {
            std::lock_guard lock(mutex);
            for (const auto& [id, pending] : requests)
                if (pending->generation == generation) wake.push_back(pending);
        }
        for (const auto& pending : wake) finish_cancel(pending);
    }

    JsonLineProtocol& protocol;
    std::mutex mutex;
    std::shared_ptr<Generation> current;
    std::map<std::string, std::shared_ptr<Pending>> requests;
    std::map<std::uint64_t, bool> permanently_cancelled;
    std::uint64_t next_id = 0;
    bool closed = false;
};

struct QueuedCommand {
    Command command;
    std::optional<MediaLease> media;
};

class CommandQueue final {
public:
    bool push(QueuedCommand command, bool wait_for_room) {
        std::unique_lock lock(mutex);
        if (wait_for_room) {
            room.wait(lock, [&] { return closed || commands.size() < 64; });
        } else if (commands.size() >= 64) {
            return false;
        }
        if (closed) return false;
        commands.push_back(std::move(command));
        ready.notify_one();
        return true;
    }

    bool can_push() {
        std::lock_guard lock(mutex);
        return !closed && commands.size() < 64;
    }

    std::optional<QueuedCommand> pop() {
        std::unique_lock lock(mutex);
        ready.wait(lock, [&] { return closed || !commands.empty(); });
        if (commands.empty()) return std::nullopt;
        auto command = std::move(commands.front());
        commands.pop_front();
        room.notify_one();
        return command;
    }
    void abort() {
        std::lock_guard lock(mutex);
        commands.clear();
        closed = true;
        ready.notify_all();
        room.notify_all();
    }


    void close() {
        std::lock_guard lock(mutex);
        closed = true;
        ready.notify_all();
        room.notify_all();
    }

private:
    std::mutex mutex;
    std::condition_variable ready;
    std::condition_variable room;
    std::deque<QueuedCommand> commands;
    bool closed = false;
};

nlohmann::json devices_result() {
    const auto endpoints = AudioEngine::enumerate_endpoints();
    nlohmann::json devices = nlohmann::json::array();
    for (const auto& endpoint : endpoints)
        devices.push_back({{"id", endpoint.id}, {"name", endpoint.name}, {"is_default", endpoint.is_default}});
    return {{"devices", std::move(devices)}, {"available", !endpoints.empty()}};
}

MediaSpec media_spec(const nlohmann::json& params, MediaReads& reads, const MediaLease& lease) {
    MediaSpec spec;
    spec.play_id = required_string(params, "play_id", 256);
    spec.sequence = unsigned_integer(params, "sequence", true);
    spec.size = signed_integer(params, "size");
    if (spec.size < -1) throw EngineError("invalid_argument", "Media size cannot be less than -1");
    if (spec.size == 0) spec.size = -1;
    spec.seekable = required_bool(params, "seekable");
    spec.mime = required_string(params, "mime", 256);
    spec.declared_duration_ms = signed_integer(params, "duration_ms");
    if (spec.declared_duration_ms < 0) throw EngineError("invalid_argument", "Media duration cannot be negative");
    for (const auto* key : {"title", "artist", "album"}) require_bounded_string(params, key);
    spec.device_id = required_string(params, "device_id", 2048);
    spec.exclusive = required_bool(params, "exclusive");
    spec.volume = required_volume(params);
    if (params.contains("transformed")) {
        if (!params["transformed"].is_boolean()) throw EngineError("invalid_argument", "transformed must be boolean");
        spec.transformed_known = true;
        spec.transformed = params["transformed"].get<bool>();
    }
    spec.source = reads.begin(spec.play_id, spec.size, lease);
    return spec;
}

void require_fence(const TransportFenceDecision& decision) {
    switch (decision.result) {
    case TransportFenceResult::accepted:
        return;
    case TransportFenceResult::media_active:
        throw EngineError("stop_required", "Stop native audio before loading another media item");
    case TransportFenceResult::stale_sequence:
        throw EngineError("action_failed", "Transport command sequence is stale");
    case TransportFenceResult::no_media:
        throw EngineError("action_failed", "No media is loaded");
    case TransportFenceResult::wrong_media:
        throw EngineError("action_failed", "Media identity changed");
    }
    throw EngineError("action_failed", "Transport command was rejected");
}

void validate_smtc(nlohmann::json& params) {
    (void)required_bool(params, "enabled");
    const auto state = required_string(params, "state", 32);
    if (state != "playing" && state != "paused" && state != "changing" && state != "stopped")
        throw EngineError("invalid_argument", "Invalid SMTC playback state");
    const auto position = signed_integer(params, "position_ms");
    const auto duration = signed_integer(params, "duration_ms");
    if (position < 0 || duration < 0) throw EngineError("invalid_argument", "SMTC timeline cannot be negative");
    if (params.contains("seekable") && !params["seekable"].is_boolean())
        throw EngineError("invalid_argument", "SMTC seekable must be boolean");
    if (!params.contains("seekable")) params["seekable"] = false;
    for (const auto* key : {"title", "artist", "album"}) require_bounded_string(params, key);
    if (params.contains("artwork_base64") &&
        (!params["artwork_base64"].is_string() ||
         params["artwork_base64"].get_ref<const std::string&>().size() > 192 * 1024))
        throw EngineError("invalid_argument", "SMTC artwork is too large");
}

} // namespace
} // namespace jastreamer

int main(int argc, char** argv) {
    using namespace jastreamer;
    if (argc == 2 && std::string(argv[1]) == "--protocol-version") {
        std::cout << nlohmann::json{{"protocol", "jastreamer-native-audio"},
                                    {"version", kNativeAudioProtocolVersion}}.dump() << '\n';
        return 0;
    }
    if (argc != 1) return 2;
    if (FAILED(CoInitializeEx(nullptr, COINIT_MULTITHREADED))) return 3;
    JsonLineProtocol protocol(std::cin, std::cout);
    MediaReads reads(protocol);
    CommandQueue commands;
    TransportFence fence;
    std::mutex engine_media_mutex;
    std::optional<MediaLease> engine_media;
    auto clear_engine_media = [&](const MediaLease& lease, bool cancel_reads) {
        {
            std::lock_guard lock(engine_media_mutex);
            if (engine_media && engine_media->generation == lease.generation &&
                engine_media->play_id == lease.play_id) {
                engine_media.reset();
            }
        }
        if (cancel_reads) reads.cancel(lease);
        reads.finish(lease);
        fence.release(lease);
    };
    auto complete_engine_media = [&](const std::string& play_id) {
        std::optional<MediaLease> lease;
        {
            std::lock_guard lock(engine_media_mutex);
            if (engine_media && engine_media->play_id == play_id) {
                lease = engine_media;
                engine_media.reset();
            }
        }
        if (!lease) return;
        reads.cancel(*lease);
        reads.finish(*lease);
        fence.release(*lease);
    };
    Smtc smtc([&](nlohmann::json event) {
        try { protocol.notify(event); } catch (...) { reads.close(); }
    });
    AudioEngine engine([&](nlohmann::json event) {
        if (event.contains("event") && event["event"] == "observation" &&
            event.contains("play_id") && event["play_id"].is_string() &&
            event.contains("observation") && event["observation"].is_object() &&
            event["observation"].contains("event") && event["observation"]["event"].is_string()) {
            const auto& name = event["observation"]["event"].get_ref<const std::string&>();
            if (name == "ended" || name == "error")
                complete_engine_media(event["play_id"].get_ref<const std::string&>());
        }
        try { protocol.notify(event); } catch (...) { reads.close(); }
    });
    std::atomic<bool> shutdown{false};
    bool requested_shutdown = false;

    std::thread worker([&] {
        const auto worker_com = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
        while (auto next = commands.pop()) {
            auto queued = std::move(*next);
            auto& command = queued.command;
            auto transport = [&](bool releases_media) {
                const auto play_id = required_string(command.params, "play_id", 256);
                const auto sequence = unsigned_integer(command.params, "sequence", true);
                if (!queued.media ||
                    !fence.permits_transport(*queued.media, sequence, releases_media)) {
                    throw EngineError("action_failed", "Transport command was superseded");
                }
                return std::pair{play_id, sequence};
            };
            auto cleanup_failed_command = [&] {
                if (!queued.media) return;
                if (command.method == "set_uri") {
                    reads.cancel(*queued.media);
                    clear_engine_media(*queued.media, false);
                } else if (command.method == "stop") {
                    reads.finish(*queued.media);
                    clear_engine_media(*queued.media, false);
                    fence.complete(*queued.media);
                } else {
                    try {
                        const auto current = engine.status();
                        if (current.contains("state") && current["state"] == "error")
                            clear_engine_media(*queued.media, true);
                    } catch (...) {
                    }
                }
            };
            try {
                if (FAILED(worker_com))
                    throw EngineError("device_unavailable", "Windows audio COM initialization failed");
                nlohmann::json result;
                if (command.method == "devices") {
                    result = devices_result();
                } else if (command.method == "status") {
                    result = engine.status();
                } else if (command.method == "set_uri") {
                    const auto current = engine.status();
                    if (current["state"] != "stopped" && current["state"] != "error")
                        throw EngineError("stop_required", "Stop native audio before loading another media item");
                    if (!queued.media || !fence.permits_media(*queued.media))
                        throw EngineError("action_failed", "Media ownership changed");
                    {
                        std::lock_guard lock(engine_media_mutex);
                        engine_media = *queued.media;
                    }
                    result = engine.set_uri(media_spec(command.params, reads, *queued.media));
                } else if (command.method == "play") {
                    const auto [play_id, sequence] = transport(false);
                    result = engine.play(play_id, sequence);
                } else if (command.method == "pause") {
                    const auto [play_id, sequence] = transport(false);
                    result = engine.pause(play_id, sequence);
                } else if (command.method == "stop") {
                    const auto [play_id, sequence] = transport(true);
                    result = engine.stop(play_id, sequence);
                    if (queued.media) {
                        reads.finish(*queued.media);
                        clear_engine_media(*queued.media, false);
                        fence.complete(*queued.media);
                    }
                } else if (command.method == "seek") {
                    const auto [play_id, sequence] = transport(false);
                    result = engine.seek(play_id, sequence,
                                         signed_integer(command.params, "position_ms"));
                } else if (command.method == "set_volume") {
                    result = engine.set_volume(required_volume(command.params));
                } else if (command.method == "smtc") {
                    validate_smtc(command.params);
                    smtc.update(command.params);
                    result = engine.status();
                } else if (command.method == "shutdown") {
                    reads.close();
                    engine.shutdown();
                    smtc.clear();
                    result = nlohmann::json::object();
                    shutdown = true;
                } else {
                    throw EngineError("unknown_method", "Unknown native audio method");
                }
                protocol.reply(command.id, result);
                if (shutdown.load()) break;
            } catch (const EngineError& error) {
                cleanup_failed_command();
                try { protocol.fail(command.id, error.code(), error.what()); } catch (...) { shutdown = true; }
            } catch (const ProtocolError& error) {
                cleanup_failed_command();
                try { protocol.fail(command.id, "protocol_error", error.what()); } catch (...) { shutdown = true; }
            } catch (const std::exception&) {
                cleanup_failed_command();
                try { protocol.fail(command.id, "native_error", "Native audio operation failed"); } catch (...) { shutdown = true; }
            }
        }
        reads.close();
        engine.shutdown();
        smtc.clear();
        if (SUCCEEDED(worker_com)) CoUninitialize();
    });

    while (!shutdown.load()) {
        nlohmann::json value;
        try {
            if (protocol.read(value) == JsonLineProtocol::ReadResult::end_of_stream) break;
            if (value.contains("method") && value["method"].is_string() && value["method"] == "read_result" &&
                !value.contains("id")) {
                if (value.contains("params") && value["params"].is_object()) reads.resolve(value["params"]);
                continue;
            }
            auto command = JsonLineProtocol::command_from(value);
            const auto id = command.id;
            try {
                const bool stop = command.method == "stop";
                const bool final = command.method == "shutdown";
                const bool interrupting = stop || final;
                if (!interrupting && !commands.can_push()) {
                    protocol.fail(id, "busy", "Native audio command queue is full");
                    continue;
                }
                QueuedCommand queued{std::move(command), std::nullopt};
                if (queued.command.method == "set_uri") {
                    const auto play_id = required_string(queued.command.params, "play_id", 256);
                    const auto sequence = unsigned_integer(queued.command.params, "sequence", true);
                    auto decision = fence.claim_media(play_id, sequence);
                    require_fence(decision);
                    queued.media = std::move(decision.lease);
                } else if (queued.command.method == "play" || queued.command.method == "pause" ||
                           queued.command.method == "stop" || queued.command.method == "seek") {
                    const auto play_id = required_string(queued.command.params, "play_id", 256);
                    const auto sequence = unsigned_integer(queued.command.params, "sequence", true);
                    if (queued.command.method == "seek" &&
                        signed_integer(queued.command.params, "position_ms") < 0) {
                        throw EngineError("invalid_argument", "Seek position cannot be negative");
                    }
                    auto decision = fence.accept_transport(play_id, sequence, stop);
                    require_fence(decision);
                    queued.media = std::move(decision.lease);
                    if (stop) reads.cancel(*queued.media);
                } else if (final) {
                    reads.close();
                }
                if (!commands.push(std::move(queued), interrupting)) {
                    if (!interrupting) protocol.fail(id, "busy", "Native audio command queue is full");
                    else break;
                }
                if (final) {
                    requested_shutdown = true;
                    break;
                }
            } catch (const EngineError& error) {
                protocol.fail(id, error.code(), error.what());
            }
        } catch (const ProtocolError& error) {
            std::cerr << sanitized_protocol_message(error.what()) << '\n';
        } catch (const std::exception&) {
            std::cerr << "Invalid native audio protocol message\n";
        }
    }
    reads.close();
    if (requested_shutdown) commands.close();
    else commands.abort();
    if (worker.joinable()) worker.join();
    CoUninitialize();
    return 0;
}
