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
#include <mutex>
#include <thread>
#include <vector>

#include <absl/functional/bind_front.h>

#include "src/common/testing/testing.h"
#include "src/stirling/bpf_tools/bpftrace_wrapper.h"

namespace px {
namespace stirling {
namespace bpf_tools {

using ::px::testing::status::StatusIs;
using ::testing::HasSubstr;

TEST(BPFTracerWrapperTest, MapRead) {
  constexpr std::string_view kScript = R"(
  interval:ms:100 {
      @retval[0] = nsecs;
  }
  )";

  BPFTraceWrapper bpftrace_wrapper;
  ASSERT_OK(bpftrace_wrapper.CompileForMapOutput(kScript));
  ASSERT_OK(bpftrace_wrapper.Deploy());
  sleep(1);

  bpftrace::BPFTraceMap entries = bpftrace_wrapper.GetBPFMap("@retval");
  EXPECT_FALSE(entries.empty());

  bpftrace_wrapper.Stop();
}

TEST(BPFTracerWrapperTest, PerfBufferPoll) {
  constexpr std::string_view kScript = R"(
    interval:ms:100 {
        printf("%llu\n", nsecs);
    }
    )";

  BPFTraceWrapper bpftrace_wrapper;
  ASSERT_OK(bpftrace_wrapper.CompileForMapOutput(kScript));
  ASSERT_OK(bpftrace_wrapper.Deploy());
  sleep(1);

  bpftrace_wrapper.PollPerfBuffers(100);

  bpftrace_wrapper.Stop();
}

// To show that the callback can be to a member function.
class CallbackWrapperClass {
 public:
  void PrintfCallback(uint8_t* /* data */) { ++callback_count; }

  int callback_count = 0;
};

TEST(BPFTracerWrapperTest, PerfBufferPollWithCallback) {
  constexpr int kProbeDurationSeconds = 1;
  constexpr int kProbeIntervalMilliseconds = 100;

  std::string script = R"(
      interval:ms:$0 {
          printf("%llu\n", nsecs);
      }
  )";
  script = absl::Substitute(script, kProbeIntervalMilliseconds);

  CallbackWrapperClass callback_target;

  BPFTraceWrapper bpftrace_wrapper;
  auto callback_fn = absl::bind_front(&CallbackWrapperClass::PrintfCallback, &callback_target);
  ASSERT_OK(bpftrace_wrapper.CompileForPrintfOutput(script));
  ASSERT_OK(bpftrace_wrapper.Deploy(callback_fn));
  sleep(kProbeDurationSeconds);

  bpftrace_wrapper.PollPerfBuffers();

  // The callback should be called `Duration / ProbeInterval` times,
  // But give a much broader range because sleep() is not precise and we don't want a flaky test.

  constexpr int kExpectedCalls = (kProbeDurationSeconds * 1000) / kProbeIntervalMilliseconds;
  constexpr int kMargin = 5;
  EXPECT_GE(callback_target.callback_count, kExpectedCalls - kMargin);
  EXPECT_LE(callback_target.callback_count, kExpectedCalls + kMargin);

  bpftrace_wrapper.Stop();
}

TEST(BPFTracerWrapperTest, OutputFields) {
  std::string script = R"(
        interval:ms:100 {
            printf("%llu %u %s %s\n", nsecs, pid, comm, ntop(0));
        }
    )";

  BPFTraceWrapper bpftrace_wrapper;
  ASSERT_OK(bpftrace_wrapper.CompileForPrintfOutput(script));

  const std::vector<bpftrace::Field>& fields = bpftrace_wrapper.OutputFields();

  ASSERT_EQ(fields.size(), 4);

  EXPECT_EQ(fields[0].type.type, bpftrace::Type::integer);
  EXPECT_EQ(fields[0].type.size(), 8);

  EXPECT_EQ(fields[1].type.type, bpftrace::Type::integer);
  EXPECT_EQ(fields[1].type.size(), 8);

  EXPECT_EQ(fields[2].type.type, bpftrace::Type::string);
  EXPECT_EQ(fields[2].type.size(), 16);

  EXPECT_EQ(fields[3].type.type, bpftrace::Type::inet);
  EXPECT_EQ(fields[3].type.size(), 24);
}

TEST(BPFTracerWrapperTest, MultiplePrintfs) {
  std::string script = R"(
          interval:ms:100 {
              printf("time_:%llu id:%u s:%s ip:%s", nsecs, pid, comm, ntop(0));
              printf("time_:%llu id:%u s:%s ip:%s", nsecs, tid, "foo", ntop(1));
          }
      )";

  BPFTraceWrapper bpftrace_wrapper;
  ASSERT_OK(bpftrace_wrapper.CompileForPrintfOutput(script));
  const std::vector<bpftrace::Field>& fields = bpftrace_wrapper.OutputFields();

  ASSERT_EQ(fields.size(), 4);

  EXPECT_EQ(fields[0].type.type, bpftrace::Type::integer);
  EXPECT_EQ(fields[0].type.size(), 8);

  EXPECT_EQ(fields[1].type.type, bpftrace::Type::integer);
  EXPECT_EQ(fields[1].type.size(), 8);

  EXPECT_EQ(fields[2].type.type, bpftrace::Type::string);
  EXPECT_EQ(fields[2].type.size(), 16);

  EXPECT_EQ(fields[3].type.type, bpftrace::Type::inet);
  EXPECT_EQ(fields[3].type.size(), 24);
}

TEST(BPFTracerWrapperTest, InconsistentPrintfs) {
  {
    std::string script = R"(
            interval:ms:100 {
                printf("time_:%llu id:%u s:%s ip:%s", nsecs, pid, comm, ntop(0));
                printf("time_:%llu XX:%u s:%s ip:%s", nsecs, tid, "foo", ntop(1));
            }
        )";

    BPFTraceWrapper bpftrace_wrapper;
    ASSERT_THAT(
        bpftrace_wrapper.CompileForPrintfOutput(script).status(),
        StatusIs(statuspb::INTERNAL,
                 HasSubstr("All printf statements must have exactly the same format string")));
  }

  {
    std::string script = R"(
            interval:ms:100 {
                printf("time_:%llu id:%u s:%s ip:%s", nsecs, pid, comm, ntop(0));
                printf("time_:%llu id:%llu s:%s ip:%s", nsecs, tid, "foo", ntop(1));
            }
        )";

    BPFTraceWrapper bpftrace_wrapper;
    ASSERT_THAT(
        bpftrace_wrapper.CompileForPrintfOutput(script).status(),
        StatusIs(statuspb::INTERNAL,
                 HasSubstr("All printf statements must have exactly the same format string")));
  }
}

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

TEST(BPFTracerWrapperTest, CompileIsSerializedAcrossInstances) {
  std::unique_lock<std::mutex> held(BPFTraceWrapper::bpftrace_mutex());

  std::atomic<bool> done = false;
  BPFTraceWrapper bpftrace_wrapper;
  std::thread compile([&] {
    EXPECT_NOT_OK(bpftrace_wrapper.CompileForPrintfOutput("not a bpftrace program"));
    done = true;
  });

  std::this_thread::sleep_for(std::chrono::seconds(2));
  EXPECT_FALSE(done);

  held.unlock();
  compile.join();
  EXPECT_TRUE(done);
}

TEST(BPFTracerWrapperTest, DeployIsSerializedAcrossInstances) {
  constexpr std::string_view kScript = R"(
    interval:ms:100 {
        printf("%llu\n", nsecs);
    }
    )";

  std::chrono::steady_clock::duration unlocked_deploy_time;
  {
    BPFTraceWrapper bpftrace_wrapper;
    ASSERT_OK(bpftrace_wrapper.CompileForMapOutput(kScript));
    const auto start = std::chrono::steady_clock::now();
    ASSERT_OK(bpftrace_wrapper.Deploy());
    unlocked_deploy_time = std::chrono::steady_clock::now() - start;
    bpftrace_wrapper.Stop();
  }

  BPFTraceWrapper bpftrace_wrapper;
  ASSERT_OK(bpftrace_wrapper.CompileForMapOutput(kScript));

  std::unique_lock<std::mutex> held(BPFTraceWrapper::bpftrace_mutex());

  std::chrono::steady_clock::time_point deployed_at;
  std::thread deploy([&] {
    EXPECT_OK(bpftrace_wrapper.Deploy());
    deployed_at = std::chrono::steady_clock::now();
  });

  std::this_thread::sleep_for(3 * unlocked_deploy_time + std::chrono::seconds(1));
  const auto released_at = std::chrono::steady_clock::now();
  held.unlock();
  deploy.join();
  EXPECT_GE(deployed_at, released_at);

  bpftrace_wrapper.Stop();
}

TEST(BPFTracerWrapperTest, ConcurrentCompilesWithKernelHeaders) {
  constexpr int kNumCompiles = 8;

  std::vector<std::unique_ptr<BPFTraceWrapper>> wrappers;
  for (int i = 0; i < kNumCompiles; ++i) {
    wrappers.push_back(std::make_unique<BPFTraceWrapper>());
  }

  std::vector<Status> results(kNumCompiles);
  std::vector<std::thread> threads;
  for (int i = 0; i < kNumCompiles; ++i) {
    threads.emplace_back(
        [&, i] { results[i] = wrappers[i]->CompileForPrintfOutput(kKernelHeaderScript); });
  }
  for (auto& t : threads) {
    t.join();
  }

  for (int i = 0; i < kNumCompiles; ++i) {
    SCOPED_TRACE(i);
    ASSERT_OK(results[i]);
    EXPECT_EQ(wrappers[i]->OutputFields().size(), 3);
  }
}

}  // namespace bpf_tools
}  // namespace stirling
}  // namespace px
