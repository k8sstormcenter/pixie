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

#include <sole.hpp>

#include "src/integrations/grpc_clocksync/grpc_clock_converter.h"
#include "src/vizier/services/agent/pem/pem_manager.h"
#include "src/vizier/services/agent/shared/base/lifecycle.h"

#include "src/common/base/base.h"
#include "src/common/signal/signal.h"
#include "src/common/system/kernel_version.h"
#include "src/common/system/linux_headers_utils.h"
#include "src/shared/version/version.h"

DEFINE_string(nats_url, gflags::StringFromEnv("PL_NATS_URL", "pl-nats"),
              "The host address of the nats cluster");
DEFINE_string(pod_name, gflags::StringFromEnv("PL_POD_NAME", ""),
              "The name of the POD the PEM is running on");
DEFINE_string(host_ip, gflags::StringFromEnv("PL_HOST_IP", ""),
              "The IP of the host this service is running on");
DEFINE_string(clock_converter, gflags::StringFromEnv("PL_CLOCK_CONVERTER", "default"),
              "Which ClockConverter to use for converting from mono to reference time. Current "
              "options are 'default' or 'grpc'");

// entlein/dx#29 — direct-query gRPC endpoint on the normal PEM. See
// src/vizier/services/agent/pem/DIRECT_QUERY_CONTRACT.md.
// Default-OFF: flag false → port never opened → existing PEM deploys unchanged.
// When true, pem_manager constructs DirectQueryServer against the PEM's live
// Carnot + EngineState and binds the listener. PL_JWT_SIGNING_KEY shares the
// existing manager.cc DEFINE_string(jwt_signing_key) env so a single secret
// covers both the outgoing mint path and the incoming verify path.
DEFINE_bool(direct_query_enabled, gflags::BoolFromEnv("PL_PEM_DIRECT_QUERY_ENABLED", false),
            "If true, expose VizierService::ExecuteScript directly from this PEM. "
            "Default false; existing PEM deploys see no behavior change.");
DEFINE_int32(direct_query_port, gflags::Int32FromEnv("PL_PEM_DIRECT_QUERY_PORT", 50305),
             "gRPC listen port for the direct-query service when "
             "--direct_query_enabled=true.");
DEFINE_string(direct_query_jwt_signing_key, gflags::StringFromEnv("PL_JWT_SIGNING_KEY", ""),
              "HMAC key the bearer JWT must verify against. Required when "
              "--direct_query_enabled=true. Shared with the existing PEM-side "
              "manager JWT mint path (manager.cc DEFINE_string(jwt_signing_key)).");

using ::px::vizier::agent::DefaultDeathHandler;
using ::px::vizier::agent::PEMManager;
using ::px::vizier::agent::TerminationHandler;

int main(int argc, char** argv) {
  px::EnvironmentGuard env_guard(&argc, argv);

  DefaultDeathHandler err_handler;

  // This covers signals such as SIGSEGV and other fatal errors.
  // We print the stack trace and die.
  auto signal_action = std::make_unique<px::SignalAction>();
  signal_action->RegisterFatalErrorHandler(err_handler);

  // Install signal handlers where graceful exit is possible.
  TerminationHandler::InstallSignalHandlers();

  sole::uuid agent_id = sole::uuid4();

  if (FLAGS_clock_converter == "grpc") {
    px::system::Config::ResetInstance(
        std::make_unique<px::integrations::grpc_clocksync::GRPCClockConverter>());
  }

  if (FLAGS_host_ip.length() == 0) {
    LOG(FATAL) << "The HOST_IP must be specified";
  }
  px::system::KernelVersion kernel_version = px::system::GetCachedKernelVersion();
  LOG(INFO) << absl::Substitute("Pixie PEM. Version: $0, id: $1, kernel version: $2",
                                px::VersionInfo::VersionString(), agent_id.str(),
                                kernel_version.ToString());

  auto kernel_headers_installed = false;
  auto uname = px::system::GetUname();
  if (uname.ok()) {
    const auto host_path = px::system::Config::GetInstance().ToHostPath(
        absl::StrCat(px::system::kLinuxModulesDir, uname.ConsumeValueOrDie(), "/build"));

    const auto resolved_host_path = px::system::ResolvePossibleSymlinkToHostPath(host_path);
    kernel_headers_installed = resolved_host_path.ok();
  }

  auto kernel_info = px::system::KernelInfo{
      kernel_version,
      kernel_headers_installed,
  };
  auto manager =
      PEMManager::Create(agent_id, FLAGS_pod_name, FLAGS_host_ip, FLAGS_nats_url, kernel_info)
          .ConsumeValueOrDie();

  TerminationHandler::set_manager(manager.get());

  PX_CHECK_OK(manager->Run());
  PX_CHECK_OK(manager->Stop(std::chrono::seconds{5}));

  // Clear the manager, because it has been stopped.
  TerminationHandler::set_manager(nullptr);

  return 0;
}
