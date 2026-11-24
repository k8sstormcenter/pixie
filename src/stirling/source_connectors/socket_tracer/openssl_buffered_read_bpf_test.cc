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
#include "src/stirling/source_connectors/socket_tracer/socket_trace_connector.h"
#include "src/stirling/source_connectors/socket_tracer/testing/container_images/nginx_openssl_3_0_8_container.h"
#include "src/stirling/source_connectors/socket_tracer/testing/container_images/tls13_coalesced_client_container.h"
#include "src/stirling/source_connectors/socket_tracer/testing/socket_trace_bpf_test_fixture.h"
#include "src/stirling/testing/common.h"

namespace px {
namespace stirling {

namespace http = protocols::http;

using ::px::stirling::testing::FindRecordIdxMatchesPID;
using ::px::stirling::testing::SocketTraceBPFTestFixture;
using ::px::stirling::testing::ToRecordVector;

using ::testing::Each;
using ::testing::Field;
using ::testing::SizeIs;
using ::testing::StrEq;

class OpenSSLBufferedReadTest : public SocketTraceBPFTestFixture</* TClientSideTracing */ false> {
 protected:
  OpenSSLBufferedReadTest() {
    constexpr bool kHostPid = false;
    PX_CHECK_OK(server_.Run(std::chrono::seconds{60}, {}, {}, kHostPid));
    sleep(1);
  }

  ::px::stirling::testing::NginxOpenSSL_3_0_8_Container server_;
};

TEST_F(OpenSSLBufferedReadTest, RequestSentWithTheClientFinishedIsCaptured) {
  constexpr int kRequests = 10;
  StartTransferDataThread();

  ::px::stirling::testing::TLS13CoalescedClientContainer client;
  constexpr bool kHostPid = false;
  ASSERT_OK(client.Run(std::chrono::seconds{60},
                       {absl::Substitute("--network=container:$0", server_.container_name())},
                       {"8443", std::to_string(kRequests)}, kHostPid));
  client.Wait();
  StopTransferDataThread();

  std::vector<TaggedRecordBatch> tablets = ConsumeRecords(SocketTraceConnector::kHTTPTableNum);
  ASSERT_FALSE(tablets.empty());
  std::vector<size_t> idx =
      FindRecordIdxMatchesPID(tablets[0].records, kHTTPUPIDIdx, server_.NginxWorkerPID());
  std::vector<http::Record> records = ToRecordVector<http::Record>(tablets[0].records, idx);

  EXPECT_THAT(records, SizeIs(kRequests));
  EXPECT_THAT(records, Each(Field(&http::Record::req,
                                  Field(&http::Message::req_path, StrEq("/index.html")))));
}

}  // namespace stirling
}  // namespace px
