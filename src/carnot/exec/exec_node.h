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

#include <map>
#include <memory>
#include <string>
#include <vector>

#include "src/carnot/exec/exec_state.h"
#include "src/carnot/plan/operators.h"
#include "src/common/base/base.h"
#include "src/common/perf/perf.h"
#include "src/table_store/table_store.h"

namespace px::carnot::exec {
// Forward declaration so enum_range can be specialized.
enum class SinkResultsDestType : uint64_t;

}  // namespace px::carot::exec

template <>
struct magic_enum::customize::enum_range<px::carnot::exec::SinkResultsDestType> {
  static constexpr int min = 1000;
  static constexpr int max = 11000;
};

namespace px {
namespace carnot {
namespace exec {

enum class ExecNodeType : int8_t {
  kSourceNode = 0,
  kSinkNode = 1,
  kProcessingNode = 2,
};

struct ExecNodeStats {
  explicit ExecNodeStats(bool collect_stats) : collect_exec_stats(collect_stats) {}
  void AddOutputStats(const table_store::schema::RowBatch& rb) {
    if (!collect_exec_stats) {
      return;
    }
    ++batches_output;
    bytes_output += rb.NumBytes();
    rows_output += rb.num_rows();
  }

  void AddInputStats(const table_store::schema::RowBatch& rb) {
    if (!collect_exec_stats) {
      return;
    }
    ++batches_input;
    bytes_input += rb.NumBytes();
    rows_input += rb.num_rows();
  }

  void ResumeChildTimer() {
    if (!collect_exec_stats) {
      return;
    }
    children_timer.Resume();
  }
  void StopChildTimer() {
    if (!collect_exec_stats) {
      return;
    }
    children_timer.Stop();
  }
  void ResumeTotalTimer() {
    if (!collect_exec_stats) {
      return;
    }
    total_timer.Resume();
  }
  void StopTotalTimer() {
    if (!collect_exec_stats) {
      return;
    }
    total_timer.Stop();
  }

  void AddExtraMetric(std::string_view key, double value) {
    if (!collect_exec_stats) {
      return;
    }

    extra_metrics[key] = value;
  }

  void AddExtraInfo(std::string_view key, std::string_view value) {
    if (!collect_exec_stats) {
      return;
    }

    extra_info[key] = value;
  }

  int64_t ChildExecTime() const { return children_timer.ElapsedTime_us() * 1000; }
  int64_t TotalExecTime() const { return total_timer.ElapsedTime_us() * 1000; }
  int64_t SelfExecTime() const { return TotalExecTime() - ChildExecTime(); }

  // Total bytes input to this exec node.
  int64_t bytes_input = 0;
  // Total rows input to this exec node.
  int64_t rows_input = 0;
  // Total batches input to this exec node.
  int64_t batches_input = 0;
  // Total bytes output by this exec node.
  int64_t bytes_output = 0;
  // Total rows output by this exec node.
  int64_t rows_output = 0;
  // Total batches input to this exec node.
  int64_t batches_output = 0;
  // Total timer for the node = children_time + self_time.
  ElapsedTimer total_timer;
  // Total timer for the children of the ndoe.
  ElapsedTimer children_timer;
  // Flag to determine whether to collect stats or not.
  bool collect_exec_stats;

  // Extra metrics to store.
  absl::flat_hash_map<std::string, double> extra_metrics;
  absl::flat_hash_map<std::string, std::string> extra_info;
};

enum class SinkResultsDestType : uint64_t {
  amqp_events = 10001, // TODO(ddelnano): This is set to not collide with the planpb::OperatorType enum
  cql_events,
  dns_events,
  http_events,
  kafka_events, // Won't work since table is suffixed with ".beta"
  mongodb_events,
  mux_events,
  mysql_events,
  nats_events, // Won't work since table is suffixed with ".beta"
  pgsql_events,
  redis_events,
};

/**
 * This is the base class for the execution nodes in Carnot.
 */
class ExecNode {
  const std::string kContextKey = "mutation_id";
  const std::string kSinkResultsTableName = "sink_results";
  const std::vector<std::string> sink_results_col_names = {"time_", "upid", "bytes_transferred",
                                                           "destination", "stream_id"};

 public:
  ExecNode() = delete;
  virtual ~ExecNode() = default;

  /**
   * Init is called with plan & schema information.
   * @param plan_node the plan class of the node.
   * @param output_descriptor The output column schema of row batches.
   * @param input_descriptors The input column schema of row batches.
   * @return
   */
  Status Init(const plan::Operator& plan_node,
                      const table_store::schema::RowDescriptor& output_descriptor,
                      std::vector<table_store::schema::RowDescriptor> input_descriptors,
                      bool collect_exec_stats = false) {
    auto op_type = plan_node.op_type();
    // TODO(ddelnano): Replace this with a template based compile time check
    // to ensure that there can't be segfaults on the subsequent static_casts
    if (op_type == planpb::MEMORY_SOURCE_OPERATOR || op_type == planpb::GRPC_SINK_OPERATOR ||
        op_type == planpb::MEMORY_SINK_OPERATOR || op_type == planpb::OTEL_EXPORT_SINK_OPERATOR) {
      const auto* sink_op = static_cast<const plan::SinkOperator*>(&plan_node);
      context_ = sink_op->context();
      auto op_type = plan_node.op_type();
      destination_ = static_cast<uint64_t>(op_type);
      if (op_type == planpb::MEMORY_SOURCE_OPERATOR) {
        const auto* memory_source_op = static_cast<const plan::MemorySourceOperator*>(&plan_node);
        auto table_name = memory_source_op->TableName();
        auto protocol_events = magic_enum::enum_cast<SinkResultsDestType>(table_name);
        if (protocol_events.has_value()) {
          destination_ = static_cast<uint64_t>(protocol_events.value());
        }
      }
    }
    is_initialized_ = true;
    output_descriptor_ = std::make_unique<table_store::schema::RowDescriptor>(output_descriptor);
    input_descriptors_ = input_descriptors;
    stats_ = std::make_unique<ExecNodeStats>(collect_exec_stats);
    return InitImpl(plan_node);
  }

  /**
   * Setup internal data structures, perform validation, etc.
   * @param exec_state The execution state.
   * @return The status of the prepare.
   */
  Status Prepare(ExecState* exec_state) {
    DCHECK(is_initialized_);
    if (context_.find(kContextKey) != context_.end()) {
      SetUpStreamResultsTable(exec_state);
    }
    return PrepareImpl(exec_state);
  }

  /**
   * Acquire memory resources, etc.
   * @param exec_state The execution state.
   * @return
   */
  Status Open(ExecState* exec_state) {
    DCHECK(is_initialized_);
    return OpenImpl(exec_state);
  }

  /**
   * Close is where cleanup should take place. This includes cleaning up objects.
   * It is highly recomended that a default destructor be used and cleanup peformed here,
   * since at the end a query the data is batch deleted and ordering is not guaranteed.
   * @param exec_state The execution state.
   * @return The status of the Finalize.axs
   */
  Status Close(ExecState* exec_state) {
    DCHECK(is_initialized_);
    return CloseImpl(exec_state);
  }

  /**
   * GenerateNext is called to produce the next row batch. This is only valid
   * on source nodes (and will result in an error on other nodes).
   * @param exec_state The execution state.
   * @return The status of the execution.
   */
  Status GenerateNext(ExecState* exec_state) {
    DCHECK(is_initialized_);
    DCHECK(type() == ExecNodeType::kSourceNode);
    stats_->ResumeTotalTimer();
    PX_RETURN_IF_ERROR(GenerateNextImpl(exec_state));
    stats_->StopTotalTimer();
    return Status::OK();
  }

  /**
   * Consume the next row batch. This function is only valid for Sink and Processing
   * Nodes.
   *
   * This needs to be careful to forward the output batch to all children.
   *
   * @param exec_state The execution state.
   * @param rb The input row batch.
   * @return The Status of consumption.
   */
  Status ConsumeNext(ExecState* exec_state, const table_store::schema::RowBatch& rb,
                             size_t parent_index) {
    DCHECK(is_initialized_);
    DCHECK(type() == ExecNodeType::kSinkNode || type() == ExecNodeType::kProcessingNode);
    if (rb.eos() && !rb.eow()) {
      return error::Internal(
          "ConsumeNext received row batch with end of stream set but not end of window.");
    }
    stats_->AddInputStats(rb);
    stats_->ResumeTotalTimer();
    PX_RETURN_IF_ERROR(ConsumeNextImpl(exec_state, rb, parent_index));
    stats_->StopTotalTimer();
    PX_RETURN_IF_ERROR(
        RecordSinkResults(rb, exec_state->time_now(), exec_state->GetAgentUPID().value()));
    return Status::OK();
  }

  /**
   * Check if it's a source node.
   */
  bool IsSource() { return type() == ExecNodeType::kSourceNode; }

  /**
   * Check if it's a sink node.
   */
  bool IsSink() { return type() == ExecNodeType::kSinkNode; }

  /**
   * Check if it's a processing node.
   */
  bool IsProcessing() { return type() == ExecNodeType::kProcessingNode; }

  /**
   * Get a debug string for the node.
   * @return the debug string/
   */
  std::string DebugString() { return DebugStringImpl(); }

  /**
   * Add a new child node where data is forwarded.
   * This node will not own the child. The lifetime of the child should
   * exceed the lifetime of this node.
   * The node also needs to know which parent index it is for its child.
   * A node that is the 2nd parent of a child needs to pass that information
   * down when it sends that child row batches so the child can differentiate
   * between the row batches of its various parents.
   *
   * @param child Another execution node.
   */
  void AddChild(ExecNode* child, size_t parent_index) {
    children_.emplace_back(child);
    parent_ids_for_children_.emplace_back(parent_index);
  }

  /**
   * Get the type of the execution node.
   * @return the ExecNodeType.
   */
  ExecNodeType type() { return type_; }

  /**
   * @ return the children of the execution node.
   */
  std::vector<ExecNode*> children() { return children_; }

  ExecNodeStats* stats() const { return stats_.get(); }

 protected:
  /**
   * Send data to children row batches.
   * @param exec_state The exec state.
   * @param rb The row batch to send.
   * @return Status of children execution.
   */
  Status SendRowBatchToChildren(ExecState* exec_state,
                                        const table_store::schema::RowBatch& rb) {
    stats_->ResumeChildTimer();
    for (size_t i = 0; i < children_.size(); ++i) {
      PX_RETURN_IF_ERROR(children_[i]->ConsumeNext(exec_state, rb, parent_ids_for_children_[i]));
    }
    stats_->StopChildTimer();
    stats_->AddOutputStats(rb);
    if (rb.eos()) {
      DCHECK(!sent_eos_);
      sent_eos_ = true;
    }
    PX_RETURN_IF_ERROR(
        RecordSinkResults(rb, exec_state->time_now(), exec_state->GetAgentUPID().value()));
    return Status::OK();
  }

  explicit ExecNode(ExecNodeType type)
      : type_(type),
        rel_({types::DataType::TIME64NS, types::DataType::UINT128, types::DataType::INT64,
              types::DataType::INT64, types::DataType::STRING},
             sink_results_col_names) {}

  // Defines the protected implementations of the non-virtual interface functions
  // defined above.
  virtual std::string DebugStringImpl() = 0;
  virtual Status InitImpl(const plan::Operator& plan_node) = 0;
  virtual Status PrepareImpl(ExecState* exec_state) = 0;
  virtual Status OpenImpl(ExecState* exec_state) = 0;
  virtual Status CloseImpl(ExecState* exec_state) = 0;

  virtual Status GenerateNextImpl(ExecState*) {
    return error::Unimplemented("Implement in derived class (if source)");
  }

  virtual Status ConsumeNextImpl(ExecState*, const table_store::schema::RowBatch&, size_t) {
    return error::Unimplemented("Implement in derived class (if sink or processing)");
  }
  bool is_closed() { return is_closed_; }

  std::unique_ptr<table_store::schema::RowDescriptor> output_descriptor_;
  std::vector<table_store::schema::RowDescriptor> input_descriptors_;
  // Whether or not the node sent EOS to its children.
  bool sent_eos_ = false;

 private:
  void SetUpStreamResultsTable(ExecState* exec_state) {
    auto sink_results = exec_state->table_store()->GetTable(kSinkResultsTableName);
    if (sink_results != nullptr) {
      table_ = sink_results;
    } else {
      auto table = table_store::HotColdTable::Create(kSinkResultsTableName, rel_);
      exec_state->table_store()->AddTable(kSinkResultsTableName, table);
      table_ = table.get();
    }
  }

  Status RecordSinkResults(const table_store::schema::RowBatch& rb,
                           const types::Time64NSValue time_now, const types::UInt128Value upid) {
    if (table_ != nullptr && context_.find(kContextKey) != context_.end()) {
      auto mutation_id = context_[kContextKey];
      std::vector<types::Time64NSValue> col1_in1 = {time_now};
      std::vector<types::UInt128Value> col2_in1 = {upid};
      std::vector<types::Int64Value> col3_in1 = {rb.NumBytes()};
      std::vector<types::Int64Value> col4_in1 = {destination_};
      std::vector<types::StringValue> col5_in1 = {mutation_id};
      auto rb_sink_stats =
          table_store::schema::RowBatch(table_store::schema::RowDescriptor(rel_.col_types()), 1);
      PX_RETURN_IF_ERROR(
          rb_sink_stats.AddColumn(types::ToArrow(col1_in1, arrow::default_memory_pool())));
      PX_RETURN_IF_ERROR(
          rb_sink_stats.AddColumn(types::ToArrow(col2_in1, arrow::default_memory_pool())));
      PX_RETURN_IF_ERROR(
          rb_sink_stats.AddColumn(types::ToArrow(col3_in1, arrow::default_memory_pool())));
      PX_RETURN_IF_ERROR(
          rb_sink_stats.AddColumn(types::ToArrow(col4_in1, arrow::default_memory_pool())));
      PX_RETURN_IF_ERROR(
          rb_sink_stats.AddColumn(types::ToArrow(col5_in1, arrow::default_memory_pool())));
      PX_RETURN_IF_ERROR(table_->WriteRowBatch(rb_sink_stats));
    }
    return Status::OK();
  }

  // The stats of this exec node.
  std::unique_ptr<ExecNodeStats> stats_;
  // Unowned reference to the children. Must remain valid for the duration of query.
  std::vector<ExecNode*> children_;
  // For each of the children (which may have multiple parents) which parent is this node?
  // Parents 0, 1, and 2 would exist for a node with 3 parents.
  std::vector<size_t> parent_ids_for_children_;
  // Whether Close() has been called on this ExecNode.
  bool is_closed_ = false;
  // The type of execution node.
  ExecNodeType type_;
  // Whether this node has been initialized.
  bool is_initialized_ = false;

  // The context key, value pairs passed to the operator node.
  // This is currently used to store the mutation_id.
  std::map<std::string, std::string> context_;

  // The operator type of the current node
  uint64_t destination_;

  table_store::Table* table_;
  table_store::schema::Relation rel_;
};

/**
 * Processing node is the base class for anything that computes
 * producing 1:1 or N:M records. For example: Agg, Map, etc.
 */
class ProcessingNode : public ExecNode {
 public:
  ProcessingNode() : ExecNode(ExecNodeType::kProcessingNode) {}
  virtual ~ProcessingNode() = default;
};

/**
 * Source node is the base class for anything that produces records from some source.
 * For example: MemorySource.
 */
class SourceNode : public ExecNode {
 public:
  SourceNode() : ExecNode(ExecNodeType::kSourceNode) {}
  virtual ~SourceNode() = default;

  bool HasBatchesRemaining() { return !sent_eos_; }
  virtual bool NextBatchReady() = 0;
  int64_t BytesProcessed() const { return bytes_processed_; }
  int64_t RowsProcessed() const { return rows_processed_; }
  Status SendEndOfStream(ExecState* exec_state) {
    // TODO(philkuz) this part is not tracked w/ the timer. Need to include this in NVI or cut
    // losses.
    PX_ASSIGN_OR_RETURN(auto rb, table_store::schema::RowBatch::WithZeroRows(
                                     *output_descriptor_, /*eow*/ true, /*eos*/ true));
    return SendRowBatchToChildren(exec_state, *rb);
  }

 protected:
  int64_t rows_processed_ = 0;
  int64_t bytes_processed_ = 0;
};

/**
 * Sink node is the base class for anything that consumes records and writes to some sink.
 * For example: MemorySink.
 */
class SinkNode : public ExecNode {
 public:
  SinkNode() : ExecNode(ExecNodeType::kSinkNode) {}
  virtual ~SinkNode() = default;
};

}  // namespace exec
}  // namespace carnot
}  // namespace px
