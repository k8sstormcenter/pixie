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

#include <grpcpp/grpcpp.h>
#include <memory>
#include <string>

#include "src/common/base/base.h"
#include "src/table_store/table_store.h"
#include "src/vizier/services/metadata/metadatapb/service.grpc.pb.h"
#include "src/vizier/services/metadata/metadatapb/service.pb.h"

namespace px {
namespace vizier {
namespace services {
namespace metadata {

/**
 * LocalMetadataServiceImpl implements a local stub for the MetadataService.
 * Only GetSchemas is implemented - it reads from the table store.
 * All other methods return UNIMPLEMENTED status.
 *
 * This is useful for testing and local execution environments where
 * a full metadata service is not available.
 */
class LocalMetadataServiceImpl final : public MetadataService::Service {
 public:
  LocalMetadataServiceImpl() = delete;
  explicit LocalMetadataServiceImpl(table_store::TableStore* table_store)
      : table_store_(table_store) {}

  ::grpc::Status GetSchemas(::grpc::ServerContext*, const SchemaRequest*,
                             SchemaResponse* response) override {
    LOG(INFO) << "GetSchemas called";

    // Get all table IDs from the table store
    auto table_ids = table_store_->GetTableIDs();

    // Build the schema response
    auto* schema = response->mutable_schema();

    for (const auto& table_id : table_ids) {
      // Get the table name
      std::string table_name = table_store_->GetTableName(table_id);
      if (table_name.empty()) {
        LOG(WARNING) << "Failed to get table name for ID: " << table_id;
        continue;
      }

      // Get the table object
      auto* table = table_store_->GetTable(table_id);
      if (table == nullptr) {
        LOG(WARNING) << "Failed to get table for ID: " << table_id;
        continue;
      }

      // Get the relation from the table
      auto relation = table->GetRelation();

      // Add to the relation map in the schema
      // The map value is a Relation proto directly
      auto& rel_proto = (*schema->mutable_relation_map())[table_name];

      // Add columns to the relation
      for (size_t i = 0; i < relation.NumColumns(); ++i) {
        auto* col = rel_proto.add_columns();
        col->set_column_name(relation.GetColumnName(i));
        col->set_column_type(relation.GetColumnType(i));
        col->set_column_desc("");  // No description available from table store
        col->set_pattern_type(types::PatternType::GENERAL);
      }

      // Set table description (empty for now)
      rel_proto.set_desc("");
    }

    return ::grpc::Status::OK;
  }

  ::grpc::Status GetAgentUpdates(::grpc::ServerContext*, const AgentUpdatesRequest*,
                                  ::grpc::ServerWriter<AgentUpdatesResponse>*) override {
    return ::grpc::Status(grpc::StatusCode::UNIMPLEMENTED, "GetAgentUpdates not implemented");
  }

  ::grpc::Status GetAgentInfo(::grpc::ServerContext*, const AgentInfoRequest*,
                               AgentInfoResponse*) override {
    return ::grpc::Status(grpc::StatusCode::UNIMPLEMENTED, "GetAgentInfo not implemented");
  }

  ::grpc::Status GetWithPrefixKey(::grpc::ServerContext*, const WithPrefixKeyRequest*,
                                   WithPrefixKeyResponse*) override {
    return ::grpc::Status(grpc::StatusCode::UNIMPLEMENTED, "GetWithPrefixKey not implemented");
  }

 private:
  table_store::TableStore* table_store_;
};

/**
 * LocalMetadataGRPCServer wraps the LocalMetadataServiceImpl and provides a gRPC server.
 * Uses in-process communication for efficiency.
 */
class LocalMetadataGRPCServer {
 public:
  LocalMetadataGRPCServer() = delete;
  explicit LocalMetadataGRPCServer(table_store::TableStore* table_store)
      : metadata_service_(std::make_unique<LocalMetadataServiceImpl>(table_store)) {
    grpc::ServerBuilder builder;

    // Use in-process communication
    builder.RegisterService(metadata_service_.get());

    grpc_server_ = builder.BuildAndStart();
    CHECK(grpc_server_ != nullptr);

    LOG(INFO) << "Starting Local Metadata service (in-process)";
  }

  void Stop() {
    if (grpc_server_) {
      grpc_server_->Shutdown();
    }
    grpc_server_.reset(nullptr);
  }

  ~LocalMetadataGRPCServer() { Stop(); }

  std::shared_ptr<MetadataService::Stub> StubGenerator() const {
    grpc::ChannelArguments args;
    // NewStub returns unique_ptr, convert to shared_ptr
    return std::shared_ptr<MetadataService::Stub>(
        MetadataService::NewStub(grpc_server_->InProcessChannel(args)));
  }

 private:
  std::unique_ptr<grpc::Server> grpc_server_;
  std::unique_ptr<LocalMetadataServiceImpl> metadata_service_;
};

}  // namespace metadata
}  // namespace services
}  // namespace vizier
}  // namespace px
