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

#include "src/carnot/planner/ir/clickhouse_source_ir.h"
#include "src/carnot/planner/ir/ir.h"

namespace px {
namespace carnot {
namespace planner {

std::string ClickHouseSourceIR::DebugString() const {
  return absl::Substitute("$0(id=$1, table=$2)", type_string(), id(), table_name_);
}

Status ClickHouseSourceIR::ToProto(planpb::Operator* op) const {
  auto pb = op->mutable_clickhouse_source_op();
  op->set_op_type(planpb::CLICKHOUSE_SOURCE_OPERATOR);

  // TODO(ddelnano): Set ClickHouse connection parameters from config
  pb->set_host("localhost");
  pb->set_port(9000);
  pb->set_username("default");
  pb->set_password("test_password");
  pb->set_database("default");

  // Build the query
  pb->set_query(absl::Substitute("SELECT * FROM $0", table_name_));

  if (!column_index_map_set()) {
    return error::InvalidArgument("ClickHouseSource columns are not set.");
  }

  DCHECK(is_type_resolved());
  DCHECK_EQ(column_index_map_.size(), resolved_table_type()->ColumnNames().size());
  for (const auto& [idx, col_name] : Enumerate(resolved_table_type()->ColumnNames())) {
    if (col_name == "upid") {
      LOG(INFO) << "Skipping upid column in ClickHouse source proto.";
      continue;
    }
    pb->add_column_names(col_name);
    auto val_type = std::static_pointer_cast<ValueType>(
        resolved_table_type()->GetColumnType(col_name).ConsumeValueOrDie());
    pb->add_column_types(val_type->data_type());
  }

  if (IsTimeStartSet()) {
    pb->set_start_time(time_start_ns());
  }

  if (IsTimeStopSet()) {
    pb->set_end_time(time_stop_ns());
  }

  // Set batch size
  pb->set_batch_size(1024);

  // Set default timestamp and partition columns (can be configured later)
  pb->set_timestamp_column("time_");
  pb->set_partition_column("hostname");

  return Status::OK();
}

Status ClickHouseSourceIR::Init(const std::string& table_name,
                                const std::vector<std::string>& select_columns) {
  table_name_ = table_name;
  column_names_ = select_columns;
  return Status::OK();
}

StatusOr<absl::flat_hash_set<std::string>> ClickHouseSourceIR::PruneOutputColumnsToImpl(
    const absl::flat_hash_set<std::string>& output_colnames) {
  DCHECK(column_index_map_set());
  DCHECK(is_type_resolved());
  std::vector<std::string> new_col_names;
  std::vector<int64_t> new_col_index_map;

  auto col_names = resolved_table_type()->ColumnNames();
  for (const auto& [idx, name] : Enumerate(col_names)) {
    if (output_colnames.contains(name)) {
      new_col_names.push_back(name);
      new_col_index_map.push_back(column_index_map_[idx]);
    }
  }
  if (new_col_names != resolved_table_type()->ColumnNames()) {
    column_names_ = new_col_names;
  }
  column_index_map_ = new_col_index_map;
  return output_colnames;
}

Status ClickHouseSourceIR::CopyFromNodeImpl(const IRNode* node,
                                            absl::flat_hash_map<const IRNode*, IRNode*>*) {
  const ClickHouseSourceIR* source_ir = static_cast<const ClickHouseSourceIR*>(node);

  table_name_ = source_ir->table_name_;
  time_start_ns_ = source_ir->time_start_ns_;
  time_stop_ns_ = source_ir->time_stop_ns_;
  column_names_ = source_ir->column_names_;
  column_index_map_set_ = source_ir->column_index_map_set_;
  column_index_map_ = source_ir->column_index_map_;

  return Status::OK();
}

Status ClickHouseSourceIR::ResolveType(CompilerState* compiler_state) {
  auto relation_it = compiler_state->relation_map()->find(table_name());
  if (relation_it == compiler_state->relation_map()->end()) {
    return CreateIRNodeError("Table '$0' not found.", table_name_);
  }
  auto table_relation = relation_it->second;
  auto full_table_type = TableType::Create(table_relation);
  if (select_all()) {
    // For select_all, add all table columns plus ClickHouse-added columns (hostname, event_time)
    std::vector<int64_t> column_indices;
    int64_t table_column_count = static_cast<int64_t>(table_relation.NumColumns());

    // Add all table columns
    for (int64_t i = 0; i < table_column_count; ++i) {
      column_indices.push_back(i);
    }

    // Add ClickHouse-added columns
    full_table_type->AddColumn("hostname", ValueType::Create(types::DataType::STRING, types::SemanticType::ST_NONE));
    column_indices.push_back(table_column_count);  // hostname is after all table columns

    full_table_type->AddColumn("event_time", ValueType::Create(types::DataType::TIME64NS, types::SemanticType::ST_TIME_NS));
    column_indices.push_back(table_column_count + 1);  // event_time is after hostname

    SetColumnIndexMap(column_indices);
    return SetResolvedType(full_table_type);
  }

  std::vector<int64_t> column_indices;
  auto new_table = TableType::Create();

  // Calculate the index offset for ClickHouse-added columns (after all table columns)
  int64_t table_column_count = static_cast<int64_t>(table_relation.NumColumns());
  auto next_count = 0;

  for (const auto& col_name : column_names_) {
    // Handle special ClickHouse-added columns that don't exist in the source table
    if (col_name == "hostname") {
      new_table->AddColumn(col_name, ValueType::Create(types::DataType::STRING, types::SemanticType::ST_NONE));
      // hostname is added by ClickHouse after all table columns
      column_indices.push_back(table_column_count + (next_count++));
      continue;
    }
    if (col_name == "event_time") {
      new_table->AddColumn(col_name, ValueType::Create(types::DataType::TIME64NS, types::SemanticType::ST_TIME_NS));
      // event_time is added by ClickHouse after hostname
      column_indices.push_back(table_column_count + (next_count++));
      continue;
    }

    PX_ASSIGN_OR_RETURN(auto col_type, full_table_type->GetColumnType(col_name));
    new_table->AddColumn(col_name, col_type);
    column_indices.push_back(table_relation.GetColumnIndex(col_name));
  }

  SetColumnIndexMap(column_indices);
  return SetResolvedType(new_table);
}

}  // namespace planner
}  // namespace carnot
}  // namespace px
