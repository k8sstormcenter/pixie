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

#pragma once

#include <functional>
#include <memory>
#include <string>

#include <clickhouse/client.h>

#include "src/common/base/base.h"

namespace px {
namespace carnot {
namespace exec {

// Thin wrappers around the exception-throwing clickhouse-cpp client API. Pixie code
// follows the Google C++ style guide and does not use exceptions, so these wrappers
// form the single boundary where a clickhouse-cpp exception is caught and translated
// into a px::Status. Callers use PX_ASSIGN_OR_RETURN / PX_RETURN_IF_ERROR and never
// need their own try/catch.

// Creates a ClickHouse client from the given options.
StatusOr<std::unique_ptr<::clickhouse::Client>> CreateClickHouseClient(
    const ::clickhouse::ClientOptions& options);

// Executes a statement that returns no rows (e.g. DDL or a connection check).
Status ClickHouseExecute(::clickhouse::Client* client, const std::string& query);

// Executes a SELECT query, invoking block_cb for each returned block.
Status ClickHouseSelect(::clickhouse::Client* client, const std::string& query,
                        std::function<void(const ::clickhouse::Block&)> block_cb);

// Inserts a block into table_name.
Status ClickHouseInsert(::clickhouse::Client* client, const std::string& table_name,
                        const ::clickhouse::Block& block);

}  // namespace exec
}  // namespace carnot
}  // namespace px
