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

#include <filesystem>
#include <fstream>

#include "src/common/testing/temp_dir.h"
#include "src/common/testing/testing.h"
#include "src/stirling/source_connectors/socket_tracer/uprobe_manager.h"

namespace px {
namespace stirling {

TEST(ProbedBinaryKeyTest, SameFileThroughProcRootHasOneKey) {
  ::px::testing::TempDir tmp_dir;
  const std::filesystem::path binary = tmp_dir.path() / "binary";
  std::ofstream(binary) << "binary";

  const std::filesystem::path proc_root_binary =
      std::filesystem::path("/proc/self/root") / binary.relative_path();

  EXPECT_EQ(ProbedBinaryKey(binary), ProbedBinaryKey(proc_root_binary));
}

TEST(ProbedBinaryKeyTest, DifferentFilesHaveDifferentKeys) {
  ::px::testing::TempDir tmp_dir;
  const std::filesystem::path binary1 = tmp_dir.path() / "binary1";
  const std::filesystem::path binary2 = tmp_dir.path() / "binary2";
  std::ofstream(binary1) << "binary";
  std::ofstream(binary2) << "binary";

  EXPECT_NE(ProbedBinaryKey(binary1), ProbedBinaryKey(binary2));
}

}  // namespace stirling
}  // namespace px
