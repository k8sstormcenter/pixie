// Copyright 2018- The Pixie Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package clickhouse

import (
	"fmt"
	"strings"
)

// Columns returns the column names of forensic_db.<table> in
// declaration order, parsed from the embedded canonical schema.sql.
// Same defensive contract as DDL: unknown table → ErrUnknownTable.
func Columns(table string) ([]string, error) {
	ddl, err := DDL(table)
	if err != nil {
		return nil, err
	}
	return parseColumnList(ddl)
}

// InsertSQL returns the parameterized INSERT for forensic_db.<table>,
// ending in "... VALUES" so a driver's batch API can append rows.
// Column order matches Columns() exactly — callers MUST append values
// in that same order. Dotted ClickHouse identifiers are auto-quoted
// with backticks.
func InsertSQL(table string) (string, error) {
	cols, err := Columns(table)
	if err != nil {
		return "", err
	}
	identifier := table
	if strings.Contains(table, ".") {
		identifier = "`" + table + "`"
	}
	return fmt.Sprintf("INSERT INTO forensic_db.%s (%s) VALUES",
		identifier, strings.Join(cols, ", ")), nil
}

// parseColumnList walks the body of a CREATE TABLE statement, returning
// the leading identifier of each non-comment, non-blank line up to the
// closing `)` that ends the column list. Defensive against the SQL
// dialect quirks present in our schema (LowCardinality(...), DEFAULT
// expressions, inline -- comments, multi-word types).
func parseColumnList(ddl string) ([]string, error) {
	open := strings.Index(ddl, "(")
	if open < 0 {
		return nil, fmt.Errorf("malformed DDL: no opening paren")
	}
	body := ddl[open+1:]
	// the closing paren of the column list is the first `)` at the
	// matching depth, but our schema doesn't nest parens inside the
	// column list except inside DEFAULT exprs (e.g. now64(3)) and
	// LowCardinality(String). Track depth.
	depth := 1
	end := -1
	for i, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("malformed DDL: no closing paren for column list")
	}
	body = body[:end]

	var cols []string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		// strip trailing comma + inline -- comment
		if i := strings.Index(line, "--"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		line = strings.TrimSuffix(line, ",")
		if line == "" {
			continue
		}
		// first whitespace-separated token = column name
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		cols = append(cols, fields[0])
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("malformed DDL: no columns parsed")
	}
	return cols, nil
}
