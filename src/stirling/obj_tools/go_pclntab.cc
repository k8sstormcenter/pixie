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

#include <algorithm>
#include <array>
#include <cstring>
#include <utility>

#include <absl/strings/match.h>
#include <absl/strings/str_cat.h>
#include <absl/strings/strip.h>

namespace px {
namespace stirling {
namespace obj_tools {

namespace {

constexpr uint32_t kGo118PclntabMagic = 0xfffffff0;
constexpr uint32_t kGo120PclntabMagic = 0xfffffff1;
constexpr size_t kPclntabHeaderSize = 72;
constexpr uint8_t kPtrSize = 8;

constexpr uint64_t kModuleDataFuncnametabOffset = 8;
constexpr uint64_t kModuleDataTextOffset = 176;
constexpr uint64_t kModuleDataTypesOffset = 296;
constexpr uint64_t kModuleDataEPclntabOffset = 328;
constexpr uint64_t kModuleDataITabLinksOffset = 376;
constexpr uint64_t kModuleDataITabLinksOffsetGo126 = 384;

constexpr uint64_t kTypeTFlagOffset = 20;
constexpr uint64_t kTypeStrOffset = 40;
constexpr uint8_t kTFlagExtraStar = 1 << 1;
constexpr uint64_t kInterfaceTypePkgPathOffset = 48;
constexpr uint64_t kInterfaceTypeMethodsLenOffset = 64;
constexpr uint64_t kITabTypeOffset = 8;
constexpr uint64_t kITabFunOffset = 24;

constexpr uint64_t kMaxITabs = 1 << 20;
constexpr uint64_t kMaxInterfaceMethods = 1 << 12;
constexpr uint64_t kMaxGoNameLength = 1 << 12;
constexpr size_t kScanWindow = 1 << 20;

constexpr std::string_view kUnreachableMethod = "runtime.unreachableMethod";
constexpr std::array<std::string_view, 2> kPclntabSections = {".gopclntab",
                                                              ".data.rel.ro.gopclntab"};
constexpr std::array<std::string_view, 3> kModuleDataSections = {".go.module", ".noptrdata",
                                                                 ".data"};

template <typename T>
T ReadInt(std::string_view buf, size_t offset) {
  T v;
  std::memcpy(&v, buf.data() + offset, sizeof(T));
  return v;
}

bool MayHoldModuleData(ELFIO::section* s) {
  return s->get_type() == ELFIO::SHT_PROGBITS && (s->get_flags() & ELFIO::SHF_ALLOC) != 0 &&
         (s->get_flags() & ELFIO::SHF_EXECINSTR) == 0 && s->get_size() != 0 &&
         !absl::StrContains(s->get_name(), "gopclntab");
}

std::optional<std::string> ReceiverFromMethod(std::string_view method) {
  size_t paren = method.find(".(");
  if (paren != std::string_view::npos) {
    size_t close = method.find(')', paren);
    if (close == std::string_view::npos) {
      return std::nullopt;
    }
    std::string_view recv = method.substr(paren + 2, close - paren - 2);
    absl::ConsumePrefix(&recv, "*");
    return absl::StrCat(method.substr(0, paren), ".", recv);
  }
  size_t dot = method.rfind('.');
  if (dot == std::string_view::npos) {
    return std::nullopt;
  }
  return std::string(method.substr(0, dot));
}

}  // namespace

StatusOr<std::unique_ptr<GoPclntab>> GoPclntab::Create(ElfReader* elf_reader) {
  std::unique_ptr<GoPclntab> pclntab(new GoPclntab(elf_reader));
  PX_RETURN_IF_ERROR(pclntab->Init());
  return pclntab;
}

Status GoPclntab::Init() {
  ELFIO::section* section = nullptr;
  for (std::string_view name : kPclntabSections) {
    auto section_or = elf_reader_->SectionWithName(name);
    if (section_or.ok()) {
      section = section_or.ValueOrDie();
      break;
    }
  }
  if (section == nullptr) {
    return error::NotFound("No .gopclntab section in binary=$0", elf_reader_->binary_path());
  }
  pclntab_addr_ = section->get_address();
  pclntab_size_ = section->get_size();
  const uint64_t file_offset = section->get_offset();

  file_.open(elf_reader_->binary_path(), std::ios::binary);
  if (!file_) {
    return error::Internal("Failed to open binary=$0", elf_reader_->binary_path());
  }

  PX_ASSIGN_OR_RETURN(std::string header, ReadAt(file_offset, kPclntabHeaderSize));
  magic_ = ReadInt<uint32_t>(header, 0);
  if (magic_ != kGo118PclntabMagic && magic_ != kGo120PclntabMagic) {
    return error::Unimplemented("Unsupported pclntab magic=$0 in binary=$1", absl::Hex(magic_),
                                elf_reader_->binary_path());
  }
  if (static_cast<uint8_t>(header[7]) != kPtrSize) {
    return error::Unimplemented("Unsupported pointer size=$0 in binary=$1",
                                static_cast<int>(header[7]), elf_reader_->binary_path());
  }
  const uint64_t nfunc = ReadInt<uint64_t>(header, 8);
  header_text_start_ = ReadInt<uint64_t>(header, 24);
  const uint64_t funcname_offset = ReadInt<uint64_t>(header, 32);
  const uint64_t cu_offset = ReadInt<uint64_t>(header, 40);
  const uint64_t pcln_offset = ReadInt<uint64_t>(header, 64);

  auto moduledata_or = FindModuleData(pclntab_addr_ + funcname_offset);
  if (moduledata_or.ok()) {
    moduledata_addr_ = moduledata_or.ValueOrDie();
  }

  if (header_text_start_ != 0) {
    text_start_ = header_text_start_;
  } else if (moduledata_addr_ != 0) {
    PX_ASSIGN_OR_RETURN(text_start_, ReadU64(moduledata_addr_ + kModuleDataTextOffset));
  } else {
    return error::NotFound("Could not locate runtime.firstmoduledata in binary=$0",
                           elf_reader_->binary_path());
  }

  return ReadFuncs(file_offset, nfunc, funcname_offset, cu_offset, pcln_offset);
}

Status GoPclntab::ReadFuncs(uint64_t file_offset, uint64_t nfunc, uint64_t funcname_offset,
                            uint64_t cu_offset, uint64_t pcln_offset) {
  if (cu_offset <= funcname_offset || cu_offset > pclntab_size_ || pcln_offset >= pclntab_size_ ||
      nfunc > (pclntab_size_ - pcln_offset) / 8) {
    return error::Internal("Malformed pclntab in binary=$0", elf_reader_->binary_path());
  }
  PX_ASSIGN_OR_RETURN(std::string functab, ReadAt(file_offset + pcln_offset, (nfunc + 1) * 8));
  PX_ASSIGN_OR_RETURN(std::string funcnames,
                      ReadAt(file_offset + funcname_offset, cu_offset - funcname_offset));

  funcs_.reserve(nfunc);
  for (uint64_t i = 0; i < nfunc; ++i) {
    const uint32_t entry = ReadInt<uint32_t>(functab, i * 8);
    const uint32_t func_offset = ReadInt<uint32_t>(functab, i * 8 + 4);
    const uint32_t next_entry = ReadInt<uint32_t>(functab, (i + 1) * 8);
    PX_ASSIGN_OR_RETURN(std::string func, ReadAt(file_offset + pcln_offset + func_offset, 8));
    const int32_t name_offset = ReadInt<int32_t>(func, 4);
    if (name_offset < 0 || static_cast<uint64_t>(name_offset) >= funcnames.size() ||
        next_entry < entry) {
      continue;
    }
    funcs_.push_back({std::string(funcnames.c_str() + name_offset), ELFIO::STT_FUNC,
                      text_start_ + entry, static_cast<uint64_t>(next_entry - entry)});
  }
  return Status::OK();
}

StatusOr<uint64_t> GoPclntab::FindModuleData(uint64_t funcnametab_addr) {
  std::string want(sizeof(uint64_t), '\0');
  std::memcpy(want.data(), &pclntab_addr_, sizeof(uint64_t));

  std::vector<ELFIO::section*> sections = elf_reader_->Sections();
  std::stable_partition(sections.begin(), sections.end(), [](ELFIO::section* s) {
    return std::find(kModuleDataSections.begin(), kModuleDataSections.end(), s->get_name()) !=
           kModuleDataSections.end();
  });

  for (ELFIO::section* s : sections) {
    if (!MayHoldModuleData(s)) {
      continue;
    }
    const uint64_t size = s->get_size();
    for (uint64_t pos = 0; pos < size;) {
      const size_t length = std::min<uint64_t>(kScanWindow, size - pos);
      PX_ASSIGN_OR_RETURN(std::string buf, ReadAt(s->get_offset() + pos, length));
      for (size_t i = buf.find(want); i != std::string::npos; i = buf.find(want, i + 1)) {
        const uint64_t candidate = s->get_address() + pos + i;
        if (candidate % sizeof(uint64_t) != 0) {
          continue;
        }
        auto funcnametab = ReadU64(candidate + kModuleDataFuncnametabOffset);
        if (funcnametab.ok() && funcnametab.ValueOrDie() == funcnametab_addr) {
          return candidate;
        }
      }
      if (pos + length >= size) {
        break;
      }
      pos += length - (want.size() - 1);
    }
  }
  return error::NotFound("runtime.firstmoduledata not found in binary=$0",
                         elf_reader_->binary_path());
}

StatusOr<std::string> GoPclntab::ReadAt(uint64_t file_offset, size_t length) {
  std::string buf(length, '\0');
  file_.clear();
  file_.seekg(file_offset);
  if (!file_.read(buf.data(), length)) {
    return error::Internal("Failed to read size=$0 bytes from offset=$1 in binary=$2", length,
                           file_offset, elf_reader_->binary_path());
  }
  return buf;
}

StatusOr<std::string> GoPclntab::ReadVirtual(uint64_t vaddr, size_t length) {
  PX_ASSIGN_OR_RETURN(uint64_t file_offset, elf_reader_->VirtualAddrToBinaryAddr(vaddr));
  return ReadAt(file_offset, length);
}

StatusOr<uint64_t> GoPclntab::ReadU64(uint64_t vaddr) {
  PX_ASSIGN_OR_RETURN(std::string buf, ReadVirtual(vaddr, sizeof(uint64_t)));
  return ReadInt<uint64_t>(buf, 0);
}

StatusOr<std::string> GoPclntab::ReadGoName(uint64_t vaddr) {
  PX_ASSIGN_OR_RETURN(std::string header, ReadVirtual(vaddr, 1 + 10));
  uint64_t length = 0;
  size_t i = 1;
  int shift = 0;
  bool complete = false;
  while (i < header.size()) {
    const uint8_t b = static_cast<uint8_t>(header[i++]);
    length |= static_cast<uint64_t>(b & 0x7f) << shift;
    if ((b & 0x80) == 0) {
      complete = true;
      break;
    }
    shift += 7;
  }
  if (!complete || length > kMaxGoNameLength) {
    return error::Internal("Invalid Go name at address=$0", vaddr);
  }
  return ReadVirtual(vaddr + i, length);
}

StatusOr<std::string> GoPclntab::TypeName(uint64_t type_addr, uint64_t types_addr) {
  PX_ASSIGN_OR_RETURN(std::string type, ReadVirtual(type_addr, kTypeStrOffset + sizeof(int32_t)));
  const uint8_t tflag = static_cast<uint8_t>(type[kTypeTFlagOffset]);
  const int32_t str = ReadInt<int32_t>(type, kTypeStrOffset);
  PX_ASSIGN_OR_RETURN(std::string name, ReadGoName(types_addr + str));
  if ((tflag & kTFlagExtraStar) != 0 && absl::StartsWith(name, "*")) {
    name.erase(0, 1);
  }
  return name;
}

StatusOr<std::string> GoPclntab::InterfaceName(uint64_t inter_addr, uint64_t types_addr) {
  PX_ASSIGN_OR_RETURN(std::string name, TypeName(inter_addr, types_addr));
  PX_ASSIGN_OR_RETURN(uint64_t pkg_path_addr, ReadU64(inter_addr + kInterfaceTypePkgPathOffset));
  if (pkg_path_addr == 0) {
    return name;
  }
  PX_ASSIGN_OR_RETURN(std::string pkg_path, ReadGoName(pkg_path_addr));
  if (pkg_path.empty()) {
    return name;
  }
  size_t dot = name.rfind('.', name.find('['));
  if (dot == std::string::npos) {
    return name;
  }
  return absl::StrCat(pkg_path, name.substr(dot));
}

std::optional<std::string> GoPclntab::ITabReceiver(uint64_t itab_addr, uint64_t inter_addr) {
  auto num_methods = ReadU64(inter_addr + kInterfaceTypeMethodsLenOffset);
  if (!num_methods.ok() || num_methods.ValueOrDie() > kMaxInterfaceMethods) {
    return std::nullopt;
  }
  for (uint64_t k = 0; k < num_methods.ValueOrDie(); ++k) {
    auto fun = ReadU64(itab_addr + kITabFunOffset + k * sizeof(uint64_t));
    if (!fun.ok()) {
      return std::nullopt;
    }
    std::optional<std::string_view> method = FuncNameForPC(fun.ValueOrDie());
    if (!method.has_value() || method.value() == kUnreachableMethod) {
      continue;
    }
    return ReceiverFromMethod(method.value());
  }
  return std::nullopt;
}

std::optional<std::string_view> GoPclntab::FuncNameForPC(uint64_t pc) const {
  auto it =
      std::upper_bound(funcs_.begin(), funcs_.end(), pc,
                       [](uint64_t pc, const ElfReader::SymbolInfo& f) { return pc < f.address; });
  if (it == funcs_.begin()) {
    return std::nullopt;
  }
  --it;
  if (pc >= it->address + it->size) {
    return std::nullopt;
  }
  return std::string_view(it->name);
}

StatusOr<std::vector<ElfReader::SymbolInfo>> GoPclntab::ITabs() {
  if (magic_ != kGo120PclntabMagic) {
    return error::Unimplemented("Recovering itabs requires Go 1.20 or later, binary=$0",
                                elf_reader_->binary_path());
  }
  if (moduledata_addr_ == 0) {
    return error::NotFound("runtime.firstmoduledata not found in binary=$0",
                           elf_reader_->binary_path());
  }
  PX_ASSIGN_OR_RETURN(uint64_t types_addr, ReadU64(moduledata_addr_ + kModuleDataTypesOffset));
  PX_ASSIGN_OR_RETURN(uint64_t epclntab, ReadU64(moduledata_addr_ + kModuleDataEPclntabOffset));
  const uint64_t links_offset = epclntab == pclntab_addr_ + pclntab_size_
                                    ? kModuleDataITabLinksOffsetGo126
                                    : kModuleDataITabLinksOffset;
  PX_ASSIGN_OR_RETURN(uint64_t links_addr, ReadU64(moduledata_addr_ + links_offset));
  PX_ASSIGN_OR_RETURN(uint64_t num_links, ReadU64(moduledata_addr_ + links_offset + 8));
  if (num_links > kMaxITabs) {
    return error::Internal("Implausible itab count=$0 in binary=$1", num_links,
                           elf_reader_->binary_path());
  }
  PX_ASSIGN_OR_RETURN(std::string links, ReadVirtual(links_addr, num_links * sizeof(uint64_t)));

  std::vector<ElfReader::SymbolInfo> itabs;
  for (uint64_t k = 0; k < num_links; ++k) {
    const uint64_t itab_addr = ReadInt<uint64_t>(links, k * sizeof(uint64_t));
    auto inter_addr = ReadU64(itab_addr);
    auto type_addr = ReadU64(itab_addr + kITabTypeOffset);
    if (!inter_addr.ok() || !type_addr.ok()) {
      continue;
    }
    std::optional<std::string> receiver = ITabReceiver(itab_addr, inter_addr.ValueOrDie());
    auto type_name = TypeName(type_addr.ValueOrDie(), types_addr);
    auto inter_name = InterfaceName(inter_addr.ValueOrDie(), types_addr);
    auto num_methods = ReadU64(inter_addr.ValueOrDie() + kInterfaceTypeMethodsLenOffset);
    if (!receiver.has_value() || !type_name.ok() || !inter_name.ok() || !num_methods.ok()) {
      continue;
    }
    std::string name =
        absl::StrCat("go:itab.", absl::StartsWith(type_name.ValueOrDie(), "*") ? "*" : "",
                     receiver.value(), ",", inter_name.ValueOrDie());
    itabs.push_back({std::move(name), ELFIO::STT_OBJECT, itab_addr,
                     kITabFunOffset + num_methods.ValueOrDie() * sizeof(uint64_t)});
  }
  return itabs;
}

}  // namespace obj_tools
}  // namespace stirling
}  // namespace px
