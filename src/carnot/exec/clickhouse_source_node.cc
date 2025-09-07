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

#include "src/carnot/exec/clickhouse_source_node.h"

#include <arrow/array.h>
#include <arrow/builder.h>

#include <absl/strings/substitute.h>

#include "src/carnot/planpb/plan.pb.h"
#include "src/common/base/base.h"
#include "src/shared/types/arrow_adapter.h"
#include "src/shared/types/types.h"

namespace px {
namespace carnot {
namespace exec {

std::string ClickHouseSourceNode::DebugStringImpl() {
  return absl::Substitute("Exec::ClickHouseSourceNode: <query: $0, output: $1>", 
                          query_, output_descriptor_->DebugString());
}

Status ClickHouseSourceNode::InitImpl(const plan::Operator& plan_node) {
  CHECK(plan_node.op_type() == planpb::OperatorType::CLICKHOUSE_SOURCE_OPERATOR);
  const auto* source_plan_node = static_cast<const plan::ClickHouseSourceOperator*>(&plan_node);
  
  // Copy the plan node to local object
  plan_node_ = std::make_unique<plan::ClickHouseSourceOperator>(*source_plan_node);
  
  // Extract connection parameters from plan node
  host_ = plan_node_->host();
  port_ = plan_node_->port();
  username_ = plan_node_->username();
  password_ = plan_node_->password();
  database_ = plan_node_->database();
  query_ = plan_node_->query();
  batch_size_ = plan_node_->batch_size();
  streaming_ = plan_node_->streaming();
  
  return Status::OK();
}

Status ClickHouseSourceNode::PrepareImpl(ExecState*) { 
  return Status::OK(); 
}

Status ClickHouseSourceNode::OpenImpl(ExecState*) {
  // Create ClickHouse client
  clickhouse::ClientOptions options;
  options.SetHost(host_);
  options.SetPort(port_);
  options.SetUser(username_);
  options.SetPassword(password_);
  options.SetDefaultDatabase(database_);
  
  try {
    client_ = std::make_unique<clickhouse::Client>(options);
  } catch (const std::exception& e) {
    return error::Internal("Failed to create ClickHouse client: $0", e.what());
  }
  
  return Status::OK();
}

Status ClickHouseSourceNode::CloseImpl(ExecState*) {
  client_.reset();
  result_blocks_.clear();
  return Status::OK();
}

StatusOr<types::DataType> ClickHouseSourceNode::ClickHouseTypeToPixieType(
    const clickhouse::TypeRef& ch_type) {
  const auto& type_name = ch_type->GetName();
  
  // Integer types - Pixie only supports INT64
  if (type_name == "UInt8" || type_name == "UInt16" || type_name == "UInt32" || 
      type_name == "UInt64" || type_name == "Int8" || type_name == "Int16" || 
      type_name == "Int32" || type_name == "Int64") {
    return types::DataType::INT64;
  }
  
  // UInt128
  if (type_name == "UInt128") {
    return types::DataType::UINT128;
  }
  
  // Floating point types - Pixie only supports FLOAT64
  if (type_name == "Float32" || type_name == "Float64") {
    return types::DataType::FLOAT64;
  }
  
  // String types
  if (type_name == "String" || type_name == "FixedString") {
    return types::DataType::STRING;
  }
  
  // Date/time types
  if (type_name == "DateTime" || type_name == "DateTime64") {
    return types::DataType::TIME64NS;
  }
  
  // Boolean
  if (type_name == "Bool") {
    return types::DataType::BOOLEAN;
  }
  
  return error::InvalidArgument("Unsupported ClickHouse type: $0", type_name);
}

StatusOr<std::unique_ptr<RowBatch>> ClickHouseSourceNode::ConvertClickHouseBlockToRowBatch(
    const clickhouse::Block& block, bool /*is_last_block*/) {
  auto num_rows = block.GetRowCount();
  auto num_cols = block.GetColumnCount();
  
  // Create output row descriptor if this is the first block
  if (current_block_index_ == 0) {
    std::vector<types::DataType> col_types;
    for (size_t i = 0; i < num_cols; ++i) {
      PX_ASSIGN_OR_RETURN(auto pixie_type, 
                          ClickHouseTypeToPixieType(block[i]->Type()));
      col_types.push_back(pixie_type);
    }
    // Note: In a real implementation, we would get column names from the plan
    // or from ClickHouse metadata
  }
  
  auto row_batch = std::make_unique<RowBatch>(*output_descriptor_, num_rows);
  
  // Convert each column
  for (size_t col_idx = 0; col_idx < num_cols; ++col_idx) {
    const auto& ch_column = block[col_idx];
    const auto& type_name = ch_column->Type()->GetName();
    
    // For now, implement conversion for common types
    // This is where column type inference happens
    
    // Integer types - all map to INT64 in Pixie
    if (type_name == "UInt8") {
      auto typed_col = ch_column->As<clickhouse::ColumnUInt8>();
      arrow::Int64Builder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(static_cast<int64_t>(typed_col->At(i)));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "UInt16") {
      auto typed_col = ch_column->As<clickhouse::ColumnUInt16>();
      arrow::Int64Builder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(static_cast<int64_t>(typed_col->At(i)));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "UInt32") {
      auto typed_col = ch_column->As<clickhouse::ColumnUInt32>();
      arrow::Int64Builder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(static_cast<int64_t>(typed_col->At(i)));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "UInt64") {
      auto typed_col = ch_column->As<clickhouse::ColumnUInt64>();
      arrow::Int64Builder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(static_cast<int64_t>(typed_col->At(i)));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "Int8") {
      auto typed_col = ch_column->As<clickhouse::ColumnInt8>();
      arrow::Int64Builder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(static_cast<int64_t>(typed_col->At(i)));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "Int16") {
      auto typed_col = ch_column->As<clickhouse::ColumnInt16>();
      arrow::Int64Builder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(static_cast<int64_t>(typed_col->At(i)));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "Int32") {
      auto typed_col = ch_column->As<clickhouse::ColumnInt32>();
      arrow::Int64Builder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(static_cast<int64_t>(typed_col->At(i)));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "Int64") {
      auto typed_col = ch_column->As<clickhouse::ColumnInt64>();
      arrow::Int64Builder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(typed_col->At(i));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "String") {
      auto typed_col = ch_column->As<clickhouse::ColumnString>();
      arrow::StringBuilder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      
      for (size_t i = 0; i < num_rows; ++i) {
        // Convert string_view to string
        std::string value(typed_col->At(i));
        PX_RETURN_IF_ERROR(builder.Append(value));
      }
      
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
      
    } else if (type_name == "Float32") {
      auto typed_col = ch_column->As<clickhouse::ColumnFloat32>();
      arrow::DoubleBuilder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(static_cast<double>(typed_col->At(i)));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "Float64") {
      auto typed_col = ch_column->As<clickhouse::ColumnFloat64>();
      arrow::DoubleBuilder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(typed_col->At(i));
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "Bool") {
      auto typed_col = ch_column->As<clickhouse::ColumnUInt8>();
      arrow::BooleanBuilder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      for (size_t i = 0; i < num_rows; ++i) {
        builder.UnsafeAppend(typed_col->At(i) != 0);
      }
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
    } else if (type_name == "DateTime") {
      auto typed_col = ch_column->As<clickhouse::ColumnDateTime>();
      arrow::Int64Builder builder;
      PX_RETURN_IF_ERROR(builder.Reserve(num_rows));
      
      for (size_t i = 0; i < num_rows; ++i) {
        // Convert DateTime (seconds since epoch) to nanoseconds
        int64_t ns = static_cast<int64_t>(typed_col->At(i)) * 1000000000LL;
        builder.UnsafeAppend(ns);
      }
      
      std::shared_ptr<arrow::Array> array;
      PX_RETURN_IF_ERROR(builder.Finish(&array));
      PX_RETURN_IF_ERROR(row_batch->AddColumn(array));
      
    } else {
      return error::InvalidArgument("Unsupported ClickHouse type for conversion: $0", type_name);
    }
  }
  
  // Set end-of-window and end-of-stream flags
  // Don't set them here - they should be set in GenerateNextImpl
  row_batch->set_eow(false);
  row_batch->set_eos(false);
  
  return row_batch;
}

Status ClickHouseSourceNode::ExecuteQuery() {
  if (query_executed_) {
    return Status::OK();
  }
  
  try {
    client_->Select(query_, 
      [this](const clickhouse::Block& block) {
        // Only store non-empty blocks
        if (block.GetRowCount() > 0) {
          result_blocks_.push_back(block);
        }
      }
    );
    query_executed_ = true;
  } catch (const std::exception& e) {
    return error::Internal("Failed to execute ClickHouse query: $0", e.what());
  }
  
  return Status::OK();
}

Status ClickHouseSourceNode::GenerateNextImpl(ExecState* exec_state) {
  // Execute query if not done yet
  PX_RETURN_IF_ERROR(ExecuteQuery());
  
  // Check if we have more blocks to process
  if (current_block_index_ >= result_blocks_.size()) {
    // Send empty batch with eos=true
    PX_ASSIGN_OR_RETURN(auto empty_batch, RowBatch::WithZeroRows(*output_descriptor_, true, true));
    PX_RETURN_IF_ERROR(SendRowBatchToChildren(exec_state, *empty_batch));
    return Status::OK();
  }
  
  // Convert current block to row batch
  bool is_last_block = (current_block_index_ == result_blocks_.size() - 1);
  PX_ASSIGN_OR_RETURN(auto row_batch, 
                      ConvertClickHouseBlockToRowBatch(result_blocks_[current_block_index_], 
                                                      is_last_block));
  
  // Update stats
  rows_processed_ += row_batch->num_rows();
  bytes_processed_ += row_batch->NumBytes();
  
  // Send to children
  PX_RETURN_IF_ERROR(SendRowBatchToChildren(exec_state, *row_batch));
  
  current_block_index_++;
  
  return Status::OK();
}

bool ClickHouseSourceNode::NextBatchReady() {
  // For now, we execute the entire query at once
  // In the future, we could support streaming
  return HasBatchesRemaining();
}

}  // namespace exec
}  // namespace carnot
}  // namespace px