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
#include <iterator>
#include <utility>

#include <absl/strings/match.h>
#include <absl/strings/str_cat.h>
#include <absl/strings/strip.h>

#include "src/stirling/utils/binary_decoder.h"

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
constexpr uint64_t kModuleDataITabOffsetGo127 = 320;
constexpr uint64_t kModuleDataEPclntabOffsetGo127 = 352;

constexpr uint64_t kTypeTFlagOffset = 20;
constexpr uint64_t kTypeStrOffset = 40;
constexpr uint8_t kTFlagExtraStar = 1 << 1;
constexpr uint64_t kInterfaceTypePkgPathOffset = 48;
constexpr uint64_t kInterfaceTypeMethodsLenOffset = 64;
constexpr uint64_t kITabTypeOffset = 8;
constexpr uint64_t kITabFunOffset = 24;
constexpr uint64_t kITabHeaderSize = 32;

constexpr uint64_t kMaxITabs = 1 << 20;
constexpr uint64_t kMaxITabTableSize = 1 << 28;
constexpr uint64_t kMaxInterfaceMethods = 1 << 12;
constexpr uint64_t kMaxGoNameLength = 1 << 12;
constexpr size_t kScanWindow = 1 << 20;
constexpr uint64_t kReadBlockSize = 1 << 16;

constexpr std::string_view kUnreachableMethod = "runtime.unreachableMethod";
constexpr size_t kRelaEntrySize = 24;
constexpr uint32_t kX86_64RelativeReloc = 8;
constexpr uint32_t kAArch64RelativeReloc = 1027;
constexpr std::array<std::string_view, 2> kPclntabSections = {".gopclntab",
                                                              ".data.rel.ro.gopclntab"};
constexpr std::string_view kEmbeddedPclntabSection = ".data.rel.ro";
constexpr std::array<std::string_view, 3> kModuleDataSections = {".go.module", ".noptrdata",
                                                                 ".data"};

bool MayHoldModuleData(ELFIO::section* s) {
  return s->get_type() == ELFIO::SHT_PROGBITS && (s->get_flags() & ELFIO::SHF_ALLOC) != 0 &&
         (s->get_flags() & ELFIO::SHF_EXECINSTR) == 0 && s->get_size() != 0 &&
         !absl::StrContains(s->get_name(), "gopclntab");
}

bool IsPclntabHeader(std::string_view header) {
  const uint32_t magic = ::px::utils::LEndianBytesToInt<uint32_t>(header);
  if (magic != kGo118PclntabMagic && magic != kGo120PclntabMagic) {
    return false;
  }
  const uint8_t quantum = header[6];
  if (header[4] != 0 || header[5] != 0 || (quantum != 1 && quantum != 2 && quantum != 4) ||
      static_cast<uint8_t>(header[7]) != kPtrSize) {
    return false;
  }
  const uint64_t nfunc = ::px::utils::LEndianBytesToInt<uint64_t>(header.substr(8));
  uint64_t prev = kPclntabHeaderSize - 1;
  for (size_t pos = 32; pos < kPclntabHeaderSize; pos += 8) {
    const uint64_t offset = ::px::utils::LEndianBytesToInt<uint64_t>(header.substr(pos));
    if (offset <= prev) {
      return false;
    }
    prev = offset;
  }
  return nfunc != 0;
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

StatusOr<std::string> GoPclntab::LocatePclntab() {
  for (std::string_view name : kPclntabSections) {
    auto section_or = elf_reader_->SectionWithName(name);
    if (section_or.ok()) {
      ELFIO::section* section = section_or.ValueOrDie();
      pclntab_addr_ = section->get_address();
      pclntab_size_ = section->get_size();
      return elf_reader_->BinaryByteCode<char>(section->get_offset(), pclntab_size_);
    }
  }

  auto relro_or = elf_reader_->SectionWithName(kEmbeddedPclntabSection);
  if (relro_or.ok()) {
    ELFIO::section* section = relro_or.ValueOrDie();
    PX_ASSIGN_OR_RETURN(std::string relro, elf_reader_->BinaryByteCode<char>(section->get_offset(),
                                                                             section->get_size()));
    for (size_t pos = 0; pos + kPclntabHeaderSize <= relro.size(); pos += sizeof(uint64_t)) {
      if (IsPclntabHeader(std::string_view(relro).substr(pos, kPclntabHeaderSize))) {
        pclntab_addr_ = section->get_address() + pos;
        pclntab_size_ = relro.size() - pos;
        return relro.substr(pos);
      }
    }
  }
  return error::NotFound("No .gopclntab section in binary=$0", elf_reader_->binary_path());
}

Status GoPclntab::Init() {
  for (ELFIO::section* s : elf_reader_->Sections()) {
    if ((s->get_flags() & ELFIO::SHF_ALLOC) != 0 && s->get_type() != ELFIO::SHT_NOBITS &&
        s->get_size() != 0) {
      data_sections_.push_back(s);
    }
  }
  std::sort(data_sections_.begin(), data_sections_.end(),
            [](ELFIO::section* a, ELFIO::section* b) { return a->get_address() < b->get_address(); });

  PX_ASSIGN_OR_RETURN(std::string pclntab, LocatePclntab());
  if (pclntab_size_ < kPclntabHeaderSize) {
    return error::Internal("Malformed pclntab in binary=$0", elf_reader_->binary_path());
  }

  std::string_view header = std::string_view(pclntab).substr(0, kPclntabHeaderSize);
  magic_ = ::px::utils::LEndianBytesToInt<uint32_t>(header);
  if (magic_ != kGo118PclntabMagic && magic_ != kGo120PclntabMagic) {
    return error::Unimplemented("Unsupported pclntab magic=$0 in binary=$1", absl::Hex(magic_),
                                elf_reader_->binary_path());
  }
  if (static_cast<uint8_t>(header[7]) != kPtrSize) {
    return error::Unimplemented("Unsupported pointer size=$0 in binary=$1",
                                static_cast<int>(header[7]), elf_reader_->binary_path());
  }
  const uint64_t nfunc = ::px::utils::LEndianBytesToInt<uint64_t>(header.substr(8));
  header_text_start_ = ::px::utils::LEndianBytesToInt<uint64_t>(header.substr(24));
  const uint64_t funcname_offset = ::px::utils::LEndianBytesToInt<uint64_t>(header.substr(32));
  const uint64_t cu_offset = ::px::utils::LEndianBytesToInt<uint64_t>(header.substr(40));
  const uint64_t pcln_offset = ::px::utils::LEndianBytesToInt<uint64_t>(header.substr(64));

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

  return ReadFuncs(pclntab, nfunc, funcname_offset, cu_offset, pcln_offset);
}

Status GoPclntab::ReadFuncs(std::string_view pclntab, uint64_t nfunc, uint64_t funcname_offset,
                            uint64_t cu_offset, uint64_t pcln_offset) {
  if (cu_offset <= funcname_offset || cu_offset > pclntab.size() || pcln_offset >= pclntab.size() ||
      nfunc >= (pclntab.size() - pcln_offset) / 8) {
    return error::Internal("Malformed pclntab in binary=$0", elf_reader_->binary_path());
  }
  std::string_view functab = pclntab.substr(pcln_offset);
  std::string_view funcnames = pclntab.substr(funcname_offset, cu_offset - funcname_offset);

  funcs_.reserve(nfunc);
  for (uint64_t i = 0; i < nfunc; ++i) {
    const uint32_t entry = ::px::utils::LEndianBytesToInt<uint32_t>(functab.substr(i * 8));
    const uint32_t func_offset =
        ::px::utils::LEndianBytesToInt<uint32_t>(functab.substr(i * 8 + 4));
    const uint32_t next_entry =
        ::px::utils::LEndianBytesToInt<uint32_t>(functab.substr((i + 1) * 8));
    if (func_offset > functab.size() - 8 || next_entry < entry) {
      continue;
    }
    const int32_t name_offset =
        ::px::utils::LEndianBytesToInt<int32_t>(functab.substr(func_offset + 4));
    if (name_offset < 0 || static_cast<uint64_t>(name_offset) >= funcnames.size()) {
      continue;
    }
    std::string_view name = funcnames.substr(name_offset);
    funcs_.push_back({std::string(name.substr(0, name.find('\0'))), ELFIO::STT_FUNC,
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
      PX_ASSIGN_OR_RETURN(std::string buf,
                          elf_reader_->BinaryByteCode<char>(s->get_offset() + pos, length));
      for (size_t i = buf.find(want); i != std::string::npos; i = buf.find(want, i + 1)) {
        const uint64_t candidate = s->get_address() + pos + i;
        if (IsModuleData(candidate, funcnametab_addr)) {
          return candidate;
        }
      }
      if (pos + length >= size) {
        break;
      }
      pos += length - (want.size() - 1);
    }
  }

  PX_RETURN_IF_ERROR(LoadRelativeRelocations());
  for (const auto& [offset, addend] : relative_relocations_) {
    if (addend == pclntab_addr_ && IsModuleData(offset, funcnametab_addr)) {
      return offset;
    }
  }
  return error::NotFound("runtime.firstmoduledata not found in binary=$0",
                         elf_reader_->binary_path());
}

bool GoPclntab::IsModuleData(uint64_t candidate, uint64_t funcnametab_addr) {
  if (candidate % sizeof(uint64_t) != 0) {
    return false;
  }
  auto funcnametab = ReadU64(candidate + kModuleDataFuncnametabOffset);
  return funcnametab.ok() && funcnametab.ValueOrDie() == funcnametab_addr;
}

Status GoPclntab::LoadRelativeRelocations() {
  if (relocations_loaded_) {
    return Status::OK();
  }
  relocations_loaded_ = true;
  for (ELFIO::section* s : elf_reader_->Sections()) {
    if (s->get_type() != ELFIO::SHT_RELA || s->get_entry_size() != kRelaEntrySize) {
      continue;
    }
    PX_ASSIGN_OR_RETURN(std::string relocs,
                        elf_reader_->BinaryByteCode<char>(s->get_offset(), s->get_size()));
    std::string_view entries(relocs);
    for (size_t pos = 0; pos + kRelaEntrySize <= entries.size(); pos += kRelaEntrySize) {
      const uint64_t offset = ::px::utils::LEndianBytesToInt<uint64_t>(entries.substr(pos));
      const uint64_t info = ::px::utils::LEndianBytesToInt<uint64_t>(entries.substr(pos + 8));
      const uint32_t type = static_cast<uint32_t>(info);
      if (type == kX86_64RelativeReloc || type == kAArch64RelativeReloc) {
        relative_relocations_[offset] =
            ::px::utils::LEndianBytesToInt<uint64_t>(entries.substr(pos + 16));
      }
    }
  }
  return Status::OK();
}

ELFIO::section* GoPclntab::DataSectionAt(uint64_t vaddr) const {
  auto it = std::upper_bound(
      data_sections_.begin(), data_sections_.end(), vaddr,
      [](uint64_t addr, ELFIO::section* s) { return addr < s->get_address(); });
  if (it == data_sections_.begin()) {
    return nullptr;
  }
  ELFIO::section* s = *std::prev(it);
  return vaddr - s->get_address() < s->get_size() ? s : nullptr;
}

StatusOr<std::string_view> GoPclntab::SectionBlock(ELFIO::section* section, uint64_t index) {
  const uint64_t offset = section->get_offset() + index * kReadBlockSize;
  auto it = blocks_.find(offset);
  if (it == blocks_.end()) {
    const uint64_t length = std::min(kReadBlockSize, section->get_size() - index * kReadBlockSize);
    PX_ASSIGN_OR_RETURN(std::string block, elf_reader_->BinaryByteCode<char>(offset, length));
    it = blocks_.emplace(offset, std::move(block)).first;
  }
  return std::string_view(it->second);
}

StatusOr<std::string> GoPclntab::ReadVirtual(uint64_t vaddr, size_t length) {
  ELFIO::section* s = DataSectionAt(vaddr);
  if (s == nullptr || length > s->get_address() + s->get_size() - vaddr) {
    PX_ASSIGN_OR_RETURN(uint64_t file_offset, elf_reader_->VirtualAddrToBinaryAddr(vaddr));
    return elf_reader_->BinaryByteCode<char>(file_offset, length);
  }
  std::string out;
  out.reserve(length);
  while (out.size() < length) {
    const uint64_t pos = vaddr - s->get_address() + out.size();
    PX_ASSIGN_OR_RETURN(std::string_view block, SectionBlock(s, pos / kReadBlockSize));
    out.append(block.substr(pos % kReadBlockSize, length - out.size()));
  }
  return out;
}

StatusOr<uint64_t> GoPclntab::ReadU64(uint64_t vaddr) {
  PX_ASSIGN_OR_RETURN(std::string buf, ReadVirtual(vaddr, sizeof(uint64_t)));
  const uint64_t value = ::px::utils::LEndianBytesToInt<uint64_t>(std::string_view(buf));
  if (value != 0) {
    return value;
  }
  PX_RETURN_IF_ERROR(LoadRelativeRelocations());
  auto reloc = relative_relocations_.find(vaddr);
  return reloc == relative_relocations_.end() ? value : reloc->second;
}

StatusOr<std::string> GoPclntab::ReadGoName(uint64_t vaddr) {
  PX_ASSIGN_OR_RETURN(std::string header, ReadVirtual(vaddr, 1 + kMaxVarintLen64));
  BinaryDecoder decoder(std::string_view(header).substr(1));
  PX_ASSIGN_OR_RETURN(uint64_t length, decoder.ExtractUVarInt());
  if (length > kMaxGoNameLength) {
    return error::Internal("Invalid Go name at address=$0", vaddr);
  }
  const size_t varint_size = header.size() - 1 - decoder.BufSize();
  return ReadVirtual(vaddr + 1 + varint_size, length);
}

StatusOr<std::string> GoPclntab::TypeName(uint64_t type_addr, uint64_t types_addr) {
  PX_ASSIGN_OR_RETURN(std::string type, ReadVirtual(type_addr, kTypeStrOffset + sizeof(int32_t)));
  const uint8_t tflag = static_cast<uint8_t>(type[kTypeTFlagOffset]);
  const int32_t str =
      ::px::utils::LEndianBytesToInt<int32_t>(std::string_view(type).substr(kTypeStrOffset));
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
    auto method = elf_reader_->InstrAddrToSymbol(fun.ValueOrDie());
    if (!method.ok() || !method.ValueOrDie().has_value() ||
        method.ValueOrDie().value() == kUnreachableMethod) {
      continue;
    }
    return ReceiverFromMethod(method.ValueOrDie().value());
  }
  return std::nullopt;
}

StatusOr<std::vector<uint64_t>> GoPclntab::ITabAddresses(uint64_t types_addr) {
  const uint64_t epclntab = pclntab_addr_ + pclntab_size_;
  std::vector<uint64_t> addrs;

  auto epclntab_go127 = ReadU64(moduledata_addr_ + kModuleDataEPclntabOffsetGo127);
  if (epclntab_go127.ok() && epclntab_go127.ValueOrDie() == epclntab) {
    PX_ASSIGN_OR_RETURN(uint64_t itab_offset,
                        ReadU64(moduledata_addr_ + kModuleDataITabOffsetGo127));
    PX_ASSIGN_OR_RETURN(uint64_t itab_size,
                        ReadU64(moduledata_addr_ + kModuleDataITabOffsetGo127 + 8));
    if (itab_size > kMaxITabTableSize) {
      return error::Internal("Implausible itab table size=$0 in binary=$1", itab_size,
                             elf_reader_->binary_path());
    }
    const uint64_t end = types_addr + itab_offset + itab_size;
    for (uint64_t p = types_addr + itab_offset; p < end && addrs.size() < kMaxITabs;) {
      addrs.push_back(p);
      PX_ASSIGN_OR_RETURN(uint64_t inter_addr, ReadU64(p));
      PX_ASSIGN_OR_RETURN(uint64_t fun0, ReadU64(p + kITabFunOffset));
      uint64_t size = kITabHeaderSize;
      if (fun0 != 0) {
        PX_ASSIGN_OR_RETURN(uint64_t num_methods,
                            ReadU64(inter_addr + kInterfaceTypeMethodsLenOffset));
        if (num_methods == 0 || num_methods > kMaxInterfaceMethods) {
          return error::Internal("Implausible itab at address=$0 in binary=$1", p,
                                 elf_reader_->binary_path());
        }
        size += (num_methods - 1) * sizeof(uint64_t);
      }
      p += size;
    }
    return addrs;
  }

  PX_ASSIGN_OR_RETURN(uint64_t epclntab_go126,
                      ReadU64(moduledata_addr_ + kModuleDataEPclntabOffset));
  const uint64_t links_offset =
      epclntab_go126 == epclntab ? kModuleDataITabLinksOffsetGo126 : kModuleDataITabLinksOffset;
  PX_ASSIGN_OR_RETURN(uint64_t links_addr, ReadU64(moduledata_addr_ + links_offset));
  PX_ASSIGN_OR_RETURN(uint64_t num_links, ReadU64(moduledata_addr_ + links_offset + 8));
  if (num_links > kMaxITabs) {
    return error::Internal("Implausible itab count=$0 in binary=$1", num_links,
                           elf_reader_->binary_path());
  }
  PX_ASSIGN_OR_RETURN(std::string links, ReadVirtual(links_addr, num_links * sizeof(uint64_t)));
  for (uint64_t k = 0; k < num_links; ++k) {
    addrs.push_back(::px::utils::LEndianBytesToInt<uint64_t>(
        std::string_view(links).substr(k * sizeof(uint64_t))));
  }
  return addrs;
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
  PX_ASSIGN_OR_RETURN(std::vector<uint64_t> itab_addrs, ITabAddresses(types_addr));

  std::vector<ElfReader::SymbolInfo> itabs;
  for (uint64_t itab_addr : itab_addrs) {
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
