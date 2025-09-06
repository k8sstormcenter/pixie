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

#include <chrono>
#include <ctime>
#include <memory>
#include <string>
#include <thread>
#include <vector>

#include <absl/strings/substitute.h>
#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <clickhouse/client.h>

#include "src/common/testing/test_utils/container_runner.h"
#include "src/common/testing/testing.h"
#include "src/common/base/logging.h"

namespace px {
namespace carnot {
namespace exec {

using ::testing::_;
using ::testing::ElementsAre;

class ClickHouseSourceNodeTest : public ::testing::Test {
 protected:
  static constexpr char kClickHouseImage[] = 
      "src/stirling/source_connectors/socket_tracer/testing/container_images/clickhouse.tar";
  static constexpr char kClickHouseReadyMessage[] = "Ready for connections";
  static constexpr int kClickHousePort = 9000;
  
  void SetUp() override {
    clickhouse_server_ = std::make_unique<ContainerRunner>(
        px::testing::BazelRunfilePath(kClickHouseImage), "clickhouse_test", kClickHouseReadyMessage);
    
    // Start ClickHouse server with necessary options
    std::vector<std::string> options = {
        absl::Substitute("--publish=$0:$0", kClickHousePort),
        "--env=CLICKHOUSE_PASSWORD=test_password",
        "--network=host",
    };
    
    ASSERT_OK(clickhouse_server_->Run(
        std::chrono::seconds{60},  // timeout
        options,
        {},  // args
        true,  // use_host_pid_namespace
        std::chrono::seconds{300}  // container_lifetime
    ));
    
    // Give ClickHouse more time to fully initialize
    std::this_thread::sleep_for(std::chrono::seconds(5));
    
    // Create ClickHouse client with retry logic (using default auth)
    clickhouse::ClientOptions client_options;
    client_options.SetHost("localhost");
    client_options.SetPort(kClickHousePort);
    client_options.SetUser("default");
    client_options.SetPassword("test_password");
    client_options.SetDefaultDatabase("default");
    
    // Retry connection a few times
    const int kMaxRetries = 5;
    for (int i = 0; i < kMaxRetries; ++i) {
      LOG(INFO) << "Attempting to connect to ClickHouse (attempt " << (i + 1) 
                << "/" << kMaxRetries << ")...";
      try {
        client_ = std::make_unique<clickhouse::Client>(client_options);
        // Test the connection with a simple query
        client_->Execute("SELECT 1");
        break;  // Connection successful
      } catch (const std::exception& e) {
        LOG(WARNING) << "Failed to connect to ClickHouse (attempt " << (i + 1) 
                     << "/" << kMaxRetries << "): " << e.what();
        if (i < kMaxRetries - 1) {
          std::this_thread::sleep_for(std::chrono::seconds(2));
        } else {
          throw;  // Re-throw on last attempt
        }
      }
    }
    
    // Create test table
    CreateTestTable();
  }
  
  void TearDown() override {
    if (client_) {
      client_.reset();
    }
    if (clickhouse_server_) {
      clickhouse_server_->Wait();
    }
  }
  
  void CreateTestTable() {
    try {
      // Drop table if exists
      client_->Execute("DROP TABLE IF EXISTS test_table");
      
      // Create test table
      client_->Execute(R"(
        CREATE TABLE test_table (
          id UInt64,
          name String,
          value Float64,
          timestamp DateTime
        ) ENGINE = MergeTree()
        ORDER BY id
      )");
      
      // Insert test data
      auto id_col = std::make_shared<clickhouse::ColumnUInt64>();
      auto name_col = std::make_shared<clickhouse::ColumnString>();
      auto value_col = std::make_shared<clickhouse::ColumnFloat64>();
      auto timestamp_col = std::make_shared<clickhouse::ColumnDateTime>();
      
      // Add test data
      std::time_t now = std::time(nullptr);
      id_col->Append(1);
      name_col->Append("test1");
      value_col->Append(10.5);
      timestamp_col->Append(now);
      
      id_col->Append(2);
      name_col->Append("test2");
      value_col->Append(20.5);
      timestamp_col->Append(now);
      
      id_col->Append(3);
      name_col->Append("test3");
      value_col->Append(30.5);
      timestamp_col->Append(now);
      
      clickhouse::Block block;
      block.AppendColumn("id", id_col);
      block.AppendColumn("name", name_col);
      block.AppendColumn("value", value_col);
      block.AppendColumn("timestamp", timestamp_col);
      
      client_->Insert("test_table", block);
      
      LOG(INFO) << "Test table created and populated successfully";
    } catch (const std::exception& e) {
      LOG(ERROR) << "Failed to create test table: " << e.what();
      throw;
    }
  }

  std::unique_ptr<ContainerRunner> clickhouse_server_;
  std::unique_ptr<clickhouse::Client> client_;
};

TEST_F(ClickHouseSourceNodeTest, BasicQuery) {
  // Test basic SELECT query
  std::vector<uint64_t> ids;
  std::vector<std::string> names;
  std::vector<double> values;
  
  client_->Select("SELECT id, name, value FROM test_table ORDER BY id", 
    [&](const clickhouse::Block& block) {
      for (size_t i = 0; i < block.GetRowCount(); ++i) {
        ids.push_back(block[0]->As<clickhouse::ColumnUInt64>()->At(i));
        names.emplace_back(block[1]->As<clickhouse::ColumnString>()->At(i));
        values.push_back(block[2]->As<clickhouse::ColumnFloat64>()->At(i));
      }
    }
  );
  
  EXPECT_THAT(ids, ElementsAre(1, 2, 3));
  EXPECT_THAT(names, ElementsAre("test1", "test2", "test3"));
  EXPECT_THAT(values, ElementsAre(10.5, 20.5, 30.5));
}

TEST_F(ClickHouseSourceNodeTest, FilteredQuery) {
  // Test SELECT with WHERE clause
  std::vector<uint64_t> ids;
  std::vector<std::string> names;
  
  client_->Select("SELECT id, name FROM test_table WHERE value > 15.0 ORDER BY id", 
    [&](const clickhouse::Block& block) {
      for (size_t i = 0; i < block.GetRowCount(); ++i) {
        ids.push_back(block[0]->As<clickhouse::ColumnUInt64>()->At(i));
        names.emplace_back(block[1]->As<clickhouse::ColumnString>()->At(i));
      }
    }
  );
  
  EXPECT_THAT(ids, ElementsAre(2, 3));
  EXPECT_THAT(names, ElementsAre("test2", "test3"));
}

TEST_F(ClickHouseSourceNodeTest, AggregateQuery) {
  // Test aggregate functions
  double sum_value = 0;
  uint64_t count = 0;
  bool query_executed = false;
  
  try {
    client_->Select("SELECT SUM(value), COUNT(*) FROM test_table", 
      [&](const clickhouse::Block& block) {
        if (block.GetRowCount() > 0) {
          sum_value = block[0]->As<clickhouse::ColumnFloat64>()->At(0);
          count = block[1]->As<clickhouse::ColumnUInt64>()->At(0);
          query_executed = true;
        }
      }
    );
  } catch (const std::exception& e) {
    LOG(ERROR) << "Aggregate query failed: " << e.what();
    FAIL() << "Aggregate query failed: " << e.what();
  }
  
  EXPECT_TRUE(query_executed) << "Query callback was not executed";
  EXPECT_DOUBLE_EQ(sum_value, 61.5);  // 10.5 + 20.5 + 30.5
  EXPECT_EQ(count, 3);
}

TEST_F(ClickHouseSourceNodeTest, EmptyResultSet) {
  // Test query that returns no rows
  int row_count = 0;
  
  client_->Select("SELECT * FROM test_table WHERE id > 1000", 
    [&](const clickhouse::Block& block) {
      row_count += block.GetRowCount();
    }
  );
  
  EXPECT_EQ(row_count, 0);
}

}  // namespace exec
}  // namespace carnot
}  // namespace px
