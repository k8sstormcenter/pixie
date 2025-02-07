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
  auto elements = d.MemberCount() + 1;  // Add additional columns for time_
  BackedDataElements data_elements(elements);

  data_elements.emplace_back("time_", "", types::DataType::TIME64NS);
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

namespace {

StatusOr<std::pair<BackedDataElements, std::ifstream>> DataElementsFromFile(
    const std::filesystem::path& file_name) {
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
    return error::Internal("Unsupported file type: $0", extension);
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
      table_schema_(std::move(table_schema)) {}

Status FileSourceConnector::InitImpl() {
  sampling_freq_mgr_.set_period(kSamplingPeriod);
  push_freq_mgr_.set_period(kPushPeriod);
  /* auto callback_fn = absl::bind_front(&FileSourceConnector::HandleEvent, this); */
  return Status::OK();
}

Status FileSourceConnector::StopImpl() {
  LOG(INFO) << "Stopped called";
  file_.close();
  // check failbit
  /* if (file_.fail()) { */
  /*   return error::Internal("Failed to close file: $0 with error=$1", file_name_,
   * strerror(errno)); */
  /* } */
  return Status::OK();
}

constexpr int kMaxLines = 1000;

void FileSourceConnector::TransferDataImpl(ConnectorContext* /* ctx */) {
  DCHECK_EQ(data_tables_.size(), 1U) << "Only one table is allowed per FileSourceConnector.";
  int i = 0;
  rapidjson::Document d;

  auto now = std::chrono::system_clock::now();
  auto duration = now.time_since_epoch();
  auto nanos = std::chrono::duration_cast<std::chrono::nanoseconds>(duration).count();
  while (i < kMaxLines) {
    std::string line;
    std::getline(file_, line);

    if (file_.eof() || line.empty()) {
      file_.clear();
      LOG_EVERY_N(INFO, 100) << absl::Substitute("Reached EOF for file=$0 eof count=$1 pos=",
                                                 file_name_.string(), eof_count_) << file_.tellg();
      eof_count_++;
      return;
    }

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
      }
      const auto& value = d[key.c_str()];
      switch (column.type()) {
        case types::DataType::TIME64NS:
          r.Append(col, types::Time64NSValue(nanos));
          break;
        case types::DataType::INT64:
          r.Append(col, types::Int64Value(value.GetInt()));
          break;
        case types::DataType::FLOAT64:
          r.Append(col, types::Float64Value(value.GetDouble()));
          break;
        case types::DataType::STRING:
          r.Append(col, types::StringValue(value.GetString()));
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
    i++;
  }
}

}  // namespace stirling
}  // namespace px
