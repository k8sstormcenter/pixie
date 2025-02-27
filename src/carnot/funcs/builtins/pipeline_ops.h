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

#include "src/carnot/udf/registry.h"
#include "src/common/base/utils.h"
#include "src/shared/types/types.h"

namespace px::carnot::builtins {
// Forward declaration so enum_range can be specialized.
enum class SinkResultsDestType : uint64_t;

}  // namespace px::carot::builtins

template <>
struct magic_enum::customize::enum_range<px::carnot::builtins::SinkResultsDestType> {
  static constexpr int min = 1000;
  static constexpr int max = 11000;
};


namespace px {
namespace carnot {
namespace builtins {

enum class SinkResultsDestType : uint64_t {
  grpc_sink = 9100,
  otel_export = 9200,
  amqp_events = 10001, // TODO(ddelnano): This is set to not collide with the planpb::OperatorType enum
  cql_events,
  dns_events,
  http_events,
  kafka_events, // Won't work since table is suffixed with ".beta"
  mongodb_events,
  mux_events,
  mysql_events,
  nats_events, // Won't work since table is suffixed with ".beta"
  pgsql_events,
  redis_events,
};

class PipelineDestToName : public udf::ScalarUDF {
 public:
  StringValue Exec(FunctionContext*, Int64Value input) {
    auto protocol_events = magic_enum::enum_cast<SinkResultsDestType>(input.val);
    if (!protocol_events.has_value()) {
      return "unknown";
    }
    return std::string(magic_enum::enum_name(protocol_events.value()));
  }

  static udf::ScalarUDFDocBuilder Doc() {
    return udf::ScalarUDFDocBuilder(
               "Convert the destination ID from the sink_results table to a human-readable name.")
        .Details("TBD")
        .Example(R"doc(
df = px.DataFrame("sink_results)
df.dest = px.pipeline_dest_to_name(df.destination))doc")
        .Arg("dest", "The destination enum to covert.")
        .Returns("The human-readable name of the destination.");
  }
};

void RegisterPipelineOpsOrDie(udf::Registry* registry);

}  // namespace builtins
}  // namespace carnot
}  // namespace px
