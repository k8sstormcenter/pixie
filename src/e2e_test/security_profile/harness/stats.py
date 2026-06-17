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

# stats.py — collapse results/summary.csv into the markdown table that
# goes straight into FINDINGS.md / the PR description. Run as:
#
#   ./stats.py results/summary.csv > FINDINGS.coverage_table.md
#
# Output is grouped by (N, R) so the three profiles sit side-by-side in
# one row — the eye-catching shape for the PR.

import collections
import csv
import sys
from typing import Dict, Tuple

if len(sys.argv) != 2:
    print(f"usage: {sys.argv[0]} <summary.csv>", file=sys.stderr)
    sys.exit(2)

rows = list(csv.DictReader(open(sys.argv[1])))
by_cell: Dict[Tuple[int, int], Dict[str, Dict[str, str]]] = collections.defaultdict(dict)
for r in rows:
    key = (int(r["n"]), int(r["rate"]))
    by_cell[key][r["profile"]] = r

profiles = sorted({r["profile"] for r in rows})
print("| N | rate (q/s) | " + " | ".join(f"{p} cov %" for p in profiles) + " |")
print("|---:|---:|" + "|".join(["---:"] * len(profiles)) + "|")
for (n, rate) in sorted(by_cell):
    cells = by_cell[(n, rate)]
    line = f"| {n} | {rate} | "
    line += " | ".join(cells.get(p, {}).get("coverage_pct", "—") for p in profiles)
    line += " |"
    print(line)
