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

#include <rapidjson/document.h>
#include <thread>

#include <absl/functional/bind_front.h>

#include "src/common/base/base.h"
#include "src/common/testing/testing.h"
#include "src/stirling/core/source_registry.h"
#include "src/stirling/core/types.h"
#include "src/stirling/stirling.h"

namespace px {
namespace stirling {

using ::px::testing::BazelRunfilePath;
using ::testing::SizeIs;
using ::testing::StrEq;

//-----------------------------------------------------------------------------
// Test fixture and shared code
//-----------------------------------------------------------------------------

class StirlingFileSourceTest : public ::testing::Test {
 protected:
  void SetUp() override {
    std::unique_ptr<SourceRegistry> registry = std::make_unique<SourceRegistry>();
    stirling_ = Stirling::Create(std::move(registry));

    // Set function to call on data pushes.
    stirling_->RegisterDataPushCallback(
        absl::bind_front(&StirlingFileSourceTest::AppendData, this));
  }

  Status AppendData(uint64_t /*table_id*/, types::TabletID /*tablet_id*/,
                    std::unique_ptr<types::ColumnWrapperRecordBatch> record_batch) {
    record_batches_.push_back(std::move(record_batch));
    return Status::OK();
  }

  StatusOr<stirlingpb::Publish> WaitForStatus(sole::uuid trace_id) {
    StatusOr<stirlingpb::Publish> s;
    do {
      s = stirling_->GetFileSourceInfo(trace_id);
      std::this_thread::sleep_for(std::chrono::seconds(1));
    } while (!s.ok() && s.code() == px::statuspb::Code::RESOURCE_UNAVAILABLE);

    return s;
  }

  std::optional<int> FindFieldIndex(const stirlingpb::TableSchema& schema,
                                    std::string_view field_name) {
    int idx = 0;
    for (const auto& e : schema.elements()) {
      if (e.name() == field_name) {
        return idx;
      }
      ++idx;
    }
    return {};
  }

  void DeployFileSource(std::string file_name, bool trigger_stop = true) {
    sole::uuid id = sole::uuid4();
    stirling_->RegisterFileSource(id, file_name);

    // Should deploy.
    stirlingpb::Publish publication;
    ASSERT_OK_AND_ASSIGN(publication, WaitForStatus(id));

    // Check the incremental publication change.
    ASSERT_EQ(publication.published_info_classes_size(), 1);
    info_class_ = publication.published_info_classes(0);

    // Run Stirling data collector.
    ASSERT_OK(stirling_->RunAsThread());

    // Wait to capture some data.
    while (record_batches_.empty()) {
      std::this_thread::sleep_for(std::chrono::seconds(1));
    }

    if (trigger_stop) {
      ASSERT_OK(stirling_->RemoveFileSource(id));

     // Should get removed.
     EXPECT_EQ(WaitForStatus(id).code(), px::statuspb::Code::NOT_FOUND);

     stirling_->Stop();
    }
  }

  std::unique_ptr<Stirling> stirling_;
  std::vector<std::unique_ptr<types::ColumnWrapperRecordBatch>> record_batches_;
  stirlingpb::InfoClass info_class_;
};

class FileSourceJSONTest : public StirlingFileSourceTest {
 protected:
  const std::string kFilePath =
      BazelRunfilePath("src/stirling/source_connectors/file_source/testdata/test.json");
};

TEST_F(FileSourceJSONTest, ParsesJSONFile) {
  DeployFileSource(kFilePath);
  EXPECT_THAT(record_batches_, SizeIs(1));
  auto& rb = record_batches_[0];
  // Expect there to be 5 columns. time_ and the 4 cols from the JSON file.
  EXPECT_EQ(rb->size(), 5);

  for (size_t i = 0; i < rb->size(); ++i) {
    auto col_wrapper = rb->at(i);
    // The JSON file has 10 lines.
    EXPECT_EQ(col_wrapper->Size(), 10);
  }
}

TEST_F(FileSourceJSONTest, ContinuesReadingAfterEOFReached) {
  std::string file_name = "./test.json";
  std::ofstream ofs(file_name, std::ios::app);
  if (!ofs) {
    LOG(FATAL) << absl::Substitute("Failed to open file= $0 received error=$1", kFilePath, strerror(errno));
  }
  // FileSourceConnector parses the first line to infer the file's schema, an empty file will cause an error.
  ofs << R"({"id": 0, "active": false, "score": 6.28, "name": "item0"})" << std::endl;

  DeployFileSource(file_name, false);
  EXPECT_THAT(record_batches_, SizeIs(1));
  auto& rb = record_batches_[0];
  // Expect there to be 5 columns. time_ and the 4 cols from the JSON file.
  EXPECT_EQ(rb->size(), 5);

  for (size_t i = 0; i < rb->size(); ++i) {
    auto col_wrapper = rb->at(i);
    // The file's first row batch has 1 line
    EXPECT_EQ(col_wrapper->Size(), 1);
  }

  ofs << R"({"id": 1, "active": false, "score": 6.28, "name": "item1"})" << std::endl;
  ofs.flush();
  ofs.close();

  while (record_batches_.size() < 2) {
    std::this_thread::sleep_for(std::chrono::seconds(3));
    LOG(INFO) << "Waiting for more data...";
  }

  auto& rb2 = record_batches_[1];
  for (size_t i = 0; i < rb2->size(); ++i) {
    auto col_wrapper = rb2->at(i);
    // The file's second row batch has 1 line
    EXPECT_EQ(col_wrapper->Size(), 1);
  }
}

TEST_F(FileSourceJSONTest, ContinuesReadingAfterFileRotation) {
  std::string file_name = "./test2.json";
  std::ofstream ofs(file_name, std::ios::app);
  if (!ofs) {
    LOG(FATAL) << absl::Substitute("Failed to open file= $0 received error=$1", kFilePath, strerror(errno));
  }
  // FileSourceConnector parses the first line to infer the file's schema, an empty file will cause an error.
  ofs << R"({"id": 0, "active": false, "score": 6.28, "name": "item0"})" << std::endl;
  ofs << R"({"id": 1, "active": false, "score": 6.28, "name": "item1"})" << std::endl;

  DeployFileSource(file_name, false);
  EXPECT_THAT(record_batches_, SizeIs(1));
  auto& rb = record_batches_[0];
  // Expect there to be 5 columns. time_ and the 4 cols from the JSON file.
  EXPECT_EQ(rb->size(), 5);

  for (size_t i = 0; i < rb->size(); ++i) {
    auto col_wrapper = rb->at(i);
    // The file's first row batch has 2 lines
    EXPECT_EQ(col_wrapper->Size(), 2);
  }

  std::ofstream ofs2(file_name, std::ios::trunc);
  ofs2 << R"({"id": 2, "active": false, "score": 6.28, "name": "item2"})" << std::endl;
  ofs2.flush();
  ofs.close();

  while (record_batches_.size() < 2) {
    std::this_thread::sleep_for(std::chrono::seconds(3));
    LOG(INFO) << "Waiting for more data...";
  }

  auto& rb2 = record_batches_[1];
  for (size_t i = 0; i < rb2->size(); ++i) {
    auto col_wrapper = rb2->at(i);
    // The file's second row batch has 1 line
    EXPECT_EQ(col_wrapper->Size(), 1);
  }
}

}  // namespace stirling
}  // namespace px
