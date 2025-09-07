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
#include <vector>

#include <clickhouse/client.h>

#include "src/carnot/exec/exec_node.h"
#include "src/carnot/exec/exec_state.h"
#include "src/carnot/plan/operators.h"
#include "src/common/base/base.h"
#include "src/common/base/status.h"
#include "src/shared/types/types.h"
#include "src/table_store/schema/row_batch.h"

namespace px {
namespace carnot {
namespace exec {

using table_store::schema::RowBatch;
using table_store::schema::RowDescriptor;

class ClickHouseSourceNode : public SourceNode {
 public:
  ClickHouseSourceNode() = default;
  virtual ~ClickHouseSourceNode() = default;

  bool NextBatchReady() override;

 protected:
  std::string DebugStringImpl() override;
  Status InitImpl(const plan::Operator& plan_node) override;
  Status PrepareImpl(ExecState* exec_state) override;
  Status OpenImpl(ExecState* exec_state) override;
  Status CloseImpl(ExecState* exec_state) override;
  Status GenerateNextImpl(ExecState* exec_state) override;

 private:
  // Convert ClickHouse column types to Pixie data types
  StatusOr<types::DataType> ClickHouseTypeToPixieType(const clickhouse::TypeRef& ch_type);
  
  // Convert ClickHouse block to Pixie RowBatch
  StatusOr<std::unique_ptr<RowBatch>> ConvertClickHouseBlockToRowBatch(
      const clickhouse::Block& block, bool is_last_block);
  
  // Execute the query and fetch results
  Status ExecuteQuery();

  // Connection information
  std::string host_;
  int port_;
  std::string username_;
  std::string password_;
  std::string database_;
  std::string query_;
  
  // Batch size configuration
  size_t batch_size_ = 1024;
  
  // ClickHouse client
  std::unique_ptr<clickhouse::Client> client_;
  
  // Query results
  std::vector<clickhouse::Block> result_blocks_;
  size_t current_block_index_ = 0;
  bool query_executed_ = false;
  
  // Streaming support (future enhancement)
  bool streaming_ = false;
  
  // Plan node
  std::unique_ptr<plan::ClickHouseSourceOperator> plan_node_;
};

}  // namespace exec
}  // namespace carnot
}  // namespace px