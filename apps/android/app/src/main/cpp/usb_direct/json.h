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
//
// Minimal JSON writer. There is no parser here on purpose: native code only
// ever produces JSON for the Kotlin layer.

#ifndef USB_DIRECT_JSON_H_
#define USB_DIRECT_JSON_H_

#include <cstdint>
#include <string>
#include <vector>

namespace usb_direct {

class JsonWriter {
 public:
  JsonWriter() = default;

  void BeginObject();
  void EndObject();
  void BeginArray();
  void EndArray();

  // Writes an object key. Must be followed by exactly one value.
  void Key(const std::string& key);

  void String(const std::string& value);
  void Number(int64_t value);
  void Number(uint64_t value);
  void Bool(bool value);

  // Key + value helpers for object members.
  void Field(const std::string& key, const std::string& value);
  void Field(const std::string& key, const char* value);
  void Field(const std::string& key, int64_t value);
  void Field(const std::string& key, uint64_t value);
  void Field(const std::string& key, bool value);

  const std::string& Result() const { return out_; }

 private:
  void Separator();

  std::string out_;
  // One entry per open container: true while it is still empty.
  std::vector<bool> empty_;
  bool after_key_ = false;
};

// Escapes `value` as a JSON string literal, quotes included.
std::string JsonQuote(const std::string& value);

}  // namespace usb_direct

#endif  // USB_DIRECT_JSON_H_
