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

#include "src/common/base/base.h"
#include "src/stirling/core/canonical_types.h"
#include "src/stirling/core/output.h"
#include "src/stirling/core/source_connector.h"

namespace px {
namespace stirling {

// clang-format off
constexpr DataElement kStreamStatusElements[] = {
  canonical_data_elements::kTime,
  canonical_data_elements::kUPID,
  {"stream_id", "The ID of the stream of interest. For file source connector this is glob_pattern",
   types::DataType::STRING, types::SemanticType::ST_NONE, types::PatternType::GENERAL},
  {"bytes_sent", "The error messages of the deployment or event, if any",
   types::DataType::INT64, types::SemanticType::ST_BYTES, types::PatternType::METRIC_COUNTER},
  {"info", "Optional extra info provided as a JSON",
   types::DataType::STRING, types::SemanticType::ST_NONE, types::PatternType::GENERAL},
};

constexpr DataTableSchema kStreamStatusTable {
  "stream_status",
  "This table contains the status of streams Stirling is ingested across various source connectors",
  kStreamStatusElements
};

// clang-format on
DEFINE_PRINT_TABLE(StreamStatus);

}  // namespace stirling
}  // namespace px
