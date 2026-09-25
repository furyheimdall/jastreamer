#include "protocol.hpp"
#ifdef _WIN32
#include "smtc.hpp"
#include <objbase.h>
#endif

#include <iostream>
#include <sstream>
#include <string>

int main() {
    using namespace jastreamer;
    bool ok = true;
    std::istringstream input("{\"id\":\"version\",\"method\":\"status\",\"params\":{}}\n");
    std::ostringstream output;
    JsonLineProtocol protocol(input, output);
    nlohmann::json value;
    ok &= protocol.read(value) == JsonLineProtocol::ReadResult::message;
    const auto command = JsonLineProtocol::command_from(value);
    ok &= command.id == "version" && command.method == "status" && command.params.is_object();
    protocol.reply(command.id, {{"protocol", "jastreamer-native-audio"},
                                {"version", kNativeAudioProtocolVersion}});
    const auto reply = nlohmann::json::parse(output.str());
    ok &= reply == nlohmann::json{{"id", "version"}, {"ok", true},
                                   {"result", {{"protocol", "jastreamer-native-audio"}, {"version", 1}}}};

    TransportFence fence;
    const auto media_b = fence.claim_media("B", 7);
    ok &= media_b.result == TransportFenceResult::accepted && media_b.lease.has_value();
    ok &= fence.permits_media(*media_b.lease);
    ok &= fence.permits_transport(*media_b.lease, 7, false);
    ok &= fence.accept_transport("A", 8, true).result == TransportFenceResult::wrong_media;
    ok &= fence.accept_transport("B", 6, true).result == TransportFenceResult::stale_sequence;
    ok &= fence.accept_transport("B", 9, false).result == TransportFenceResult::accepted;
    ok &= !fence.permits_transport(*media_b.lease, 7, false);
    ok &= fence.permits_transport(*media_b.lease, 9, false);
    ok &= fence.accept_transport("B", 8, true).result == TransportFenceResult::stale_sequence;
    const auto stopped_b = fence.accept_transport("B", 9, true);
    ok &= stopped_b.result == TransportFenceResult::accepted && stopped_b.lease.has_value() &&
          stopped_b.lease->generation == media_b.lease->generation;
    ok &= !fence.permits_transport(*media_b.lease, 9, false);
    ok &= fence.permits_transport(*media_b.lease, 9, true);

    const auto media_c = fence.claim_media("C", 1);
    ok &= media_c.result == TransportFenceResult::accepted && media_c.lease.has_value() &&
          media_c.lease->generation != media_b.lease->generation;
    fence.release(*media_b.lease);
    fence.complete(*media_b.lease);
    ok &= fence.accept_transport("C", 1, false).result == TransportFenceResult::accepted;

    std::istringstream oversized(
        std::string(kMaximumProtocolLineBytes + 1, 'x') +
        "\r{\"id\":\"after-limit\",\"method\":\"status\",\"params\":{}}\r\n");
    std::ostringstream discarded;
    JsonLineProtocol bounded(oversized, discarded);
    try {
        bounded.read(value);
        ok = false;
    } catch (const ProtocolError&) {
    }
    ok &= bounded.read(value) == JsonLineProtocol::ReadResult::message;
    const auto after_limit = JsonLineProtocol::command_from(value);
    ok &= after_limit.id == "after-limit" && after_limit.method == "status";

#ifdef _WIN32
    const auto initialized = SUCCEEDED(CoInitializeEx(nullptr, COINIT_MULTITHREADED));
    if (initialized) {
        try {
            Smtc smtc([](nlohmann::json) {});
            smtc.update({{"enabled", false}, {"state", "stopped"}, {"position_ms", 0},
                         {"duration_ms", 0}, {"title", ""}, {"artist", ""}, {"album", ""},
                         {"seekable", false}});
            smtc.clear();
        } catch (...) {
            ok = false;
        }
        CoUninitialize();
    } else {
        ok = false;
    }
#endif
    if (!ok) std::cerr << "native protocol smoke failed\n";
    return ok ? 0 : 1;
}
