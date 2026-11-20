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

#include <algorithm>
#include <chrono>
#include <filesystem>
#include <fstream>
#include <string>
#include <thread>
#include <vector>

#include <absl/strings/match.h>
#include <absl/strings/str_cat.h>

#include "src/common/exec/subprocess.h"
#include "src/common/system/proc_pid_path.h"
#include "src/common/testing/temp_dir.h"
#include "src/common/testing/testing.h"
#include "src/stirling/source_connectors/socket_tracer/testing/socket_trace_bpf_test_fixture.h"
#include "src/stirling/source_connectors/socket_tracer/uprobe_manager.h"
#include "src/stirling/testing/go_https_server_fixture.h"
#include "src/stirling/utils/monitor.h"

DECLARE_string(offsetgen_filepath);

namespace px {
namespace stirling {

using ::px::stirling::testing::GoHTTPSServerFixture;

constexpr std::string_view kServerCert =
    "src/stirling/testing/demo_apps/go_https/server/server.crt";
constexpr std::string_view kServerKey = "src/stirling/testing/demo_apps/go_https/server/server.key";

class UProbeManagerBPFTest
    : public testing::SocketTraceBPFTestFixture</* TClientSideTracing */ false> {
 protected:
  void SetUp() override {
    const std::filesystem::path empty_offsets = offsets_dir_.path() / "offsetgen_offsets.json";
    std::ofstream(empty_offsets) << R"({"structs": {}, "funcs": {}})";
    FLAGS_offsetgen_filepath = empty_offsets.string();

    const std::filesystem::path server = GoHTTPSServerFixture::StrippedPath("1_24");
    StartServer(server, 50101, 50100, &server_);
    StartServer(server, 50111, 50110, &second_server_);

    SocketTraceBPFTestFixture::SetUp();
  }

  void TearDown() override {
    SocketTraceBPFTestFixture::TearDown();
    for (SubProcess* server : {&server_, &second_server_}) {
      server->Kill();
      server->Wait();
    }
  }

  void StartServer(const std::filesystem::path& server, int https_port, int http_port,
                   SubProcess* process) {
    ASSERT_OK(process->Start(
        {server.string(),
         absl::StrCat("--cert=", ::px::testing::BazelRunfilePath(kServerCert).string()),
         absl::StrCat("--key=", ::px::testing::BazelRunfilePath(kServerKey).string()),
         absl::StrCat("--https_port=", https_port), absl::StrCat("--http_port=", http_port)}));
    WaitForExec(*process, server.filename());
  }

  void WaitForExec(const SubProcess& process, const std::filesystem::path& filename) {
    const std::filesystem::path exe = system::ProcPidPath(process.child_pid(), "exe");
    for (int i = 0; i < 100; ++i) {
      std::error_code ec;
      if (std::filesystem::read_symlink(exe, ec).filename() == filename) {
        return;
      }
      std::this_thread::sleep_for(std::chrono::milliseconds(50));
    }
    FAIL() << "Server did not start: " << exe;
  }

  bool IsServerRecord(const SourceStatusRecord& r) const {
    return r.context == "UpdateGoCommonSymAddrs" &&
           (absl::StrContains(r.error, absl::StrCat("/proc/", server_.child_pid(), "/root")) ||
            absl::StrContains(r.error,
                              absl::StrCat("/proc/", second_server_.child_pid(), "/root")));
  }

  ::px::testing::TempDir offsets_dir_;
  SubProcess server_;
  SubProcess second_server_;
};

TEST_F(UProbeManagerBPFTest, GoBinaryWithoutOffsetsIsReported) {
  std::vector<SourceStatusRecord> records =
      StirlingMonitor::GetInstance()->ConsumeSourceStatusRecords();

  auto record = std::find_if(records.begin(), records.end(),
                             [&](const SourceStatusRecord& r) { return IsServerRecord(r); });
  ASSERT_NE(record, records.end());
  EXPECT_EQ(record->source_connector, "socket_tracer");
  EXPECT_NE(record->status, px::statuspb::Code::OK);
}

TEST_F(UProbeManagerBPFTest, GoBinaryFailureIsAnalyzedOncePerFile) {
  std::vector<SourceStatusRecord> records =
      StirlingMonitor::GetInstance()->ConsumeSourceStatusRecords();

  EXPECT_EQ(std::count_if(records.begin(), records.end(),
                          [&](const SourceStatusRecord& r) { return IsServerRecord(r); }),
            1);
}

class UProbeLoadBPFTest
    : public testing::SocketTraceBPFTestFixture</* TClientSideTracing */ false> {};

TEST_F(UProbeLoadBPFTest, EveryProbeLoads) {
  ebpf::BPF* bpf = source_->BCC().BPF().ConsumeValueOrDie();

  for (const std::string& probe_fn : UProbeManager::ProbeFuncs()) {
    int fd = -1;
    const ebpf::StatusTuple status = bpf->load_func(probe_fn, BPF_PROG_TYPE_KPROBE, fd);
    EXPECT_TRUE(status.ok()) << probe_fn << ": " << status.msg();
  }
}

}  // namespace stirling
}  // namespace px
