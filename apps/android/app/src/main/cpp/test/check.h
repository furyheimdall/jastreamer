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
// Dependency-free check macros for the usb_direct host tests.

#ifndef USB_DIRECT_TEST_CHECK_H_
#define USB_DIRECT_TEST_CHECK_H_

#include <iostream>
#include <sstream>
#include <string>
#include <type_traits>

namespace check {

inline int g_checks = 0;
inline int g_failures = 0;

template <typename T>
std::string ToText(const T& value) {
  std::ostringstream stream;
  if constexpr (std::is_same_v<T, bool>) {
    stream << (value ? "true" : "false");
  } else if constexpr (std::is_enum_v<T>) {
    stream << static_cast<long long>(value);
  } else if constexpr (std::is_integral_v<T>) {
    if constexpr (std::is_unsigned_v<T>) {
      stream << static_cast<unsigned long long>(value);
    } else {
      stream << static_cast<long long>(value);
    }
  } else {
    stream << value;
  }
  return stream.str();
}

template <typename A, typename B>
bool ValuesEqual(const A& a, const B& b) {
  if constexpr ((std::is_integral_v<A> || std::is_enum_v<A>) &&
                (std::is_integral_v<B> || std::is_enum_v<B>)) {
    return static_cast<long long>(a) == static_cast<long long>(b);
  } else {
    return a == b;
  }
}

inline void Report(const char* file, int line, const char* expression, bool ok,
                   const std::string& actual, const std::string& expected) {
  ++g_checks;
  if (ok) {
    return;
  }
  ++g_failures;
  std::cout << "FAIL " << file << ":" << line << ": " << expression
            << "\n  actual:   " << actual << "\n  expected: " << expected
            << std::endl;
}

// The checked expressions are evaluated exactly once: several of them mutate
// the object under test.
template <typename A, typename B>
void CheckEqual(const char* file, int line, const char* expression,
                const A& actual, const B& expected) {
  Report(file, line, expression, ValuesEqual(actual, expected), ToText(actual),
         ToText(expected));
}

inline void CheckTrue(const char* file, int line, const char* expression,
                      bool value) {
  Report(file, line, expression, value, ToText(value), std::string("true"));
}

}  // namespace check

#define CHECK_EQ(actual, expected)                                  \
  ::check::CheckEqual(__FILE__, __LINE__, #actual " == " #expected, \
                      (actual), (expected))

#define CHECK_TRUE(expression)                                        \
  ::check::CheckTrue(__FILE__, __LINE__, #expression " is true",      \
                     static_cast<bool>(expression))

#endif  // USB_DIRECT_TEST_CHECK_H_
