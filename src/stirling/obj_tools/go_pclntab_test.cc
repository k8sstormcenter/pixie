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

#include "src/stirling/obj_tools/go_pclntab.h"

#include <memory>
#include <string>
#include <vector>

#include <absl/container/flat_hash_map.h>
#include <absl/strings/str_cat.h>
#include <absl/strings/str_join.h>

#include "src/common/testing/testing.h"
#include "src/stirling/obj_tools/elf_reader.h"
#include "src/stirling/testing/go_https_server_fixture.h"

namespace px {
namespace stirling {
namespace obj_tools {

using ::px::stirling::testing::GoHTTPSServerFixture;

absl::flat_hash_map<std::string, ElfReader::SymbolInfo> SymtabByName(ElfReader* elf_reader,
                                                                     std::string_view prefix,
                                                                     int type) {
  absl::flat_hash_map<std::string, ElfReader::SymbolInfo> out;
  auto symbols = elf_reader->SearchSymbols(prefix, SymbolMatchType::kPrefix, type);
  if (!symbols.ok()) {
    return out;
  }
  for (auto& s : symbols.ValueOrDie()) {
    out.emplace(s.name, s);
  }
  return out;
}

const std::vector<std::string_view> kProbedFuncs = {
    "crypto/tls.(*Conn).Read",
    "crypto/tls.(*Conn).Write",
    "crypto/tls.(*Conn).readRecordOrCCS",
    "net/http.(*http2Framer).WriteDataPadded",
    "net/http.(*http2Framer).checkFrameOrder",
    "net/http.(*http2serverConn).processHeaders",
    "net/http.(*http2writeResHeaders).writeFrame",
    "main.main",
};

const std::vector<std::string_view> kProbedITabs = {
    "go:itab.*crypto/tls.Conn,net.Conn",
    "go:itab.*net.TCPConn,net.Conn",
};

class GoPclntabTest : public ::testing::TestWithParam<std::string_view> {
 protected:
  void SetUp() override {
    ASSERT_OK_AND_ASSIGN(elf_, ElfReader::Create(GoHTTPSServerFixture::Path(GetParam())));
    ASSERT_OK_AND_ASSIGN(stripped_elf_,
                         ElfReader::Create(GoHTTPSServerFixture::StrippedPath(GetParam())));
  }

  std::unique_ptr<ElfReader> elf_;
  std::unique_ptr<ElfReader> stripped_elf_;
};

TEST_P(GoPclntabTest, StrippedFixtureHasNoSymtab) {
  EXPECT_TRUE(elf_->HasSymtab());
  EXPECT_FALSE(stripped_elf_->HasSymtab());
  EXPECT_OK(stripped_elf_->SectionWithName(".gopclntab"));
}

TEST_P(GoPclntabTest, TextStartMatchesRuntimeText) {
  ASSERT_OK_AND_ASSIGN(auto pclntab, GoPclntab::Create(elf_.get()));
  auto runtime_text = elf_->SymbolAddress("runtime.text");
  ASSERT_TRUE(runtime_text.has_value());
  EXPECT_EQ(pclntab->text_start(), static_cast<uint64_t>(runtime_text.value()));

  auto firstmoduledata = elf_->SymbolAddress("runtime.firstmoduledata");
  ASSERT_TRUE(firstmoduledata.has_value());
  EXPECT_EQ(pclntab->moduledata_addr(), static_cast<uint64_t>(firstmoduledata.value()));
}

TEST_P(GoPclntabTest, HeaderTextStartIsZeroFromGo1_26) {
  ASSERT_OK_AND_ASSIGN(auto pclntab, GoPclntab::Create(elf_.get()));
  if (GetParam() == "1_26") {
    EXPECT_EQ(pclntab->header_text_start(), uint64_t{0});
  } else {
    EXPECT_EQ(pclntab->header_text_start(), pclntab->text_start());
  }
}

TEST_P(GoPclntabTest, FuncsMatchSymtab) {
  ASSERT_OK_AND_ASSIGN(auto pclntab, GoPclntab::Create(elf_.get()));
  auto symtab = SymtabByName(elf_.get(), "", ELFIO::STT_FUNC);
  ASSERT_GT(symtab.size(), 1000);

  absl::flat_hash_map<std::string, ElfReader::SymbolInfo> funcs;
  for (const auto& f : pclntab->funcs()) {
    EXPECT_EQ(f.type, ELFIO::STT_FUNC);
    funcs.emplace(f.name, f);
  }

  size_t compared = 0;
  size_t matched = 0;
  for (const auto& [name, f] : funcs) {
    auto it = symtab.find(name);
    if (it == symtab.end()) {
      continue;
    }
    ++compared;
    if (it->second.address == f.address && f.size >= it->second.size) {
      ++matched;
    }
  }
  EXPECT_GT(compared, 1000);
  EXPECT_GE(matched * 100, compared * 99);

  for (std::string_view name : kProbedFuncs) {
    SCOPED_TRACE(name);
    auto sym = symtab.find(name);
    auto fn = funcs.find(name);
    ASSERT_NE(sym, symtab.end());
    ASSERT_NE(fn, funcs.end());
    EXPECT_EQ(fn->second.address, sym->second.address);
    EXPECT_GE(fn->second.size, sym->second.size);
    EXPECT_LT(fn->second.size, sym->second.size + 64);
  }
}

TEST_P(GoPclntabTest, ReturnInstructionsMatchSymtab) {
  ASSERT_OK_AND_ASSIGN(auto pclntab, GoPclntab::Create(stripped_elf_.get()));
  for (std::string_view name : {"crypto/tls.(*Conn).Read", "crypto/tls.(*Conn).Write"}) {
    SCOPED_TRACE(name);
    ASSERT_OK_AND_ASSIGN(auto sym, elf_->SearchTheOnlySymbol(name));
    ElfReader::SymbolInfo fn;
    for (const auto& f : pclntab->funcs()) {
      if (f.name == name) {
        fn = f;
      }
    }
    ASSERT_EQ(fn.name, name);
    ASSERT_OK_AND_ASSIGN(auto expected, elf_->FuncRetInstAddrs(sym));
    ASSERT_OK_AND_ASSIGN(auto actual, stripped_elf_->FuncRetInstAddrs(fn));
    EXPECT_FALSE(expected.empty());
    EXPECT_EQ(actual, expected);
  }
}

TEST_P(GoPclntabTest, FuncsResolveAddressesWithoutSymtab) {
  ASSERT_OK_AND_ASSIGN(auto pclntab, GoPclntab::Create(stripped_elf_.get()));
  stripped_elf_->SetFallbackSymbols(pclntab->funcs());
  ASSERT_OK_AND_ASSIGN(auto sym, elf_->SearchTheOnlySymbol("crypto/tls.(*Conn).Write"));
  const std::optional<std::string> expected = "crypto/tls.(*Conn).Write";
  ASSERT_OK_AND_EQ(stripped_elf_->InstrAddrToSymbol(sym.address), expected);
  ASSERT_OK_AND_EQ(stripped_elf_->InstrAddrToSymbol(sym.address + sym.size - 1), expected);
  ASSERT_OK_AND_EQ(stripped_elf_->InstrAddrToSymbol(0), std::optional<std::string>());
}

TEST_P(GoPclntabTest, ITabsMatchSymtab) {
  ASSERT_OK_AND_ASSIGN(auto pclntab, GoPclntab::Create(elf_.get()));
  ASSERT_OK_AND_ASSIGN(std::vector<ElfReader::SymbolInfo> itabs, pclntab->ITabs());
  auto symtab = SymtabByName(elf_.get(), "go:itab.", ELFIO::STT_OBJECT);
  ASSERT_GT(symtab.size(), 50);

  absl::flat_hash_map<std::string, ElfReader::SymbolInfo> by_name;
  absl::flat_hash_map<uint64_t, std::string> name_by_address;
  for (const auto& itab : itabs) {
    EXPECT_EQ(itab.type, ELFIO::STT_OBJECT);
    by_name.emplace(itab.name, itab);
    name_by_address.emplace(itab.address, itab.name);
  }

  size_t matched = 0;
  std::vector<std::string> mismatches;
  for (const auto& [name, sym] : symtab) {
    auto it = by_name.find(name);
    if (it != by_name.end() && it->second.address == sym.address) {
      ++matched;
      continue;
    }
    auto recovered = name_by_address.find(sym.address);
    mismatches.push_back(absl::StrCat(
        name, " -> ", recovered == name_by_address.end() ? "<none>" : recovered->second));
  }
  EXPECT_GE(matched * 100, symtab.size() * 95) << absl::StrJoin(mismatches, "\n");

  for (std::string_view name : kProbedITabs) {
    SCOPED_TRACE(name);
    auto sym = symtab.find(name);
    auto itab = by_name.find(name);
    ASSERT_NE(sym, symtab.end());
    ASSERT_NE(itab, by_name.end());
    EXPECT_EQ(itab->second.address, sym->second.address);
  }
}

TEST_P(GoPclntabTest, StrippedMatchesUnstripped) {
  ASSERT_OK_AND_ASSIGN(auto pclntab, GoPclntab::Create(elf_.get()));
  ASSERT_OK_AND_ASSIGN(auto stripped, GoPclntab::Create(stripped_elf_.get()));
  EXPECT_EQ(stripped->text_start(), pclntab->text_start());
  EXPECT_EQ(stripped->moduledata_addr(), pclntab->moduledata_addr());

  ASSERT_EQ(stripped->funcs().size(), pclntab->funcs().size());
  for (size_t i = 0; i < pclntab->funcs().size(); ++i) {
    EXPECT_EQ(stripped->funcs()[i].name, pclntab->funcs()[i].name);
    EXPECT_EQ(stripped->funcs()[i].address, pclntab->funcs()[i].address);
  }

  stripped_elf_->SetFallbackSymbols(stripped->funcs());
  ASSERT_OK_AND_ASSIGN(auto itabs, pclntab->ITabs());
  ASSERT_OK_AND_ASSIGN(auto stripped_itabs, stripped->ITabs());
  ASSERT_EQ(stripped_itabs.size(), itabs.size());
  for (size_t i = 0; i < itabs.size(); ++i) {
    EXPECT_EQ(stripped_itabs[i].name, itabs[i].name);
    EXPECT_EQ(stripped_itabs[i].address, itabs[i].address);
  }
}

INSTANTIATE_TEST_SUITE_P(GoVersions, GoPclntabTest,
                         ::testing::ValuesIn(GoHTTPSServerFixture::kVersions));

TEST(GoPclntabPIETest, FindsPclntabInsideDataRelRo) {
  ASSERT_OK_AND_ASSIGN(auto elf, ElfReader::Create(px::testing::BazelRunfilePath(
                                     "src/stirling/obj_tools/testdata/go/cgo_tls/cgo_tls_pie_binary")));
  ASSERT_OK_AND_ASSIGN(
      auto stripped_elf,
      ElfReader::Create(px::testing::BazelRunfilePath(
          "src/stirling/obj_tools/testdata/go/cgo_tls/cgo_tls_pie_stripped_binary")));
  EXPECT_NOT_OK(stripped_elf->SectionWithName(".gopclntab"));
  EXPECT_NOT_OK(stripped_elf->SectionWithName(".data.rel.ro.gopclntab"));

  ASSERT_OK_AND_ASSIGN(auto pclntab, GoPclntab::Create(stripped_elf.get()));

  auto runtime_text = elf->SymbolAddress("runtime.text");
  ASSERT_TRUE(runtime_text.has_value());
  EXPECT_EQ(pclntab->text_start(), static_cast<uint64_t>(runtime_text.value()));
  auto firstmoduledata = elf->SymbolAddress("runtime.firstmoduledata");
  ASSERT_TRUE(firstmoduledata.has_value());
  EXPECT_EQ(pclntab->moduledata_addr(), static_cast<uint64_t>(firstmoduledata.value()));

  auto symtab = SymtabByName(elf.get(), "", ELFIO::STT_FUNC);
  absl::flat_hash_map<std::string, ElfReader::SymbolInfo> funcs;
  for (const auto& f : pclntab->funcs()) {
    funcs.emplace(f.name, f);
  }
  for (std::string_view name : {"main.main", "runtime.main", "fmt.Println"}) {
    SCOPED_TRACE(name);
    auto sym = symtab.find(name);
    auto fn = funcs.find(name);
    ASSERT_NE(sym, symtab.end());
    ASSERT_NE(fn, funcs.end());
    EXPECT_EQ(fn->second.address, sym->second.address);
  }
}

TEST(GoPclntabNonGoTest, ReturnsErrorWithoutPclntab) {
  const std::string path =
      px::testing::BazelRunfilePath("src/stirling/obj_tools/testdata/cc/prebuilt_test_exe");
  ASSERT_OK_AND_ASSIGN(auto elf_reader, ElfReader::Create(path));
  EXPECT_NOT_OK(GoPclntab::Create(elf_reader.get()));
}

}  // namespace obj_tools
}  // namespace stirling
}  // namespace px
