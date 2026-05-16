#!/usr/bin/env python3
"""render-allmetrics.py — single log-log scaling.png with ALL metric
categories: loadgen, pixie, server CPUs, clickhouse, kubescape, conntrack.

Inputs:
  $DIR/sweep.log     — per-multiplier loadgen achieved + server/PEM CPU + ct
                       (old format, no per-mult timestamps)
  $DIR/ch-growth.log — per-minute CH row totals (optional)
Plus: retroactive direct CH queries for the per-multiplier wall-clock window
to recover kubescape_logs rate and forensic_db.{http,redis,pgsql}_events rates.

Output:
  $DIR/scaling.png   — 4×5 panel grid covering every measured metric
"""
import sys, os, re, csv, glob, math, subprocess, datetime as dt, urllib.parse
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

DIR = sys.argv[1] if len(sys.argv) > 1 else sorted(glob.glob('/tmp/proto-sweep-2*'))[-1]
print(f"rendering: {DIR}")

CH_URL = 'http://localhost:30123'
CH_AUTH = ('pixie', 'pixie_password')

def ch_query(q):
    try:
        url = f"{CH_URL}/?query=" + urllib.parse.quote(q + " FORMAT TabSeparated")
        out = subprocess.run(
            ['curl', '-s', '-u', f"{CH_AUTH[0]}:{CH_AUTH[1]}", url],
            capture_output=True, text=True, timeout=15)
        v = out.stdout.strip()
        if v.startswith("Code:"):
            return None
        return int(v) if v.isdigit() else None
    except Exception as e:
        print(f"ch query failed: {e}")
        return None

# ------------------------------------------------------------------ parse sweep.log
sweep_start = None
sweep_end = None
res_re = re.compile(r'^\s*(\d+)x\s+achieved\s+http=(-?\d+)\s+redis=(-?\d+)\s+pgsql=(-?\d+)\s+TOTAL=(-?\d+)\s+\|\s+srv-cpu\s+http=(\d+)m\s+redis=(\d+)m\s+pgsql=(\d+)m\s+\|\s+pem=(\d+)m\s+\|\s+ct\s+(\d+)→(\d+)')
start_re = re.compile(r'^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z) start$')
end_re   = re.compile(r'^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z) end$')

rows = []
with open(os.path.join(DIR, 'sweep.log')) as f:
    for line in f:
        m = start_re.match(line)
        if m: sweep_start = dt.datetime.fromisoformat(m.group(1).replace('Z','+00:00'))
        m = end_re.match(line)
        if m: sweep_end = dt.datetime.fromisoformat(m.group(1).replace('Z','+00:00'))
        m = res_re.match(line)
        if not m: continue
        rows.append({
            'mult': int(m.group(1)),
            'http_target': 1000*int(m.group(1)),
            'redis_target': 1000*int(m.group(1)),
            'pgsql_target': 1000*int(m.group(1)),
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
        })
rows.sort(key=lambda r: r['mult'])

if not rows or sweep_start is None or sweep_end is None:
    print(f"could not parse sweep.log start/end ({sweep_start}, {sweep_end}, rows={len(rows)})")
    sys.exit(1)

n = len(rows)
total_s = (sweep_end - sweep_start).total_seconds()
per_mult = total_s / n
warmup = 30
window_s = 180
print(f"sweep {sweep_start} → {sweep_end}  ({total_s:.0f}s, ~{per_mult:.0f}s per mult, {n} mults)")

# Compute per-mult wall-clock windows.
for i, r in enumerate(rows):
    t0 = sweep_start + dt.timedelta(seconds=i*per_mult + warmup)
    t1 = t0 + dt.timedelta(seconds=window_s)
    r['t0'] = t0
    r['t1'] = t1

# ------------------------------------------------------------------ retroactive CH queries
print("retroactive CH queries per mult window...")
for r in rows:
    t0_iso = r['t0'].strftime('%Y-%m-%d %H:%M:%S')
    t1_iso = r['t1'].strftime('%Y-%m-%d %H:%M:%S')
    win = (r['t1'] - r['t0']).total_seconds()
    # http_events / redis_events / pgsql_events use `time_` column (DateTime64(9))
    for tbl, key in [('http_events','ch_http_rate'),
                     ('redis_events','ch_redis_rate'),
                     ('pgsql_events','ch_pgsql_rate'),
                     ('adaptive_attribution','ch_attribution_rate')]:
        if tbl == 'adaptive_attribution':
            qcol = 't_start'  # adaptive_attribution uses t_start (or similar)
            cnt = ch_query(f"SELECT count() FROM forensic_db.{tbl} WHERE {qcol} >= '{t0_iso}' AND {qcol} < '{t1_iso}'")
            if cnt is None:
                # try generic timestamp column
                cnt = ch_query(f"SELECT count() FROM forensic_db.{tbl} WHERE last_seen >= '{t0_iso}' AND last_seen < '{t1_iso}'")
        else:
            cnt = ch_query(f"SELECT count() FROM forensic_db.{tbl} WHERE time_ >= '{t0_iso}' AND time_ < '{t1_iso}'")
        r[key] = int((cnt or 0) / win) if cnt else 0
    # kubescape_logs uses event_time (UInt64 nanos)
    t0_ns = int(r['t0'].timestamp() * 1e9)
    t1_ns = int(r['t1'].timestamp() * 1e9)
    cnt = ch_query(f"SELECT count() FROM forensic_db.kubescape_logs WHERE event_time >= {t0_ns} AND event_time < {t1_ns}")
    r['ch_kubescape_rate'] = int((cnt or 0) / win) if cnt else 0

print("retro queries done")

# ------------------------------------------------------------------ render
def i_(r, k): return r.get(k, 0) or 0

mults = [r['mult'] for r in rows]

def kpi(col, scale=1.0):
    def _f(r):
        v = i_(r, col) * scale
        return v, v
    return _f

PANELS = [
    # === LOADGEN ===
    (kpi('http_target'),         "loadgen: http target ops/s",        "ops/sec"),
    (kpi('http_achieved'),       "loadgen: http achieved ops/s",      "ops/sec"),
    (kpi('redis_achieved'),      "loadgen: redis achieved ops/s",     "ops/sec"),
    (kpi('pgsql_achieved'),      "loadgen: pgsql achieved ops/s",     "ops/sec"),
    (kpi('loadgen_total'),       "loadgen: TOTAL achieved ops/s",     "ops/sec"),

    # === PIXIE ===
    (kpi('pem_cpu_m', 0.1),      "pixie: PEM CPU",                    "% of one core"),

    # === SERVER CPUs ===
    (kpi('http_srv_cpu_m', 0.1), "server: http-server CPU",           "% of one core"),
    (kpi('redis_srv_cpu_m', 0.1),"server: redis-server CPU",          "% of one core"),
    (kpi('pgsql_srv_cpu_m', 0.1),"server: pgsql-server CPU",          "% of one core"),

    # === KUBESCAPE ===
    (kpi('ch_kubescape_rate'),   "kubescape: alerts (kubescape_logs) /s", "rows/sec"),

    # === CLICKHOUSE ===
    (kpi('ch_http_rate'),        "CH: http_events  /s",               "rows/sec"),
    (kpi('ch_redis_rate'),       "CH: redis_events /s",               "rows/sec"),
    (kpi('ch_pgsql_rate'),       "CH: pgsql_events /s",               "rows/sec"),
    (kpi('ch_attribution_rate'), "CH: adaptive_attribution /s",       "rows/sec"),

    # === HOST ===
    (lambda r: (i_(r,'ct_start'), i_(r,'ct_end')),
                                 "host: nf_conntrack (start/end)",    "count"),
]

n_kpis = len(PANELS)
cols = 5
nrows = (n_kpis + cols - 1) // cols
fig, axes = plt.subplots(nrows, cols, figsize=(5*cols, 4*nrows), constrained_layout=True)
fig.suptitle(f"ALL metrics — log-log scaling  ·  {os.path.basename(DIR)}", fontsize=14, y=1.01)
axes = axes.flatten()

for ax, (extractor, atitle, unit) in zip(axes, PANELS):
    means, maxes = [], []
    for r in rows:
        m, mx = extractor(r); means.append(m); maxes.append(mx)
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

for k in range(n_kpis, len(axes)):
    axes[k].set_visible(False)

out = os.path.join(DIR, 'scaling.png')
fig.savefig(out, dpi=120, bbox_inches='tight')
plt.close(fig)
print(f"wrote {out}")

# text dump
print()
print(f"{'mult':<5}{'loadgen tot':>12}{'pem%':>7}{'CH http/s':>10}{'CH redis/s':>11}{'CH pgsql/s':>11}{'CH ks/s':>9}{'CH attr/s':>10}")
for r in rows:
    print(f"{r['mult']}x  {i_(r,'loadgen_total'):>10}  {i_(r,'pem_cpu_m')/10:>5.1f}  "
          f"{i_(r,'ch_http_rate'):>8}  {i_(r,'ch_redis_rate'):>9}  {i_(r,'ch_pgsql_rate'):>9}  "
          f"{i_(r,'ch_kubescape_rate'):>7}  {i_(r,'ch_attribution_rate'):>8}")
