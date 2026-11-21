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

#include <algorithm>
#include <utility>

#include "src/common/system/proc_parser.h"

namespace px {
namespace stirling {

std::filesystem::path ProcTracker::ExePath(const md::UPID& upid) const {
  return proc_parser_.GetExePath(upid.pid()).ConsumeValueOr({});
}

bool ProcTracker::ExeChanged(const md::UPID& upid) {
  ExeCheck& check = exe_checks_[upid];
  if (update_count_ < check.due) {
    return false;
  }
  std::filesystem::path exe = ExePath(upid);
  const bool changed = exe != check.exe;
  check.exe = std::move(exe);
  check.period = changed ? 1 : std::min(2 * check.period, kMaxExeCheckPeriod);
  check.due = update_count_ + check.period;
  return changed;
}

void ProcTracker::Update(absl::flat_hash_set<md::UPID> upids) {
  ++update_count_;
  new_upids_.clear();
  for (const auto& upid : upids) {
    auto iter = upids_.find(upid);
    if (iter != upids_.end()) {
      upids_.erase(iter);
      if (ExeChanged(upid)) {
        new_upids_.emplace(upid);
      }
      continue;
    }
    new_upids_.emplace(upid);
    exe_checks_[upid] = {ExePath(upid), 1, update_count_ + 1};
  }
  deleted_upids_ = std::move(upids_);
  for (const auto& upid : deleted_upids_) {
    exe_checks_.erase(upid);
  }
  upids_ = std::move(upids);
}

}  // namespace stirling
}  // namespace px
