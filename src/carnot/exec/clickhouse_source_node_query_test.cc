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

#include <memory>
#include <vector>

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include "src/carnot/planpb/plan.pb.h"
#include "src/common/testing/testing.h"

namespace px {
namespace carnot {
namespace exec {

using table_store::schema::RowDescriptor;

// dx_kubescape_mitre and the dx_* views over kubescape_logs expose event_time as
// UInt64 unix NANOSECONDS. Dividing the window bound down to seconds made the
// predicate trivially true for every row, so ClickHouse scanned the whole view and
// the query died on the 90s server timeout instead of reading one partition.
namespace {
std::unique_ptr<ClickHouseSourceNode> TimePushdownNode(types::DataType ts_type, int64_t start_ns,
                                                     int64_t end_ns) {
  planpb::Operator op;
  op.set_op_type(planpb::OperatorType::CLICKHOUSE_SOURCE_OPERATOR);
  auto* ch_op = op.mutable_clickhouse_source_op();
  ch_op->set_query("SELECT uniqueID, event_time FROM dx_kubescape_mitre");
  ch_op->set_batch_size(1024);
  ch_op->set_timestamp_column("event_time");
  if (ts_type != types::DataType::DATA_TYPE_UNKNOWN) {
    ch_op->set_timestamp_column_type(ts_type);
  }
  ch_op->set_start_time(start_ns);
  ch_op->set_end_time(end_ns);
  auto plan_node = plan::ClickHouseSourceOperator::FromProto(op, 1);
  auto node = std::make_unique<ClickHouseSourceNode>();
  PX_CHECK_OK(node->Init(*plan_node, RowDescriptor({}), std::vector<RowDescriptor>({})));
  return node;
}
}  // namespace

TEST(ClickHouseSourceNodeTest, TimePushdownUsesNanosForIntegerColumn) {
  auto node = TimePushdownNode(types::DataType::INT64, 1757990000000000000LL,
                              1757991800000000000LL);
  auto query = node->BuildQuery();
  EXPECT_THAT(query, ::testing::HasSubstr("event_time >= 1757990000000000000"));
  EXPECT_THAT(query, ::testing::HasSubstr("event_time <= 1757991800000000000"));
}

TEST(ClickHouseSourceNodeTest, TimePushdownUsesDateTimeSeconds) {
  auto node = TimePushdownNode(types::DataType::TIME64NS, 1757990000000000000LL,
                              1757991800000000000LL);
  auto query = node->BuildQuery();
  EXPECT_THAT(query, ::testing::HasSubstr("event_time >= 1757990000"));
  EXPECT_THAT(query, ::testing::HasSubstr("event_time <= 1757991800"));
}

TEST(ClickHouseSourceNodeTest, TimePushdownDefaultsToDateTimeSeconds) {
  auto node = TimePushdownNode(types::DataType::DATA_TYPE_UNKNOWN, 1757990000000000000LL, 0);
  auto query = node->BuildQuery();
  EXPECT_THAT(query, ::testing::HasSubstr("event_time >= 1757990000"));
}

}  // namespace exec
}  // namespace carnot
}  // namespace px
