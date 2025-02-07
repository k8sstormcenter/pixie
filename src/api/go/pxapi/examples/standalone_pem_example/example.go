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

package main

import (
	"context"
	"fmt"
	"io"

	"px.dev/pixie/src/api/go/pxapi"
	"px.dev/pixie/src/api/go/pxapi/errdefs"
	"px.dev/pixie/src/api/go/pxapi/types"
)

// Define PxL script with one table output.
var (
	stream = `
import px
df = px.DataFrame('http_events')
px.display(df.stream())
`
	pxl = `
import px
import pxlog
table = 'vector.json'
f = '/home/ddelnano/code/pixie-worktree/vector.json'
pxlog.FileSource(f, table, '5m')
df = px.DataFrame(table)

px.display(df)`
	bpftrace = `
import pxtrace
import px
# Adapted from https://github.com/iovisor/bpftrace/blob/master/tools/tcpretrans.bt
program = """
// tcpretrans.bt Trace or count TCP retransmits
//               For Linux, uses bpftrace and eBPF.
//
// Copyright (c) 2018 Dale Hamel.
// Licensed under the Apache License, Version 2.0 (the "License")
#include <linux/socket.h>
#include <net/sock.h>
kprobe:tcp_retransmit_skb
{
  $sk = (struct sock *)arg0;
  $inet_family = $sk->__sk_common.skc_family;
  $AF_INET = (uint16) 2;
  $AF_INET6 = (uint16) 10;
  if ($inet_family == $AF_INET || $inet_family == $AF_INET6) {
    if ($inet_family == $AF_INET) {
      $daddr = ntop($sk->__sk_common.skc_daddr);
      $saddr = ntop($sk->__sk_common.skc_rcv_saddr);
    } else {
      $daddr = ntop($sk->__sk_common.skc_v6_daddr.in6_u.u6_addr8);
      $saddr = ntop($sk->__sk_common.skc_v6_rcv_saddr.in6_u.u6_addr8);
    }
    $sport = $sk->__sk_common.skc_num;
    $dport = $sk->__sk_common.skc_dport;
    // Destination port is big endian, it must be flipped
    $dport = ($dport >> 8) | (($dport << 8) & 0x00FF00);
    printf(\"time_:%llu src_ip:%s src_port:%d dst_ip:%s dst_port:%d\",
      nsecs,
      $saddr,
      $sport,
      $daddr,
      $dport);
  }
}
"""
table_name = 'tcp_retransmits_table'
pxtrace.UpsertTracepoint('tcp_retranmits_probe',
    table_name,
    program,
    pxtrace.kprobe(),
    "2m")
df = px.DataFrame(table=table_name, select=['time_', 'src_ip', 'src_port', 'dst_ip', 'dst_port'])
px.display(df)`
)

func main() {
	// Create a Pixie client with local standalonePEM listening address
	ctx := context.Background()
	client, err := pxapi.NewClient(
		ctx,
		pxapi.WithDirectAddr("127.0.0.1:12345"),
		pxapi.WithDirectCredsInsecure(),
	)
	if err != nil {
		panic(err)
	}
	// Create a connection to the host.
	hostID := "localhost"
	vz, err := client.NewVizierClient(ctx, hostID)
	if err != nil {
		panic(err)
	}
	// Create TableMuxer to accept results table.
	tm := &tableMux{}
	// Execute the PxL script.
	resultSet, err := vz.ExecuteScript(ctx, pxl, tm)
	if err != nil && err != io.EOF {
		panic(err)
	}
	// Receive the PxL script results.
	defer resultSet.Close()
	if err := resultSet.Stream(); err != nil {
		if errdefs.IsCompilationError(err) {
			fmt.Printf("Got compiler error: \n %s\n", err.Error())
		} else {
			fmt.Printf("Got error : %+v, while streaming\n", err)
		}
	}
	// Get the execution stats for the script execution.
	stats := resultSet.Stats()
	fmt.Printf("Execution Time: %v\n", stats.ExecutionTime)
	fmt.Printf("Bytes received: %v\n", stats.TotalBytes)
}

// Satisfies the TableRecordHandler interface.
type tablePrinter struct{}

func (t *tablePrinter) HandleInit(ctx context.Context, metadata types.TableMetadata) error {
	return nil
}
func (t *tablePrinter) HandleRecord(ctx context.Context, r *types.Record) error {
	for _, d := range r.Data {
		fmt.Printf("%s ", d.String())
	}
	fmt.Printf("\n")
	return nil
}

func (t *tablePrinter) HandleDone(ctx context.Context) error {
	return nil
}

// Satisfies the TableMuxer interface.
type tableMux struct {
}

func (s *tableMux) AcceptTable(ctx context.Context, metadata types.TableMetadata) (pxapi.TableRecordHandler, error) {
	return &tablePrinter{}, nil
}
