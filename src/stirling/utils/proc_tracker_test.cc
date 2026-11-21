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

#include "src/stirling/utils/proc_tracker.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <filesystem>

#include "src/common/system/proc_pid_path.h"
#include "src/common/testing/temp_dir.h"
#include "src/common/testing/test_environment.h"

namespace px {
namespace stirling {

using ::testing::IsEmpty;
using ::testing::UnorderedElementsAre;

class ProcTrackerTest : public ::testing::Test {
 protected:
  ProcTracker proc_tracker_;
};

TEST_F(ProcTrackerTest, Basic) {
  using UPIDSet = absl::flat_hash_set<md::UPID>;

  const md::UPID kUPID1 = md::UPID(0, 1, 111);
  const md::UPID kUPID2 = md::UPID(0, 2, 222);
  const md::UPID kUPID3 = md::UPID(0, 3, 333);
  const md::UPID kUPID4 = md::UPID(0, 4, 444);

  proc_tracker_.Update(UPIDSet{kUPID1, kUPID2});
  EXPECT_THAT(proc_tracker_.upids(), UnorderedElementsAre(kUPID1, kUPID2));
  EXPECT_THAT(proc_tracker_.new_upids(), UnorderedElementsAre(kUPID1, kUPID2));
  EXPECT_THAT(proc_tracker_.deleted_upids(), IsEmpty());

  proc_tracker_.Update(UPIDSet{kUPID1, kUPID2, kUPID3});
  EXPECT_THAT(proc_tracker_.upids(), UnorderedElementsAre(kUPID1, kUPID2, kUPID3));
  EXPECT_THAT(proc_tracker_.new_upids(), UnorderedElementsAre(kUPID3));
  EXPECT_THAT(proc_tracker_.deleted_upids(), IsEmpty());

  proc_tracker_.Update(UPIDSet{kUPID1, kUPID3});
  EXPECT_THAT(proc_tracker_.upids(), UnorderedElementsAre(kUPID1, kUPID3));
  EXPECT_THAT(proc_tracker_.new_upids(), IsEmpty());
  EXPECT_THAT(proc_tracker_.deleted_upids(), UnorderedElementsAre(kUPID2));

  proc_tracker_.Update(UPIDSet{kUPID1, kUPID4});
  EXPECT_THAT(proc_tracker_.upids(), UnorderedElementsAre(kUPID1, kUPID4));
  EXPECT_THAT(proc_tracker_.new_upids(), UnorderedElementsAre(kUPID4));
  EXPECT_THAT(proc_tracker_.deleted_upids(), UnorderedElementsAre(kUPID3));
}

TEST_F(ProcTrackerTest, ReportsUPIDAgainWhenExeChanges) {
  using UPIDSet = absl::flat_hash_set<md::UPID>;

  ::px::testing::TempDir proc;
  PX_SET_FOR_SCOPE(FLAGS_proc_path, proc.path().string());
  const std::filesystem::path exe = proc.path() / "123" / "exe";
  std::filesystem::create_directories(exe.parent_path());
  std::filesystem::create_symlink("/runc", exe);

  const md::UPID kUPID = md::UPID(0, 123, 111);

  proc_tracker_.Update(UPIDSet{kUPID});
  EXPECT_THAT(proc_tracker_.new_upids(), UnorderedElementsAre(kUPID));

  proc_tracker_.Update(UPIDSet{kUPID});
  EXPECT_THAT(proc_tracker_.new_upids(), IsEmpty());

  std::filesystem::remove(exe);
  std::filesystem::create_symlink("/app", exe);

  for (int i = 0; i < 512 && proc_tracker_.new_upids().empty(); ++i) {
    proc_tracker_.Update(UPIDSet{kUPID});
  }
  EXPECT_THAT(proc_tracker_.new_upids(), UnorderedElementsAre(kUPID));
  EXPECT_THAT(proc_tracker_.deleted_upids(), IsEmpty());

  proc_tracker_.Update(UPIDSet{kUPID});
  EXPECT_THAT(proc_tracker_.new_upids(), IsEmpty());
}

}  // namespace stirling
}  // namespace px
