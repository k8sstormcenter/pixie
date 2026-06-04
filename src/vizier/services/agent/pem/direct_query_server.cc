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

// STUB IMPLEMENTATION — entlein/dx#29. Deliberately the TDD "red" state: auth
// fails closed and ExecuteScript is UNIMPLEMENTED so direct_query_server_test.cc
// fails on the execution-path cases until the pem-agent ports the real logic from
// src/experimental/standalone_pem/vizier_server.h (against the live Carnot) and
// implements JWT verification.

#include "src/vizier/services/agent/pem/direct_query_server.h"

#include <string>

namespace px {
namespace vizier {
namespace agent {

::grpc::Status AuthenticateRequest(::grpc::ServerContext* ctx, const std::string& jwt_signing_key) {
  // Fail closed: with no real verification yet, every call is unauthenticated.
  // This already satisfies the "missing/invalid token → UNAUTHENTICATED" cases.
  // TODO(pem-agent): extract the bearer token from ctx->client_metadata()
  // ("authorization"), verify HS256 signature against jwt_signing_key, check exp
  // and the vizier audience, and return OK only then. Use src/shared/services/utils.
  (void)ctx;
  (void)jwt_signing_key;
  return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED,
                        "direct-query: auth not implemented (#29)");
}

::grpc::Status DirectQueryServer::ExecuteScript(
    ::grpc::ServerContext* context, const ::px::api::vizierpb::ExecuteScriptRequest* request,
    ::grpc::ServerWriter<::px::api::vizierpb::ExecuteScriptResponse>* writer) {
  if (auto s = AuthenticateRequest(context, jwt_signing_key_); !s.ok()) {
    return s;
  }
  if (request->mutation()) {
    return ::grpc::Status(::grpc::StatusCode::UNIMPLEMENTED,
                          "direct-query: mutations out of scope (#29)");
  }
  // Members are placeholders until Step 2 ports the standalone_pem exec path;
  // touch them to keep -Wunused-private-field happy under -Werror.
  (void)writer;
  (void)carnot_;
  (void)engine_state_;
  // TODO(pem-agent): port the standalone_pem VizierServer execution path — compile
  // the PxL via engine_state_->CreateLocalExecutionCompilerState, run on carnot_,
  // stream the result table(s) as ExecuteScriptResponse rows.
  return ::grpc::Status(::grpc::StatusCode::UNIMPLEMENTED,
                        "direct-query: ExecuteScript not implemented (#29)");
}

}  // namespace agent
}  // namespace vizier
}  // namespace px
