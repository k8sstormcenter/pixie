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

#include <memory>
#include <string>

#include <sole.hpp>

#include "src/stirling/stirling.h"
#include "src/vizier/services/agent/shared/manager/manager.h"

namespace px {
namespace vizier {
namespace agent {

struct FileSourceInfo {
  std::string name;
  sole::uuid id;
  statuspb::LifeCycleState expected_state;
  statuspb::LifeCycleState current_state;
  std::chrono::time_point<std::chrono::steady_clock> last_updated_at;
};

class FileSourceManager {
 public:
  FileSourceManager() = delete;
  FileSourceManager(px::event::Dispatcher* dispatcher, stirling::Stirling* stirling,
                    table_store::TableStore* table_store);

  std::string DebugString() const;
  Status HandleRegisterFileSourceRequest(sole::uuid id, std::string file_name);
  Status HandleRemoveFileSourceRequest(sole::uuid id, const messages::FileSourceMessage& req);
  FileSourceInfo* GetFileSourceInfo(std::string name);

 private:
  // The tracepoint Monitor that is responsible for watching and updating the state of
  // active tracepoints.
  void Monitor();
  Status UpdateSchema(const stirling::stirlingpb::Publish& publish_proto);

  px::event::Dispatcher* dispatcher_;
  stirling::Stirling* stirling_;
  table_store::TableStore* table_store_;

  event::TimerUPtr file_source_monitor_timer_;
  mutable std::mutex mu_;
  absl::flat_hash_map<sole::uuid, FileSourceInfo> file_sources_;
  // File source name to UUID.
  absl::flat_hash_map<std::string, sole::uuid> file_source_name_map_;
};

}  // namespace agent
}  // namespace vizier
}  // namespace px
