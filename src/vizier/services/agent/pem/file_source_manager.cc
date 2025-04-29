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
#include "src/vizier/services/agent/pem/file_source_manager.h"

constexpr auto kUpdateInterval = std::chrono::seconds(2);

namespace px {
namespace vizier {
namespace agent {

FileSourceManager::FileSourceManager(px::event::Dispatcher* dispatcher, Info* agent_info,
                                     Manager::VizierNATSConnector* nats_conn,
                                     stirling::Stirling* stirling,
                                     table_store::TableStore* table_store,
                                     RelationInfoManager* relation_info_manager)
    : MessageHandler(dispatcher, agent_info, nats_conn),
      dispatcher_(dispatcher),
      nats_conn_(nats_conn),
      stirling_(stirling),
      table_store_(table_store),
      relation_info_manager_(relation_info_manager) {
  file_source_monitor_timer_ =
      dispatcher_->CreateTimer(std::bind(&FileSourceManager::Monitor, this));
  // Kick off the background monitor.
  file_source_monitor_timer_->EnableTimer(kUpdateInterval);
}

Status FileSourceManager::HandleMessage(std::unique_ptr<messages::VizierMessage> msg) {
  // The main purpose of handle message is to update the local state based on updates
  // from the MDS.
  if (!msg->has_file_source_message()) {
    return error::InvalidArgument("Can only handle file source requests");
  }

  const messages::FileSourceMessage& file_source = msg->file_source_message();
  switch (file_source.msg_case()) {
    case messages::FileSourceMessage::kRegisterFileSourceRequest: {
      return HandleRegisterFileSourceRequest(file_source.register_file_source_request());
    }
    case messages::FileSourceMessage::kRemoveFileSourceRequest: {
      return HandleRemoveFileSourceRequest(file_source.remove_file_source_request());
    }
    default:
      LOG(ERROR) << "Unknown message type: " << file_source.msg_case() << " skipping";
  }
  return Status::OK();
}

std::string FileSourceManager::DebugString() const {
  std::lock_guard<std::mutex> lock(mu_);
  std::stringstream ss;
  auto now = std::chrono::steady_clock::now();
  ss << absl::Substitute("File Source Manager Debug State:\n");
  ss << absl::Substitute("ID\tNAME\tCURRENT_STATE\tEXPECTED_STATE\tlast_updated\n");
  for (const auto& [id, file_source] : file_sources_) {
    ss << absl::Substitute(
        "$0\t$1\t$2\t$3\t$4 seconds\n", id.str(), file_source.name,
        statuspb::LifeCycleState_Name(file_source.current_state),
        statuspb::LifeCycleState_Name(file_source.expected_state),
        std::chrono::duration_cast<std::chrono::seconds>(now - file_source.last_updated_at)
            .count());
  }
  return ss.str();
}

Status FileSourceManager::HandleRegisterFileSourceRequest(
    const messages::RegisterFileSourceRequest& req) {
  auto glob_pattern = req.file_source_deployment().glob_pattern();
  PX_ASSIGN_OR_RETURN(auto id, ParseUUID(req.id()));
  LOG(INFO) << "Registering file source: " << glob_pattern << " uuid string=" << id.str();

  FileSourceInfo info;
  info.name = glob_pattern;
  info.id = id;
  info.expected_state = statuspb::RUNNING_STATE;
  info.current_state = statuspb::PENDING_STATE;
  info.last_updated_at = dispatcher_->GetTimeSource().MonotonicTime();
  stirling_->RegisterFileSource(id, glob_pattern);
  {
    std::lock_guard<std::mutex> lock(mu_);
    file_sources_[id] = std::move(info);
  }
  return Status::OK();
}

Status FileSourceManager::HandleRemoveFileSourceRequest(
    const messages::RemoveFileSourceRequest& req) {
  PX_ASSIGN_OR_RETURN(auto id, ParseUUID(req.id()));
  std::lock_guard<std::mutex> lock(mu_);
  auto it = file_sources_.find(id);
  if (it == file_sources_.end()) {
    return error::NotFound("File source with ID: $0, not found", id.str());
  }

  it->second.expected_state = statuspb::TERMINATED_STATE;
  return stirling_->RemoveFileSource(id);
}

void FileSourceManager::Monitor() {
  std::lock_guard<std::mutex> lock(mu_);

  for (auto& [id, file_source] : file_sources_) {
    auto s_or_publish = stirling_->GetFileSourceInfo(id);
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
          current_state = (file_source.expected_state == statuspb::TERMINATED_STATE)
                              ? statuspb::TERMINATED_STATE
                              : statuspb::UNKNOWN_STATE;
          break;
        default:
          current_state = statuspb::FAILED_STATE;
          break;
      }
    }

    if (current_state != statuspb::RUNNING_STATE &&
        file_source.expected_state == statuspb::TERMINATED_STATE) {
      current_state = statuspb::TERMINATED_STATE;
    }

    if (current_state == file_source.current_state) {
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
    LOG(INFO) << absl::Substitute("File source[$0]::$1 has transitioned $2 -> $3", id.str(),
                                  file_source.name,
                                  statuspb::LifeCycleState_Name(file_source.current_state),
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

    file_source.current_state = current_state;

    // Update MDS with the latest status.
    px::vizier::messages::VizierMessage msg;
    auto file_source_msg = msg.mutable_file_source_message();
    auto update_msg = file_source_msg->mutable_file_source_info_update();
    ToProto(agent_info()->agent_id, update_msg->mutable_agent_id());
    ToProto(id, update_msg->mutable_id());
    update_msg->set_state(file_source.current_state);
    probe_status.ToProto(update_msg->mutable_status());
    VLOG(1) << "Sending file source info update message: " << msg.DebugString();
    auto s = nats_conn_->Publish(msg);
    if (!s.ok()) {
      LOG(ERROR) << "Failed to update nats";
    }
  }
  file_source_monitor_timer_->EnableTimer(kUpdateInterval);
}

Status FileSourceManager::UpdateSchema(const stirling::stirlingpb::Publish& publish_pb) {
  LOG(INFO) << "Updating schema for file source";
  auto relation_info_vec = ConvertPublishPBToRelationInfo(publish_pb);
  // TODO(zasgar): Failure here can lead to an inconsistent schema state. We should
  // figure out how to handle this as part of the data model refactor project.
  for (const auto& relation_info : relation_info_vec) {
    if (!relation_info_manager_->HasRelation(relation_info.name)) {
      table_store_->AddTable(
          table_store::HotColdTable::Create(relation_info.name, relation_info.relation),
          relation_info.name, relation_info.id);
      PX_RETURN_IF_ERROR(relation_info_manager_->AddRelationInfo(relation_info));
    } else {
      if (relation_info.relation != table_store_->GetTable(relation_info.name)->GetRelation()) {
        return error::Internal(
            "File source is not compatible with the schema of the specified output table. "
            "[table_name=$0]",
            relation_info.name);
      }
      PX_RETURN_IF_ERROR(table_store_->AddTableAlias(relation_info.id, relation_info.name));
    }
  }
  return Status::OK();
}

}  // namespace agent
}  // namespace vizier
}  // namespace px
