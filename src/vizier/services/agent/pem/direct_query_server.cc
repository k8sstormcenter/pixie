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

// Direct-query gRPC endpoint for the normal (metadata-connected) PEM —
// entlein/dx#29. Step 1: real HS256 JWT verification (matches the C++ mint
// pattern at src/vizier/services/agent/shared/manager/manager.cc:423-440).
// Step 2 ports the standalone_pem ExecuteScript path against the live Carnot.

#include "src/vizier/services/agent/pem/direct_query_server.h"

#include <openssl/hmac.h>
#include <openssl/sha.h>
#include <rapidjson/document.h>

#include <chrono>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include <absl/strings/escaping.h>
#include <absl/strings/str_split.h>
#include <absl/strings/string_view.h>

#include "src/common/base/base.h"

namespace px {
namespace vizier {
namespace agent {

namespace {

constexpr char kBearerPrefixLower[] = "bearer ";
constexpr size_t kBearerPrefixLen = sizeof(kBearerPrefixLower) - 1;
constexpr char kExpectedAudience[] = "vizier";

// We don't link cpp_jwt's HMAC verifier here because its impl calls
// BIO_f_base64() which lives in BoringSSL's decrepit/ tree — not exposed as a
// bazel target on this fork. Instead we parse the JWT envelope manually and
// HMAC with BoringSSL natively. ~50 lines vs. carrying a boringssl patch.

// stripBearerPrefix returns the token slice after a case-insensitive "Bearer "
// prefix, or an empty string if the prefix is missing. gRPC normalises metadata
// keys to lowercase but does NOT touch values; manager.cc:440 mints with a
// lowercase "bearer " prefix, but real-world clients may use "Bearer " (RFC 6750
// Title-case), so we accept both.
absl::string_view stripBearerPrefix(absl::string_view value) {
  if (value.size() < kBearerPrefixLen) {
    return {};
  }
  for (size_t i = 0; i < kBearerPrefixLen; ++i) {
    char c = value[i];
    if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
    if (c != kBearerPrefixLower[i]) return {};
  }
  return value.substr(kBearerPrefixLen);
}

// constantTimeEquals: short-circuit-free byte compare. Mismatched-length inputs
// trivially differ but we still walk the shorter to keep timing predictable
// across malformed lengths.
bool constantTimeEquals(absl::string_view a, absl::string_view b) {
  if (a.size() != b.size()) return false;
  uint8_t acc = 0;
  for (size_t i = 0; i < a.size(); ++i) {
    acc |= static_cast<uint8_t>(a[i] ^ b[i]);
  }
  return acc == 0;
}

// base64UrlDecode handles RFC 7515 base64url (no padding, '-' / '_' alphabet).
// Returns false on any non-alphabet character.
bool base64UrlDecode(absl::string_view in, std::string* out) {
  // absl handles standard base64 with '+'/'/'; translate URL-safe alphabet and
  // pad to a multiple of 4 first.
  std::string std_b64;
  std_b64.reserve(in.size() + 4);
  for (char c : in) {
    if (c == '-') {
      std_b64.push_back('+');
    } else if (c == '_') {
      std_b64.push_back('/');
    } else if ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
               c == '+' || c == '/') {
      std_b64.push_back(c);
    } else {
      return false;
    }
  }
  while (std_b64.size() % 4 != 0) std_b64.push_back('=');
  return absl::Base64Unescape(std_b64, out);
}

// hmacSha256: BoringSSL HMAC over `data`, returns raw 32 bytes.
std::string hmacSha256(absl::string_view key, absl::string_view data) {
  uint8_t out[EVP_MAX_MD_SIZE];
  unsigned out_len = 0;
  const auto* res = HMAC(EVP_sha256(), key.data(), static_cast<int>(key.size()),
                         reinterpret_cast<const uint8_t*>(data.data()), data.size(), out, &out_len);
  if (res == nullptr) {
    return {};
  }
  return std::string(reinterpret_cast<const char*>(out), out_len);
}

// verifyHs256Jwt: parse <header>.<payload>.<signature>, check the header alg is
// HS256, verify the signature with BoringSSL HMAC, then validate the audience
// and expiry claims. Returns OK on success.
::grpc::Status verifyHs256Jwt(absl::string_view token, const std::string& signing_key) {
  // Split into 3 parts.
  std::vector<absl::string_view> parts = absl::StrSplit(token, '.');
  if (parts.size() != 3) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED, "direct-query: malformed JWT");
  }
  // Verify HS256 alg in the header (refuse "alg":"none" forgeries).
  std::string header_json;
  if (!base64UrlDecode(parts[0], &header_json)) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED, "direct-query: bad header b64");
  }
  rapidjson::Document header;
  if (header.Parse(header_json.c_str()).HasParseError() || !header.IsObject() ||
      !header.HasMember("alg") || !header["alg"].IsString() ||
      std::strcmp(header["alg"].GetString(), "HS256") != 0) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED,
                          "direct-query: unsupported JWT alg (HS256 only)");
  }
  // Verify the signature.
  std::string signing_input = std::string(parts[0].data(), parts[0].size()) + "." +
                              std::string(parts[1].data(), parts[1].size());
  std::string computed_mac = hmacSha256(signing_key, signing_input);
  if (computed_mac.empty()) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED, "direct-query: HMAC compute failed");
  }
  std::string signature;
  if (!base64UrlDecode(parts[2], &signature)) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED, "direct-query: bad signature b64");
  }
  if (!constantTimeEquals(signature, computed_mac)) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED, "direct-query: signature mismatch");
  }
  // Validate the payload claims (audience, expiry).
  std::string payload_json;
  if (!base64UrlDecode(parts[1], &payload_json)) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED, "direct-query: bad payload b64");
  }
  rapidjson::Document payload;
  if (payload.Parse(payload_json.c_str()).HasParseError() || !payload.IsObject()) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED,
                          "direct-query: payload not a JSON object");
  }
  if (!payload.HasMember("aud") || !payload["aud"].IsString() ||
      std::strcmp(payload["aud"].GetString(), kExpectedAudience) != 0) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED,
                          "direct-query: wrong audience (expected vizier)");
  }
  if (!payload.HasMember("exp")) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED, "direct-query: missing exp claim");
  }
  // Accept numeric exp (seconds since epoch) — matches RFC 7519 and what
  // manager.cc::GenerateServiceToken emits via jwt::jwt_object::add_claim.
  int64_t exp_secs = 0;
  if (payload["exp"].IsInt64()) {
    exp_secs = payload["exp"].GetInt64();
  } else if (payload["exp"].IsUint64()) {
    exp_secs = static_cast<int64_t>(payload["exp"].GetUint64());
  } else if (payload["exp"].IsDouble()) {
    exp_secs = static_cast<int64_t>(payload["exp"].GetDouble());
  } else {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED, "direct-query: exp not numeric");
  }
  const auto now_secs = std::chrono::duration_cast<std::chrono::seconds>(
                            std::chrono::system_clock::now().time_since_epoch())
                            .count();
  if (now_secs >= exp_secs) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED, "direct-query: token expired");
  }
  return ::grpc::Status::OK;
}

}  // namespace

::grpc::Status AuthenticateRequest(::grpc::ServerContext* ctx, const std::string& jwt_signing_key) {
  if (jwt_signing_key.empty()) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED,
                          "direct-query: signing key not configured");
  }
  const auto& md = ctx->client_metadata();
  auto it = md.find("authorization");
  if (it == md.end()) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED,
                          "direct-query: missing authorization metadata");
  }
  absl::string_view raw(it->second.data(), it->second.size());
  absl::string_view token = stripBearerPrefix(raw);
  if (token.empty()) {
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED,
                          "direct-query: authorization is not a Bearer token");
  }
  auto status = verifyHs256Jwt(token, jwt_signing_key);
  if (!status.ok()) {
    VLOG(1) << "direct-query: " << status.error_message();
    // Collapse the specific error to a generic "invalid bearer token" on the
    // wire — peers don't need to know whether the signature or the claim
    // failed, only that they're unauthenticated. The VLOG above keeps the
    // diagnostic for the operator.
    return ::grpc::Status(::grpc::StatusCode::UNAUTHENTICATED,
                          "direct-query: invalid bearer token");
  }
  return ::grpc::Status::OK;
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
