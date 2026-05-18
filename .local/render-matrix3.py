#!/usr/bin/env python3

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

"""render-matrix3.py — render time-series + summary from matrix-3 CSVs.
Each run dir under $MATRIX has samples.csv. Produces:
  - $MATRIX/<run>.png — multi-panel time-series for that run
  - $MATRIX/summary.png — bar charts of headline metrics across runs
  - $MATRIX/summary.txt — text table
"""
import matplotlib.pyplot as plt
import sys
import os
import csv
import glob
import re
from typing import Any
import matplotlib
matplotlib.use('Agg')

MATRIX = sys.argv[1] if len(sys.argv) > 1 else sorted(glob.glob('/tmp/matrix3-2*'))[-1]
print(f"rendering: {MATRIX}")


def parse_summary_line(line):
    """Parse '  k6=2541/s (target=8000/s)  |  ct_max=... pem_cpu_max=...' into dict."""
    out = {}
    m = re.search(r'k6=(-?\d+)/s', line)
    if m:
        out['k6_actual'] = int(m.group(1))
    m = re.search(r'target=(\d+)/s', line)
    if m:
        out['target'] = int(m.group(1))
    for k in ['ct_max', 'sused_max', 'stw_max', 'r_ops_max', 'r_conn_rate', 'pg_commit_rate',
              'pg_insert_rate', 'api_req_rate', 'pem_cpu_max', 'pem_cpu_avg', 'k6sa_cpu_max',
              'coredns_q_rate', 'coredns_miss_rate']:
        m = re.search(rf'{k}=(\?|-?\d+)', line)
        if m:
            v = m.group(1)
            out[k] = None if v == '?' else int(v)
    return out


# Parse matrix.log
# `Any` value type — run dicts mix str names, int counters, list CSVs.
runs: list[dict[str, Any]] = []
with open(os.path.join(MATRIX, 'matrix.log')) as f:
    cur: dict[str, Any] | None = None
    for line in f:
        m = re.match(r'=== RUN: (\S+) \(loadgen×(\d+) @ (\d+) qps/pod', line)
        if m:
            cur = {'name': m.group(1), 'replicas': int(m.group(2)), 'qps_per_pod': int(m.group(3))}
        elif line.strip().startswith('k6=') and cur:
            cur.update(parse_summary_line(line))
            runs.append(cur)
            cur = None

if not runs:
    print("no runs found!")
    sys.exit(1)

# Read CSVs
for run in runs:
    csvp = os.path.join(MATRIX, run['name'], 'samples.csv')
    if not os.path.exists(csvp):
        run['csv'] = []
        continue
    with open(csvp) as f:
        run['csv'] = list(csv.DictReader(f))

# === Per-run time-series PNGs ===
TS_METRICS = [
    ('conntrack_count', 'nf_conntrack_count', 'k'),
    ('sock_tw', 'TCP sockets in TIME_WAIT', 'tab:orange'),
    ('sock_used', 'TCP sockets in use', 'tab:green'),
    ('pem_cpu_m', 'vizier-PEM CPU (millicores)', 'tab:red'),
    ('redis_ops_s', 'redis instantaneous ops/sec', 'tab:purple'),
    ('coredns_q_total', 'CoreDNS total queries (cumul.)', 'tab:brown'),
    ('pg_xact_commit', 'PG xact_commit (cumul.)', 'tab:cyan'),
    ('api_access_lines', 'API access-log lines (cumul.)', 'tab:pink'),
]
for run in runs:
    if not run['csv']:
        continue
    fig, axes = plt.subplots(4, 2, figsize=(14, 10), sharex=True)
    axes = axes.flatten()
    t0 = int(run['csv'][0]['ts'])
    ts = [(int(r['ts']) - t0) for r in run['csv']]
    for ax, (col, label, color) in zip(axes, TS_METRICS):
        ys = []
        for r in run['csv']:
            try:
                ys.append(float(r.get(col, 0) or 0))
            except (ValueError, TypeError):
                ys.append(0)
        ax.plot(ts, ys, color=color, marker='o', markersize=3, linewidth=1.2)
        ax.set_title(label, fontsize=9)
        ax.grid(True, alpha=0.3)
        ax.set_xlabel('seconds')
    fig.suptitle(f"{run['name']}  k6={run.get('k6_actual',
                                              '?')}/s  target={run.get('target',
                                                                       '?')}/s  ({run['replicas']}×{run['qps_per_pod']})",
                 fontsize=11)
    fig.tight_layout()
    out = os.path.join(MATRIX, f"{run['name']}.png")
    fig.savefig(out, dpi=110, bbox_inches='tight')
    plt.close(fig)
    print(f"wrote {out}")

# === Summary bar charts ===
fig, axes = plt.subplots(3, 2, figsize=(16, 11))
names = [r['name'] for r in runs]
targets = [r.get('target', 0) for r in runs]
actuals = [r.get('k6_actual', 0) for r in runs]
delivery = [(a / t * 100 if t else 0) for a, t in zip(actuals, targets)]
ct_max = [r.get('ct_max', 0) or 0 for r in runs]
pem_max = [r.get('pem_cpu_max', 0) or 0 for r in runs]
pem_avg = [r.get('pem_cpu_avg', 0) or 0 for r in runs]
coredns = [r.get('coredns_q_rate', 0) or 0 for r in runs]

ax = axes[0, 0]
x = list(range(len(names)))
w = 0.4
ax.bar([i - w / 2 for i in x], targets, width=w, label='target', color='tab:gray', alpha=0.7)
ax.bar([i + w / 2 for i in x], actuals, width=w, label='actual', color='tab:blue')
ax.set_xticks(x)
ax.set_xticklabels(names, rotation=20, ha='right')
ax.set_ylabel('req/sec')
ax.set_title('k6 achieved vs target')
ax.legend()
ax.grid(True, alpha=0.3)

ax = axes[0, 1]
ax.bar(x, delivery, color='tab:green')
ax.set_xticks(x)
ax.set_xticklabels(names, rotation=20, ha='right')
ax.set_ylabel('% of target')
ax.set_title('Delivery ratio (k6 / target)')
ax.axhline(100, color='k', linewidth=0.5, linestyle='--')
ax.grid(True, alpha=0.3)

ax = axes[1, 0]
ax.bar(x, ct_max, color='tab:red')
ax.set_xticks(x)
ax.set_xticklabels(names, rotation=20, ha='right')
ax.set_ylabel('count')
ax.set_title('Peak nf_conntrack_count (cap: 1,048,576)')
ax.axhline(1048576, color='r', linewidth=0.5, linestyle='--', label='nf_conntrack_max')
ax.legend()
ax.grid(True, alpha=0.3)

ax = axes[1, 1]
ax.bar([i - w / 2 for i in x], pem_max, width=w, label='pem peak (m)', color='tab:purple')
ax.bar([i + w / 2 for i in x], pem_avg, width=w, label='pem avg (m)', color='tab:pink')
ax.set_xticks(x)
ax.set_xticklabels(names, rotation=20, ha='right')
ax.set_ylabel('millicores')
ax.set_title('vizier-PEM CPU')
ax.legend()
ax.grid(True, alpha=0.3)

ax = axes[2, 0]
ax.bar(x, coredns, color='tab:brown')
ax.set_xticks(x)
ax.set_xticklabels(names, rotation=20, ha='right')
ax.set_ylabel('queries/sec')
ax.set_title('CoreDNS query rate')
ax.grid(True, alpha=0.3)

ax = axes[2, 1]
loadgen_count = [r['replicas'] for r in runs]
ax.scatter(loadgen_count, actuals, s=80, c='tab:blue')
for n, lg, ac in zip(names, loadgen_count, actuals):
    ax.annotate(n, (lg, ac), fontsize=8, xytext=(5, 3), textcoords='offset points')
ax.set_xlabel('loadgen pod count')
ax.set_ylabel('k6 achieved req/sec')
ax.set_title('Throughput vs loadgen replicas')
ax.grid(True, alpha=0.3)

fig.tight_layout()
out = os.path.join(MATRIX, 'summary.png')
fig.savefig(out, dpi=120, bbox_inches='tight')
plt.close(fig)
print(f"wrote {out}")

# === Text summary ===
with open(os.path.join(MATRIX, 'summary.txt'), 'w') as f:
    f.write(f"matrix3: {MATRIX}\n\n")
    f.write(
        f"{
            'run':<14} {
            'lg×qps':<10} {
                'target':>7} {
                    'actual':>7} {
                        '%':>5} {
                            'ct_max':>9} {
                                'pem_max':>8} {
                                    'pem_avg':>8} {
                                        'coredns':>9}\n")
    for r in runs:
        f.write(f"{r['name']:<14} {r['replicas']}×{r['qps_per_pod']:<8} "
                f"{r.get('target', 0):>7} {r.get('k6_actual', 0):>7} "
                f"{(r.get('k6_actual', 0) / r.get('target', 1) * 100):>5.1f} "
                f"{r.get('ct_max', 0) or 0:>9} {r.get('pem_cpu_max', 0) or 0:>8} "
                f"{r.get('pem_cpu_avg', 0) or 0:>8} {r.get('coredns_q_rate', 0) or 0:>9}\n")
print(f"wrote summary.txt")
