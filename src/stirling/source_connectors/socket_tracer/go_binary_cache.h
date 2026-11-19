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

#include <filesystem>
#include <list>
#include <optional>
#include <string>
#include <string_view>
#include <utility>

#include <absl/container/flat_hash_map.h>

#include "src/common/base/base.h"
#include "src/stirling/obj_tools/elf_reader.h"
#include "src/stirling/source_connectors/socket_tracer/bcc_bpf_intf/common.h"
#include "src/stirling/source_connectors/socket_tracer/bcc_bpf_intf/symaddrs.h"

namespace px {
namespace stirling {

struct FileVersion {
  uint64_t dev = 0;
  uint64_t ino = 0;
  int64_t size = 0;
  int64_t mtime_ns = 0;
  int64_t ctime_ns = 0;

  bool operator==(const FileVersion& other) const {
    return dev == other.dev && ino == other.ino && size == other.size &&
           mtime_ns == other.mtime_ns && ctime_ns == other.ctime_ns;
  }

  template <typename H>
  friend H AbslHashValue(H h, const FileVersion& v) {
    return H::combine(std::move(h), v.dev, v.ino, v.size, v.mtime_ns, v.ctime_ns);
  }
};

StatusOr<FileVersion> GetFileVersion(const std::filesystem::path& path);

struct GoUProbeSymAddrs {
  Status status;
  std::string_view failed_stage;
  struct go_common_symaddrs_t common = {};
  std::optional<struct go_tls_symaddrs_t> tls;
  std::optional<struct go_http2_symaddrs_t> http2;
};

class GoBinaryCache {
 public:
  explicit GoBinaryCache(size_t capacity) : capacity_(capacity) {}

  const GoUProbeSymAddrs* Find(const FileVersion& version);
  void Insert(const FileVersion& version, GoUProbeSymAddrs value);
  size_t size() const { return index_.size(); }

 private:
  using Entry = std::pair<FileVersion, GoUProbeSymAddrs>;

  size_t capacity_;
  std::list<Entry> lru_;
  absl::flat_hash_map<FileVersion, std::list<Entry>::iterator> index_;
};

GoUProbeSymAddrs AnalyzeGoUProbeBinary(const std::string& binary, obj_tools::ElfReader* elf_reader,
                                       bool tls, bool http2);

}  // namespace stirling
}  // namespace px
