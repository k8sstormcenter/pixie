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

#include <chrono>
#include <cstring>
#include <filesystem>
#include <string>
#include <thread>

#include <absl/strings/str_cat.h>

#include "src/common/exec/subprocess.h"
#include "src/common/system/proc_pid_path.h"
#include "src/common/testing/testing.h"
#include "src/stirling/bpf_tools/bcc_wrapper.h"
#include "src/stirling/source_connectors/socket_tracer/bcc_bpf_intf/common.h"
#include "src/stirling/source_connectors/socket_tracer/bcc_bpf_intf/symaddrs.h"
#include "src/stirling/source_connectors/socket_tracer/testing/socket_trace_bpf_test_fixture.h"
#include "src/stirling/testing/go_https_server_fixture.h"

DECLARE_string(offsetgen_filepath);

namespace px {
namespace stirling {

using ::px::stirling::bpf_tools::WrappedBCCMap;
using ::px::stirling::testing::GoHTTPSServerFixture;

constexpr std::string_view kServerCert =
    "src/stirling/testing/demo_apps/go_https/server/server.crt";
constexpr std::string_view kServerKey = "src/stirling/testing/demo_apps/go_https/server/server.key";

class GoBinaryCacheBPFTest
    : public testing::SocketTraceBPFTestFixture</* TClientSideTracing */ false> {
 protected:
  void SetUp() override {
    FLAGS_offsetgen_filepath =
        ::px::testing::BazelRunfilePath("src/stirling/offsetgen_offsets.json").string();
    const std::filesystem::path server = GoHTTPSServerFixture::StrippedPath("1_24");
    StartServer(server, 50121, 50120, &server_);
    StartServer(server, 50131, 50130, &second_server_);

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
    const std::filesystem::path exe = system::ProcPidPath(process->child_pid(), "exe");
    for (int i = 0; i < 100; ++i) {
      std::error_code ec;
      if (std::filesystem::read_symlink(exe, ec).filename() == server.filename()) {
        return;
      }
      std::this_thread::sleep_for(std::chrono::milliseconds(50));
    }
    FAIL() << "Server did not start: " << exe;
  }

  template <typename V>
  void ExpectSameValueForBothServers(const std::string& map_name) {
    auto map = WrappedBCCMap<uint32_t, V>::Create(&source_->BCC(), map_name);
    ASSERT_OK_AND_ASSIGN(V first, map->GetValue(server_.child_pid()));
    ASSERT_OK_AND_ASSIGN(V second, map->GetValue(second_server_.child_pid()));
    EXPECT_EQ(std::memcmp(&first, &second, sizeof(V)), 0);
  }

  SubProcess server_;
  SubProcess second_server_;
};

TEST_F(GoBinaryCacheBPFTest, EveryProcessOfABinaryGetsItsSymAddrs) {
  ExpectSameValueForBothServers<struct go_common_symaddrs_t>("go_common_symaddrs_map");
  ExpectSameValueForBothServers<struct go_tls_symaddrs_t>("go_tls_symaddrs_map");
  ExpectSameValueForBothServers<struct go_http2_symaddrs_t>("http2_symaddrs_map");
}

}  // namespace stirling
}  // namespace px
