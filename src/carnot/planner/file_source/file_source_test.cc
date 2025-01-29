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

#include "src/carnot/planner/probes/probes.h"
#include "src/carnot/planner/compiler/ast_visitor.h"
#include "src/carnot/planner/compiler/test_utils.h"

namespace px {
namespace carnot {
namespace planner {
namespace compiler {
using ::testing::ContainsRegex;
using ::testing::Not;
using ::testing::UnorderedElementsAre;

constexpr char kSingleFileSource[] = R"pxl(
import pxlog

glob_pattern = 'test.json'
pxlog.FileSource(glob_pattern, 'test_table', '5m')
)pxl";

constexpr char kSingleFileSourceProgramPb[] = R"pxl(
glob_pattern: "test.json"
table_name: "test_table"
ttl {
  seconds: 300
}
)pxl";

class FileSourceCompilerTest : public ASTVisitorTest {
 protected:
  StatusOr<std::shared_ptr<compiler::MutationsIR>> CompileFileSourceScript(
      std::string_view query, const ExecFuncs& exec_funcs = {}) {
    absl::flat_hash_set<std::string> reserved_names;
    for (const auto& func : exec_funcs) {
      reserved_names.insert(func.output_table_prefix());
    }
    auto func_based_exec = exec_funcs.size() > 0;

    Parser parser;
    PX_ASSIGN_OR_RETURN(auto ast, parser.Parse(query));

    std::shared_ptr<IR> ir = std::make_shared<IR>();
    std::shared_ptr<compiler::MutationsIR> mutation_ir = std::make_shared<compiler::MutationsIR>();

    ModuleHandler module_handler;
    PX_ASSIGN_OR_RETURN(auto ast_walker, compiler::ASTVisitorImpl::Create(
                                             ir.get(), mutation_ir.get(), compiler_state_.get(),
                                             &module_handler, func_based_exec, reserved_names, {}));

    PX_RETURN_IF_ERROR(ast_walker->ProcessModuleNode(ast));
    if (func_based_exec) {
      PX_RETURN_IF_ERROR(ast_walker->ProcessExecFuncs(exec_funcs));
    }
    return mutation_ir;
  }
};

// TODO(ddelnano): Add test that verifies missing arguments provides a compiler error
// instead of the "Query should not be empty" error. There seems to be a bug where default
// arguments are not being handled correctly.

TEST_F(FileSourceCompilerTest, parse_single_file_source) {
  ASSERT_OK_AND_ASSIGN(auto mutation_ir, CompileFileSourceScript(kSingleFileSource));
  plannerpb::CompileMutationsResponse pb;
  EXPECT_OK(mutation_ir->ToProto(&pb));
  ASSERT_EQ(pb.mutations_size(), 1);
  EXPECT_THAT(pb.mutations()[0].file_source(), testing::proto::EqualsProto(kSingleFileSourceProgramPb));
}

}  // namespace compiler
}  // namespace planner
}  // namespace carnot
}  // namespace px
