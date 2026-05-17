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
	"time"

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
	// Bounded conn lifetime so lib/pq re-establishes flows periodically,
	// giving Pixie's eBPF protocol classifier a fresh StartupMessage to
	// latch onto. Without this, any PEM restart after the loadtest
	// started leaves flows permanently classified as kProtocolUnknown
	// and pgsql_events silent. 5min default is generous vs typical
	// PEM MTBF; 0 = legacy infinite (NOT recommended).
	pflag.Duration("conn_max_lifetime", 5*time.Minute, "Max TCP connection lifetime before recycle (0 = infinite). Recycling lets Pixie's PEM classify connections it joined mid-stream.")
}

func main() {
	viper.AutomaticEnv()
	// pflag.Parse() MUST come before viper.BindPFlags — otherwise the
	// pflag.CommandLine flags don't have their values populated yet, and
	// viper.GetX() will return the registered defaults regardless of what
	// was passed on the command line.
	pflag.Parse()
	if err := viper.BindPFlags(pflag.CommandLine); err != nil {
		log.WithError(err).Fatal("viper.BindPFlags failed")
	}

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
	connMaxLife := viper.GetDuration("conn_max_lifetime")
	if numConns <= 0 || numMessagesPerConn <= 0 {
		log.Fatal("num_connections and num_messages must both be > 0")
	}
	numMessages := numMessagesPerConn * numConns

	seqNum := 0
	for {
		log.WithFields(log.Fields{
			"conns": numConns, "messages": numMessages, "pad_size": padSize,
			"target_rps": targetRPS, "conn_max_lifetime": connMaxLife,
		}).Info("Started pgsql loadtest")
		c := pgsqlclient.New(dsn, seqNum, numMessages, numConns, padSize, targetRPS, connMaxLife)
		if err := c.Run(); err != nil {
			log.WithError(err).Error("pgsql seq client run failed")
			time.Sleep(5 * time.Second) // back off so an immediate fatal config error doesn't hot-loop
		}
		if err := c.PrintStats(); err != nil {
			log.WithError(err).Error("pgsql seq client stats failed")
		}
		seqNum += numMessages + 1
	}
}
