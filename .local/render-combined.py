#!/usr/bin/env python3
"""render-combined.py — overlay multiple proto-sweep results into one
collapse-curve chart. Pass sweep dirs as args; results merged by mult.
"""
import sys, os, re, glob
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

dirs = sys.argv[1:] if len(sys.argv) > 1 else sorted(glob.glob('/tmp/proto-sweep-2*'))
outdir = dirs[-1]
print(f"out -> {outdir}")

res_re = re.compile(r'^\s*(\d+)x\s+achieved\s+http=(-?\d+)\s+redis=(-?\d+)\s+pgsql=(-?\d+)\s+TOTAL=(-?\d+)\s+\|\s+srv-cpu\s+http=(\d+)m\s+redis=(\d+)m\s+pgsql=(\d+)m\s+\|\s+pem=(\d+)m')

rows = {}
for d in dirs:
    p = os.path.join(d, 'sweep.log')
    if not os.path.exists(p):
        continue
    with open(p) as f:
        for line in f:
            m = res_re.match(line)
            if not m:
                continue
            mult = int(m.group(1))
            target = 1000 * mult
            # Clamp negative (k6 restart artifacts) to NaN-ish 0 for the chart
            http_a  = max(0, int(m.group(2)))
            redis_a = max(0, int(m.group(3)))
            pgsql_a = max(0, int(m.group(4)))
            total   = max(0, int(m.group(5)))
            rows[mult] = {
                'http': http_a, 'redis': redis_a, 'pgsql': pgsql_a,
                'total': total, 'target': target * 3,
                'pem': int(m.group(9)),
                'http_cpu': int(m.group(6)),
                'redis_cpu': int(m.group(7)),
                'pgsql_cpu': int(m.group(8)),
            }

if not rows:
    print("no rows"); sys.exit(1)

mults = sorted(rows.keys())

# Per-protocol delivery curve (linear)
fig, axes = plt.subplots(2, 2, figsize=(15, 10))

# Panel 1: achieved per protocol
ax = axes[0, 0]
ax.plot(mults, [rows[m]['target']/3 for m in mults], marker='o', label='target (per protocol)', color='gray', linestyle='--', linewidth=1.5)
ax.plot(mults, [rows[m]['http'] for m in mults], marker='o', label='http achieved', color='tab:blue', linewidth=2)
ax.plot(mults, [rows[m]['redis'] for m in mults], marker='o', label='redis achieved', color='tab:red', linewidth=2)
ax.plot(mults, [rows[m]['pgsql'] for m in mults], marker='o', label='pgsql achieved', color='tab:green', linewidth=2)
ax.set_xlabel('multiplier'); ax.set_ylabel('ops/sec')
ax.set_title('Per-protocol achieved vs target')
ax.set_xticks(mults); ax.legend(); ax.grid(True, alpha=0.3)
# annotate collapse point with vertical band
ax.axvspan(16, 20, alpha=0.1, color='red', label='collapse zone')

# Panel 2: total system (target vs achieved, linear)
ax = axes[0, 1]
ax.plot(mults, [rows[m]['target'] for m in mults], marker='o', label='total target', color='gray', linestyle='--')
ax.plot(mults, [rows[m]['total']  for m in mults], marker='o', label='total achieved', color='black', linewidth=2)
ax.set_xlabel('multiplier'); ax.set_ylabel('ops/sec')
ax.set_title('Total system throughput — collapse curve')
ax.set_xticks(mults); ax.legend(); ax.grid(True, alpha=0.3)
ax.axvspan(16, 20, alpha=0.1, color='red')

# Panel 3: delivery ratio per protocol
ax = axes[1, 0]
for proto, color in [('http','tab:blue'),('redis','tab:red'),('pgsql','tab:green')]:
    ratios = [rows[m][proto]/(rows[m]['target']/3)*100 for m in mults]
    ax.plot(mults, ratios, marker='o', label=proto, color=color, linewidth=2)
ax.axhline(100, color='k', linewidth=0.5, linestyle='--', label='target')
ax.axhline(50, color='r', linewidth=0.5, linestyle=':', alpha=0.5)
ax.set_xlabel('multiplier'); ax.set_ylabel('% of target')
ax.set_title('Delivery ratio per protocol')
ax.set_xticks(mults); ax.legend(); ax.grid(True, alpha=0.3)
ax.axvspan(16, 20, alpha=0.1, color='red')

# Panel 4: PEM cpu + server cpus
ax = axes[1, 1]
ax.plot(mults, [rows[m]['pem']       for m in mults], marker='o', label='vizier-PEM', color='tab:purple', linewidth=2.5)
ax.plot(mults, [rows[m]['http_cpu']  for m in mults], marker='o', label='http-server',  color='tab:blue')
ax.plot(mults, [rows[m]['redis_cpu'] for m in mults], marker='o', label='redis-server', color='tab:red')
ax.plot(mults, [rows[m]['pgsql_cpu'] for m in mults], marker='o', label='pgsql-server', color='tab:green')
ax.set_xlabel('multiplier'); ax.set_ylabel('millicores')
ax.set_title('Server-side + PEM CPU consumption')
ax.set_xticks(mults); ax.legend(); ax.grid(True, alpha=0.3)
ax.axvspan(16, 20, alpha=0.1, color='red')

fig.suptitle('3-protocol sweep — combined collapse-point analysis (red zone = 16→20× transition)', fontsize=13)
fig.tight_layout()
out = os.path.join(outdir, 'combined-collapse.png')
fig.savefig(out, dpi=120, bbox_inches='tight')
plt.close(fig)
print(f"wrote {out}")

# text summary
print()
print(f"{'mult':<6}{'http':>10}{'redis':>10}{'pgsql':>10}{'TOTAL':>10}{'%tgt':>7}{'pem':>9}")
for m in mults:
    r = rows[m]
    pct = r['total']/r['target']*100 if r['target'] else 0
    print(f"{m}x   {r['http']:>8}  {r['redis']:>8}  {r['pgsql']:>8}  {r['total']:>8}  {pct:>5.1f}%  {r['pem']:>5}m")
