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

"""stats.py — reduce an experiment CSV to a per-metric reproducibility report.

Exact reproducibility ⇔ every measured (`*_act`) metric has a single distinct
value across all PASS reps (std = 0 / CV = 0). Prints per-metric
n/distinct/mean/std/CV%/min/max and an overall verdict. No fabrication: it only
summarizes the rows the harness actually recorded.

Usage: stats.py <csv> [<csv> ...]
"""
import csv
import statistics as st
import sys


def num(x):
    try:
        return float(x)
    except (TypeError, ValueError):
        return None


def report(path):
    with open(path) as f:
        rows = list(csv.DictReader(f))
    if not rows:
        print(f"== {path}: empty ==")
        return
    cols = list(rows[0].keys())
    passcol = "pass" if "pass" in cols else None
    npass = sum(1 for r in rows if passcol and str(r[passcol]).startswith("PASS"))
    print(f"== {path} ==  reps={len(rows)} PASS={npass}/{len(rows)}")

    # Reproducibility metrics = the COUNT columns AE wrote (must be constant
    # across reps). wm_act is EXCLUDED: it equals each rep's distinct event_time
    # by design (monotone), validated per-rep as wm_act==wm_exp via the pass flag
    # — it is not expected to be constant across reps.
    metrics = [c for c in cols if c.endswith("_act") and c != "wm_act"]
    metrics = list(dict.fromkeys(metrics))  # dedupe, keep order
    repro_ok = True
    for c in metrics:
        vals = [num(r[c]) for r in rows if (not passcol or str(r[passcol]).startswith("PASS"))]
        vals = [v for v in vals if v is not None]
        if not vals:
            print(f"  {c:16s}  (no numeric PASS values)")
            continue
        distinct = sorted(set(vals))
        mean = st.fmean(vals)
        sd = st.pstdev(vals) if len(vals) > 1 else 0.0
        cv = (sd / mean * 100) if mean else 0.0
        flag = "EXACT" if len(distinct) == 1 else f"VARIES({len(distinct)})"
        if len(distinct) != 1:
            repro_ok = False
        print(f"  {c:16s}  n={len(vals):4d} distinct={len(distinct):3d} "
              f"mean={mean:.3f} std={sd:.3f} cv={cv:.4f}% "
              f"min={min(vals):.0f} max={max(vals):.0f}  {flag}")
    print(f"  VERDICT: {'EXACTLY REPRODUCIBLE (all metrics std=0)' if repro_ok else 'NOT exactly reproducible (see VARIES above)'}")
    print()


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(2)
    for p in sys.argv[1:]:
        report(p)


if __name__ == "__main__":
    main()
