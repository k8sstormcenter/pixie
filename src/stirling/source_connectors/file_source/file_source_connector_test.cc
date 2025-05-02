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

#include <gmock/gmock.h>
#include <utility>

#include "src/common/testing/testing.h"
#include "src/stirling/source_connectors/file_source/file_source_connector.h"

namespace px {
namespace stirling {

TEST(FileSourceConnectorTest, DataElementsFromJSON) {
  const auto file_path =
      testing::BazelRunfilePath("src/stirling/source_connectors/file_source/testdata/test.json");
  auto stream = std::ifstream(file_path);
  auto result = DataElementsFromJSON(stream);
  ASSERT_OK(result);
  BackedDataElements elements = std::move(result.ValueOrDie());

  ASSERT_EQ(elements.elements().size(), 8);
  EXPECT_EQ(elements.elements()[0].name(), "time_");
  EXPECT_EQ(elements.elements()[0].type(), types::DataType::TIME64NS);
  EXPECT_EQ(elements.elements()[1].name(), "uuid");
  EXPECT_EQ(elements.elements()[1].type(), types::DataType::UINT128);
  EXPECT_EQ(elements.elements()[2].name(), "id");
  EXPECT_EQ(elements.elements()[2].type(), types::DataType::INT64);
  EXPECT_EQ(elements.elements()[3].name(), "active");
  EXPECT_EQ(elements.elements()[3].type(), types::DataType::BOOLEAN);
  EXPECT_EQ(elements.elements()[4].name(), "score");
  EXPECT_EQ(elements.elements()[4].type(), types::DataType::FLOAT64);
  EXPECT_EQ(elements.elements()[5].name(), "name");
  EXPECT_EQ(elements.elements()[5].type(), types::DataType::STRING);
  EXPECT_EQ(elements.elements()[6].name(), "object");
  EXPECT_EQ(elements.elements()[6].type(), types::DataType::STRING);
  EXPECT_EQ(elements.elements()[7].name(), "arr");
  EXPECT_EQ(elements.elements()[7].type(), types::DataType::STRING);
}

TEST(FileSourceConnectorTest, DISABLED_DataElementsFromJSON_UnsupportedTypes) {
  const auto file_path = testing::BazelRunfilePath(
      "src/stirling/source_connectors/file_source/testdata/unsupported.json");
  auto stream = std::ifstream(file_path);
  auto result = DataElementsFromJSON(stream);
  ASSERT_EQ(result.ok(), false);
  ASSERT_EQ(result.status().msg(),
            "Unable to parse JSON key 'unsupported': unsupported type: Object");
}

TEST(FileSourceConnectorTest, DataElementsForUnstructuredFile) {

  const auto file_path = testing::BazelRunfilePath(
      "src/stirling/source_connectors/file_source/testdata/kern.log");
  auto stream = std::ifstream(file_path);
  auto result = DataElementsForUnstructuredFile();
  ASSERT_OK(result);
  BackedDataElements elements = std::move(result.ValueOrDie());
  EXPECT_EQ(elements.elements()[0].name(), "time_");
  EXPECT_EQ(elements.elements()[0].type(), types::DataType::TIME64NS);
  EXPECT_EQ(elements.elements()[1].name(), "uuid");
  EXPECT_EQ(elements.elements()[1].type(), types::DataType::UINT128);
  EXPECT_EQ(elements.elements()[2].name(), "raw_line");
  EXPECT_EQ(elements.elements()[2].type(), types::DataType::STRING);
}

}  // namespace stirling
}  // namespace px
