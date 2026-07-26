# Copyright 2018- The Pixie Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0

load("@rules_foreign_cc//foreign_cc:defs.bzl", "cmake")

licenses(["notice"])

exports_files(["LICENSE"])

filegroup(
    name = "all",
    srcs = glob(["**"]),
)

cmake(
    name = "clickhouse_cpp",
    build_args = [
        "--",  # <- Pass remaining options to the native tool.
        "-j`nproc`",
        "-l`nproc`",
    ],
    cache_entries = {
        "BUILD_BENCHMARK": "OFF",
        "BUILD_TESTS": "OFF",
        "BUILD_SHARED_LIBS": "OFF",
        "CMAKE_BUILD_TYPE": "Release",
        "WITH_OPENSSL": "ON",
        "WITH_SYSTEM_ABSEIL": "ON",
        "WITH_SYSTEM_LZ4": "OFF",
        "WITH_SYSTEM_CITYHASH": "OFF",
        "WITH_SYSTEM_ZSTD": "OFF",
        "CMAKE_POSITION_INDEPENDENT_CODE": "ON",
    },
    lib_source = ":all",
    out_static_libs = [
        "libclickhouse-cpp-lib.a",
        "liblz4.a",
        "libcityhash.a",
        "libzstdstatic.a",
    ],
    targets = [
        "clickhouse-cpp-lib",
        "lz4",
        "cityhash",
        "zstdstatic",
    ],
    visibility = ["//visibility:public"],
    working_directory = "",
    deps = [
        "@boringssl//:crypto",
        "@boringssl//:ssl",
        "@com_google_absl//absl/numeric:int128",
    ],
)
