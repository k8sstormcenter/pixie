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

#include "src/stirling/source_connectors/socket_tracer/go_binary_cache.h"

#include <unistd.h>

#include <fstream>

#include "src/common/testing/temp_dir.h"
#include "src/common/testing/testing.h"
#include "src/stirling/obj_tools/elf_reader.h"

namespace px {
namespace stirling {

GoUProbeSymAddrs SymAddrsWithFD(int32_t fd_offset) {
  GoUProbeSymAddrs symaddrs;
  symaddrs.common.FD_Sysfd_offset = fd_offset;
  return symaddrs;
}

TEST(GoBinaryCacheTest, BinaryWithoutTCPConnIsNotAFailure) {
  const std::string binary = ::px::testing::BazelRunfilePath(
      "src/stirling/obj_tools/testdata/go/cgo_tls/cgo_tls_binary");
  ASSERT_OK_AND_ASSIGN(std::unique_ptr<obj_tools::ElfReader> elf_reader,
                       obj_tools::ElfReader::Create(binary));

  GoUProbeSymAddrs symaddrs = AnalyzeGoUProbeBinary(binary, elf_reader.get(), true, true);

  EXPECT_EQ(symaddrs.status.code(), px::statuspb::NOT_FOUND);
  EXPECT_TRUE(symaddrs.failed_stage.empty());
  EXPECT_FALSE(symaddrs.tls.has_value());
  EXPECT_FALSE(symaddrs.http2.has_value());
}

TEST(GoBinaryCacheTest, FileVersionIdentifiesTheFile) {
  ::px::testing::TempDir dir;
  const std::filesystem::path file = dir.path() / "binary";
  const std::filesystem::path link = dir.path() / "link";
  std::ofstream(file) << "v1";
  ASSERT_EQ(::link(file.c_str(), link.c_str()), 0);

  ASSERT_OK_AND_ASSIGN(FileVersion v1, GetFileVersion(file));
  ASSERT_OK_AND_ASSIGN(FileVersion v1_again, GetFileVersion(file));
  ASSERT_OK_AND_ASSIGN(FileVersion v1_link, GetFileVersion(link));
  EXPECT_EQ(v1, v1_again);
  EXPECT_EQ(v1, v1_link);
  EXPECT_NE(v1.ino, 0U);
  EXPECT_EQ(v1.size, 2);

  std::ofstream(file) << "version2";
  ASSERT_OK_AND_ASSIGN(FileVersion v2, GetFileVersion(file));
  EXPECT_FALSE(v1 == v2);
  EXPECT_EQ(v2.ino, v1.ino);
}

TEST(GoBinaryCacheTest, MissingFileIsAnError) {
  ::px::testing::TempDir dir;
  EXPECT_NOT_OK(GetFileVersion(dir.path() / "missing"));
}

TEST(GoBinaryCacheTest, FindReturnsInsertedAnswers) {
  GoBinaryCache cache(8);
  const FileVersion go_binary{1, 10, 100, 1000, 1000};
  const FileVersion not_go{1, 11, 100, 1000, 1000};
  GoUProbeSymAddrs negative;
  negative.status = error::NotFound("not a Go executable");

  cache.Insert(go_binary, SymAddrsWithFD(16));
  cache.Insert(not_go, negative);

  const GoUProbeSymAddrs* hit = cache.Find(go_binary);
  ASSERT_NE(hit, nullptr);
  EXPECT_OK(hit->status);
  EXPECT_EQ(hit->common.FD_Sysfd_offset, 16);
  const GoUProbeSymAddrs* negative_hit = cache.Find(not_go);
  ASSERT_NE(negative_hit, nullptr);
  EXPECT_NOT_OK(negative_hit->status);
  EXPECT_EQ(cache.Find(FileVersion{1, 10, 101, 1000, 1000}), nullptr);
}

TEST(GoBinaryCacheTest, InsertReplacesAnExistingAnswer) {
  GoBinaryCache cache(8);
  const FileVersion version{1, 10, 100, 1000, 1000};

  cache.Insert(version, SymAddrsWithFD(16));
  cache.Insert(version, SymAddrsWithFD(24));

  EXPECT_EQ(cache.size(), 1U);
  ASSERT_NE(cache.Find(version), nullptr);
  EXPECT_EQ(cache.Find(version)->common.FD_Sysfd_offset, 24);
}

TEST(GoBinaryCacheTest, EvictsLeastRecentlyUsed) {
  GoBinaryCache cache(2);
  const FileVersion a{1, 1, 1, 1, 1};
  const FileVersion b{1, 2, 1, 1, 1};
  const FileVersion c{1, 3, 1, 1, 1};

  cache.Insert(a, SymAddrsWithFD(1));
  cache.Insert(b, SymAddrsWithFD(2));
  ASSERT_NE(cache.Find(a), nullptr);
  cache.Insert(c, SymAddrsWithFD(3));

  EXPECT_EQ(cache.size(), 2U);
  EXPECT_NE(cache.Find(a), nullptr);
  EXPECT_EQ(cache.Find(b), nullptr);
  EXPECT_NE(cache.Find(c), nullptr);
}

}  // namespace stirling
}  // namespace px
