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

#include "src/stirling/source_connectors/tetragon/tetragon_connector.h"

#include <rapidjson/document.h>
#include <rapidjson/error/en.h>

#include <string>
#include <utility>

//DeployTetragonConnector

using px::StatusOr;
using px::utils::RapidJSONTypeToString;

constexpr size_t kMaxStringBytes = std::numeric_limits<size_t>::max();

namespace px {
namespace stirling {

namespace {

StatusOr<std::pair<BackedDataElements, std::ifstream>> DataElementsFromFile(
    const std::filesystem::path& file_name) {
  auto f = std::ifstream(file_name.string());
  if (!f.is_open()) {
    return error::Internal("Failed to open file: $0 with error=$1", file_name.string(),
                           strerror(errno));
  }

  BackedDataElements data_elements;
  PX_ASSIGN_OR_RETURN(data_elements, DataElementsForTetragonFile());
  f.seekg(0, std::ios::beg);
  return std::make_pair(std::move(data_elements), std::move(f));
}

}  // namespace

StatusOr<std::unique_ptr<TetragonConnector>> TetragonConnector::Create(
    std::string_view source_name,
    const std::filesystem::path input_file_name) 
{
  // get the file extension of the files
  auto inputExtension = input_file_name.extension().string();
  if (inputExtension != ".log")
  {
    return error::InvalidArgument("Input file has not *.log extension")
  }
  auto in_host_path = px::system::Config::GetInstance().ToHostPath(input_file_name);
  PX_ASSIGN_OR_RETURN(auto data_elements_and_file, DataElementsFromFile(in_host_path));
  auto& [data_elements, file] = data_elements_and_file;

  // Get just the filename and extension
  auto name = in_host_path.filename().string();
  std::unique_ptr<DynamicDataTableSchema> table_schema =
      DynamicDataTableSchema::Create(name, "", std::move(data_elements));
  return std::unique_ptr<TetragonConnector>(new TetragonConnector(
      source_name, std::move(in_host_path), std::move(file), std::move(table_schema), std::move(out_host_path)));  
}

TetragonConnector::TetragonConnector(
  std::string_view source_name, 
  const std::filesystem::path& input_file_name,
  std::ifstream file,
  std::unique_ptr<DynamicDataTableSchema> table_schema)
    : SourceConnector(source_name, ArrayView<DataTableSchema>(&table_schema->Get(), 1)),
      name_(source_name),
      in_file_name_(input_file_name),
      file_(std::move(file)),
      table_schema_(std::move(table_schema)),
      transfer_specs_({
          {".log", {&TetragonConnector::TransferTetragonData}},
      }){}



StatusOr<BackedDataElements> DataElementsForTetragonFile() {
  BackedDataElements data_elements(4);
  data_elements.emplace_back("time_", "", types::DataType::STRING);
  data_elements.emplace_back("node_name", "", types::DataType::STRING);
  data_elements.emplace_back("type", "", types::DataType::STRING);
  data_elements.emplace_back("payload", "", types::DataType::STRING);
  return data_elements;
}

Status TetragonConnector::InitImpl() {
  sampling_freq_mgr_.set_period(kSamplingPeriod);
  push_freq_mgr_.set_period(kPushPeriod);
  return Status::OK();
}

Status TetragonConnector::StopImpl() {
  file_.close();
  return Status::OK();
}

constexpr int kMaxLines = 1000;


void TetragonConnector::TransferDataFromUnstructuredFile(DataTable::DynamicRecordBuilder* /*r*/,
                                               uint64_t nanos, const std::string& line) {
  DataTable::DynamicRecordBuilder r(data_tables_[0]);
  rapidjson::Document tetragon_data;
  tetragon_data.Parse(line.c_str());
  rapidjson::Document::AllocatorType& allocator = tetragon_data.GetAllocator();
  if (tetragon_data.HasParseError()) {
      LOG(ERROR) << "Error parsing JSON string:" << line;
      return;
  }

  // Extract common fields
  for (const auto& entry : tetragon_data.GetArray()) {
    if (entry.HasMember("time")) {
      if (entry["time"].IsString()) {
        r.Append(0, types::StringValue(tetragon_data["time"]), kMaxStringBytes);
      } else {
        LOG(ERROR) << "Key ""time"" is present but its value is not a string.";
      }
    } else {
      LOG(ERROR) << "Key ""time"" is not present in json.";
    }

    if (entry.HasMember("node_name")) {
      if (entry["node_name"].IsString()) {
        r.Append(1, types::StringValue(tetragon_data["node_name"]), kMaxStringBytes);
      } else {
        LOG(ERROR) << "Key ""node_name"" is present but its value is not a string.";
      }
    } else {
      LOG(ERROR) << "Key ""node_name"" is not present in json.";
    }
  }

  // Find the first key to use as the type
  if (!tetragon_data.ObjectEmpty()) {
      auto itr = tetragon_data.MemberBegin();
      std::string type = itr->name.GetString();
      r.Append(2, types::StringValue(type.c_str()), kMaxStringBytes);

      // Add the payload (content of the first key)
      r.Append(3, types::StringValue(tetragon_data[type.c_str()]), kMaxStringBytes);
  } else {
      LOG(ERROR) << "Error: JSON object is empty.";
      return;
  }
}

void TetragonConnector::TransferDataImpl(ConnectorContext* /* ctx */) {
  DCHECK_EQ(data_tables_.size(), 1U) << "Only one table is allowed per TetragonConnector.";
  int i = 0;
  auto extension = in_file_name_.extension().string();
  auto transfer_fn = transfer_specs_.at(extension).transfer_fn;

  auto now = std::chrono::system_clock::now();
  auto duration = now.time_since_epoch();
  uint64_t nanos = std::chrono::duration_cast<std::chrono::nanoseconds>(duration).count();
  auto before_pos = file_.tellg();
  while (i < kMaxLines) {
    std::string line;
    std::getline(file_, line);

    if (file_.eof() || line.empty()) {
      file_.clear();
      auto after_pos = file_.tellg();
      if (after_pos == last_pos_) {
        LOG_EVERY_N(INFO, 100) << absl::Substitute("Reached EOF for file=$0 eof count=$1 pos=",
                                                   file_name_.string(), eof_count_)
                               << after_pos;
        eof_count_++;

        // TODO(ddlenano): Using a file's inode is a better way to detect file rotation. For now,
        // this will suffice.
        std::ifstream s(file_name_, std::ios::ate | std::ios::binary);
        if (s.tellg() < after_pos) {
          LOG(INFO) << "Detected file rotation, resetting file position";
          file_.close();
          file_.open(file_name_, std::ios::in);
        }
      }
      break;
    }

    transfer_fn(*this, nullptr, nanos, line);
    i++;
  }
  auto after_pos = file_.tellg();
  last_pos_ = after_pos;
  monitor_.AppendStreamStatusRecord(file_name_, after_pos - before_pos, "");
}

}  // namespace stirling
}  // namespace px
