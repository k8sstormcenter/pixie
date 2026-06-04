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

// TDD contract for the PEM direct-query endpoint (entlein/dx#29).
//
// Authored by dx-agent as the executable spec; the pem-agent makes it pass.
// These are the acceptance criteria from DIRECT_QUERY_CONTRACT.md. The stub in
// direct_query_server.cc fails closed (UNAUTHENTICATED / UNIMPLEMENTED), so the
// auth-negative cases pass today and the positive/execution cases are the red work.
//
// The fixture runs an in-process gRPC server hosting DirectQueryServer and a real
// client stub, so authorization metadata flows exactly as in production.

#include <chrono>
#include <memory>
#include <string>
#include <vector>  // std::vector for the aud-array mint

#include <grpcpp/grpcpp.h>
#include <grpcpp/security/server_credentials.h>
#include <jwt/jwt.hpp>
#include <sole.hpp>

#include "src/api/proto/vizierpb/vizierapi.grpc.pb.h"
#include "src/carnot/carnot.h"
#include "src/carnot/exec/local_grpc_result_server.h"
#include "src/carnot/funcs/funcs.h"
#include "src/carnot/udf/registry.h"
#include "src/common/testing/testing.h"
#include "src/table_store/table_store.h"
#include "src/vizier/services/agent/pem/direct_query_server.h"

namespace px {
namespace vizier {
namespace agent {

constexpr char kTestSigningKey[] = "test-signing-key-do-not-use-in-prod";
constexpr char kWrongSigningKey[] = "a-different-key";

enum class TokenKind { kValid, kWrongKey, kExpired };

// MakeBearerToken mints a JWT for the in-process call's `authorization` metadata,
// matching the verifier in AuthenticateRequest. Claim shape mirrors
// src/vizier/services/agent/shared/manager/manager.cc::GenerateServiceToken —
// HS256 / iss=PL / aud=vizier / iat,nbf,exp / sub=service. The token-maker and
// the verifier are a matched pair.
//
// - kValid:    signed with `signing_key`, exp +60s
// - kWrongKey: signed with `signing_key` argument — caller passes the wrong key
//              so the resulting token won't verify against the server's key
// - kExpired:  signed with `signing_key`, exp 60s in the past
std::string MakeBearerToken(const std::string& signing_key, TokenKind kind) {
  using std::chrono::seconds;
  using std::chrono::system_clock;
  auto now = system_clock::now();
  auto exp_offset = (kind == TokenKind::kExpired) ? seconds{-60} : seconds{60};

  jwt::jwt_object obj{jwt::params::algorithm("HS256")};
  obj.add_claim("iss", "PL");
  // RFC 7519 §4.1.3 + pixie's go mint convention (jwt.go:46
  // Audience([]string{...})) serialize aud as a JSON array. The verifier
  // (direct_query_server.cc) accepts both string and array forms, but the
  // unit test mints the ARRAY form so it guards against a regression to
  // string-only verification — dx-agent reports the live e2e would
  // UNAUTHENTICATED if we drifted back.
  obj.add_claim("aud", std::vector<std::string>{"vizier"});
  obj.add_claim("jti", "direct-query-test");
  obj.add_claim("iat", now);
  obj.add_claim("nbf", now - seconds{60});
  obj.add_claim("exp", now + exp_offset);
  obj.add_claim("sub", "service");
  obj.add_claim("Scopes", "service");
  obj.add_claim("ServiceID", "dx-test");
  obj.secret(signing_key);
  return obj.signature();
}

// Test fixture: in-process server hosting DirectQueryServer + a client stub.
class DirectQueryServerTest : public ::testing::Test {
 protected:
  void SetUp() override {
    // carnot/engine/result_server null for the auth + scope-guard cases (no
    // execution reached). Subclass DirectQueryServerExecTest below builds the
    // real Carnot fixture for ValidToken_TrivialQuery_StreamsRows.
    service_ = std::make_unique<DirectQueryServer>(/*carnot*/ nullptr, /*engine_state*/ nullptr,
                                                   /*result_server*/ nullptr, kTestSigningKey);
    ::grpc::ServerBuilder builder;
    builder.RegisterService(service_.get());
    server_ = builder.BuildAndStart();
    stub_ = ::px::api::vizierpb::VizierService::NewStub(server_->InProcessChannel({}));
  }
  void TearDown() override {
    if (server_) server_->Shutdown();
  }

  // Calls ExecuteScript with the given bearer token (empty = no auth header) and a
  // trivial query, draining the stream. Returns the final gRPC status.
  ::grpc::Status CallExecuteScript(const std::string& bearer, bool mutation = false) {
    ::grpc::ClientContext ctx;
    if (!bearer.empty()) ctx.AddMetadata("authorization", "Bearer " + bearer);
    ::px::api::vizierpb::ExecuteScriptRequest req;
    req.set_query_str("import px\npx.display(px.DataFrame('http_events'))");
    req.set_mutation(mutation);
    auto reader = stub_->ExecuteScript(&ctx, req);
    ::px::api::vizierpb::ExecuteScriptResponse resp;
    while (reader->Read(&resp)) { /* drain */
    }
    return reader->Finish();
  }

  std::unique_ptr<DirectQueryServer> service_;
  std::unique_ptr<::grpc::Server> server_;
  std::unique_ptr<::px::api::vizierpb::VizierService::Stub> stub_;
};

// 3a. No token → UNAUTHENTICATED (passes against the fail-closed stub today).
TEST_F(DirectQueryServerTest, NoToken_Unauthenticated) {
  EXPECT_EQ(::grpc::StatusCode::UNAUTHENTICATED, CallExecuteScript("").error_code());
}

// 3b. Token signed with the wrong key → UNAUTHENTICATED.
TEST_F(DirectQueryServerTest, WrongKey_Unauthenticated) {
  auto tok = MakeBearerToken(kWrongSigningKey, TokenKind::kWrongKey);
  EXPECT_EQ(::grpc::StatusCode::UNAUTHENTICATED, CallExecuteScript(tok).error_code());
}

// 3c. Expired token → UNAUTHENTICATED.
TEST_F(DirectQueryServerTest, ExpiredToken_Unauthenticated) {
  auto tok = MakeBearerToken(kTestSigningKey, TokenKind::kExpired);
  EXPECT_EQ(::grpc::StatusCode::UNAUTHENTICATED, CallExecuteScript(tok).error_code());
}

// 5. Mutations are out of scope → UNIMPLEMENTED (and proves a valid token authenticates).
TEST_F(DirectQueryServerTest, ValidToken_Mutation_Unimplemented) {
  auto tok = MakeBearerToken(kTestSigningKey, TokenKind::kValid);
  EXPECT_EQ(::grpc::StatusCode::UNIMPLEMENTED,
            CallExecuteScript(tok, /*mutation*/ true).error_code());
}

// 2. Valid token + trivial query → OK stream. The fixture below builds a
// real CarnotTest-style table_store + Carnot + LocalGRPCResultSinkServer, then
// passes them to DirectQueryServer, so ExecuteScript actually compiles and
// runs the PxL.
class DirectQueryServerExecTest : public ::testing::Test {
 protected:
  void SetUp() override {
    table_store_ = std::make_shared<::px::table_store::TableStore>();
    result_server_ = std::make_unique<::px::carnot::exec::LocalGRPCResultSinkServer>();

    auto func_registry = std::make_unique<::px::carnot::udf::Registry>("direct_query_test");
    ::px::carnot::funcs::RegisterFuncsOrDie(func_registry.get());

    auto clients_config =
        std::make_unique<::px::carnot::Carnot::ClientsConfig>(::px::carnot::Carnot::ClientsConfig{
            [this](const std::string& address, const std::string&) {
              return result_server_->StubGenerator(address);
            },
            [](::grpc::ClientContext*) {},
        });
    auto server_config = std::make_unique<::px::carnot::Carnot::ServerConfig>();
    server_config->grpc_server_creds = ::grpc::InsecureServerCredentials();
    server_config->grpc_server_port = 0;

    carnot_ = ::px::carnot::Carnot::Create(sole::uuid4(), std::move(func_registry), table_store_,
                                           std::move(clients_config), std::move(server_config))
                  .ConsumeValueOrDie();
    table_store_->AddTable("http_events", MakeHTTPEventsTable());

    service_ = std::make_unique<DirectQueryServer>(carnot_.get(), carnot_->GetEngineState(),
                                                   result_server_.get(), kTestSigningKey);
    ::grpc::ServerBuilder builder;
    builder.RegisterService(service_.get());
    server_ = builder.BuildAndStart();
    stub_ = ::px::api::vizierpb::VizierService::NewStub(server_->InProcessChannel({}));
  }

  void TearDown() override {
    if (server_) server_->Shutdown();
  }

  // Same schema as CarnotTestUtils::HTTPEventsTable — kept inline so we don't
  // pull //src/carnot/exec:test_utils through a fragile alias. Empty (no rows
  // appended) is fine for the trivial query: the PxL just enumerates the
  // schema; the test asserts OK + >=1 streamed response.
  std::shared_ptr<::px::table_store::Table> MakeHTTPEventsTable() {
    ::px::table_store::schema::Relation rel(
        {
            ::px::types::DataType::TIME64NS,
            ::px::types::DataType::UINT128,
            ::px::types::DataType::STRING,
            ::px::types::DataType::INT64,
            ::px::types::DataType::INT64,
        },
        {"time_", "upid", "remote_addr", "remote_port", "trace_role"});
    return ::px::table_store::Table::Create("http_events", rel);
  }

  std::shared_ptr<::px::table_store::TableStore> table_store_;
  std::unique_ptr<::px::carnot::exec::LocalGRPCResultSinkServer> result_server_;
  std::unique_ptr<::px::carnot::Carnot> carnot_;
  std::unique_ptr<DirectQueryServer> service_;
  std::unique_ptr<::grpc::Server> server_;
  std::unique_ptr<::px::api::vizierpb::VizierService::Stub> stub_;
};

TEST_F(DirectQueryServerExecTest, ValidToken_TrivialQuery_StreamsRows) {
  auto tok = MakeBearerToken(kTestSigningKey, TokenKind::kValid);
  ::grpc::ClientContext ctx;
  ctx.AddMetadata("authorization", "Bearer " + tok);
  ::px::api::vizierpb::ExecuteScriptRequest req;
  req.set_query_str("import px\npx.display(px.DataFrame('http_events'))");
  req.set_mutation(false);
  auto reader = stub_->ExecuteScript(&ctx, req);
  int response_count = 0;
  ::px::api::vizierpb::ExecuteScriptResponse resp;
  while (reader->Read(&resp)) {
    ++response_count;
  }
  auto status = reader->Finish();
  EXPECT_EQ(::grpc::StatusCode::OK, status.error_code()) << status.error_message();
  EXPECT_GE(response_count, 1) << "expected at least one ExecuteScriptResponse";
}

// 4. Metadata-connected per-pod filter returns only that pod's rows (closes #15 on
// the real PEM). Integration-style; needs metadata + a multi-pod fixture.
TEST_F(DirectQueryServerTest, PerPodFilter_MetadataConnected) {
  GTEST_SKIP() << "TODO(pem-agent): with metadata wired, a per-pod-filtered PxL must "
                  "return only the target pod's rows (the gap standalone_pem could not close).";
}

}  // namespace agent
}  // namespace vizier
}  // namespace px
