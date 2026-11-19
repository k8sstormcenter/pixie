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

#include <benchmark/benchmark.h>

#include <memory>
#include <string>

#include "src/common/testing/test_environment.h"
#include "src/stirling/obj_tools/elf_reader.h"
#include "src/stirling/source_connectors/socket_tracer/go_binary_cache.h"
#include "src/stirling/source_connectors/socket_tracer/uprobe_symaddrs.h"

namespace px {
namespace stirling {

using ::px::stirling::obj_tools::ElfReader;

constexpr std::string_view kGoServer =
    "src/stirling/obj_tools/testdata/go/golang_1_24_server_stripped_binary";
constexpr std::string_view kCgoPIE =
    "src/stirling/obj_tools/testdata/go/cgo_tls/cgo_tls_pie_stripped_binary";
constexpr std::string_view kOffsets = "src/stirling/offsetgen_offsets.json";

void LoadOffsets() {
  static const bool loaded = [] {
    ParseGoOffsetgenFile(::px::testing::BazelRunfilePath(kOffsets).string());
    return true;
  }();
  benchmark::DoNotOptimize(loaded);
}

GoUProbeSymAddrs Analyze(const std::string& binary) {
  std::unique_ptr<ElfReader> elf_reader = ElfReader::Create(binary).ConsumeValueOrDie();
  return AnalyzeGoUProbeBinary(binary, elf_reader.get(), /*tls*/ true, /*http2*/ true);
}

void BM_AnalyzePerProcess(benchmark::State& state, std::string_view rel_path) {
  LoadOffsets();
  const std::string binary = ::px::testing::BazelRunfilePath(rel_path).string();
  for (auto _ : state) {
    for (int64_t pid = 0; pid < state.range(0); ++pid) {
      GoUProbeSymAddrs symaddrs = Analyze(binary);
      benchmark::DoNotOptimize(symaddrs);
    }
  }
  state.counters["analyses_per_iteration"] = static_cast<double>(state.range(0));
  state.SetItemsProcessed(state.iterations() * state.range(0));
}

void BM_AnalyzePerFileVersion(benchmark::State& state, std::string_view rel_path) {
  LoadOffsets();
  const std::string binary = ::px::testing::BazelRunfilePath(rel_path).string();
  int64_t analyses = 0;
  for (auto _ : state) {
    GoBinaryCache cache(2048);
    for (int64_t pid = 0; pid < state.range(0); ++pid) {
      const FileVersion version = GetFileVersion(binary).ConsumeValueOrDie();
      if (cache.Find(version) == nullptr) {
        cache.Insert(version, Analyze(binary));
        ++analyses;
      }
      benchmark::DoNotOptimize(cache.Find(version));
    }
  }
  state.counters["analyses_per_iteration"] =
      static_cast<double>(analyses) / static_cast<double>(state.iterations());
  state.SetItemsProcessed(state.iterations() * state.range(0));
}

BENCHMARK_CAPTURE(BM_AnalyzePerProcess, go_server, kGoServer)
    ->Arg(1)
    ->Arg(10)
    ->Arg(50)
    ->Unit(benchmark::kMillisecond);
BENCHMARK_CAPTURE(BM_AnalyzePerFileVersion, go_server, kGoServer)
    ->Arg(1)
    ->Arg(10)
    ->Arg(50)
    ->Unit(benchmark::kMillisecond);
BENCHMARK_CAPTURE(BM_AnalyzePerProcess, cgo_pie_unsupported, kCgoPIE)
    ->Arg(1)
    ->Arg(10)
    ->Arg(50)
    ->Unit(benchmark::kMillisecond);
BENCHMARK_CAPTURE(BM_AnalyzePerFileVersion, cgo_pie_unsupported, kCgoPIE)
    ->Arg(1)
    ->Arg(10)
    ->Arg(50)
    ->Unit(benchmark::kMillisecond);

}  // namespace stirling
}  // namespace px
