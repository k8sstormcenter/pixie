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

// Driver binary that wraps redisclient.RedisSeqClient. Mirrors the HTTP
// driver at src/e2e_test/protocol_loadtest/client/client.go.
package main

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"px.dev/pixie/src/e2e_test/vizier/seq_tests/client/pkg/redisclient"
)

func init() {
	pflag.String("redis_host", "", "Host of the redis server")
	pflag.Int("redis_port", 6379, "Port of the redis server")

	pflag.Int("num_connections", 0, "Number of simultaneous redis connections")
	pflag.Int("target_rps", 0, "Target ops/sec across all connections")
	pflag.Int("val_size", 1024, "Size of the SET value payload in bytes")
	pflag.Int("num_messages", 1000, "Num messages per loop per conn")
}

func main() {
	viper.AutomaticEnv()
	viper.BindPFlags(pflag.CommandLine)

	host := viper.GetString("redis_host")
	port := viper.GetInt("redis_port")
	addr := fmt.Sprintf("%s:%d", host, port)

	numConns := viper.GetInt("num_connections")
	targetRPS := viper.GetInt("target_rps")
	numMessagesPerConn := viper.GetInt("num_messages")
	valSize := viper.GetInt("val_size")
	numMessages := numMessagesPerConn * numConns

	seqNum := 0
	for {
		log.WithFields(log.Fields{
			"conns": numConns, "messages": numMessages, "val_size": valSize, "target_rps": targetRPS,
		}).Info("Started redis loadtest")
		c := redisclient.New(addr, seqNum, numMessages, numConns, valSize, targetRPS)
		if err := c.Run(); err != nil {
			log.WithError(err).Error("redis seq client run failed")
		}
		if err := c.PrintStats(); err != nil {
			log.WithError(err).Error("redis seq client stats failed")
		}
		seqNum += numMessages + 1
	}
}
