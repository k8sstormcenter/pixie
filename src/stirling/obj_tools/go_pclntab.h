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
#include <optional>
#include <string>
#include <string_view>
#include <vector>

#include <absl/container/flat_hash_map.h>
#include <absl/container/node_hash_map.h>

#include "src/common/base/base.h"
#include "src/stirling/obj_tools/elf_reader.h"

namespace px {
namespace stirling {
namespace obj_tools {

class GoPclntab {
 public:
  static StatusOr<std::unique_ptr<GoPclntab>> Create(ElfReader* elf_reader);

  uint64_t header_text_start() const { return header_text_start_; }

  uint64_t text_start() const { return text_start_; }

  uint64_t moduledata_addr() const { return moduledata_addr_; }

  const std::vector<ElfReader::SymbolInfo>& funcs() const { return funcs_; }

  StatusOr<std::vector<ElfReader::SymbolInfo>> ITabs();

 private:
  explicit GoPclntab(ElfReader* elf_reader) : elf_reader_(elf_reader) {}

  Status Init();
  StatusOr<std::string> LocatePclntab();
  Status ReadFuncs(std::string_view pclntab, uint64_t nfunc, uint64_t funcname_offset,
                   uint64_t cu_offset, uint64_t pcln_offset);
  StatusOr<uint64_t> FindModuleData(uint64_t funcnametab_addr);
  bool IsModuleData(uint64_t candidate, uint64_t funcnametab_addr);
  Status LoadRelativeRelocations();
  ELFIO::section* DataSectionAt(uint64_t vaddr) const;
  StatusOr<std::string_view> SectionBlock(ELFIO::section* section, uint64_t index);
  StatusOr<std::string> ReadVirtual(uint64_t vaddr, size_t length);
  StatusOr<uint64_t> ReadU64(uint64_t vaddr);
  StatusOr<std::string> ReadGoName(uint64_t vaddr);
  StatusOr<std::string> TypeName(uint64_t type_addr, uint64_t types_addr);
  StatusOr<std::string> InterfaceName(uint64_t inter_addr, uint64_t types_addr);
  std::optional<std::string> ITabReceiver(uint64_t itab_addr, uint64_t inter_addr);
  StatusOr<std::vector<uint64_t>> ITabAddresses(uint64_t types_addr);

  ElfReader* elf_reader_;
  uint32_t magic_ = 0;
  uint64_t pclntab_addr_ = 0;
  uint64_t pclntab_size_ = 0;
  uint64_t header_text_start_ = 0;
  uint64_t text_start_ = 0;
  uint64_t moduledata_addr_ = 0;
  std::vector<ElfReader::SymbolInfo> funcs_;
  bool relocations_loaded_ = false;
  absl::flat_hash_map<uint64_t, uint64_t> relative_relocations_;
  std::vector<ELFIO::section*> data_sections_;
  absl::node_hash_map<uint64_t, std::string> blocks_;
};

}  // namespace obj_tools
}  // namespace stirling
}  // namespace px
