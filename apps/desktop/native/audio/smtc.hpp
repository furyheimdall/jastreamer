#pragma once

#include <functional>
#include <memory>

#include <nlohmann/json.hpp>

namespace jastreamer {

class Smtc {
public:
    explicit Smtc(std::function<void(nlohmann::json)> emit);
    ~Smtc();

    Smtc(const Smtc&) = delete;
    Smtc& operator=(const Smtc&) = delete;

    void update(const nlohmann::json& state);
    void clear();

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

} // namespace jastreamer
