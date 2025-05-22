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

#include <string>
#include <utility>

#include "src/common/base/base.h"
#include "src/experimental/standalone_pem/tetragon_manager.h"

constexpr auto kUpdateInterval = std::chrono::seconds(2);

namespace px {
namespace vizier {
namespace agent {

TetragonManager::TetragonManager(px::event::Dispatcher* dispatcher,
                                     stirling::Stirling* stirling,
                                     table_store::TableStore* table_store)
    : dispatcher_(dispatcher), stirling_(stirling), table_store_(table_store) {
  tetragon_monitor_timer_ =
      dispatcher_->CreateTimer(std::bind(&TetragonManager::Monitor, this));
  // Kick off the background monitor.
  tetragon_monitor_timer_->EnableTimer(kUpdateInterval);
}

std::string TetragonManager::DebugString() const {
  std::lock_guard<std::mutex> lock(mu_);
  std::stringstream ss;
  auto now = std::chrono::steady_clock::now();
  ss << absl::Substitute("Tetragon Manager Debug State:\n");
  ss << absl::Substitute("ID\tNAME\tCURRENT_STATE\tEXPECTED_STATE\tlast_updated\n");
  for (const auto& [id, tetragon] : tetragons_) {
    ss << absl::Substitute(
        "$0\t$1\t$2\t$3\t$4 seconds\n", id.str(), tetragon.name,
        statuspb::LifeCycleState_Name(tetragon.current_state),
        statuspb::LifeCycleState_Name(tetragon.expected_state),
        std::chrono::duration_cast<std::chrono::seconds>(now - tetragon.last_updated_at)
            .count());
  }
  return ss.str();
}

Status TetragonManager::HandleRegisterTetragonRequest(sole::uuid id, std::string file_name) {
  LOG(INFO) << "Registering tetragon: " << file_name;

  TetragonInfo info;
  info.name = file_name;
  info.id = id;
  info.expected_state = statuspb::RUNNING_STATE;
  info.current_state = statuspb::PENDING_STATE;
  info.last_updated_at = dispatcher_->GetTimeSource().MonotonicTime();
  stirling_->RegisterTetragon(id, file_name);
  {
    std::lock_guard<std::mutex> lock(mu_);
    tetragons_[id] = std::move(info);
    tetragon_name_map_[file_name] = id;
  }
  return Status::OK();
}

Status TetragonManager::HandleRemoveTetragonRequest(
    sole::uuid id, const messages::TetragonMessage& /*msg*/) {
  std::lock_guard<std::mutex> lock(mu_);
  auto it = tetragons_.find(id);
  if (it == tetragons_.end()) {
    return error::NotFound("Tetragon with ID: $0, not found", id.str());
  }

  it->second.expected_state = statuspb::TERMINATED_STATE;
  return stirling_->RemoveTetragon(id);
}

void TetragonManager::Monitor() {
  std::lock_guard<std::mutex> lock(mu_);

  for (auto& [id, tetragon] : tetragons_) {
    auto s_or_publish = stirling_->GetTetragonInfo(id);
    statuspb::LifeCycleState current_state;
    // Get the latest current state according to stirling.
    if (s_or_publish.ok()) {
      current_state = statuspb::RUNNING_STATE;
    } else {
      switch (s_or_publish.code()) {
        case statuspb::FAILED_PRECONDITION:
          // Means the binary has not been found.
          current_state = statuspb::FAILED_STATE;
          break;
        case statuspb::RESOURCE_UNAVAILABLE:
          current_state = statuspb::PENDING_STATE;
          break;
        case statuspb::NOT_FOUND:
          // Means we didn't actually find the probe. If we requested termination,
          // it's because the probe has been removed.
          current_state = (tetragon.expected_state == statuspb::TERMINATED_STATE)
                              ? statuspb::TERMINATED_STATE
                              : statuspb::UNKNOWN_STATE;
          break;
        default:
          current_state = statuspb::FAILED_STATE;
          break;
      }
    }

    if (current_state != statuspb::RUNNING_STATE &&
        tetragon.expected_state == statuspb::TERMINATED_STATE) {
      current_state = statuspb::TERMINATED_STATE;
    }

    if (current_state == tetragon.current_state) {
      // No state transition, nothing to do.
      continue;
    }

    // The following transitions are legal:
    // 1. Pending -> Terminated: Probe is stopped before starting.
    // 2. Pending -> Running : Probe starts up.
    // 3. Running -> Terminated: Probe is stopped.
    // 4. Running -> Failed: Probe got dettached because binary died.
    // 5. Failed -> Running: Probe started up because binary came back to life.
    //
    // In all cases we basically inform the MDS.
    // In the cases where we transition to running, we need to update the schemas.

    Status probe_status = Status::OK();
    LOG(INFO) << absl::Substitute("Tetragon[$0]::$1 has transitioned $2 -> $3", id.str(),
                                  tetragon.name,
                                  statuspb::LifeCycleState_Name(tetragon.current_state),
                                  statuspb::LifeCycleState_Name(current_state));
    // Check if running now, then update the schema.
    if (current_state == statuspb::RUNNING_STATE) {
      // We must have just transitioned into running. We try to apply the new schema.
      // If it fails we will trigger an error and report that to MDS.
      auto publish_pb = s_or_publish.ConsumeValueOrDie();
      auto s = UpdateSchema(publish_pb);
      if (!s.ok()) {
        current_state = statuspb::FAILED_STATE;
        probe_status = s;
      }
    } else {
      probe_status = s_or_publish.status();
    }

    tetragon.current_state = current_state;
  }
  tetragon_monitor_timer_->EnableTimer(kUpdateInterval);
}

Status TetragonManager::UpdateSchema(const stirling::stirlingpb::Publish& publish_pb) {
  LOG(INFO) << "Updating schema for tetragon";
  auto relation_info_vec = ConvertPublishPBToRelationInfo(publish_pb);

  // TODO(ddelnano): Failure here can lead to an inconsistent schema state. We should
  // figure out how to handle this as part of the data model refactor project.
  for (const auto& relation_info : relation_info_vec) {
    LOG(INFO) << absl::Substitute("Adding table: $0", relation_info.name);
    table_store_->AddTable(
        table_store::HotOnlyTable::Create(relation_info.name, relation_info.relation),
        relation_info.name, relation_info.id);
  }
  return Status::OK();
}

TetragonInfo* TetragonManager::GetTetragonInfo(std::string name) {
  std::lock_guard<std::mutex> lock(mu_);
  auto pair = tetragon_name_map_.find(name);
  if (pair == tetragon_name_map_.end()) {
    return nullptr;
  }

  auto id_pair = tetragons_.find(pair->second);
  if (id_pair == tetragons_.end()) {
    return nullptr;
  }

  return &id_pair->second;
}

}  // namespace agent
}  // namespace vizier
}  // namespace px
