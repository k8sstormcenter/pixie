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

#include <cstdio>
#include <memory>
#include <string>
#include <vector>

#include "src/stirling/core/source_connector.h"
#include "src/stirling/utils/monitor.h"

namespace px {
namespace stirling {

class FileSourceConnector : public SourceConnector {
  using pos_type = std::ifstream::pos_type;

 public:
  static constexpr auto kSamplingPeriod = std::chrono::milliseconds{100};
  // Set this high enough to avoid the following error:
  // F20250129 00:05:30.980778 2527479 source_connector.cc:64] Failed to push data. Message =
  // Table_id 1 doesn't exist.
  //
  // This occurs when the Stirling data table has data but the table store hasn't received its
  // schema yet. I'm not sure why the dynamic tracer doesn't experience this case.
  static constexpr auto kPushPeriod = std::chrono::milliseconds{7000};

  static StatusOr<std::unique_ptr<SourceConnector> > Create(std::string_view source_name,
                                                            const std::filesystem::path file_name);

  FileSourceConnector() = delete;
  ~FileSourceConnector() override = default;

 protected:
  explicit FileSourceConnector(std::string_view source_name, const std::filesystem::path& file_name,
                               std::ifstream file,
                               std::unique_ptr<DynamicDataTableSchema> table_schema);
  Status InitImpl() override;
  Status StopImpl() override;
  void TransferDataImpl(ConnectorContext* ctx) override;

 private:
  void TransferDataFromJSON(DataTable::DynamicRecordBuilder* builder, uint64_t nanos,
                            const std::string& line);
  void TransferDataFromCSV(DataTable::DynamicRecordBuilder* builder, uint64_t nanos,
                           const std::string& line);

  struct FileTransferSpec {
    std::function<void(FileSourceConnector&, DataTable::DynamicRecordBuilder*, uint64_t nanos,
                       const std::string&)>
        transfer_fn;
  };
  std::string name_;
  const std::filesystem::path file_name_;
  std::ifstream file_;
  std::unique_ptr<DynamicDataTableSchema> table_schema_;
  absl::flat_hash_map<std::string, FileTransferSpec> transfer_specs_;
  int eof_count_ = 0;
  pos_type last_pos_ = 0;
  StirlingMonitor& monitor_ = *StirlingMonitor::GetInstance();
};

StatusOr<BackedDataElements> DataElementsFromJSON(std::ifstream& f_stream);
StatusOr<BackedDataElements> DataElementsFromCSV(std::ifstream& f_stream);
StatusOr<BackedDataElements> DataElementsForUnstructuredFile();

}  // namespace stirling
}  // namespace px
