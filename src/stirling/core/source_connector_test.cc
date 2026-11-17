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

#include <gtest/gtest.h>
#include <memory>
#include <vector>

#include "src/stirling/core/source_connector.h"

namespace px {
namespace stirling {

constexpr DataElement kElements[] = {
    {"time_", "time", types::DataType::TIME64NS, types::SemanticType::ST_NONE,
     types::PatternType::METRIC_COUNTER},
    {"x", "an int value", types::DataType::INT64, types::SemanticType::ST_NONE,
     types::PatternType::GENERAL},
};
constexpr auto kTableA = DataTableSchema("table_a", "first table", kElements);
constexpr auto kTableB = DataTableSchema("table_b", "second table", kElements);
constexpr DataTableSchema kTables[] = {kTableA, kTableB};

class TwoTableConnector : public SourceConnector {
 public:
  TwoTableConnector() : SourceConnector("two_tables", kTables) {}

 protected:
  Status InitImpl() override { return Status::OK(); }
  void TransferDataImpl(ConnectorContext*) override {}
  Status StopImpl() override { return Status::OK(); }
};

template <const DataTableSchema* TSchema>
void AppendRow(DataTable* table, int64_t x) {
  DataTable::RecordBuilder<TSchema> r(table);
  r.template Append<r.ColIndex("time_")>(x);
  r.template Append<r.ColIndex("x")>(x);
}

TEST(SourceConnectorTest, PushToUnregisteredTableIdDoesNotAbort) {
  constexpr uint64_t kUnregisteredTableId = 22;
  constexpr uint64_t kRegisteredTableId = 23;
  DataTable unregistered(kUnregisteredTableId, kTableA);
  DataTable registered(kRegisteredTableId, kTableB);
  AppendRow<&kTableA>(&unregistered, 1);
  AppendRow<&kTableB>(&registered, 2);

  TwoTableConnector connector;
  connector.set_data_tables({&unregistered, &registered});

  std::vector<uint64_t> pushed;
  connector.PushData([&](uint32_t table_id, types::TabletID,
                         std::unique_ptr<types::ColumnWrapperRecordBatch>) -> Status {
    if (table_id == kUnregisteredTableId) {
      return error::InvalidArgument("Table_id $0 doesn't exist.", table_id);
    }
    pushed.push_back(table_id);
    return Status::OK();
  });

  EXPECT_EQ(pushed, std::vector<uint64_t>{kRegisteredTableId});
}

}  // namespace stirling
}  // namespace px
