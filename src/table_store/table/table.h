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

#include <absl/synchronization/mutex.h>
#include <arrow/array.h>
#include <arrow/record_batch.h>
#include <algorithm>
#include <deque>
#include <memory>
#include <optional>
#include <string>
#include <unordered_map>
#include <utility>
#include <variant>
#include <vector>

#include <absl/base/internal/spinlock.h>
#include <absl/strings/str_format.h>
#include "src/common/base/base.h"
#include "src/common/metrics/metrics.h"
#include "src/shared/types/column_wrapper.h"
#include "src/shared/types/types.h"
#include "src/table_store/schema/relation.h"
#include "src/table_store/schema/row_batch.h"
#include "src/table_store/schema/row_descriptor.h"
#include "src/table_store/schemapb/schema.pb.h"
#include "src/table_store/table/internal/arrow_array_compactor.h"
#include "src/table_store/table/internal/batch_size_accountant.h"
#include "src/table_store/table/internal/record_or_row_batch.h"
#include "src/table_store/table/internal/store_with_row_accounting.h"
#include "src/table_store/table/internal/types.h"
#include "src/table_store/table/table_metrics.h"

DECLARE_int32(table_store_table_size_limit);

namespace px {
namespace table_store {

using RecordBatchSPtr = std::shared_ptr<arrow::RecordBatch>;

struct TableStats {
  int64_t bytes;
  int64_t hot_bytes;
  int64_t cold_bytes;
  int64_t num_batches;
  int64_t batches_added;
  int64_t batches_expired;
  int64_t bytes_added;
  int64_t compacted_batches;
  int64_t max_table_size;
  int64_t min_time;
};

class Table;

/**
 * Cursor allows iterating the table, while guaranteeing that no row is returned twice (even when
 * compactions occur between accesses). {Start,Stop}Spec specify what rows the cursor should begin
 * and end at when iterating the cursor.
 */
class Cursor {
  using Time = internal::Time;
  using RowID = internal::RowID;

 public:
  /**
   * StartSpec defines where a Cursor should begin within the table. Current options are to start
   * at a given time, or start at the first row currently in the table.
   */
  struct StartSpec {
    enum StartType {
      StartAtTime,
      CurrentStartOfTable,
    };
    StartType type = CurrentStartOfTable;
    Time start_time = -1;
  };

  /**
   * StopSpec defines when a Cursor should stop and be considered exhausted. Current options are
   * to stop at a given time, stop at the last row currently in the table, or infinite (i.e. the
   * Cursor never becomes exhausted).
   */
  struct StopSpec {
    enum StopType {
      // Iterating a StopAtTime cursor will return all records with `timestamp <= stop_time`.
      // The cursor will not be considered `Done()` until a record with `timestamp > stop_time` is
      // added to the table.
      // Note that StopAtTime is the most expensive of the StopTypes because it requires holding a
      // table lock very briefly on each call to `Done()` or `NextBatchReady()`
      StopAtTime,
      // Iterating a StopAtTimeOrEndOfTable cursor will return all records with `timestamp <=
      // stop_time` that existed in the table at the time of cursor creation. The cursor will be
      // considered `Done()` once all records with `timestamp <= stop_time` have been consumed or
      // when the end of the table is reached (end of the table is determined at cursor creation
      // time).
      StopAtTimeOrEndOfTable,
      // Iterating a CurrentEndOfTable cursor will return all records in the table at cursor
      // creation time.
      CurrentEndOfTable,
      // An Infinite cursor will never be considered `Done()`.
      Infinite,
    };
    StopType type = CurrentEndOfTable;
    // Only valid for StopAtTime or StopAtTimeOrEndOfTable types.
    Time stop_time = -1;
  };

  explicit Cursor(const Table* table) : Cursor(table, StartSpec{}, StopSpec{}) {}
  Cursor(const Table* table, StartSpec start, StopSpec stop);

  // In the case of StopType == Infinite or StopType == StopAtTime, this returns whether the table
  // has the next batch ready. In the case of StopType == CurrentEndOfTable, this returns !Done().
  // Note that `NextBatchReady() == true` doesn't guarantee that `GetNextRowBatch` will succeed.
  // For instance, the desired row batch could have been expired between the call to
  // `NextBatchReady()` and `GetNextRowBatch(...)`, and then the row batch after the expired one
  // is past the stopping condition. In this case `GetNextRowBatch(...)` will return an error.
  bool NextBatchReady();
  StatusOr<std::unique_ptr<schema::RowBatch>> GetNextRowBatch(const std::vector<int64_t>& cols);
  // In the case of StopType == Infinite, this function always returns false.
  bool Done();
  // Change the StopSpec of the cursor.
  void UpdateStopSpec(StopSpec stop);

 private:
  void AdvanceToStart(const StartSpec& start);
  void StopStateFromSpec(StopSpec&& stop);
  void UpdateStopStateForStopAtTime();

  // The following methods are made private so that they are only accessible from Table.
  internal::RowID* LastReadRowID();
  internal::BatchHints* Hints();
  std::optional<internal::RowID> StopRowID() const;

  struct StopState {
    StopSpec spec;
    RowID stop_row_id;
    // If StopSpec.type is StopAtTime, then stop_row_id doesn't become finalized until the time is
    // within the table. This bool keeps track of when that happens.
    bool stop_row_id_final = false;
  };
  const Table* table_;
  internal::BatchHints hints_;
  RowID last_read_row_id_;
  StopState stop_;

  friend class Table;
  friend class HotColdTable;
  friend class HotOnlyTable;
};

class Table : public NotCopyable {
 public:
  using RecordBatchPtr = internal::RecordBatchPtr;
  using ArrowArrayPtr = internal::ArrowArrayPtr;
  using ColdBatch = internal::ColdBatch;
  using Time = internal::Time;
  using TimeInterval = internal::TimeInterval;
  using RowID = internal::RowID;
  using RowIDInterval = internal::RowIDInterval;
  using BatchID = internal::BatchID;

  Table() = delete;
  virtual ~Table() = default;

  schema::Relation GetRelation() const { return rel_; }

  /**
   * Get a RowBatch of data corresponding to the next data after the given cursor.
   * @param cursor the Cursor to get the next row batch after.
   * @param cols a vector of column indices to get data for.
   * @return a unique ptr to a RowBatch with the requested data.
   */
  virtual StatusOr<std::unique_ptr<schema::RowBatch>> GetNextRowBatch(
      Cursor* cursor, const std::vector<int64_t>& cols) const = 0;

  /**
   * Get the unique identifier of the first row in the table.
   * If all the data is expired from the table, this returns the last row id that was in the table.
   * @return unique identifier of the first row.
   */
  virtual RowID FirstRowID() const = 0;

  /**
   * Get the unique identifier of the last row in the table.
   * If all the data is expired from the table, this returns the last row id that was in the table.
   * @return unique identifier of the last row.
   */
  virtual RowID LastRowID() const = 0;

  /**
   * Find the unique identifier of the first row for which its corresponding time is greater than or
   * equal to the given time.
   * @param time the time to search for.
   * @return unique identifier of the first row with time greater than or equal to the given time.
   */
  virtual RowID FindRowIDFromTimeFirstGreaterThanOrEqual(Time time) const = 0;

  /**
   * Find the unique identifier of the first row for which its corresponding time is greater than
   * the given time.
   * @param time the time to search for.
   * @return unique identifier of the first row with time greater than the given time.
   */
  virtual RowID FindRowIDFromTimeFirstGreaterThan(Time time) const = 0;

  /**
   * Covert the table and store in passed in proto.
   * @param table_proto The table proto to write to.
   * @return Status of conversion.
   */
  virtual Status ToProto(table_store::schemapb::Table* table_proto) const = 0;

  virtual TableStats GetTableStats() const = 0;

  /**
   * Compacts hot batches into compacted_batch_size_ sized cold batches. Each call to
   * CompactHotToCold will create a maximum of kMaxBatchesPerCompactionCall cold batches.
   * @param mem_pool arrow MemoryPool to be used for creating new cold batches.
   */
  virtual Status CompactHotToCold(arrow::MemoryPool* mem_pool) = 0;

  /**
   * Transfers the given record batch (from Stirling) into the Table.
   *
   * @param record_batch the record batch to be appended to the Table.
   * @return status
   */
  Status TransferRecordBatch(std::unique_ptr<px::types::ColumnWrapperRecordBatch> record_batch) {
    // Don't transfer over empty row batches.
    if (record_batch->empty() || record_batch->at(0)->Size() == 0) {
      return Status::OK();
    }

    auto record_batch_w_cache = internal::RecordBatchWithCache{
        std::move(record_batch),
        std::vector<ArrowArrayPtr>(rel_.NumColumns()),
        std::vector<bool>(rel_.NumColumns(), false),
    };
    internal::RecordOrRowBatch record_or_row_batch(std::move(record_batch_w_cache));

    PX_RETURN_IF_ERROR(WriteHot(std::move(record_or_row_batch)));
    return Status::OK();
  }

  /**
   * Writes a row batch to the table.
   * @param rb Rowbatch to write to the table.
   */
  Status WriteRowBatch(const schema::RowBatch& rb) {
    // Don't write empty row batches.
    if (rb.num_columns() == 0 || rb.ColumnAt(0)->length() == 0) {
      return Status::OK();
    }

    internal::RecordOrRowBatch record_or_row_batch(rb);

    PX_RETURN_IF_ERROR(WriteHot(std::move(record_or_row_batch)));
    return Status::OK();
  }

 protected:
  virtual Time MaxTime() const = 0;

  virtual Status ExpireBatch() = 0;

  virtual Status UpdateTableMetricGauges() = 0;

  Table(TableMetrics metrics, const schema::Relation& relation, size_t max_table_size)
      : metrics_(metrics), rel_(relation), max_table_size_(max_table_size) {}

  Status ExpireHot() {
    absl::base_internal::SpinLockHolder hot_lock(&hot_lock_);
    if (hot_store_->Size() == 0) {
      return error::InvalidArgument("Failed to expire row batch, no row batches in table");
    }
    hot_store_->PopFront();
    batch_size_accountant_->ExpireHotBatch();
    return Status::OK();
  }

  Status ExpireRowBatches(int64_t row_batch_size) {
    if (row_batch_size > max_table_size_) {
      return error::InvalidArgument("RowBatch size ($0) is bigger than maximum table size ($1).",
                                    row_batch_size, max_table_size_);
    }
    int64_t bytes;
    {
      absl::base_internal::SpinLockHolder hot_lock(&hot_lock_);
      bytes = batch_size_accountant_->HotBytes() + batch_size_accountant_->ColdBytes();
    }
    while (bytes + row_batch_size > max_table_size_) {
      PX_RETURN_IF_ERROR(ExpireBatch());
      {
        absl::base_internal::SpinLockHolder hot_lock(&hot_lock_);
        bytes = batch_size_accountant_->HotBytes() + batch_size_accountant_->ColdBytes();
      }
      {
        absl::base_internal::SpinLockHolder lock(&stats_lock_);
        batches_expired_++;
        metrics_.batches_expired_counter.Increment();
      }
    }
    return Status::OK();
  }

  Status WriteHot(internal::RecordOrRowBatch&& record_or_row_batch) {
    // See BatchSizeAccountantNonMutableState for an explanation of the thread safety and necessity
    // of NonMutableState.
    auto batch_stats = internal::BatchSizeAccountant::CalcBatchStats(
        ABSL_TS_UNCHECKED_READ(batch_size_accountant_)->NonMutableState(), record_or_row_batch);

    PX_RETURN_IF_ERROR(ExpireRowBatches(batch_stats.bytes));

    {
      absl::base_internal::SpinLockHolder hot_lock(&hot_lock_);
      auto batch_length = record_or_row_batch.Length();
      batch_size_accountant_->NewHotBatch(std::move(batch_stats));
      hot_store_->EmplaceBack(next_row_id_, std::move(record_or_row_batch));
      next_row_id_ += batch_length;
    }

    {
      absl::base_internal::SpinLockHolder lock(&stats_lock_);
      ++batches_added_;
      metrics_.batches_added_counter.Increment();
      bytes_added_ += batch_stats.bytes;
      metrics_.bytes_added_counter.Increment(batch_stats.bytes);
    }

    // Make sure locks are released for this call, since they are reacquired inside.
    PX_RETURN_IF_ERROR(UpdateTableMetricGauges());
    return Status::OK();
  }

  mutable absl::base_internal::SpinLock hot_lock_;

  TableMetrics metrics_;

  schema::Relation rel_;

  int64_t max_table_size_ = 0;

  int64_t time_col_idx_ = -1;

  mutable absl::base_internal::SpinLock stats_lock_;
  int64_t batches_expired_ ABSL_GUARDED_BY(stats_lock_) = 0;
  int64_t batches_added_ ABSL_GUARDED_BY(stats_lock_) = 0;
  int64_t bytes_added_ ABSL_GUARDED_BY(stats_lock_) = 0;
  int64_t compacted_batches_ ABSL_GUARDED_BY(stats_lock_) = 0;

  std::unique_ptr<internal::StoreWithRowTimeAccounting<internal::StoreType::Hot>> hot_store_
      ABSL_GUARDED_BY(hot_lock_);

  // Counter to assign a unique row ID to each row. Synchronized by hot_lock_ since its only
  // accessed on a hot write.
  int64_t next_row_id_ ABSL_GUARDED_BY(hot_lock_) = 0;

  std::unique_ptr<internal::BatchSizeAccountant> batch_size_accountant_ ABSL_GUARDED_BY(hot_lock_);

  friend class Cursor;
};

/**
 * Table stores data in two separate partitions, hot and cold. Hot data is "hot" from the
 * perspective of writes, in other words data is first written to the hot partitiion, and then later
 * moved to the cold partition. Reads can hit both hot and cold data. Hot data can be written in
 * RecordBatch format (i.e. for writes from stirling) or schema::RowBatch format (i.e. for writes
 * from MemorySinkNodes, which are not currently used). Both stores use a wrapper around std::deque
 * to store the data while keeping track of row and time indexes (see `StoreWithRowTimeAccounting`
 * and `Time and Row Indexing` below).
 *
 * Synchronization Scheme:
 * The hot and cold partitions are synchronized separately with spinlocks.
 *
 * Compaction Scheme:
 * Hot batches are compacted into batches of size roughly `compacted_batch_size_` +/- the size of a
 * single row.  The compaction routine should be called periodically but that is not the
 * responsibility of this class.
 *
 * Time and Row Indexing:
 * The first and last values of the time columns for each batch are stored in
 * `StoreWithRowTimeAccounting` which internally maintains a sorted list for O(logN) time lookup.
 * See that class for more details. Additionally, this class supports multiple batches with the same
 * timestamps. In order to support this, we keep track of an incrementing identifier for each row
 * (we store only the identifiers of the first and last rows in a batch). This allows us to support
 * returning only valid data even if a compaction occurs in the middle of query execution. For
 * example, suppose we have two batches of data all with the same timestamps. Suppose a query reads
 * through all this data. Now after reading the first batch, compaction is called and both batches
 * are compacted together and put into cold storage. Then if the query were to naively try to access
 * the "second" batch since it already saw the "first" batch, there wouldn't be any more data and
 * the query will have skipped all the rows in what was initially the "second" batch. Instead, the
 * Cursor stores the unique row identifier of the last read row, so
 * that when GetNextRowBatch is called on the cursor it can work out that it needs to return a slice
 * of the batch with the original "second" batch's data.
 */
class HotColdTable : public Table {
  using RecordBatchPtr = internal::RecordBatchPtr;
  using ArrowArrayPtr = internal::ArrowArrayPtr;
  using ColdBatch = internal::ColdBatch;
  using Time = internal::Time;
  using TimeInterval = internal::TimeInterval;
  using RowID = internal::RowID;
  using RowIDInterval = internal::RowIDInterval;
  using BatchID = internal::BatchID;

  // TODO(ddelnano): Maybe this should be removed
  friend class Cursor;

  static inline constexpr int64_t kDefaultColdBatchMinSize = 64 * 1024;

 public:
  static inline constexpr int64_t kMaxBatchesPerCompactionCall = 256;
  using StopPosition = int64_t;
  static inline std::shared_ptr<Table> Create(std::string_view table_name,
                                              const schema::Relation& relation) {
    // Create naked pointer, because std::make_shared() cannot access the private ctor.
    return std::shared_ptr<Table>(
        new HotColdTable(table_name, relation, FLAGS_table_store_table_size_limit));
  }

  /**
   * @brief Construct a new Table object along with its columns. Can be used to create
   * a table (along with columns) based on a subscription message from Stirling.
   *
   * @param relation the relation for the table.
   * @param max_table_size the maximum number of bytes that the table can hold. This is limitless
   * (-1) by default.
   */
  explicit HotColdTable(std::string_view table_name, const schema::Relation& relation,
                        size_t max_table_size)
      : HotColdTable(table_name, relation, max_table_size, kDefaultColdBatchMinSize) {}

  HotColdTable(std::string_view table_name, const schema::Relation& relation, size_t max_table_size,
               size_t compacted_batch_size_);

  /**
   * Get a RowBatch of data corresponding to the next data after the given cursor.
   * @param cursor the Cursor to get the next row batch after.
   * @param cols a vector of column indices to get data for.
   * @return a unique ptr to a RowBatch with the requested data.
   */
  StatusOr<std::unique_ptr<schema::RowBatch>> GetNextRowBatch(
      Cursor* cursor, const std::vector<int64_t>& cols) const override;

  /**
   * Get the unique identifier of the first row in the table.
   * If all the data is expired from the table, this returns the last row id that was in the table.
   * @return unique identifier of the first row.
   */
  RowID FirstRowID() const override;

  /**
   * Get the unique identifier of the last row in the table.
   * If all the data is expired from the table, this returns the last row id that was in the table.
   * @return unique identifier of the last row.
   */
  RowID LastRowID() const override;

  /**
   * Find the unique identifier of the first row for which its corresponding time is greater than or
   * equal to the given time.
   * @param time the time to search for.
   * @return unique identifier of the first row with time greater than or equal to the given time.
   */
  RowID FindRowIDFromTimeFirstGreaterThanOrEqual(Time time) const override;

  /**
   * Find the unique identifier of the first row for which its corresponding time is greater than
   * the given time.
   * @param time the time to search for.
   * @return unique identifier of the first row with time greater than the given time.
   */
  RowID FindRowIDFromTimeFirstGreaterThan(Time time) const override;

  /**
   * Transfers the given record batch (from Stirling) into the Table.
   *
   * @param record_batch the record batch to be appended to the Table.
   * @return status
   */
  /* Status TransferRecordBatch(std::unique_ptr<px::types::ColumnWrapperRecordBatch> record_batch)
   * override; */

  /**
   * Covert the table and store in passed in proto.
   * @param table_proto The table proto to write to.
   * @return Status of conversion.
   */
  Status ToProto(table_store::schemapb::Table* table_proto) const override;

  TableStats GetTableStats() const override;

  /**
   * Compacts hot batches into compacted_batch_size_ sized cold batches. Each call to
   * CompactHotToCold will create a maximum of kMaxBatchesPerCompactionCall cold batches.
   * @param mem_pool arrow MemoryPool to be used for creating new cold batches.
   */
  Status CompactHotToCold(arrow::MemoryPool* mem_pool) override;

 private:
  Time MaxTime() const override;

  Status ExpireBatch() override;

  Status UpdateTableMetricGauges() override;

  const int64_t compacted_batch_size_;

  mutable absl::base_internal::SpinLock cold_lock_;
  std::unique_ptr<internal::StoreWithRowTimeAccounting<internal::StoreType::Cold>> cold_store_
      ABSL_GUARDED_BY(cold_lock_);
  std::deque<int64_t> cold_batch_bytes_ ABSL_GUARDED_BY(cold_lock_);

  StatusOr<bool> ExpireCold();
  Status CompactSingleBatchUnlocked(arrow::MemoryPool* mem_pool)
      ABSL_EXCLUSIVE_LOCKS_REQUIRED(cold_lock_) ABSL_EXCLUSIVE_LOCKS_REQUIRED(hot_lock_);

  internal::ArrowArrayCompactor compactor_;
};

class HotOnlyTable : public Table {
  using RowID = internal::RowID;

 public:
  using StopPosition = int64_t;
  static inline std::shared_ptr<Table> Create(std::string_view table_name,
                                              const schema::Relation& relation) {
    // Create naked pointer, because std::make_shared() cannot access the private ctor.
    return std::shared_ptr<Table>(
        new HotOnlyTable(table_name, relation, FLAGS_table_store_table_size_limit));
  }

  /**
   * @brief Construct a new Table object along with its columns. Can be used to create
   * a table (along with columns) based on a subscription message from Stirling.
   *
   * @param relation the relation for the table.
   * @param max_table_size the maximum number of bytes that the table can hold. This is limitless
   * (-1) by default.
   */
  explicit HotOnlyTable(std::string_view table_name, const schema::Relation& relation,
                        size_t max_table_size);

  /**
   * Get a RowBatch of data corresponding to the next data after the given cursor.
   * @param cursor the Cursor to get the next row batch after.
   * @param cols a vector of column indices to get data for.
   * @return a unique ptr to a RowBatch with the requested data.
   */
  StatusOr<std::unique_ptr<schema::RowBatch>> GetNextRowBatch(
      Cursor* cursor, const std::vector<int64_t>& cols) const override;

  /**
   * Get the unique identifier of the first row in the table.
   * If all the data is expired from the table, this returns the last row id that was in the table.
   * @return unique identifier of the first row.
   */
  RowID FirstRowID() const override;

  /**
   * Get the unique identifier of the last row in the table.
   * If all the data is expired from the table, this returns the last row id that was in the table.
   * @return unique identifier of the last row.
   */
  RowID LastRowID() const override;

  /**
   * Find the unique identifier of the first row for which its corresponding time is greater than or
   * equal to the given time.
   * @param time the time to search for.
   * @return unique identifier of the first row with time greater than or equal to the given time.
   */
  RowID FindRowIDFromTimeFirstGreaterThanOrEqual(Time time) const override;

  /**
   * Find the unique identifier of the first row for which its corresponding time is greater than
   * the given time.
   * @param time the time to search for.
   * @return unique identifier of the first row with time greater than the given time.
   */
  RowID FindRowIDFromTimeFirstGreaterThan(Time time) const override;

  /**
   * Transfers the given record batch (from Stirling) into the Table.
   *
   * @param record_batch the record batch to be appended to the Table.
   * @return status
   */
  /* Status TransferRecordBatch(std::unique_ptr<px::types::ColumnWrapperRecordBatch> record_batch)
   * override; */

  /**
   * Covert the table and store in passed in proto.
   * @param table_proto The table proto to write to.
   * @return Status of conversion.
   */
  Status ToProto(table_store::schemapb::Table* table_proto) const override;

  TableStats GetTableStats() const override;

  /**
   * Compacts hot batches into compacted_batch_size_ sized cold batches. Each call to
   * CompactHotToCold will create a maximum of kMaxBatchesPerCompactionCall cold batches.
   * @param mem_pool arrow MemoryPool to be used for creating new cold batches.
   */
  Status CompactHotToCold(arrow::MemoryPool* mem_pool) override;

 private:
  Time MaxTime() const override;

  Status ExpireBatch() override;

  Status UpdateTableMetricGauges() override;

  friend class Cursor;
};

}  // namespace table_store
}  // namespace px
