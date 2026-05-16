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

// Driver binary that wraps pgsqlclient.PgsqlSeqClient. Mirrors the HTTP
// driver at src/e2e_test/protocol_loadtest/client/client.go.
package main

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"px.dev/pixie/src/e2e_test/vizier/seq_tests/client/pkg/pgsqlclient"
)

func init() {
	pflag.String("pg_host", "", "Host of the postgres server")
	pflag.Int("pg_port", 5432, "Port of the postgres server")
	pflag.String("pg_user", "postgres", "Postgres username")
	pflag.String("pg_password", "postgres", "Postgres password")
	pflag.String("pg_database", "postgres", "Postgres database")
	pflag.String("pg_sslmode", "disable", "Postgres sslmode (disable, require, verify-full)")

	pflag.Int("num_connections", 0, "Number of simultaneous pgsql connections")
	pflag.Int("target_rps", 0, "Target queries/sec across all connections")
	pflag.Int("pad_size", 1024, "Size of the SELECT pad-text in bytes")
	pflag.Int("num_messages", 1000, "Num messages per loop per conn")
}

func main() {
	viper.AutomaticEnv()
	viper.BindPFlags(pflag.CommandLine)

	host := viper.GetString("pg_host")
	port := viper.GetInt("pg_port")
	user := viper.GetString("pg_user")
	password := viper.GetString("pg_password")
	dbname := viper.GetString("pg_database")
	sslmode := viper.GetString("pg_sslmode")
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)

	numConns := viper.GetInt("num_connections")
	targetRPS := viper.GetInt("target_rps")
	numMessagesPerConn := viper.GetInt("num_messages")
	padSize := viper.GetInt("pad_size")
	numMessages := numMessagesPerConn * numConns

	seqNum := 0
	for {
		log.WithFields(log.Fields{
			"conns": numConns, "messages": numMessages, "pad_size": padSize, "target_rps": targetRPS,
		}).Info("Started pgsql loadtest")
		c := pgsqlclient.New(dsn, seqNum, numMessages, numConns, padSize, targetRPS)
		if err := c.Run(); err != nil {
			log.WithError(err).Error("pgsql seq client run failed")
		}
		if err := c.PrintStats(); err != nil {
			log.WithError(err).Error("pgsql seq client stats failed")
		}
		seqNum += numMessages + 1
	}
}
