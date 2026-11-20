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

#include <iostream>
#include <memory>
#include <string>

#include <absl/strings/match.h>

#include "src/common/base/base.h"
#include "src/stirling/obj_tools/elf_reader.h"
#include "src/stirling/obj_tools/go_pclntab.h"
#include "src/stirling/obj_tools/go_syms.h"
#include "src/stirling/source_connectors/socket_tracer/go_binary_cache.h"
#include "src/stirling/source_connectors/socket_tracer/uprobe_symaddrs.h"

DEFINE_string(binary, "", "Go binary to analyze");
DEFINE_string(offsets, "", "Path to offsetgen_offsets.json");

using ::px::stirling::AnalyzeGoUProbeBinary;
using ::px::stirling::GoUProbeSymAddrs;
using ::px::stirling::ParseGoOffsetgenFile;
using ::px::stirling::obj_tools::ElfReader;
using ::px::stirling::obj_tools::GoPclntab;
using ::px::stirling::obj_tools::ReadGoBuildInfo;

int main(int argc, char** argv) {
  px::EnvironmentGuard env_guard(&argc, argv);
  if (FLAGS_binary.empty() || FLAGS_offsets.empty()) {
    std::cerr << "Usage: " << argv[0] << " --binary <path> --offsets <offsetgen_offsets.json>\n";
    return 1;
  }
  ParseGoOffsetgenFile(FLAGS_offsets);

  std::unique_ptr<ElfReader> elf_reader = ElfReader::Create(FLAGS_binary).ConsumeValueOrDie();

  auto build_info = ReadGoBuildInfo(elf_reader.get());
  if (build_info.ok()) {
    std::cout << "go_version=" << build_info.ValueOrDie().first
              << " deps=" << build_info.ValueOrDie().second.deps.size() << "\n";
  } else {
    std::cout << "build_info error: " << build_info.msg() << "\n";
  }

  auto pclntab = GoPclntab::Create(elf_reader.get());
  if (pclntab.ok()) {
    std::cout << "pclntab funcs=" << pclntab.ValueOrDie()->funcs().size()
              << " moduledata=0x" << std::hex << pclntab.ValueOrDie()->moduledata_addr()
              << std::dec << "\n";
    auto itabs = pclntab.ValueOrDie()->ITabs();
    if (itabs.ok()) {
      std::cout << "itabs=" << itabs.ValueOrDie().size() << "\n";
      for (const auto& itab : itabs.ValueOrDie()) {
        if (absl::StrContains(itab.name, "TCPConn") || absl::StrContains(itab.name, "bufWriter") ||
            absl::StrContains(itab.name, "syscallConn") ||
            absl::StrContains(itab.name, "crypto/tls.Conn")) {
          std::cout << "  " << itab.name << " 0x" << std::hex << itab.address << std::dec << "\n";
        }
      }
    } else {
      std::cout << "itabs error: " << itabs.msg() << "\n";
    }
  } else {
    std::cout << "pclntab error: " << pclntab.msg() << "\n";
  }

  GoUProbeSymAddrs symaddrs =
      AnalyzeGoUProbeBinary(FLAGS_binary, elf_reader.get(), /*tls*/ true, /*http2*/ true);
  std::cout << "status=" << symaddrs.status.ToString() << " failed_stage=" << symaddrs.failed_stage
            << "\n";
  std::cout << "net_TCPConn=" << symaddrs.common.net_TCPConn
            << " tls_Conn=" << symaddrs.common.tls_Conn
            << " internal_syscallConn=" << symaddrs.common.internal_syscallConn
            << " FD_Sysfd_offset=" << symaddrs.common.FD_Sysfd_offset << "\n";
  std::cout << "tls=" << symaddrs.tls.has_value() << " http2=" << symaddrs.http2.has_value()
            << "\n";
  return 0;
}
