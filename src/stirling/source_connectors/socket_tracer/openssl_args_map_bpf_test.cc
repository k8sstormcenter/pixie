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

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <string>
#include <vector>

#include "src/common/base/base.h"
#include "src/stirling/bpf_tools/bcc_wrapper.h"
#include "src/stirling/source_connectors/socket_tracer/socket_trace_connector.h"
#include "src/stirling/source_connectors/socket_tracer/testing/container_images/curl_container.h"
#include "src/stirling/source_connectors/socket_tracer/testing/container_images/python_min_310_memory_bio_container.h"
#include "src/stirling/source_connectors/socket_tracer/testing/socket_trace_bpf_test_fixture.h"
#include "src/stirling/testing/common.h"

namespace px {
namespace stirling {

using ::px::stirling::testing::FindRecordIdxMatchesPID;
using ::px::stirling::testing::SocketTraceBPFTestFixture;

using ::testing::IsEmpty;
using ::testing::Le;
using ::testing::Not;

using DataArgsMapT = bpf_tools::WrappedBCCMap<uint64_t, struct data_args_t>;

class OpenSSLArgsMapTest : public SocketTraceBPFTestFixture</* TClientSideTracing */ false> {
 protected:
  OpenSSLArgsMapTest() {
    constexpr bool kHostPid = false;
    PX_CHECK_OK(server_.Run(std::chrono::seconds{60}, {}, {}, kHostPid));
    sleep(1);
  }

  size_t EntriesOfServer(const std::string& map_name) {
    auto map = DataArgsMapT::Create(&source_->BCC(), map_name);
    const uint64_t tgid = server_.process_pid();
    size_t n = 0;
    for (const auto& [id, args] : map->GetTableOffline()) {
      if ((id >> 32) == tgid) {
        ++n;
      }
    }
    return n;
  }

  ::px::stirling::testing::PythonMin310MemoryBIOContainer server_;
};

TEST_F(OpenSSLArgsMapTest, CallsWithoutASocketSyscallReleaseTheirArgs) {
  StartTransferDataThread();

  ::px::stirling::testing::CurlContainer client;
  constexpr bool kHostPid = false;
  ASSERT_OK(client.Run(std::chrono::seconds{60},
                       {absl::Substitute("--network=container:$0", server_.container_name())},
                       {"--insecure", "-s", "-S", "https://127.0.0.1:443/index.html"}, kHostPid));
  client.Wait();
  sleep(5);
  StopTransferDataThread();

  std::vector<TaggedRecordBatch> tablets = ConsumeRecords(SocketTraceConnector::kHTTPTableNum);
  ASSERT_THAT(tablets, Not(IsEmpty()));
  EXPECT_THAT(FindRecordIdxMatchesPID(tablets[0].records, kHTTPUPIDIdx, server_.process_pid()),
              Not(IsEmpty()));

  EXPECT_THAT(EntriesOfServer("active_ssl_write_args_map"), Le(1));
  EXPECT_THAT(EntriesOfServer("active_ssl_read_args_map"), Le(1));
}

}  // namespace stirling
}  // namespace px
