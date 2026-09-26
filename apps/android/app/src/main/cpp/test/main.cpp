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

#include <iostream>

#include "check.h"

void RunDescriptorTests();
void RunFormatChoiceTests();
void RunPacketSizingTests();
void RunRingBufferTests();

int main() {
  RunDescriptorTests();
  RunFormatChoiceTests();
  RunPacketSizingTests();
  RunRingBufferTests();

  std::cout << "usb_direct host tests: " << check::g_checks << " checks, "
            << check::g_failures << " failures" << std::endl;
  if (check::g_failures != 0) {
    std::cout << "FAILED" << std::endl;
    return 1;
  }
  std::cout << "PASSED" << std::endl;
  return 0;
}
