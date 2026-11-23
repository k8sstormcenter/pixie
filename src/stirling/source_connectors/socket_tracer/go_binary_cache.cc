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

#include "src/stirling/source_connectors/socket_tracer/go_binary_cache.h"

#include <sys/stat.h>

#include <utility>

#include "src/common/fs/fs_wrapper.h"
#include "src/stirling/obj_tools/go_syms.h"
#include "src/stirling/source_connectors/socket_tracer/uprobe_symaddrs.h"

namespace px {
namespace stirling {

namespace {

int64_t ToNanos(const struct timespec& ts) {
  return static_cast<int64_t>(ts.tv_sec) * 1000 * 1000 * 1000 + ts.tv_nsec;
}

}  // namespace

StatusOr<FileVersion> GetFileVersion(const std::filesystem::path& path) {
  PX_ASSIGN_OR_RETURN(const struct stat st, fs::Stat(path));
  return FileVersion{static_cast<uint64_t>(st.st_dev), static_cast<uint64_t>(st.st_ino),
                     static_cast<int64_t>(st.st_size), ToNanos(st.st_mtim), ToNanos(st.st_ctim)};
}

const GoUProbeSymAddrs* GoBinaryCache::Find(const FileVersion& version) {
  auto it = index_.find(version);
  if (it == index_.end()) {
    return nullptr;
  }
  lru_.splice(lru_.begin(), lru_, it->second);
  return &it->second->second;
}

void GoBinaryCache::Insert(const FileVersion& version, GoUProbeSymAddrs value) {
  auto it = index_.find(version);
  if (it != index_.end()) {
    it->second->second = std::move(value);
    lru_.splice(lru_.begin(), lru_, it->second);
    return;
  }
  lru_.emplace_front(version, std::move(value));
  index_[version] = lru_.begin();
  while (index_.size() > capacity_) {
    index_.erase(lru_.back().first);
    lru_.pop_back();
  }
}

GoUProbeSymAddrs AnalyzeGoUProbeBinary(const std::string& binary, obj_tools::ElfReader* elf_reader,
                                       bool tls, bool http2) {
  GoUProbeSymAddrs result;
  if (!obj_tools::IsGoExecutable(elf_reader)) {
    result.status = error::NotFound("Binary $0 is not a Go executable", binary);
    return result;
  }

  StatusOr<GoBinaryInfo> go_binary_or = AnalyzeGoBinary(binary, elf_reader);
  if (!go_binary_or.ok()) {
    result.status = go_binary_or.status();
    result.failed_stage = "AnalyzeGoBinary";
    return result;
  }
  GoBinaryInfo go_binary = go_binary_or.ConsumeValueOrDie();
  GoOffsetLocator offset_locator(go_binary.dwarf_reader.get(), GetGoStructOffsets(),
                                 GetGoFunctionArgOffsets(), go_binary.build_info,
                                 go_binary.go_version);

  StatusOr<struct go_common_symaddrs_t> common_or = GoCommonSymAddrs(elf_reader, &offset_locator);
  if (!common_or.ok()) {
    result.status = common_or.status();
    if (!error::IsNotFound(result.status)) {
      result.failed_stage = "UpdateGoCommonSymAddrs";
    }
    return result;
  }
  result.common = common_or.ConsumeValueOrDie();

  if (tls) {
    StatusOr<struct go_tls_symaddrs_t> tls_or = GoTLSSymAddrs(&offset_locator);
    if (tls_or.ok()) {
      result.tls = tls_or.ConsumeValueOrDie();
    }
  }
  if (http2) {
    StatusOr<struct go_http2_symaddrs_t> http2_or = GoHTTP2SymAddrs(elf_reader, &offset_locator);
    if (http2_or.ok()) {
      result.http2 = http2_or.ConsumeValueOrDie();
    }
  }
  return result;
}

}  // namespace stirling
}  // namespace px
