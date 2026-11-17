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

#include <dlfcn.h>

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <algorithm>
#include <memory>
#include <string>
#include <utility>
#include <vector>

#include "src/common/testing/testing.h"
#include "src/stirling/obj_tools/dwarf_reader.h"
#include "src/stirling/obj_tools/elf_reader.h"
#include "src/stirling/obj_tools/go_syms.h"
#include "src/stirling/source_connectors/socket_tracer/uprobe_symaddrs.h"

namespace px {
namespace stirling {

using obj_tools::DwarfReader;
using obj_tools::ElfReader;
using obj_tools::SymbolMatchType;

class UprobeSymaddrsTest : public ::testing::Test {
 protected:
  void SetUp() {
    std::filesystem::path p = px::testing::BazelRunfilePath(kGoGRPCServer);
    ASSERT_OK_AND_ASSIGN(dwarf_reader_, DwarfReader::CreateIndexingAll(p));
    offset_locator_ = std::make_unique<GoOffsetLocator>(GoOffsetLocator(
        dwarf_reader_.get(), struct_offsets_, function_args_, build_info_, go_version_));
    ASSERT_OK_AND_ASSIGN(elf_reader_, ElfReader::Create(p));
  }

  static inline constexpr std::string_view kGoGRPCServer =
      "src/stirling/testing/demo_apps/go_grpc_tls_pl/server/golang_1_24_grpc_tls_server_binary";

  std::unique_ptr<DwarfReader> dwarf_reader_;
  std::unique_ptr<GoOffsetLocator> offset_locator_;
  StructOffsetMap struct_offsets_;
  FunctionArgMap function_args_;
  obj_tools::BuildInfo build_info_;
  std::string go_version_ = "1.19.0";
  std::unique_ptr<ElfReader> elf_reader_;
};

TEST_F(UprobeSymaddrsTest, GoCommonSymAddrs) {
  ASSERT_OK_AND_ASSIGN(struct go_common_symaddrs_t symaddrs,
                       GoCommonSymAddrs(elf_reader_.get(), offset_locator_.get()));

  // Check a few interface types.
  EXPECT_NE(symaddrs.tls_Conn, -1);
  EXPECT_NE(symaddrs.tls_Conn, 0);

  // Check some member offsets.
  // The values may change when golang version is updated.
  // If the test breaks because of that, just update the numbers here.
  EXPECT_EQ(symaddrs.FD_Sysfd_offset, 16);
  EXPECT_EQ(symaddrs.tlsConn_conn_offset, 0);
  EXPECT_EQ(symaddrs.g_goid_offset, 160);
}

TEST_F(UprobeSymaddrsTest, GoHTTP2SymAddrs) {
  ASSERT_OK_AND_ASSIGN(struct go_http2_symaddrs_t symaddrs,
                       GoHTTP2SymAddrs(elf_reader_.get(), offset_locator_.get()));

  // Check a few interface types.
  EXPECT_NE(symaddrs.transport_bufWriter, -1);
  EXPECT_NE(symaddrs.transport_bufWriter, 0);

  // Check some member offsets.
  // The values may change when golang version is updated.
  // If the test breaks because of that, just update the numbers here.
  EXPECT_EQ(symaddrs.http2Framer_WriteDataPadded_f_loc,
            (location_t{.type = kLocationTypeRegisters, .offset = 0}));
  EXPECT_EQ(symaddrs.writeHeader_hf_ptr_loc,
            (location_t{.type = kLocationTypeRegisters, .offset = 24}));
}

TEST_F(UprobeSymaddrsTest, GoTLSSymAddrs) {
  ASSERT_OK_AND_ASSIGN(struct go_tls_symaddrs_t symaddrs, GoTLSSymAddrs(offset_locator_.get()));

  // Check some member offsets.
  // The values may change when golang version is updated.
  // If the test breaks because of that, just update the numbers here.
  EXPECT_EQ(symaddrs.Write_c_loc, (location_t{.type = kLocationTypeRegisters, .offset = 0}));
  EXPECT_EQ(symaddrs.Write_b_loc, (location_t{.type = kLocationTypeRegisters, .offset = 8}));
  EXPECT_EQ(symaddrs.Read_c_loc, (location_t{.type = kLocationTypeRegisters, .offset = 0}));
  EXPECT_EQ(symaddrs.Read_b_loc, (location_t{.type = kLocationTypeRegisters, .offset = 8}));
}

// Note that DwarfReader cannot be created if there is no dwarf info.
TEST(UprobeSymaddrsNodeTest, TLSWrapSymAddrsFromDwarfInfo) {
  std::filesystem::path p =
      px::testing::BazelRunfilePath("src/stirling/testing/demo_apps/node/node_debug/node_debug");
  ASSERT_OK_AND_ASSIGN(struct node_tlswrap_symaddrs_t symaddrs, NodeTLSWrapSymAddrs(p, {0, 0, 0}));
  EXPECT_EQ(symaddrs.TLSWrap_StreamListener_offset, 0x08);
  EXPECT_EQ(symaddrs.StreamListener_stream_offset, 0x08);
  EXPECT_EQ(symaddrs.StreamBase_StreamResource_offset, 0x00);
  EXPECT_EQ(symaddrs.LibuvStreamWrap_StreamBase_offset, 0x00);
  EXPECT_EQ(symaddrs.LibuvStreamWrap_stream_offset, 0x08);
  EXPECT_EQ(symaddrs.uv_stream_s_io_watcher_offset, 0x00);
  EXPECT_EQ(symaddrs.uv__io_s_fd_offset, 0x04);
}

StructOffsetMap empty_struct_map;
FunctionArgMap empty_fn_arg_map;
obj_tools::BuildInfo empty_build_info;

TEST(GoOffsetLocator, GetStructMemberOffsetNoDWARFMissingStruct) {
  auto offset_locator =
      GoOffsetLocator(nullptr, empty_struct_map, empty_fn_arg_map, empty_build_info, "");
  auto status = offset_locator.GetStructMemberOffset("runtime.g", "goid");
  EXPECT_FALSE(status.ok());
}

// NOLINTNEXTLINE: runtime/string
std::string go_version = "1.19.0";
// NOLINTNEXTLINE: runtime/string
std::string golang_x_net_version = "v1.0.0";

auto struct_map = StructOffsetMap{
    {
        "runtime.g",
        {"std", {{"goid", {{go_version, 152}}}}},
    },
    {
        "golang.org/x/net/http2.DataFrame",
        {"golang.org/x/net", {{"data", {{golang_x_net_version.substr(1), 16}}}}},
    },
};

TEST(GoOffsetLocator, GetStructMemberOffsetNoDWARFMissingMember) {
  auto offset_locator =
      GoOffsetLocator(nullptr, struct_map, empty_fn_arg_map, empty_build_info, go_version);
  EXPECT_FALSE(offset_locator.GetStructMemberOffset("runtime.g", "missing_member").ok());
}

TEST(GoOffsetLocator, GetStructMemberOffsetNoDWARFMissingVersion) {
  auto offset_locator =
      GoOffsetLocator(nullptr, struct_map, empty_fn_arg_map, empty_build_info, "1.18.0");
  EXPECT_FALSE(offset_locator.GetStructMemberOffset("runtime.g", "goid").ok());
}

TEST(GoOffsetLocator, GetStructMemberOffsetNoDWARFSuccessfulLookupStdlib) {
  auto offset_locator =
      GoOffsetLocator(nullptr, struct_map, empty_fn_arg_map, empty_build_info, go_version);

  ASSERT_OK_AND_ASSIGN(uint64_t offset, offset_locator.GetStructMemberOffset("runtime.g", "goid"));
  EXPECT_EQ(offset, 152);
}

TEST(GoOffsetLocator, GetStructMemberOffsetNoDWARFSuccessfulLookupGolangXNet) {
  obj_tools::BuildInfo build_info;
  build_info.deps.push_back(
      obj_tools::Module{"golang.org/x/net", golang_x_net_version, "", nullptr});
  auto offset_locator =
      GoOffsetLocator(nullptr, struct_map, empty_fn_arg_map, build_info, go_version);

  ASSERT_OK_AND_ASSIGN(uint64_t offset, offset_locator.GetStructMemberOffset(
                                            "golang.org/x/net/http2.DataFrame", "data"));
  EXPECT_EQ(offset, 16);
}

TEST(GoOffsetLocator, GetFunctionArgInfoNoDWARFMissingFunction) {
  auto offset_locator =
      GoOffsetLocator(nullptr, empty_struct_map, empty_fn_arg_map, empty_build_info, go_version);
  auto status = offset_locator.GetFunctionArgInfo("missing_func");
  EXPECT_FALSE(status.ok());
}

FunctionArgMap GetFunctionArgMap() {
  auto var_location = obj_tools::VarLocation{obj_tools::LocationType::kRegister, 8};
  FunctionArgMap fn_arg_map;
  fn_arg_map["crypto/tls.(*Conn).Read"] = std::make_pair("std", FuncVersionMap{});
  fn_arg_map["crypto/tls.(*Conn).Read"].second["b"][go_version] =
      std::make_unique<obj_tools::VarLocation>(var_location);
  return fn_arg_map;
}

TEST(GoOffsetLocator, GetFunctionArgInfoNoDWARFMissingVersion) {
  FunctionArgMap fn_arg_map = GetFunctionArgMap();
  auto offset_locator =
      GoOffsetLocator(nullptr, struct_map, fn_arg_map, empty_build_info, "1.18.0");
  auto status = offset_locator.GetFunctionArgInfo("crypto/tls.(*Conn).Write");
  EXPECT_FALSE(status.ok());
}

TEST(GoOffsetLocator, GetStructMemberOffsetNoDWARFUnknownLocation) {
  auto var_location = obj_tools::VarLocation{obj_tools::LocationType::kUnknown, -1};
  auto fn_arg_map = GetFunctionArgMap();
  fn_arg_map["crypto/tls.(*Conn).Read"].second["b"][go_version] =
      std::make_unique<obj_tools::VarLocation>(var_location);

  auto offset_locator =
      GoOffsetLocator(nullptr, struct_map, fn_arg_map, empty_build_info, go_version);

  ASSERT_OK_AND_ASSIGN(auto args, offset_locator.GetFunctionArgInfo("crypto/tls.(*Conn).Read"));
  EXPECT_NE(args.find("b"), args.end());
  auto& arg_info = args["b"];
  EXPECT_EQ(arg_info.location.loc_type, obj_tools::LocationType::kUnknown);
  EXPECT_EQ(arg_info.location.offset, -1);
}

TEST(GoOffsetLocator, GetStructMemberOffsetNoDWARFNullLocation) {
  FunctionArgMap fn_arg_map = GetFunctionArgMap();
  fn_arg_map["crypto/tls.(*Conn).Read"].second["b"][go_version] = nullptr;

  auto offset_locator =
      GoOffsetLocator(nullptr, struct_map, fn_arg_map, empty_build_info, go_version);

  auto status = offset_locator.GetFunctionArgInfo("crypto/tls.(*Conn).Read");
  EXPECT_FALSE(status.ok());
}

TEST(GoOffsetLocator, GetFunctionArgInfoNoDWARFSuccessfulLookup) {
  FunctionArgMap fn_arg_map = GetFunctionArgMap();
  auto offset_locator =
      GoOffsetLocator(nullptr, struct_map, fn_arg_map, empty_build_info, go_version);

  ASSERT_OK_AND_ASSIGN(auto args, offset_locator.GetFunctionArgInfo("crypto/tls.(*Conn).Read"));
  EXPECT_NE(args.find("b"), args.end());
  auto& arg_info = args["b"];
  EXPECT_EQ(arg_info.location.loc_type, obj_tools::LocationType::kRegister);
  EXPECT_EQ(arg_info.location.offset, 8);
}

static inline constexpr std::string_view kGoOffsetgenFile =
    "src/stirling/source_connectors/socket_tracer/testdata/go_offsetgen.json";

TEST(ParseGoOffsetgenFileTest, ParseGoStructsObject) {
  ParseGoOffsetgenFile(std::string(kGoOffsetgenFile));
  auto& struct_offsets = GetGoStructOffsets();
  EXPECT_GT(struct_offsets.size(), 0);

  // Assert offsets for std package
  EXPECT_EQ(struct_offsets["runtime.g"].first, "std");
  EXPECT_EQ(struct_offsets["runtime.g"].second["goid"]["1.19.0"], 152);
  EXPECT_EQ(struct_offsets["runtime.g"].second["goid"]["1.24.1"], 160);

  EXPECT_EQ(struct_offsets["runtime.hmap"].first, "std");
  EXPECT_EQ(struct_offsets["runtime.hmap"].second["buckets"]["1.19.0"], 16);
  // Check that null offsets are handled correctly.
  EXPECT_EQ(struct_offsets["runtime.hmap"].second["buckets"]["1.24.1"], -1);

  // Check non std packages are handled correctly.
  EXPECT_EQ(struct_offsets["google.golang.org/grpc/internal/transport.http2Client"].first,
            "google.golang.org/grpc");
  EXPECT_EQ(struct_offsets["google.golang.org/grpc/internal/transport.http2Client"]
                .second["conn"]["1.0.0"],
            -1);
  EXPECT_EQ(struct_offsets["google.golang.org/grpc/internal/transport.http2Client"]
                .second["conn"]["1.57.2"],
            136);
}

TEST(ParseGoOffsetgenFileTest, ParseGoFuncsObject) {
  ParseGoOffsetgenFile(std::string(kGoOffsetgenFile));
  auto& function_args = GetGoFunctionArgOffsets();
  EXPECT_GT(function_args.size(), 0);

  EXPECT_EQ(function_args["crypto/tls.(*Conn).Read"].first, "std");

  EXPECT_EQ(function_args["google.golang.org/grpc/internal/transport.(*http2Client).operateHeaders"]
                .first,
            "google.golang.org/grpc");
  auto null_loc = std::move(
      function_args["google.golang.org/grpc/internal/transport.(*http2Client).operateHeaders"]
          .second["frame"]["1.0.0"]);
  EXPECT_TRUE(null_loc != nullptr);
  EXPECT_EQ(null_loc->loc_type, obj_tools::LocationType::kUnknown);
  EXPECT_EQ(null_loc->offset, -1);

  auto loc = std::move(
      function_args["google.golang.org/grpc/internal/transport.(*http2Client).operateHeaders"]
          .second["frame"]["1.40.0"]);
  EXPECT_TRUE(loc != nullptr);
  EXPECT_EQ(loc->loc_type, obj_tools::LocationType::kRegister);
  EXPECT_EQ(loc->offset, 8);
}

TEST(ResolveOffsetVersion, ExactMatch) {
  EXPECT_EQ(ResolveOffsetVersion({"1.24.0", "1.24.3"}, "1.24.3", true), "1.24.3");
}

TEST(ResolveOffsetVersion, LatestOlderPatchOfSameMinor) {
  EXPECT_EQ(ResolveOffsetVersion({"1.23.9", "1.24.0", "1.24.3"}, "1.24.6", true), "1.24.3");
  EXPECT_EQ(ResolveOffsetVersion({"1.24.0", "1.24.3"}, "1.24.1", true), "1.24.0");
}

TEST(ResolveOffsetVersion, NewerPatchOfSameMinorWhenNoOlderOne) {
  EXPECT_EQ(ResolveOffsetVersion({"1.24.3", "1.25.1"}, "1.24.0", true), "1.24.3");
}

TEST(ResolveOffsetVersion, NoFallbackAcrossMinorForStd) {
  EXPECT_EQ(ResolveOffsetVersion({"1.23.9", "1.24.3"}, "1.25.1", true), std::nullopt);
}

TEST(ResolveOffsetVersion, LatestOlderVersionForModules) {
  EXPECT_EQ(ResolveOffsetVersion({"0.38.0", "0.40.0"}, "0.47.0", false), "0.40.0");
  EXPECT_EQ(ResolveOffsetVersion({"0.38.0", "0.40.0"}, "0.39.1", false), "0.38.0");
  EXPECT_EQ(ResolveOffsetVersion({"0.38.0", "0.40.0"}, "0.1.0", false), std::nullopt);
}

TEST(ResolveOffsetVersion, IgnoresUnparsableVersions) {
  EXPECT_EQ(ResolveOffsetVersion({"bogus", "1.24.3"}, "1.24.6", true), "1.24.3");
  EXPECT_EQ(ResolveOffsetVersion({"1.24.3"}, "bogus", true), std::nullopt);
}

TEST(GoOffsetLocator, StructOffsetFallsBackToSameMinor) {
  std::string version = "1.19.7";
  auto offset_locator =
      GoOffsetLocator(nullptr, struct_map, empty_fn_arg_map, empty_build_info, version);
  ASSERT_OK_AND_ASSIGN(uint64_t offset, offset_locator.GetStructMemberOffset("runtime.g", "goid"));
  EXPECT_EQ(offset, 152);
}

TEST(GoOffsetLocator, FunctionArgFallsBackToSameMinor) {
  FunctionArgMap fn_arg_map = GetFunctionArgMap();
  std::string version = "1.19.7";
  auto offset_locator = GoOffsetLocator(nullptr, struct_map, fn_arg_map, empty_build_info, version);
  ASSERT_OK_AND_ASSIGN(auto args, offset_locator.GetFunctionArgInfo("crypto/tls.(*Conn).Read"));
  ASSERT_NE(args.find("b"), args.end());
  EXPECT_EQ(args["b"].location.offset, 8);
}

TEST(GoOffsetLocator, FunctionArgSkipsArgsWithoutVersion) {
  FunctionArgMap fn_arg_map = GetFunctionArgMap();
  fn_arg_map["crypto/tls.(*Conn).Read"].second["fh"]["1.26.7"] =
      std::make_unique<obj_tools::VarLocation>(
          obj_tools::VarLocation{obj_tools::LocationType::kRegister, 8});
  std::string version = "1.19.7";
  auto offset_locator = GoOffsetLocator(nullptr, struct_map, fn_arg_map, empty_build_info, version);
  ASSERT_OK_AND_ASSIGN(auto args, offset_locator.GetFunctionArgInfo("crypto/tls.(*Conn).Read"));
  EXPECT_EQ(args.size(), 1);
  EXPECT_NE(args.find("b"), args.end());
}

constexpr std::string_view kTLSFixtureDir = "src/stirling/obj_tools/testdata/go/tls_fixture/";
constexpr std::string_view kOffsetgenOffsetsFile = "src/stirling/offsetgen_offsets.json";

std::string TLSFixturePath(std::string_view version, bool stripped) {
  return px::testing::BazelRunfilePath(absl::StrCat(kTLSFixtureDir, "test_go_tls_", version,
                                                    stripped ? "_stripped" : "", "_binary"));
}

const std::vector<std::pair<std::string_view, std::string_view>> kStdStructMembers = {
    {"runtime.g", "goid"},
    {"internal/poll.FD", "Sysfd"},
    {"crypto/tls.Conn", "conn"},
    {"net/http.http2serverConn", "conn"},
    {"net/http.http2serverConn", "hpackEncoder"},
    {"net/http.http2HeadersFrame", "http2FrameHeader"},
    {"net/http.http2FrameHeader", "Type"},
    {"net/http.http2FrameHeader", "Flags"},
    {"net/http.http2FrameHeader", "StreamID"},
    {"net/http.http2DataFrame", "data"},
    {"net/http.http2writeResHeaders", "streamID"},
    {"net/http.http2writeResHeaders", "endStream"},
    {"net/http.http2MetaHeadersFrame", "http2HeadersFrame"},
    {"net/http.http2MetaHeadersFrame", "Fields"},
    {"net/http.http2Framer", "w"},
    {"net/http.http2bufferedWriter", "w"},
    {"net/http.http2bufferedWriter", "conn"},
};

const std::vector<std::string_view> kStdFuncs = {
    "crypto/tls.(*Conn).Write",
    "crypto/tls.(*Conn).Read",
    "net/http.(*http2Framer).WriteDataPadded",
    "net/http.(*http2Framer).checkFrameOrder",
    "net/http.(*http2writeResHeaders).writeFrame",
    "net/http.(*http2serverConn).processHeaders",
};

class GoFixtureTest : public ::testing::TestWithParam<std::string_view> {
 protected:
  void SetUp() override {
    FLAGS_disable_dwarf_parsing = false;
    ParseGoOffsetgenFile(px::testing::BazelRunfilePath(kOffsetgenOffsetsFile), &struct_offsets_,
                         &function_args_);
    ASSERT_GT(struct_offsets_.size(), 0);

    unstripped_path_ = TLSFixturePath(GetParam(), false);
    stripped_path_ = TLSFixturePath(GetParam(), true);
    ASSERT_OK_AND_ASSIGN(unstripped_elf_, ElfReader::Create(unstripped_path_));
    ASSERT_OK_AND_ASSIGN(stripped_elf_, ElfReader::Create(stripped_path_));
  }

  StructOffsetMap struct_offsets_;
  FunctionArgMap function_args_;
  std::string unstripped_path_;
  std::string stripped_path_;
  std::unique_ptr<ElfReader> unstripped_elf_;
  std::unique_ptr<ElfReader> stripped_elf_;
};

TEST_P(GoFixtureTest, AnalyzeStrippedBinaryWithoutDwarf) {
  ASSERT_OK_AND_ASSIGN(GoBinaryInfo info, AnalyzeGoBinary(stripped_path_, stripped_elf_.get()));
  EXPECT_TRUE(info.dwarf_reader == nullptr);
  std::string minor(GetParam());
  std::replace(minor.begin(), minor.end(), '_', '.');
  EXPECT_TRUE(absl::StartsWith(info.go_version, absl::StrCat(minor, "."))) << info.go_version;
  EXPECT_NE(stripped_elf_->SymbolAddress("go:itab.*net.TCPConn,net.Conn"), std::nullopt);
  ASSERT_OK_AND_ASSIGN(auto funcs, stripped_elf_->ListFuncSymbols("crypto/tls.(*Conn).Write",
                                                                  SymbolMatchType::kSuffix));
  EXPECT_EQ(funcs.size(), 1);
}

TEST_P(GoFixtureTest, AnalyzeUnstrippedBinaryWithDwarf) {
  ASSERT_OK_AND_ASSIGN(GoBinaryInfo info, AnalyzeGoBinary(unstripped_path_, unstripped_elf_.get()));
  EXPECT_TRUE(info.dwarf_reader != nullptr);
}

TEST_P(GoFixtureTest, AnalyzeWithDwarfParsingDisabled) {
  FLAGS_disable_dwarf_parsing = true;
  ASSERT_OK_AND_ASSIGN(GoBinaryInfo info, AnalyzeGoBinary(unstripped_path_, unstripped_elf_.get()));
  FLAGS_disable_dwarf_parsing = false;
  EXPECT_TRUE(info.dwarf_reader == nullptr);
}

TEST_P(GoFixtureTest, OffsetTableMatchesDwarf) {
  ASSERT_OK_AND_ASSIGN(GoBinaryInfo dwarf_info,
                       AnalyzeGoBinary(unstripped_path_, unstripped_elf_.get()));
  ASSERT_TRUE(dwarf_info.dwarf_reader != nullptr);
  const std::string version = dwarf_info.go_version;
  GoOffsetLocator from_dwarf(dwarf_info.dwarf_reader.get(), struct_offsets_, function_args_,
                             dwarf_info.build_info, version);
  GoOffsetLocator from_table(nullptr, struct_offsets_, function_args_, dwarf_info.build_info,
                             version);

  for (const auto& [st, member] : kStdStructMembers) {
    SCOPED_TRACE(absl::StrCat(st, ".", member));
    auto expected = from_dwarf.GetStructMemberOffset(st, member);
    if (!expected.ok()) {
      auto actual = from_table.GetStructMemberOffset(st, member);
      EXPECT_TRUE(!actual.ok() || static_cast<int32_t>(actual.ValueOrDie()) == -1)
          << absl::Substitute("offsetgen: structs[\"$0\"][\"$1\"][\"$2\"] = null", st, member,
                              version);
      continue;
    }
    const std::string msg = absl::Substitute("offsetgen: structs[\"$0\"][\"$1\"][\"$2\"] = $3", st,
                                             member, version, expected.ValueOrDie());
    auto actual = from_table.GetStructMemberOffset(st, member);
    EXPECT_OK(actual) << msg;
    if (actual.ok()) {
      EXPECT_EQ(actual.ValueOrDie(), expected.ValueOrDie()) << msg;
    }
  }

  for (std::string_view fn : kStdFuncs) {
    SCOPED_TRACE(fn);
    ASSERT_OK_AND_ASSIGN(auto expected, from_dwarf.GetFunctionArgInfo(fn));
    auto actual_or = from_table.GetFunctionArgInfo(fn);
    if (!actual_or.ok()) {
      std::string msg;
      for (const auto& [arg, info] : expected) {
        absl::StrAppend(
            &msg,
            absl::Substitute("offsetgen: funcs[\"$0\"][\"$1\"][\"$2\"] = $3/$4\n", fn, arg, version,
                             magic_enum::enum_name(info.location.loc_type), info.location.offset));
      }
      ADD_FAILURE() << actual_or.msg() << "\n" << msg;
      continue;
    }
    auto actual = actual_or.ConsumeValueOrDie();
    EXPECT_FALSE(actual.empty());
    for (const auto& [arg, info] : actual) {
      auto it = expected.find(arg);
      ASSERT_NE(it, expected.end()) << "offsetgen arg not in debug info: " << arg;
      EXPECT_EQ(info.location.loc_type, it->second.location.loc_type) << arg;
      EXPECT_EQ(info.location.offset, it->second.location.offset)
          << absl::Substitute("offsetgen: funcs[\"$0\"][\"$1\"][\"$2\"] = $3", fn, arg, version,
                              it->second.location.offset);
    }
  }
}

TEST_P(GoFixtureTest, StrippedSymAddrsMatchUnstripped) {
  ASSERT_OK_AND_ASSIGN(GoBinaryInfo dwarf_info,
                       AnalyzeGoBinary(unstripped_path_, unstripped_elf_.get()));
  ASSERT_OK_AND_ASSIGN(GoBinaryInfo stripped_info,
                       AnalyzeGoBinary(stripped_path_, stripped_elf_.get()));
  ASSERT_EQ(stripped_info.go_version, dwarf_info.go_version);

  GoOffsetLocator from_dwarf(dwarf_info.dwarf_reader.get(), struct_offsets_, function_args_,
                             dwarf_info.build_info, dwarf_info.go_version);
  GoOffsetLocator from_table(nullptr, struct_offsets_, function_args_, stripped_info.build_info,
                             stripped_info.go_version);

  ASSERT_OK_AND_ASSIGN(auto expected_common, GoCommonSymAddrs(unstripped_elf_.get(), &from_dwarf));
  ASSERT_OK_AND_ASSIGN(auto actual_common, GoCommonSymAddrs(stripped_elf_.get(), &from_table));
  EXPECT_NE(expected_common.tls_Conn, -1);
  EXPECT_EQ(actual_common.tls_Conn, expected_common.tls_Conn);
  EXPECT_EQ(actual_common.net_TCPConn, expected_common.net_TCPConn);
  EXPECT_EQ(actual_common.FD_Sysfd_offset, expected_common.FD_Sysfd_offset);
  EXPECT_EQ(actual_common.tlsConn_conn_offset, expected_common.tlsConn_conn_offset);
  EXPECT_EQ(actual_common.g_goid_offset, expected_common.g_goid_offset);
  EXPECT_EQ(actual_common.g_addr_offset, expected_common.g_addr_offset);

  ASSERT_OK_AND_ASSIGN(auto expected_tls, GoTLSSymAddrs(&from_dwarf));
  ASSERT_OK_AND_ASSIGN(auto actual_tls, GoTLSSymAddrs(&from_table));
  EXPECT_EQ(actual_tls.Write_c_loc, expected_tls.Write_c_loc);
  EXPECT_EQ(actual_tls.Write_b_loc, expected_tls.Write_b_loc);
  EXPECT_EQ(actual_tls.Write_retval0_loc, expected_tls.Write_retval0_loc);
  EXPECT_EQ(actual_tls.Write_retval1_loc, expected_tls.Write_retval1_loc);
  EXPECT_EQ(actual_tls.Read_c_loc, expected_tls.Read_c_loc);
  EXPECT_EQ(actual_tls.Read_b_loc, expected_tls.Read_b_loc);
  EXPECT_EQ(actual_tls.Read_retval0_loc, expected_tls.Read_retval0_loc);
  EXPECT_EQ(actual_tls.Read_retval1_loc, expected_tls.Read_retval1_loc);

  ASSERT_OK_AND_ASSIGN(auto expected_h2, GoHTTP2SymAddrs(unstripped_elf_.get(), &from_dwarf));
  ASSERT_OK_AND_ASSIGN(auto actual_h2, GoHTTP2SymAddrs(stripped_elf_.get(), &from_table));
  EXPECT_EQ(actual_h2.http_http2bufferedWriter, expected_h2.http_http2bufferedWriter);
  EXPECT_EQ(actual_h2.http2Framer_WriteDataPadded_f_loc,
            expected_h2.http2Framer_WriteDataPadded_f_loc);
  EXPECT_EQ(actual_h2.http2Framer_WriteDataPadded_data_len_loc,
            expected_h2.http2Framer_WriteDataPadded_data_len_loc);
  EXPECT_EQ(actual_h2.http2Framer_checkFrameOrder_f_loc,
            expected_h2.http2Framer_checkFrameOrder_f_loc);
  EXPECT_EQ(actual_h2.writeFrame_w_loc, expected_h2.writeFrame_w_loc);
  EXPECT_EQ(actual_h2.processHeaders_f_loc, expected_h2.processHeaders_f_loc);
  EXPECT_EQ(actual_h2.http2serverConn_conn_offset, expected_h2.http2serverConn_conn_offset);
  EXPECT_EQ(actual_h2.http2serverConn_hpackEncoder_offset,
            expected_h2.http2serverConn_hpackEncoder_offset);
  EXPECT_EQ(actual_h2.http2Framer_w_offset, expected_h2.http2Framer_w_offset);
  EXPECT_EQ(actual_h2.http2MetaHeadersFrame_Fields_offset,
            expected_h2.http2MetaHeadersFrame_Fields_offset);
  EXPECT_EQ(actual_h2.http2writeResHeaders_endStream_offset,
            expected_h2.http2writeResHeaders_endStream_offset);
  EXPECT_EQ(actual_h2.http2bufferedWriter_w_offset, expected_h2.http2bufferedWriter_w_offset);
  EXPECT_EQ(actual_h2.http2bufferedWriter_conn_offset, expected_h2.http2bufferedWriter_conn_offset);
}

INSTANTIATE_TEST_SUITE_P(GoVersions, GoFixtureTest,
                         ::testing::Values("1_23", "1_24", "1_25", "1_26"));

TEST(AnalyzeGoBinaryTest, NonGoBinaryIsAnError) {
  const std::string path =
      px::testing::BazelRunfilePath("src/stirling/obj_tools/testdata/cc/stripped_test_exe");
  ASSERT_OK_AND_ASSIGN(auto elf_reader, ElfReader::Create(path));
  EXPECT_NOT_OK(AnalyzeGoBinary(path, elf_reader.get()));
}

}  // namespace stirling
}  // namespace px
