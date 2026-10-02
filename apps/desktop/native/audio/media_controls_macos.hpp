#pragma once

#include <functional>
#include <memory>
#include <nlohmann/json.hpp>

namespace jastreamer {

// Owns only system presentation and remote-command forwarding, never playback.
class MacMediaControls final {
public:
    explicit MacMediaControls(std::function<void(nlohmann::json)> emit);
    ~MacMediaControls();
    MacMediaControls(const MacMediaControls&) = delete;
    MacMediaControls& operator=(const MacMediaControls&) = delete;
    void update(const nlohmann::json& state);
    void clear();

private:
    struct Impl;
    std::shared_ptr<Impl> impl_;
};

// AppKit/MediaPlayer stay on the process main thread; stdin and audio commands do not.
int run_macos_audio_application(int (*run_protocol)());

} // namespace jastreamer
