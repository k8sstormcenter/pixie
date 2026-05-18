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

"""render-proto-sweep.py — renders the full instrumented proto-sweep CSV
into ONE log-log scaling.png with ALL metric categories: loadgen, pixie,
kubescape, clickhouse, server CPUs, conntrack.

Input:  $DIR/metrics.csv  (one row per multiplier, written by protocol-sweep.sh)
Output: $DIR/scaling.png   — 4×5 panel grid, log-log axes, mean (blue circle) +
                             max (red square dashed) per multiplier with point
                             annotations. Matches render-sweep.py's chart language.
        $DIR/summary.txt   — text table.

If $DIR has no metrics.csv (older sweep format), falls back to the
sweep.log + ch-growth.log retroactive parser.
"""
import matplotlib.pyplot as plt
import sys
import os
import csv
import glob
import math
import re
import matplotlib
matplotlib.use('Agg')

DIR = sys.argv[1] if len(sys.argv) > 1 else sorted(glob.glob('/tmp/proto-sweep-2*'))[-1]
print(f"rendering: {DIR}")

csv_path = os.path.join(DIR, 'metrics.csv')
rows = []
if os.path.exists(csv_path):
    with open(csv_path) as f:
        for r in csv.DictReader(f):
            rows.append({k: int(v) if v and v.lstrip('-').isdigit() else v for k, v in r.items()})
    rows = sorted(rows, key=lambda r: int(r['mult']))
else:
    # Fallback: parse sweep.log
    res_re = re.compile(
        r'^\s*(\d+)x\s+achieved\s+http=(-?\d+)\s+redis=(-?\d+)\s+pgsql=(-?\d+)\s+TOTAL=(-?\d+)\s+\|\s+srv-cpu\s+http=(\d+)m\s+redis=(\d+)m\s+pgsql=(\d+)m\s+\|\s+pem=(\d+)m\s+\|\s+ct\s+(\d+)→(\d+)')
    with open(os.path.join(DIR, 'sweep.log')) as f:
        for line in f:
            m = res_re.match(line)
            if not m:
                continue
            mult = int(m.group(1))
            rows.append({
                'mult': mult,
                'http_target': 1000 * mult, 'redis_target': 1000 * mult, 'pgsql_target': 1000 * mult,
                'http_achieved': max(0, int(m.group(2))),
                'redis_achieved': max(0, int(m.group(3))),
                'pgsql_achieved': max(0, int(m.group(4))),
                'loadgen_total': max(0, int(m.group(5))),
                'http_srv_cpu_m': int(m.group(6)),
                'redis_srv_cpu_m': int(m.group(7)),
                'pgsql_srv_cpu_m': int(m.group(8)),
                'pem_cpu_m': int(m.group(9)),
                'ct_start': int(m.group(10)),
                'ct_end': int(m.group(11)),
                'pem_mem_mi': 0, 'kelvin_cpu_m': 0, 'kelvin_mem_mi': 0,
                'querybroker_cpu_m': 0, 'querybroker_mem_mi': 0,
                'nodeagent_cpu_m': 0, 'nodeagent_mem_mi': 0, 'nodeagent_goroutines': 0,
                'ch_http_rate': 0, 'ch_redis_rate': 0, 'ch_pgsql_rate': 0,
                'ch_kubescape_rate': 0, 'ch_attribution_rate': 0,
            })
    rows = sorted(rows, key=lambda r: r['mult'])

if not rows:
    print("no rows")
    sys.exit(1)


def i(r, k):
    v = r.get(k, 0)
    try:
        return int(v)
    except BaseException:
        return 0


mults = [i(r, 'mult') for r in rows]

# Compute running totals across the sweep — each mult's _cum_X is the sum
# of ch_X_rate × mult_dur over all rows so far. mult_dur prefers the new
# (mult_t_start, mult_t_end) cols if present; falls back to elapsed (t1-t0).


def _mult_dur(r):
    ts = i(r, 'mult_t_start')
    te = i(r, 'mult_t_end')
    if ts > 0 and te > ts:
        return te - ts
    return max(1, i(r, 't1') - i(r, 't0'))


cum = {'http': 0, 'redis': 0, 'pgsql': 0, 'attrib': 0}
for r in rows:
    dur = _mult_dur(r)
    cum['http'] += i(r, 'ch_http_rate') * dur
    cum['redis'] += i(r, 'ch_redis_rate') * dur
    cum['pgsql'] += i(r, 'ch_pgsql_rate') * dur
    cum['attrib'] += i(r, 'ch_attribution_rate') * dur
    r['_cum_http'] = cum['http']
    r['_cum_redis'] = cum['redis']
    r['_cum_pgsql'] = cum['pgsql']
    r['_cum_attrib'] = cum['attrib']

# ------------------------------------------------------------------ KPI defs
# Each KPI returns (mean, max). For our single-window-snapshot data,
# mean == max in most cases; conntrack uses (start, end) → (mean, max).


def kpi(col, scale=1.0):
    def _f(r):
        v = i(r, col) * scale
        return v, v
    return _f


CATEGORIES = {
    "loadgen": [
        (kpi('http_target'), "http target ops/s", "ops/sec"),
        (kpi('http_achieved'), "http achieved ops/s", "ops/sec"),
        (kpi('redis_achieved'), "redis achieved ops/s", "ops/sec"),
        (kpi('pgsql_achieved'), "pgsql achieved ops/s", "ops/sec"),
        (kpi('loadgen_total'), "TOTAL achieved ops/s", "ops/sec"),
    ],
    "pixie": [
        (kpi('pem_cpu_m', 0.1), "PEM CPU", "% of one core"),
        (kpi('pem_mem_mi'), "PEM mem", "MiB"),
        (kpi('kelvin_cpu_m', 0.1), "kelvin CPU", "% of one core"),
        (kpi('kelvin_mem_mi'), "kelvin mem", "MiB"),
        (kpi('querybroker_cpu_m', 0.1), "query-broker CPU", "% of one core"),
        (kpi('querybroker_mem_mi'), "query-broker mem", "MiB"),
    ],
    "kubescape": [
        (kpi('nodeagent_cpu_m', 0.1), "node-agent CPU", "% of one core"),
        (kpi('nodeagent_mem_mi'), "node-agent mem", "MiB"),
        (kpi('nodeagent_goroutines'), "node-agent goroutines", "count"),
        (kpi('ch_kubescape_rate'), "alerts → CH /s", "rows/sec"),
    ],
    "clickhouse": [
        # NOTE: switched away from ch_*_rate (per-mult rows/s) because the
        # operator's anomaly windows persist across mults, so a write
        # landing during mult N can be for an alert fired during mult N-k.
        # Per-mult rate ended up depending on operator-catch-up timing,
        # not on the mult's load. Cumulative count at end-of-mult is the
        # honest "how much landed in CH by this point" metric and grows
        # monotonically.
        # We compute these on-the-fly below as running totals of the
        # ch_*_rate × mult_dur values from each row.
        (lambda r: (i(r, '_cum_http'), i(r, '_cum_http')), "http_events  cumulative", "rows"),
        (lambda r: (i(r, '_cum_redis'), i(r, '_cum_redis')), "redis_events cumulative", "rows"),
        (lambda r: (i(r, '_cum_pgsql'), i(r, '_cum_pgsql')), "pgsql_events cumulative", "rows"),
        (lambda r: (i(r, '_cum_attrib'), i(r, '_cum_attrib')), "adaptive_attribution cum", "rows"),
    ],
    "server": [
        (kpi('http_srv_cpu_m', 0.1), "http-server CPU", "% of one core"),
        (kpi('redis_srv_cpu_m', 0.1), "redis-server CPU", "% of one core"),
        (kpi('pgsql_srv_cpu_m', 0.1), "pgsql-server CPU", "% of one core"),
    ],
    "host": [
        (lambda r: (i(r, 'ct_start'), i(r, 'ct_end')),
         "nf_conntrack (start/end)", "count"),
    ],
}

# Flat list for the combined scaling.png (back-compat)
SCALING_KPIS = []
for cat, kpis in CATEGORIES.items():
    for ex, title, unit in kpis:
        SCALING_KPIS.append((ex, f"{cat}: {title}", unit))

# ------------------------------------------------------------------ render

n_kpis = len(SCALING_KPIS)
cols = 5
nrows = (n_kpis + cols - 1) // cols  # 5 rows for 23 slots (2 hidden)
fig, axes = plt.subplots(nrows, cols, figsize=(5 * cols, 4 * nrows), constrained_layout=True)
fig.suptitle(f"3-protocol sweep — ALL metrics, log-log scaling  ·  {os.path.basename(DIR)}",
             fontsize=14, y=1.01)
axes = axes.flatten()

for ax, (extractor, atitle, unit) in zip(axes, SCALING_KPIS):
    means, maxes = [], []
    for r in rows:
        m, mx = extractor(r)
        means.append(m)
        maxes.append(mx)
    ax.plot(mults, means, marker="o", linewidth=1.4, color="#1f77b4", label="mean")
    ax.plot(mults, maxes, marker="s", linewidth=1.0, color="#d62728",
            linestyle="--", label="max")
    for x, y in zip(mults, means):
        if y is not None and not (isinstance(y, float) and math.isnan(y)):
            ax.annotate(f"{y:.0f}" if y >= 10 else f"{y:.1f}",
                        (x, y), textcoords="offset points", xytext=(4, 4),
                        fontsize=7, color="#1f77b4")
    for x, y in zip(mults, maxes):
        if y is not None and not (isinstance(y, float) and math.isnan(y)):
            ax.annotate(f"{y:.0f}" if y >= 10 else f"{y:.1f}",
                        (x, y), textcoords="offset points", xytext=(4, -10),
                        fontsize=7, color="#d62728")
    all_vals = [v for v in means + maxes if v and v > 0]
    if all_vals and min(all_vals) > 0:
        ax.set_xscale("log", base=2)
        ax.set_yscale("log")
    ax.set_xticks(mults)
    ax.set_xticklabels([f"{m}x" for m in mults])
    ax.set_title(atitle, fontsize=10)
    ax.set_ylabel(unit, fontsize=8)
    ax.grid(True, alpha=0.3, which="both")
    ax.legend(loc="best", fontsize=7)

# Hide unused subplots
for k in range(n_kpis, len(axes)):
    axes[k].set_visible(False)

out = os.path.join(DIR, 'scaling.png')
fig.savefig(out, dpi=120, bbox_inches='tight')
plt.close(fig)
print(f"wrote {out}")

# ------------------------------------------------------------------ per-category PNGs


def render_category(name, kpis):
    nk = len(kpis)
    if nk == 0:
        return
    c = min(nk, 3)
    r = (nk + c - 1) // c
    f2, ax2 = plt.subplots(r, c, figsize=(5.5 * c, 4.2 * r), constrained_layout=True,
                           squeeze=False)
    f2.suptitle(f"{name} — {os.path.basename(DIR)}", fontsize=13, y=1.01)
    ax2_flat = ax2.flatten()
    for ax, (extractor, atitle, unit) in zip(ax2_flat, kpis):
        means, maxes = [], []
        for row in rows:
            mn, mx = extractor(row)
            means.append(mn)
            maxes.append(mx)
        ax.plot(mults, means, marker="o", linewidth=1.4, color="#1f77b4", label="mean")
        ax.plot(mults, maxes, marker="s", linewidth=1.0, color="#d62728",
                linestyle="--", label="max")
        for x, y in zip(mults, means):
            if y is not None and not (isinstance(y, float) and math.isnan(y)):
                ax.annotate(f"{y:.0f}" if y >= 10 else f"{y:.1f}",
                            (x, y), textcoords="offset points", xytext=(4, 4),
                            fontsize=8, color="#1f77b4")
        for x, y in zip(mults, maxes):
            if y is not None and not (isinstance(y, float) and math.isnan(y)):
                ax.annotate(f"{y:.0f}" if y >= 10 else f"{y:.1f}",
                            (x, y), textcoords="offset points", xytext=(4, -10),
                            fontsize=8, color="#d62728")
        all_vals = [v for v in means + maxes if v and v > 0]
        if all_vals and min(all_vals) > 0:
            ax.set_xscale("log", base=2)
            ax.set_yscale("log")
        ax.set_xticks(mults)
        ax.set_xticklabels([f"{m}x" for m in mults])
        ax.set_title(atitle, fontsize=11)
        ax.set_ylabel(unit, fontsize=9)
        ax.grid(True, alpha=0.3, which="both")
        ax.legend(loc="best", fontsize=8)
    for k in range(nk, len(ax2_flat)):
        ax2_flat[k].set_visible(False)
    pout = os.path.join(DIR, f'{name}.png')
    f2.savefig(pout, dpi=120, bbox_inches='tight')
    plt.close(f2)
    print(f"wrote {pout}")


for cat_name, cat_kpis in CATEGORIES.items():
    render_category(cat_name, cat_kpis)

# ------------------------------------------------------------------ text
with open(os.path.join(DIR, 'summary.txt'), 'w') as f:
    f.write(f"proto sweep: {DIR}\n\n")
    f.write(f"{'mult':<6}{'loadgen':>30}{'CH inserts/s':>40}{'PEM/kel/QB/NA cpu(m)':>30}\n")
    for r in rows:
        lg = f"h={i(r,
                    'http_achieved')} r={i(r,
                                           'redis_achieved')} p={i(r,
                                                                   'pgsql_achieved')} tot={i(r,
                                                                                             'loadgen_total')}"
        ch = f"h={i(r,
                    'ch_http_rate')} r={i(r,
                                          'ch_redis_rate')} p={i(r,
                                                                 'ch_pgsql_rate')} ks={i(r,
                                                                                         'ch_kubescape_rate')} att={i(r,
                                                                                                                      'ch_attribution_rate')}"
        cpus = f"pem={i(r,
                        'pem_cpu_m')} kel={i(r,
                                             'kelvin_cpu_m')} qb={i(r,
                                                                    'querybroker_cpu_m')} na={i(r,
                                                                                                'nodeagent_cpu_m')}"
        f.write(f"{i(r, 'mult')}x   {lg:<30}{ch:<40}{cpus}\n")
print(f"wrote summary.txt")
