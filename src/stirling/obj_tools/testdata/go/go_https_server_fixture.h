/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

#pragma once

#include <algorithm>
#include <array>
#include <filesystem>
#include <string_view>

#include <absl/strings/str_cat.h>

#include "src/common/testing/testing.h"

namespace px {
namespace stirling {
namespace obj_tools {

class GoHTTPSServerFixture {
 public:
  static constexpr std::array<std::string_view, 4> kVersions = {"1_23", "1_24", "1_25", "1_26"};

  static std::filesystem::path Path(std::string_view version) {
    if (IsPrebuilt(version)) {
      return testing::BazelRunfilePath(
          absl::StrCat(kTestDataDir, "golang_", version, "_server_binary"));
    }
    return testing::BazelRunfilePath(
        absl::StrCat(kGoHTTPSServerDir, "golang_", version, "_server_binary"));
  }

  static std::filesystem::path StrippedPath(std::string_view version) {
    return testing::BazelRunfilePath(
        absl::StrCat(kTestDataDir, "golang_", version, "_server_stripped_binary"));
  }

 private:
  static constexpr std::string_view kTestDataDir = "src/stirling/obj_tools/testdata/go/";
  static constexpr std::string_view kGoHTTPSServerDir =
      "src/stirling/testing/demo_apps/go_https/server/";
  static constexpr std::array<std::string_view, 2> kPrebuiltVersions = {"1_25", "1_26"};

  static bool IsPrebuilt(std::string_view version) {
    return std::find(kPrebuiltVersions.begin(), kPrebuiltVersions.end(), version) !=
           kPrebuiltVersions.end();
  }
};

}  // namespace obj_tools
}  // namespace stirling
}  // namespace px
