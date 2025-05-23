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

#include "src/stirling/source_connectors/file_source/file_source_connector.h"

#include <rapidjson/document.h>
#include <rapidjson/error/en.h>

#include <string>
#include <utility>

using px::StatusOr;

constexpr size_t kMaxStringBytes = std::numeric_limits<size_t>::max();

namespace px {
namespace stirling {

using px::utils::RapidJSONTypeToString;

StatusOr<BackedDataElements> DataElementsFromJSON(std::ifstream& f_stream) {
  std::string line;
  std::getline(f_stream, line);

  if (f_stream.eof()) {
    return error::Internal("Failed to read file, hit EOF");
  }

  rapidjson::Document d;
  rapidjson::ParseResult ok = d.Parse(line.c_str());
  if (!ok) {
    return error::Internal("Failed to parse JSON: $0 $1", line,
                           rapidjson::GetParseError_En(ok.Code()));
  }
  auto elements = d.MemberCount() + 2;  // Add additional columns for time_
  BackedDataElements data_elements(elements);

  data_elements.emplace_back("time_", "", types::DataType::TIME64NS);
  // TODO(ddelnano): Make it configurable to request a UUID in PxL rather than creating it by default.
  data_elements.emplace_back("uuid", "", types::DataType::UINT128);
  for (rapidjson::Value::ConstMemberIterator itr = d.MemberBegin(); itr != d.MemberEnd(); ++itr) {
    auto name = itr->name.GetString();
    const auto& value = itr->value;
    types::DataType col_type;

    if (value.IsInt()) {
      col_type = types::DataType::INT64;
    } else if (value.IsDouble()) {
      col_type = types::DataType::FLOAT64;
    } else if (value.IsString()) {
      col_type = types::DataType::STRING;
    } else if (value.IsBool()) {
      col_type = types::DataType::BOOLEAN;
    } else if (value.IsObject()) {
      col_type = types::DataType::STRING;
    } else if (value.IsArray()) {
      col_type = types::DataType::STRING;
    } else {
      return error::Internal("Unable to parse JSON key '$0': unsupported type: $1", name,
                             RapidJSONTypeToString(itr->value.GetType()));
    }
    data_elements.emplace_back(name, "", col_type);
  }

  return data_elements;
}

StatusOr<BackedDataElements> DataElementsFromCSV(std::ifstream& file_name) {
  PX_UNUSED(file_name);
  return BackedDataElements(0);
}

StatusOr<BackedDataElements> DataElementsForUnstructuredFile() {
  BackedDataElements data_elements(3);
  data_elements.emplace_back("time_", "", types::DataType::TIME64NS);
  data_elements.emplace_back("uuid", "", types::DataType::UINT128);
  data_elements.emplace_back("raw_line", "", types::DataType::STRING);
  return data_elements;
}

namespace {

StatusOr<std::pair<BackedDataElements, std::ifstream>> DataElementsFromFile(
    const std::filesystem::path& file_name, bool allow_unstructured = true) {
  auto f = std::ifstream(file_name.string());
  if (!f.is_open()) {
    return error::Internal("Failed to open file: $0 with error=$1", file_name.string(),
                           strerror(errno));
  }

  // get the file extension of the file
  auto extension = file_name.extension().string();
  BackedDataElements data_elements;
  if (extension == ".csv") {
    PX_ASSIGN_OR_RETURN(data_elements, DataElementsFromCSV(f));
  } else if (extension == ".json") {
    PX_ASSIGN_OR_RETURN(data_elements, DataElementsFromJSON(f));
  } else {
    if (allow_unstructured) {
      LOG(WARNING) << absl::Substitute("Unsupported file type: $0, treating each line as a single column", extension);
      PX_ASSIGN_OR_RETURN(data_elements, DataElementsForUnstructuredFile());
    } else {
      // TODO(ddelnano): If file extension is blank this isn't a helpful error message.
      return error::Internal("Unsupported file type: $0", extension);
    }
  }

  f.seekg(0, std::ios::beg);
  return std::make_pair(std::move(data_elements), std::move(f));
}

}  // namespace

StatusOr<std::unique_ptr<SourceConnector>> FileSourceConnector::Create(
    std::string_view source_name, const std::filesystem::path file_name) {
  auto host_path = px::system::Config::GetInstance().ToHostPath(file_name);
  PX_ASSIGN_OR_RETURN(auto data_elements_and_file, DataElementsFromFile(host_path));
  auto& [data_elements, file] = data_elements_and_file;

  // Get just the filename and extension
  auto name = host_path.filename().string();
  std::unique_ptr<DynamicDataTableSchema> table_schema =
      DynamicDataTableSchema::Create(name, "", std::move(data_elements));
  return std::unique_ptr<SourceConnector>(new FileSourceConnector(
      source_name, std::move(host_path), std::move(file), std::move(table_schema)));
}

FileSourceConnector::FileSourceConnector(std::string_view source_name,
                                         const std::filesystem::path& file_name, std::ifstream file,
                                         std::unique_ptr<DynamicDataTableSchema> table_schema)
    : SourceConnector(source_name, ArrayView<DataTableSchema>(&table_schema->Get(), 1)),
      name_(source_name),
      file_name_(file_name),
      file_(std::move(file)),
      table_schema_(std::move(table_schema)),
      transfer_specs_({
          {".json", {&FileSourceConnector::TransferDataFromJSON}},
          {".csv", {&FileSourceConnector::TransferDataFromCSV}},
          {"", {&FileSourceConnector::TransferDataFromUnstructuredFile}},
          {".log", {&FileSourceConnector::TransferDataFromUnstructuredFile}},
      }) {}

Status FileSourceConnector::InitImpl() {
  sampling_freq_mgr_.set_period(kSamplingPeriod);
  push_freq_mgr_.set_period(kPushPeriod);
  return Status::OK();
}

Status FileSourceConnector::StopImpl() {
  file_.close();
  return Status::OK();
}

constexpr int kMaxLines = 1000;

void FileSourceConnector::TransferDataFromJSON(DataTable::DynamicRecordBuilder* /*r*/,
                                               uint64_t nanos, const std::string& line) {
  rapidjson::Document d;
  rapidjson::ParseResult ok = d.Parse(line.c_str());
  if (!ok) {
    LOG(ERROR) << absl::Substitute("Failed to parse JSON: $0 $1", line,
                                   rapidjson::GetParseError_En(ok.Code()));
    return;
  }
  DataTable::DynamicRecordBuilder r(data_tables_[0]);
  const auto& columns = table_schema_->Get().elements();

  for (size_t col = 0; col < columns.size(); col++) {
    const auto& column = columns[col];
    std::string key(column.name());
    // time_ is inserted by stirling and not within the polled file
    if (key == "time_") {
      r.Append(col, types::Time64NSValue(nanos));
      continue;
    } else if (key == "uuid") {
      sole::uuid u = sole::uuid4();
      r.Append(col, types::UInt128Value(u.ab, u.cd));
      continue;
    }
    const auto& value = d[key.c_str()];
    switch (column.type()) {
      case types::DataType::INT64:
        r.Append(col, types::Int64Value(value.GetInt()));
        break;
      case types::DataType::FLOAT64:
        r.Append(col, types::Float64Value(value.GetDouble()));
        break;
      case types::DataType::STRING:
        if (value.IsArray() || value.IsObject()) {
          rapidjson::StringBuffer buffer;
          rapidjson::Writer<rapidjson::StringBuffer> writer(buffer);
          value.Accept(writer);
          r.Append(col, types::StringValue(buffer.GetString()), kMaxStringBytes);
        } else {
          r.Append(col, types::StringValue(value.GetString()), kMaxStringBytes);
        }
        break;
      case types::DataType::BOOLEAN:
        r.Append(col, types::BoolValue(value.GetBool()));
        break;
      default:
        LOG(FATAL) << absl::Substitute(
            "Failed to insert field into DataTable: unsupported type '$0'",
            types::DataType_Name(column.type()));
    }
  }
  return;
}

void FileSourceConnector::TransferDataFromUnstructuredFile(DataTable::DynamicRecordBuilder* /*r*/,
                                               uint64_t nanos, const std::string& line) {
  DataTable::DynamicRecordBuilder r(data_tables_[0]);
  r.Append(0, types::Time64NSValue(nanos));
  sole::uuid u = sole::uuid4();
  r.Append(1, types::UInt128Value(u.ab, u.cd));
  r.Append(2, types::StringValue(line), kMaxStringBytes);
  return;
}

void FileSourceConnector::TransferDataFromCSV(DataTable::DynamicRecordBuilder* r, uint64_t nanos,
                                              const std::string& line) {
  PX_UNUSED(r);
  PX_UNUSED(nanos);
  PX_UNUSED(line);
  return;
}

void FileSourceConnector::TransferDataImpl(ConnectorContext* /* ctx */) {
  DCHECK_EQ(data_tables_.size(), 1U) << "Only one table is allowed per FileSourceConnector.";
  int i = 0;
  auto extension = file_name_.extension().string();
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
