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
#include <algorithm>
#include <map>
#include <vector>

#include <absl/strings/numbers.h>
#include "src/carnot/funcs/builtins/pipeline_ops.h"

namespace px {
namespace carnot {
namespace builtins {

void RegisterPipelineOpsOrDie(udf::Registry* registry) {
  CHECK(registry != nullptr);
  /*****************************************
   * Scalar UDFs.
   *****************************************/
  registry->RegisterOrDie<PipelineDestToName>("pipeline_dest_to_name");
}

}  // namespace builtins
}  // namespace carnot
}  // namespace px
