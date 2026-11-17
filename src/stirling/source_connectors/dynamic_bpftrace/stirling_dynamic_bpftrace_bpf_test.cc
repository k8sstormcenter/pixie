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

#include <atomic>
#include <chrono>
#include <memory>
#include <string>
#include <thread>
#include <vector>

#include <absl/functional/bind_front.h>

#include "src/common/base/base.h"
#include "src/common/testing/testing.h"
#include "src/stirling/core/source_registry.h"
#include "src/stirling/stirling.h"

namespace px {
namespace stirling {

using ::px::stirling::dynamic_tracing::ir::logical::TracepointDeployment;

constexpr std::string_view kKernelHeaderScript = R"(
#include <linux/cred.h>
#include <linux/fs.h>
#include <linux/nsproxy.h>
#include <linux/pid_namespace.h>
#include <linux/sched.h>

interval:ms:100 {
  $task = (struct task_struct *)curtask;
  printf("time_:%llu pid:%u pidns:%u\n", nsecs, $task->tgid,
         $task->nsproxy->pid_ns_for_children->ns.inum);
}
)";

constexpr std::string_view kMissingHeaderScript = R"(
#include <linux/px_header_that_does_not_exist.h>

interval:ms:100 {
  printf("time_:%llu pid:%u\n", nsecs, pid);
}
)";

class StirlingDynamicBPFTraceTest : public ::testing::Test {
 protected:
  void SetUp() override {
    stirling_ = Stirling::Create(std::make_unique<SourceRegistry>());
    stirling_->RegisterDataPushCallback(
        absl::bind_front(&StirlingDynamicBPFTraceTest::AppendData, this));
  }

  void TearDown() override { stirling_->Stop(); }

  Status AppendData(uint64_t /* table_id */, types::TabletID /* tablet_id */,
                    std::unique_ptr<types::ColumnWrapperRecordBatch> /* record_batch */) {
    ++num_pushes_;
    return Status::OK();
  }

  sole::uuid Register(std::string_view table_name, std::string_view script) {
    auto deployment = std::make_unique<TracepointDeployment>();
    deployment->set_name(std::string(table_name));
    auto* tracepoint = deployment->add_tracepoints();
    tracepoint->set_table_name(std::string(table_name));
    tracepoint->mutable_bpftrace()->set_program(std::string(script));

    sole::uuid trace_id = sole::uuid4();
    stirling_->RegisterTracepoint(trace_id, std::move(deployment));
    return trace_id;
  }

  StatusOr<stirlingpb::Publish> WaitForFinalStatus(sole::uuid trace_id) {
    const auto deadline = std::chrono::steady_clock::now() + std::chrono::minutes(3);
    StatusOr<stirlingpb::Publish> s;
    do {
      std::this_thread::sleep_for(std::chrono::milliseconds(100));
      s = stirling_->GetTracepointInfo(trace_id);
    } while (!s.ok() && s.code() == px::statuspb::Code::RESOURCE_UNAVAILABLE &&
             std::chrono::steady_clock::now() < deadline);
    return s;
  }

  std::unique_ptr<Stirling> stirling_;
  std::atomic<uint64_t> num_pushes_ = 0;
};

TEST_F(StirlingDynamicBPFTraceTest, ConcurrentDeploysSettleAndRunLoopKeepsPushing) {
  constexpr int kNumGood = 4;
  constexpr int kNumBad = 2;

  ASSERT_OK(stirling_->RunAsThread());
  ASSERT_OK(stirling_->WaitUntilRunning(std::chrono::seconds(5)));

  std::vector<sole::uuid> good;
  std::vector<sole::uuid> bad;
  for (int i = 0; i < kNumGood + kNumBad; ++i) {
    if (i % 3 == 2) {
      bad.push_back(Register(absl::StrCat("bad_", i), kMissingHeaderScript));
    } else {
      good.push_back(Register(absl::StrCat("good_", i), kKernelHeaderScript));
    }
  }
  ASSERT_EQ(good.size(), kNumGood);
  ASSERT_EQ(bad.size(), kNumBad);

  for (const auto& trace_id : good) {
    SCOPED_TRACE(trace_id.str());
    ASSERT_OK_AND_ASSIGN(stirlingpb::Publish publication, WaitForFinalStatus(trace_id));
    EXPECT_EQ(publication.published_info_classes_size(), 1);
  }

  for (const auto& trace_id : bad) {
    SCOPED_TRACE(trace_id.str());
    StatusOr<stirlingpb::Publish> s = WaitForFinalStatus(trace_id);
    EXPECT_NOT_OK(s);
    EXPECT_NE(s.code(), px::statuspb::Code::RESOURCE_UNAVAILABLE);
  }

  EXPECT_TRUE(stirling_->IsRunning());
  const uint64_t pushes_before = num_pushes_;
  std::this_thread::sleep_for(std::chrono::seconds(5));
  EXPECT_GT(num_pushes_, pushes_before);

  for (const auto& trace_id : good) {
    ASSERT_OK(stirling_->RemoveTracepoint(trace_id));
  }
  for (const auto& trace_id : good) {
    EXPECT_EQ(WaitForFinalStatus(trace_id).code(), px::statuspb::Code::NOT_FOUND);
  }
}

}  // namespace stirling
}  // namespace px
