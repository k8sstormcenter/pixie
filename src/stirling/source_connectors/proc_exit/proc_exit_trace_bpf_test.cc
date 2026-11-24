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

#include "src/stirling/source_connectors/proc_exit/proc_exit_connector.h"

#include <sys/stat.h>

#include <fstream>
#include <string>
#include <vector>

#include <absl/container/flat_hash_map.h>

#include "src/common/exec/subprocess.h"
#include "src/common/metrics/metrics.h"
#include "src/common/system/proc_parser.h"
#include "src/common/testing/testing.h"
#include "src/stirling/core/connector_context.h"
#include "src/stirling/core/data_tables.h"
#include "src/stirling/testing/common.h"

namespace px {
namespace stirling {
namespace proc_exit_tracer {

using ::px::SubProcess;
using ::px::stirling::testing::RecordBatchSizeIs;
using ::px::testing::BazelRunfilePath;
using ::testing::ElementsAre;
using ::testing::Pair;
using ::testing::SizeIs;

class ProcessParentContext : public StandaloneContext {
 public:
  using StandaloneContext::StandaloneContext;

  void AddProcessParent(const md::UPID& upid, const md::UPID& parent_upid) override {
    parents_[upid] = parent_upid;
  }

  void AddProcessCgroup(const md::UPID& upid, uint64_t cgroup_id,
                        const std::string& cgroup_path) override {
    cgroups_[upid] = cgroup_id;
    cgroup_paths_[upid] = cgroup_path;
  }

  const absl::flat_hash_map<md::UPID, md::UPID>& parents() const { return parents_; }
  const absl::flat_hash_map<md::UPID, uint64_t>& cgroups() const { return cgroups_; }
  const absl::flat_hash_map<md::UPID, std::string>& cgroup_paths() const { return cgroup_paths_; }

 private:
  absl::flat_hash_map<md::UPID, md::UPID> parents_;
  absl::flat_hash_map<md::UPID, uint64_t> cgroups_;
  absl::flat_hash_map<md::UPID, std::string> cgroup_paths_;
};

// Tests that ProcExitConnector::TransferData() does not throw any failures.
TEST(ProcExitConnectorTest, TransferData) {
  auto connector = ProcExitConnector::Create("test_proc_exit_connector");
  ASSERT_TRUE(connector != nullptr);
  EXPECT_OK(connector->Init());
  StandaloneContext context({md::UPID{0, 1, 1}});

  DataTables data_tables{ProcExitConnector::kTables};
  DataTable* data_table = data_tables.tables().front();

  connector->set_data_tables(data_tables.tables());

  const std::filesystem::path sleep_path =
      BazelRunfilePath("src/stirling/source_connectors/proc_exit/testing/sleep");
  SubProcess proc;
  ASSERT_OK(proc.Start({sleep_path.string()}));
  ASSERT_TRUE(proc.IsRunning());
  proc.Kill();
  int retval = proc.Wait();
  EXPECT_EQ(retval, 9) << "Exit should not be 0 when killed";
  connector->TransferData(&context);

  types::ColumnWrapperRecordBatch result = testing::ExtractRecordsMatchingPID(
      data_table, kProcExitEventsTable.ColIndex("upid"), proc.child_pid());
  ASSERT_THAT(result, RecordBatchSizeIs(1));
  EXPECT_EQ(result[proc_exit_tracer::kSignalIdx]->Get<types::Int64Value>(0), 9);
  // Process abnormally terminated will not have a meaningful exit code.
  // So here 0 means it's not set, instead of that it succeeded.
  EXPECT_EQ(result[proc_exit_tracer::kExitCodeIdx]->Get<types::Int64Value>(0), 0);
  EXPECT_EQ(result[proc_exit_tracer::kCommIdx]->Get<types::StringValue>(0), "sleep");
}

TEST(ProcExitConnectorTest, ReportsParentOfNewProcess) {
  auto connector = ProcExitConnector::Create("test_proc_exit_connector");
  ASSERT_TRUE(connector != nullptr);
  EXPECT_OK(connector->Init());
  ProcessParentContext context({md::UPID{0, 1, 1}});

  DataTables data_tables{ProcExitConnector::kTables};
  connector->set_data_tables(data_tables.tables());

  const std::filesystem::path sleep_path =
      BazelRunfilePath("src/stirling/source_connectors/proc_exit/testing/sleep");
  SubProcess proc;
  ASSERT_OK(proc.Start({sleep_path.string()}));
  system::ProcParser proc_parser;
  ASSERT_OK_AND_ASSIGN(int64_t start_time, proc_parser.GetPIDStartTimeTicks(proc.child_pid()));
  ASSERT_OK_AND_ASSIGN(int64_t parent_start_time, proc_parser.GetPIDStartTimeTicks(getpid()));
  proc.Kill();
  proc.Wait();
  connector->TransferData(&context);

  const md::UPID upid(0, proc.child_pid(), start_time);
  ASSERT_TRUE(context.parents().contains(upid));
  EXPECT_EQ(context.parents().at(upid), md::UPID(0, getpid(), parent_start_time));
}

TEST(ProcExitConnectorTest, ReportsCgroupOfExitedProcess) {
  auto connector = ProcExitConnector::Create("test_proc_exit_connector");
  ASSERT_TRUE(connector != nullptr);
  EXPECT_OK(connector->Init());
  ProcessParentContext context({md::UPID{0, 1, 1}});

  DataTables data_tables{ProcExitConnector::kTables};
  connector->set_data_tables(data_tables.tables());

  std::string cgroup;
  std::ifstream ifs("/proc/self/cgroup");
  for (std::string line; std::getline(ifs, line);) {
    if (absl::StartsWith(line, "0::")) {
      cgroup = line.substr(3);
    }
  }
  struct stat st;
  ASSERT_EQ(0, stat(("/sys/fs/cgroup" + cgroup).c_str(), &st));

  SubProcess proc;
  ASSERT_OK(proc.Start([] { return 0; }, {}));
  proc.Wait();
  connector->TransferData(&context);

  std::vector<uint64_t> cgroups;
  for (const auto& [upid, cgroup_id] : context.cgroups()) {
    if (upid.pid() == static_cast<uint32_t>(proc.child_pid())) {
      cgroups.push_back(cgroup_id);
    }
  }
  EXPECT_THAT(cgroups, ElementsAre(st.st_ino));
}

TEST(ProcExitConnectorTest, ReportsPathOfCgroupCreatedWhileTracing) {
  auto connector = ProcExitConnector::Create("test_proc_exit_connector");
  ASSERT_TRUE(connector != nullptr);
  EXPECT_OK(connector->Init());
  ProcessParentContext context({md::UPID{0, 1, 1}});

  DataTables data_tables{ProcExitConnector::kTables};
  connector->set_data_tables(data_tables.tables());

  const std::string name = absl::Substitute("proc_exit_test_$0", getpid());
  const std::string dir = "/sys/fs/cgroup/" + name;
  ASSERT_EQ(0, mkdir(dir.c_str(), 0755));
  struct stat st;
  ASSERT_EQ(0, stat(dir.c_str(), &st));

  SubProcess proc;
  ASSERT_OK(proc.Start(
      [&dir] {
        std::ofstream(dir + "/cgroup.procs") << getpid();
        return 0;
      },
      {}));
  proc.Wait();
  ASSERT_EQ(0, rmdir(dir.c_str()));
  connector->TransferData(&context);

  std::vector<std::pair<uint64_t, std::string>> cgroups;
  for (const auto& [upid, cgroup_path] : context.cgroup_paths()) {
    if (upid.pid() == static_cast<uint32_t>(proc.child_pid())) {
      cgroups.emplace_back(context.cgroups().at(upid), cgroup_path);
    }
  }
  EXPECT_THAT(cgroups, ElementsAre(Pair(st.st_ino, "/" + name)));
}

TEST(ProcExitConnectorTest, ReportsParentOfShortLivedProcess) {
  auto connector = ProcExitConnector::Create("test_proc_exit_connector");
  ASSERT_TRUE(connector != nullptr);
  EXPECT_OK(connector->Init());
  ProcessParentContext context({md::UPID{0, 1, 1}});

  DataTables data_tables{ProcExitConnector::kTables};
  connector->set_data_tables(data_tables.tables());

  system::ProcParser proc_parser;
  ASSERT_OK_AND_ASSIGN(int64_t parent_start_time, proc_parser.GetPIDStartTimeTicks(getpid()));
  SubProcess proc;
  ASSERT_OK(proc.Start([] { return 0; }, {}));
  proc.Wait();
  connector->TransferData(&context);

  std::vector<md::UPID> parents;
  for (const auto& [upid, parent_upid] : context.parents()) {
    if (upid.pid() == static_cast<uint32_t>(proc.child_pid())) {
      parents.push_back(parent_upid);
    }
  }
  EXPECT_THAT(parents, ElementsAre(md::UPID(0, getpid(), parent_start_time)));
}

TEST(ProcExitConnectorTest, CountsLostForkEvents) {
  auto connector = ProcExitConnector::Create("test_proc_exit_connector");
  static_cast<ProcExitConnector*>(connector.get())->AcceptProcForkEventLoss(3);

  double lost = 0;
  for (const auto& family : GetMetricsRegistry().Collect()) {
    if (family.name == "proc_fork_events_lost") {
      lost = family.metric[0].counter.value;
    }
  }
  EXPECT_EQ(lost, 3);
}

}  // namespace proc_exit_tracer
}  // namespace stirling
}  // namespace px
