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

#include "src/carnot/planner/file_source/log_module.h"

namespace px {
namespace carnot {
namespace planner {
namespace compiler {

class FileSourceHandler {
 public:
  static StatusOr<QLObjectPtr> Eval(MutationsIR* mutations_ir, const pypa::AstPtr& ast,
                                    const ParsedArgs& args, ASTVisitor* visitor);
};

class DeleteFileSourceHandler {
 public:
  static StatusOr<QLObjectPtr> Eval(MutationsIR* mutations_ir, const pypa::AstPtr& ast,
                                    const ParsedArgs& args, ASTVisitor* visitor);
};

StatusOr<std::shared_ptr<LogModule>> LogModule::Create(MutationsIR* mutations_ir,
                                                           ASTVisitor* ast_visitor) {
  auto tracing_module = std::shared_ptr<LogModule>(new LogModule(mutations_ir, ast_visitor));
  PX_RETURN_IF_ERROR(tracing_module->Init());
  return tracing_module;
}

Status LogModule::Init() {
  PX_ASSIGN_OR_RETURN(
      std::shared_ptr<FuncObject> upsert_fn,
      FuncObject::Create(kFileSourceID, {"glob_pattern", "table_name", "ttl"}, {},
                         /* has_variable_len_args */ false,
                         /* has_variable_len_kwargs */ false,
                         std::bind(FileSourceHandler::Eval, mutations_ir_, std::placeholders::_1,
                                   std::placeholders::_2, std::placeholders::_3),
                         ast_visitor()));
  PX_RETURN_IF_ERROR(upsert_fn->SetDocString(kFileSourceDocstring));
  AddMethod(kFileSourceID, upsert_fn);

  PX_ASSIGN_OR_RETURN(
      std::shared_ptr<FuncObject> delete_fn,
      FuncObject::Create(kFileSourceID, {"name"}, {},
                         /* has_variable_len_args */ false,
                         /* has_variable_len_kwargs */ false,
                         std::bind(DeleteFileSourceHandler::Eval, mutations_ir_, std::placeholders::_1,
                                   std::placeholders::_2, std::placeholders::_3),
                         ast_visitor()));
  PX_RETURN_IF_ERROR(upsert_fn->SetDocString(kDeleteFileSourceDocstring));
  AddMethod(kDeleteFileSourceID, delete_fn);

  return Status::OK();
}

StatusOr<QLObjectPtr> FileSourceHandler::Eval(MutationsIR* mutations_ir, const pypa::AstPtr& ast,
                                          const ParsedArgs& args, ASTVisitor* visitor) {
  DCHECK(mutations_ir);

  PX_ASSIGN_OR_RETURN(auto glob_pattern_ir, GetArgAs<StringIR>(ast, args, "glob_pattern"));
  PX_ASSIGN_OR_RETURN(auto table_name_ir, GetArgAs<StringIR>(ast, args, "table_name"));
  PX_ASSIGN_OR_RETURN(auto ttl_ir, GetArgAs<StringIR>(ast, args, "ttl"));

  const std::string& glob_pattern_str = glob_pattern_ir->str();
  const std::string& table_name_str = table_name_ir->str();
  PX_ASSIGN_OR_RETURN(int64_t ttl_ns, StringToTimeInt(ttl_ir->str()));

  mutations_ir->CreateFileSourceDeployment(glob_pattern_str, table_name_str, ttl_ns);

  return std::static_pointer_cast<QLObject>(std::make_shared<NoneObject>(ast, visitor));
}

StatusOr<QLObjectPtr> DeleteFileSourceHandler::Eval(MutationsIR* mutations_ir, const pypa::AstPtr& ast,
                                          const ParsedArgs& args, ASTVisitor* visitor) {
  DCHECK(mutations_ir);

  PX_ASSIGN_OR_RETURN(auto glob_pattern_ir, GetArgAs<StringIR>(ast, args, "name"));
  const std::string& glob_pattern_str = glob_pattern_ir->str();

  mutations_ir->DeleteFileSource(glob_pattern_str);

  return std::static_pointer_cast<QLObject>(std::make_shared<NoneObject>(ast, visitor));
}

}  // namespace compiler
}  // namespace planner
}  // namespace carnot
}  // namespace px
