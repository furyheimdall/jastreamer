// Copyright 2026 The Jastreamer Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

#include "json.h"

#include <cstdio>

namespace usb_direct {
namespace {

const char kHexDigits[] = "0123456789abcdef";

}  // namespace

std::string JsonQuote(const std::string& value) {
  std::string out;
  out.reserve(value.size() + 2);
  out.push_back('"');
  for (const char raw : value) {
    const unsigned char c = static_cast<unsigned char>(raw);
    switch (c) {
      case '"':
        out += "\\\"";
        break;
      case '\\':
        out += "\\\\";
        break;
      case '\b':
        out += "\\b";
        break;
      case '\f':
        out += "\\f";
        break;
      case '\n':
        out += "\\n";
        break;
      case '\r':
        out += "\\r";
        break;
      case '\t':
        out += "\\t";
        break;
      default:
        if (c < 0x20 || c == 0x7F) {
          out += "\\u00";
          out.push_back(kHexDigits[(c >> 4) & 0x0F]);
          out.push_back(kHexDigits[c & 0x0F]);
        } else {
          out.push_back(raw);
        }
        break;
    }
  }
  out.push_back('"');
  return out;
}

void JsonWriter::Separator() {
  if (after_key_) {
    after_key_ = false;
    return;
  }
  if (empty_.empty()) {
    return;
  }
  if (empty_.back()) {
    empty_.back() = false;
  } else {
    out_.push_back(',');
  }
}

void JsonWriter::BeginObject() {
  Separator();
  out_.push_back('{');
  empty_.push_back(true);
}

void JsonWriter::EndObject() {
  out_.push_back('}');
  if (!empty_.empty()) {
    empty_.pop_back();
  }
}

void JsonWriter::BeginArray() {
  Separator();
  out_.push_back('[');
  empty_.push_back(true);
}

void JsonWriter::EndArray() {
  out_.push_back(']');
  if (!empty_.empty()) {
    empty_.pop_back();
  }
}

void JsonWriter::Key(const std::string& key) {
  Separator();
  out_ += JsonQuote(key);
  out_.push_back(':');
  after_key_ = true;
}

void JsonWriter::String(const std::string& value) {
  Separator();
  out_ += JsonQuote(value);
}

void JsonWriter::Number(int64_t value) {
  Separator();
  char buffer[32];
  const int written = std::snprintf(buffer, sizeof(buffer), "%lld",
                                    static_cast<long long>(value));
  if (written > 0) {
    out_.append(buffer, static_cast<size_t>(written));
  }
}

void JsonWriter::Number(uint64_t value) {
  Separator();
  char buffer[32];
  const int written = std::snprintf(buffer, sizeof(buffer), "%llu",
                                    static_cast<unsigned long long>(value));
  if (written > 0) {
    out_.append(buffer, static_cast<size_t>(written));
  }
}

void JsonWriter::Bool(bool value) {
  Separator();
  out_ += value ? "true" : "false";
}

void JsonWriter::Field(const std::string& key, const std::string& value) {
  Key(key);
  String(value);
}

void JsonWriter::Field(const std::string& key, const char* value) {
  Key(key);
  String(value == nullptr ? std::string() : std::string(value));
}

void JsonWriter::Field(const std::string& key, int64_t value) {
  Key(key);
  Number(value);
}

void JsonWriter::Field(const std::string& key, uint64_t value) {
  Key(key);
  Number(value);
}

void JsonWriter::Field(const std::string& key, bool value) {
  Key(key);
  Bool(value);
}

}  // namespace usb_direct
