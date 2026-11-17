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

#include "src/stirling/obj_tools/go_pclntab.h"

namespace px {
namespace stirling {
namespace obj_tools {

StatusOr<std::unique_ptr<GoPclntab>> GoPclntab::Create(ElfReader* /*elf_reader*/) {
  return error::Unimplemented("GoPclntab is not implemented");
}

std::optional<std::string_view> GoPclntab::FuncNameForPC(uint64_t /*pc*/) const {
  return std::nullopt;
}

StatusOr<std::vector<ElfReader::SymbolInfo>> GoPclntab::ITabs() {
  return error::Unimplemented("GoPclntab::ITabs is not implemented");
}

}  // namespace obj_tools
}  // namespace stirling
}  // namespace px
