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

#include <deque>
#include <vector>

#include "src/stirling/source_connectors/socket_tracer/protocols/kafka/common/types.h"
#include "src/stirling/source_connectors/socket_tracer/protocols/kafka/stitcher.h"
#include "src/stirling/source_connectors/socket_tracer/protocols/kafka/test_data.h"

namespace px {
namespace stirling {
namespace protocols {
namespace kafka {

using ::testing::ElementsAre;

TEST(KafkaStitcherTest, BasicMatching) {
  std::deque<Packet> req_packets;
  std::deque<Packet> resp_packets;
  State state{};
  RecordsWithErrorCount<Record> result;

  result = StitchFrames(&req_packets, &resp_packets, &state);
  EXPECT_TRUE(resp_packets.empty());
  EXPECT_TRUE(req_packets.empty());
  EXPECT_EQ(result.error_count, 0);
  EXPECT_EQ(result.records.size(), 0);

  req_packets.push_back(testdata::kProduceReqPacket);

  result = StitchFrames(&req_packets, &resp_packets, &state);
  EXPECT_TRUE(resp_packets.empty());
  EXPECT_EQ(req_packets.size(), 1);
  EXPECT_EQ(result.error_count, 0);
  EXPECT_EQ(result.records.size(), 0);

  resp_packets.push_back(testdata::kProduceRespPacket);

  result = StitchFrames(&req_packets, &resp_packets, &state);
  EXPECT_TRUE(resp_packets.empty());
  EXPECT_EQ(req_packets.size(), 0);
  EXPECT_EQ(result.error_count, 0);
  EXPECT_EQ(result.records.size(), 1);
  EXPECT_EQ(
      result.records[0].req.msg,
      "{\"transactional_id\":\"\",\"acks\":1,\"timeout_ms\":1500,\"topics\":[{\"name\":"
      "\"quickstart-events\",\"partitions\":[{\"index\":0,\"message_set\":{\"size\":91}}]}]}");
  EXPECT_EQ(result.records[0].resp.msg,
            "{\"topics\":[{\"name\":\"quickstart-events\",\"partitions\":[{\"index\":0,\"error_"
            "code\":0,\"base_offset\":0,\"log_append_time_ms\":-1,\"log_start_offset\":0,"
            "\"record_errors\":[],\"error_message\":\"\"}]}],\"throttle_time_ms\":0}");
}

TEST(KafkaStitcherTest, StitchesCurrentAPIVersionsWithoutErrors) {
  std::deque<Packet> req_packets = {
      testdata::GenPacket(message_type_t::kRequest, testdata::kFetchV16Request, 0, 0),
      testdata::GenPacket(message_type_t::kRequest, testdata::kFetchV18Request, 2, 0),
      testdata::GenPacket(message_type_t::kRequest, testdata::kOffsetCommitV9Request, 4, 0),
      testdata::GenPacket(message_type_t::kRequest, testdata::kHeartbeatV4Request, 6, 0),
      testdata::GenPacket(message_type_t::kRequest, testdata::kListOffsetsV11Request, 8, 0)};
  std::deque<Packet> resp_packets = {
      testdata::GenPacket(message_type_t::kResponse, testdata::kFetchV16Response, 1, 0),
      testdata::GenPacket(message_type_t::kResponse, testdata::kFetchV18Response, 3, 0),
      testdata::GenPacket(message_type_t::kResponse, testdata::kOffsetCommitV9Response, 5, 0),
      testdata::GenPacket(message_type_t::kResponse, testdata::kHeartbeatV4Response, 7, 0),
      testdata::GenPacket(message_type_t::kResponse, testdata::kListOffsetsV11Response, 9, 0)};
  State state{};

  RecordsWithErrorCount<Record> result = StitchFrames(&req_packets, &resp_packets, &state);

  EXPECT_EQ(result.error_count, 0);
  EXPECT_TRUE(req_packets.empty());
  EXPECT_TRUE(resp_packets.empty());
  std::vector<APIKey> api_keys;
  for (const auto& record : result.records) {
    api_keys.push_back(record.req.api_key);
  }
  EXPECT_THAT(api_keys, ElementsAre(APIKey::kFetch, APIKey::kFetch, APIKey::kOffsetCommit,
                                    APIKey::kHeartbeat, APIKey::kListOffsets));
  ASSERT_FALSE(result.records.empty());
  EXPECT_EQ(result.records[0].req.client_id, "rdkafka");
}

}  // namespace kafka
}  // namespace protocols
}  // namespace stirling
}  // namespace px
