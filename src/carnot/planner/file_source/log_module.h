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
#include <memory>
#include <string>
#include <utility>
#include <vector>

#include "src/carnot/planner/compiler_state/compiler_state.h"
#include "src/carnot/planner/objects/funcobject.h"
#include "src/carnot/planner/objects/none_object.h"
#include "src/carnot/planner/probes/probes.h"

namespace px {
namespace carnot {
namespace planner {
namespace compiler {

class LogModule : public QLObject {
 public:
  static constexpr TypeDescriptor LogModuleType = {
      /* name */ "pxlog",
      /* type */ QLObjectType::kLogModule,
  };
  static StatusOr<std::shared_ptr<LogModule>> Create(MutationsIR* mutations_ir,
                                                       ASTVisitor* ast_visitor);

  // Constant for the modules.
  inline static constexpr char kLogModuleObjName[] = "pxlog";

  inline static constexpr char kFileSourceID[] = "FileSource";
  inline static constexpr char kFileSourceDocstring[] = R"doc(
  TBD
  )doc";

  inline static constexpr char kDeleteFileSourceID[] = "DeleteFileSource";
  inline static constexpr char kDeleteFileSourceDocstring[] = R"doc(
  TBD
  )doc";

 protected:
  explicit LogModule(MutationsIR* mutations_ir, ASTVisitor* ast_visitor)
      : QLObject(LogModuleType, ast_visitor), mutations_ir_(mutations_ir) {}
  Status Init();

 private:
  MutationsIR* mutations_ir_;
};

}  // namespace compiler
}  // namespace planner
}  // namespace carnot
}  // namespace px
