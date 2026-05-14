#!/usr/bin/env python3
"""render-sweep.py — turn perf_tool parquet output into inspection PNGs.

Discovers every `<sweep_dir>/<Nx>/.../*.parquet` produced by `perf-sweep.sh`,
renders a per-run multi-panel PNG, and a cross-run summary that compares all
multipliers on the same axes.

Run idempotently — re-rendering existing PNGs is safe; the watcher
(`render-sweep-watch.sh`) reinvokes this script every time a new parquet
appears on disk so you can inspect partial results during the sweep.

Inputs assumed:
  <sweep_dir>/<Nx>/2026/MM/DD/<exp_uuid>/results_0000*.parquet
  <sweep_dir>/<Nx>/2026/MM/DD/<exp_uuid>/spec.parquet

Output:
  <sweep_dir>/<Nx>.png          — 6-panel per-run inspection chart
  <sweep_dir>/summary.png       — small-multiples cross-run comparison
  <sweep_dir>/scorecard.png     — bar chart: peak/mean of key metrics per run

Spotting bugs:
  * recorder rate flat across BURNIN vs RUN  → bobctl/k6 not adding load
  * PEM CPU plateaus before 100%             → bottleneck elsewhere
  * CH memory climbing monotonically         → OOM coming
  * forensic_alert_count stays 0             → kubescape→Vector pipeline broken
"""

import argparse
import json
import os
import re
import sys
from dataclasses import dataclass
from pathlib import Path

import matplotlib
matplotlib.use("Agg")  # no display on the VM
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import pandas as pd
import pyarrow.parquet as pq

# ------------------------------------------------------------------ helpers

MULTIPLIER_RE = re.compile(r"^(\d+)x$")


@dataclass
class RunData:
    name: str               # "1x"
    multiplier: int         # 1
    results_path: Path
    spec_path: Path | None
    results: pd.DataFrame   # long-format metric rows
    actions: pd.DataFrame   # begin_X/end_X timeline
    spec_tags: list[str]    # tags from spec.parquet

    @property
    def run_start(self):
        m = self.actions.query("name == 'begin_run:'")
        return m["timestamp"].iloc[0] if not m.empty else None

    @property
    def run_end(self):
        m = self.actions.query("name == 'end_run:'")
        return m["timestamp"].iloc[0] if not m.empty else None

    @property
    def burnin_start(self):
        m = self.actions.query("name == 'begin_burnin:'")
        return m["timestamp"].iloc[0] if not m.empty else None


def find_runs(sweep_dir: Path) -> list[RunData]:
    """Discover all Nx/ subdirs with finished parquets. Skip in-flight runs."""
    runs: list[RunData] = []
    for sub in sorted(sweep_dir.iterdir(), key=lambda p: p.name):
        if not sub.is_dir():
            continue
        m = MULTIPLIER_RE.match(sub.name)
        if not m:
            continue
        results = list(sub.rglob("results_*.parquet"))
        if not results:
            continue  # in-flight, no parquet yet
        # If there are multiple result files, pick the largest (most rows).
        results.sort(key=lambda p: p.stat().st_size, reverse=True)
        res_path = results[0]
        # A 0-byte parquet means perf_tool aborted mid-write — skip.
        if res_path.stat().st_size < 1024:
            continue
        spec_candidates = list(res_path.parent.glob("spec.parquet"))
        spec_path = spec_candidates[0] if spec_candidates else None

        results_df = pq.read_table(res_path).to_pandas()
        results_df["timestamp"] = pd.to_datetime(
            results_df["timestamp"], utc=True
        )
        actions = results_df[
            results_df["name"].str.startswith(("begin_", "end_"))
        ].copy()
        spec_tags: list[str] = []
        if spec_path is not None:
            try:
                spec_row = pq.read_table(spec_path).to_pandas().iloc[0]
                spec_obj = json.loads(spec_row["spec"])
                spec_tags = list(spec_obj.get("tags", []))
            except Exception as e:  # pragma: no cover
                print(f"  ! spec parse failed for {sub.name}: {e}",
                      file=sys.stderr)
        runs.append(
            RunData(
                name=sub.name,
                multiplier=int(m.group(1)),
                results_path=res_path,
                spec_path=spec_path,
                results=results_df,
                actions=actions,
                spec_tags=spec_tags,
            )
        )
    return runs


def _phase_markers(ax, run: RunData):
    """Vertical lines for BURNIN start, RUN start, RUN end."""
    for ts, label, color in [
        (run.burnin_start, "burnin", "#888888"),
        (run.run_start, "RUN", "#cc0000"),
        (run.run_end, "end", "#888888"),
    ]:
        if ts is not None:
            ax.axvline(ts, color=color, linestyle="--", linewidth=1, alpha=0.6)
            ax.text(
                ts, ax.get_ylim()[1], f" {label}",
                fontsize=7, color=color, va="top", ha="left",
            )


def _filter_run(df: pd.DataFrame, run: RunData) -> pd.DataFrame:
    """Limit a metric series to the experiment's begin_run..end_run window."""
    if run.run_start is None or run.run_end is None:
        return df
    return df[(df["timestamp"] >= run.run_start)
              & (df["timestamp"] <= run.run_end)]


def _delta_rate(df: pd.DataFrame, per_seconds: float = 60.0) -> pd.DataFrame:
    """Convert a monotonic-counter time series to per-N-seconds rate."""
    df = df.sort_values("timestamp").reset_index(drop=True)
    df["dt"] = df["timestamp"].diff().dt.total_seconds()
    df["dv"] = df["value"].diff()
    df["rate"] = (df["dv"] / df["dt"]) * per_seconds
    df = df[df["rate"] >= 0]  # drop the first row + any counter resets
    return df


# ------------------------------------------------------------------ per-run

POD_COLORS = {
    "vizier-pem": "#1f77b4",
    "kelvin":     "#ff7f0e",
    "vizier-query-broker": "#2ca02c",
    "vizier-metadata": "#9467bd",
    "vizier-cloud-connector": "#8c564b",
    "pl-nats": "#7f7f7f",
}


def _pod_color(pod: str) -> str:
    for prefix, c in POD_COLORS.items():
        if prefix in pod:
            return c
    return "#cccccc"


def render_run(run: RunData, out_path: Path) -> None:
    fig, axes = plt.subplots(4, 2, figsize=(15, 14), constrained_layout=True)
    fig.suptitle(
        f"{run.name} ({run.multiplier}× load) — "
        f"results: {run.results_path.relative_to(run.results_path.parents[5])}",
        fontsize=12,
        y=1.02,
    )

    # ----- panel (0,0) recorder export rate (events / 5s tick) -----
    ax = axes[0, 0]
    ex = run.results[run.results["name"] == "clickhouse_export_rows"]
    if not ex.empty:
        ax.plot(ex["timestamp"], ex["value"], marker=".", markersize=2,
                linewidth=0.8, label="rows/tick")
        ax.set_title("Pixie → CH recorder rate (rows per 5s tick)")
        ax.set_ylabel("rows per tick")
        ax.grid(alpha=0.3)
        _phase_markers(ax, run)
    else:
        ax.set_title("clickhouse_export_rows — NO DATA")
        ax.text(0.5, 0.5, "no data", ha="center", va="center",
                transform=ax.transAxes, color="red")

    # ----- panel (0,1) per-pod CPU (during RUN) -----
    ax = axes[0, 1]
    cpu = run.results[run.results["name"] == "cpu_usage"]
    cpu = _filter_run(cpu, run)
    if not cpu.empty:
        for pod, g in cpu.groupby("tag_pod"):
            label = pod.split("/")[-1] if pod else "?"
            ax.plot(g["timestamp"], g["value"] * 100,
                    label=label[:30], linewidth=1.0, color=_pod_color(pod))
        ax.set_title("Pixie pods CPU% (during RUN)")
        ax.set_ylabel("% of one core")
        ax.legend(fontsize=7, loc="upper right", ncol=2)
        ax.grid(alpha=0.3)
    else:
        ax.set_title("cpu_usage — NO DATA")

    # ----- panel (1,0) CH memory -----
    ax = axes[1, 0]
    mem = run.results[
        run.results["name"] == "clickhouse_memory_tracking_bytes"
    ]
    if not mem.empty:
        ax.plot(mem["timestamp"], mem["value"] / 1e9,
                color="#d62728", linewidth=1.2)
        ax.set_title("ClickHouse memory_tracking (GB)")
        ax.set_ylabel("GB")
        ax.grid(alpha=0.3)
        _phase_markers(ax, run)
    else:
        ax.set_title("clickhouse_memory_tracking_bytes — NO DATA")

    # ----- panel (1,1) CH parts_active + queries_total rate -----
    ax = axes[1, 1]
    parts = run.results[run.results["name"] == "clickhouse_parts_active"]
    qrate = run.results[run.results["name"] == "clickhouse_queries_total"]
    qrate = _delta_rate(qrate)
    if not parts.empty:
        ax.plot(parts["timestamp"], parts["value"],
                color="#17becf", linewidth=1.2, label="parts_active")
    if not qrate.empty:
        ax2 = ax.twinx()
        ax2.plot(qrate["timestamp"], qrate["rate"],
                 color="#bcbd22", linewidth=1.0, label="queries/min")
        ax2.set_ylabel("queries/min", color="#bcbd22")
        ax2.tick_params(axis="y", labelcolor="#bcbd22")
    ax.set_title("CH parts_active + query rate")
    ax.set_ylabel("parts_active", color="#17becf")
    ax.tick_params(axis="y", labelcolor="#17becf")
    ax.grid(alpha=0.3)

    # ----- panel (2,0) forensic_alert_count over time -----
    ax = axes[2, 0]
    alerts = run.results[run.results["name"] == "forensic_alert_count"]
    if not alerts.empty:
        ax.plot(alerts["timestamp"], alerts["value"],
                color="#e377c2", linewidth=1.2, marker=".", markersize=3)
        ax.set_title(f"forensic_alert_count "
                     f"(max={int(alerts['value'].max())})")
        ax.set_ylabel("alerts in window")
        ax.grid(alpha=0.3)
        _phase_markers(ax, run)
    else:
        ax.set_title("forensic_alert_count — NO DATA")

    # ----- panel (2,1) inserted_rows rate (rows/min) -----
    ax = axes[2, 1]
    ins = run.results[run.results["name"] == "clickhouse_inserted_rows_total"]
    ins = _delta_rate(ins)
    if not ins.empty:
        ax.plot(ins["timestamp"], ins["rate"] / 1e3,
                color="#9467bd", linewidth=1.2)
        ax.set_title(f"CH inserted rows/min (peak: "
                     f"{int(ins['rate'].max()/1e3)}K/min)")
        ax.set_ylabel("K rows/min")
        ax.grid(alpha=0.3)
        _phase_markers(ax, run)
    else:
        ax.set_title("clickhouse_inserted_rows_total — NO DATA")

    # ----- panel (3,0) kubescape node-agent CPU% + RSS -----
    ax = axes[3, 0]
    ks_cpu_total = run.results[
        run.results["name"] == "kubescape_node_agent_cpu_seconds_total"
    ]
    # cpu_seconds_total is a monotonic Prometheus counter — convert to
    # CPU% by dividing the delta by the wall-clock delta and *100.
    ks_cpu_rate = _delta_rate(ks_cpu_total, per_seconds=100.0)
    ks_rss = run.results[run.results["name"] == "kubescape_node_agent_rss"]
    plotted_any = False
    if not ks_cpu_rate.empty:
        ax.plot(ks_cpu_rate["timestamp"], ks_cpu_rate["rate"],
                color="#1f77b4", linewidth=1.2, label="CPU %")
        plotted_any = True
    if not ks_rss.empty:
        ax2 = ax.twinx()
        ax2.plot(ks_rss["timestamp"], ks_rss["value"] / (1024 * 1024),
                 color="#ff7f0e", linewidth=1.2, label="RSS MB")
        ax2.set_ylabel("RSS MB", color="#ff7f0e")
        ax2.tick_params(axis="y", labelcolor="#ff7f0e")
        plotted_any = True
    if plotted_any:
        cpu_peak = (ks_cpu_rate['rate'].max()
                    if not ks_cpu_rate.empty else 0)
        rss_peak_mb = (ks_rss['value'].max() / (1024 * 1024)
                       if not ks_rss.empty else 0)
        ax.set_title(
            f"Kubescape node-agent (peak: {cpu_peak:.0f}% CPU, "
            f"{rss_peak_mb:.0f} MB RSS)"
        )
        ax.set_ylabel("CPU %", color="#1f77b4")
        ax.tick_params(axis="y", labelcolor="#1f77b4")
        ax.grid(alpha=0.3)
        _phase_markers(ax, run)
    else:
        ax.set_title("kubescape_node_agent_* — NO DATA")

    # ----- panel (3,1) kubescape node-agent goroutines (leak detector) -----
    ax = axes[3, 1]
    ks_g = run.results[run.results["name"] == "kubescape_node_agent_goroutines"]
    if not ks_g.empty:
        ax.plot(ks_g["timestamp"], ks_g["value"],
                color="#2ca02c", linewidth=1.2, marker=".", markersize=3)
        # First-vs-last comparison flags monotonic growth → goroutine leak.
        first = ks_g.iloc[0]["value"]
        last = ks_g.iloc[-1]["value"]
        ax.set_title(
            f"Kubescape goroutines (start={int(first)}, end={int(last)}, "
            f"peak={int(ks_g['value'].max())})"
        )
        ax.set_ylabel("goroutines")
        ax.grid(alpha=0.3)
        _phase_markers(ax, run)
    else:
        ax.set_title("kubescape_node_agent_goroutines — NO DATA")

    # x-axis formatter for all time-series panels
    for ax in axes.flat:
        ax.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M"))

    fig.savefig(out_path, dpi=120, bbox_inches="tight")
    plt.close(fig)


# ------------------------------------------------------------------ summary

def render_summary(runs: list[RunData], out_path: Path) -> None:
    """Small-multiples: each multiplier on the same recorder-rate axis,
    stacked top-to-bottom so it's obvious whether 16× actually achieves 16×.
    """
    if not runs:
        return
    fig, axes = plt.subplots(
        len(runs), 1,
        figsize=(14, 2.5 * len(runs)),
        sharex=False,
        constrained_layout=True,
    )
    if len(runs) == 1:
        axes = [axes]
    fig.suptitle(
        "Recorder rate across load multipliers (rows / 5s tick)",
        fontsize=13,
        y=1.0,
    )
    for ax, run in zip(axes, runs):
        ex = run.results[run.results["name"] == "clickhouse_export_rows"]
        if ex.empty:
            ax.text(0.5, 0.5, "no data", ha="center", va="center",
                    transform=ax.transAxes, color="red")
            ax.set_title(f"{run.name}")
            continue
        ax.plot(ex["timestamp"], ex["value"],
                marker=".", markersize=2, linewidth=0.8)
        ax.set_title(
            f"{run.name} ({run.multiplier}×): "
            f"mean={ex['value'].mean():.0f}, peak={ex['value'].max():.0f}, "
            f"n={len(ex)} ticks"
        )
        ax.set_ylabel("rows/tick")
        ax.grid(alpha=0.3)
        ax.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M"))
        _phase_markers(ax, run)
    fig.savefig(out_path, dpi=120, bbox_inches="tight")
    plt.close(fig)


# ------------------------------------------------------------------ scorecard

def render_scorecard(runs: list[RunData], out_path: Path) -> None:
    """Grouped bar chart: peak/mean of key metrics per multiplier.
    Designed to make non-linear scaling jump out (e.g. 16× recorder rate
    not actually 16× because of a bottleneck)."""
    if not runs:
        return
    rows = []
    for r in runs:
        ex = r.results[r.results["name"] == "clickhouse_export_rows"]
        cpu_pem = r.results[
            (r.results["name"] == "cpu_usage")
            & r.results["tag_pod"].fillna("").str.contains("vizier-pem")
        ]
        cpu_pem = _filter_run(cpu_pem, r)
        mem = r.results[
            r.results["name"] == "clickhouse_memory_tracking_bytes"
        ]
        ins = _delta_rate(
            r.results[r.results["name"] == "clickhouse_inserted_rows_total"]
        )
        ks_cpu = _delta_rate(
            r.results[r.results["name"] == "kubescape_node_agent_cpu_seconds_total"],
            per_seconds=100.0,
        )
        ks_rss = r.results[
            r.results["name"] == "kubescape_node_agent_rss"
        ]
        ks_g = r.results[
            r.results["name"] == "kubescape_node_agent_goroutines"
        ]
        rows.append({
            "multiplier": r.multiplier,
            "name": r.name,
            "recorder_mean_per_tick":  ex["value"].mean() if not ex.empty else 0,
            "recorder_peak_per_tick":  ex["value"].max()  if not ex.empty else 0,
            "pem_cpu_mean_pct":        (cpu_pem["value"].mean()*100) if not cpu_pem.empty else 0,
            "pem_cpu_peak_pct":        (cpu_pem["value"].max()*100)  if not cpu_pem.empty else 0,
            "ch_mem_peak_gb":          (mem["value"].max()/1e9) if not mem.empty else 0,
            "ch_ins_peak_kpm":         (ins["rate"].max()/1e3)  if not ins.empty else 0,
            "ks_cpu_mean_pct":         ks_cpu["rate"].mean() if not ks_cpu.empty else 0,
            "ks_cpu_peak_pct":         ks_cpu["rate"].max()  if not ks_cpu.empty else 0,
            "ks_rss_peak_mb":          (ks_rss["value"].max()/(1024*1024)) if not ks_rss.empty else 0,
            "ks_goroutines_peak":      ks_g["value"].max() if not ks_g.empty else 0,
        })
    df = pd.DataFrame(rows).sort_values("multiplier").reset_index(drop=True)
    metrics = [
        ("recorder_mean_per_tick",  "Recorder mean rows/tick"),
        ("recorder_peak_per_tick",  "Recorder peak rows/tick"),
        ("pem_cpu_mean_pct",        "PEM CPU mean %"),
        ("pem_cpu_peak_pct",        "PEM CPU peak %"),
        ("ch_mem_peak_gb",          "CH memory peak GB"),
        ("ch_ins_peak_kpm",         "CH inserts peak K/min"),
        ("ks_cpu_mean_pct",         "Kubescape node-agent CPU mean %"),
        ("ks_cpu_peak_pct",         "Kubescape node-agent CPU peak %"),
        ("ks_rss_peak_mb",          "Kubescape node-agent RSS peak MB"),
    ]
    fig, axes = plt.subplots(3, 3, figsize=(15, 10), constrained_layout=True)
    fig.suptitle(
        "Scorecard across load multipliers — "
        "ideal: linear in mult unless bottlenecked",
        fontsize=12, y=1.02,
    )
    for ax, (col, title) in zip(axes.flat, metrics):
        bars = ax.bar(df["name"], df[col], color="#1f77b4")
        for b, v in zip(bars, df[col]):
            ax.text(b.get_x() + b.get_width() / 2, b.get_height(),
                    f"{v:.1f}", ha="center", va="bottom", fontsize=8)
        ax.set_title(title)
        ax.grid(axis="y", alpha=0.3)
    fig.savefig(out_path, dpi=120, bbox_inches="tight")
    plt.close(fig)


# ------------------------------------------------------------------ alerts

def render_alert_distribution(runs: list[RunData], out_path: Path) -> None:
    """Plot forensic_alert_count vs minutes-from-RUN-start across all runs,
    plus a cumulative view + an "alerts in first half vs second half" stat.

    The hypothesis we're testing: Kubescape's ApplicationProfile is in
    "learning" state for the first few minutes after pod creation, then
    transitions to "completed" — at which point R0002 et al start firing
    against actual baseline-deviating traffic. If the profile completes
    deep into the RUN window, every alert clusters near the end.
    """
    if not runs:
        return
    fig, axes = plt.subplots(3, 1, figsize=(14, 11), constrained_layout=True)
    fig.suptitle(
        "forensic_alert_count distribution — when do alerts actually fire?",
        fontsize=13, y=1.0,
    )
    cmap = plt.colormaps.get_cmap("viridis")
    n = len(runs)

    # ----- panel 0: alerts per 30s tick, time relative to RUN-start -----
    ax = axes[0]
    rows_for_table = []
    for i, run in enumerate(runs):
        alerts = run.results[run.results["name"] == "forensic_alert_count"]
        if alerts.empty or run.run_start is None:
            continue
        rel = (alerts["timestamp"] - run.run_start).dt.total_seconds() / 60.0
        ax.plot(rel, alerts["value"],
                color=cmap(i / max(n - 1, 1)),
                marker=".", markersize=4, linewidth=1.0,
                label=f"{run.name} (peak {int(alerts['value'].max())})")
        # phase ratio: alerts in first half of RUN vs second half
        dur_min = (run.run_end - run.run_start).total_seconds() / 60.0 \
            if run.run_end is not None else rel.max()
        first_half = alerts[
            (alerts["timestamp"] >= run.run_start)
            & (alerts["timestamp"] < run.run_start + pd.Timedelta(
                minutes=dur_min / 2))
        ]["value"].sum()
        second_half = alerts[
            (alerts["timestamp"] >= run.run_start + pd.Timedelta(
                minutes=dur_min / 2))
            & (alerts["timestamp"] <= (run.run_end or alerts["timestamp"].max()))
        ]["value"].sum()
        total = first_half + second_half
        rows_for_table.append({
            "name": run.name,
            "total": int(total),
            "first_half": int(first_half),
            "second_half": int(second_half),
            "second_half_pct": (100.0 * second_half / total) if total else 0,
        })
    ax.axvline(0, color="red", linestyle="--", linewidth=1, alpha=0.7,
               label="RUN start")
    ax.set_title("Alerts per 30 s metric tick (x-axis: minutes since RUN start)")
    ax.set_xlabel("minutes since begin_run")
    ax.set_ylabel("alerts in last 1-min window")
    ax.legend(fontsize=8, loc="upper left")
    ax.grid(alpha=0.3)

    # ----- panel 1: cumulative alerts over RUN-relative time -----
    ax = axes[1]
    for i, run in enumerate(runs):
        alerts = run.results[run.results["name"] == "forensic_alert_count"] \
            .sort_values("timestamp")
        if alerts.empty or run.run_start is None:
            continue
        rel = (alerts["timestamp"] - run.run_start).dt.total_seconds() / 60.0
        cum = alerts["value"].cumsum()
        ax.plot(rel, cum,
                color=cmap(i / max(n - 1, 1)),
                linewidth=1.4,
                label=f"{run.name} (Σ {int(cum.iloc[-1])})")
    ax.axvline(0, color="red", linestyle="--", linewidth=1, alpha=0.7)
    ax.set_title("Cumulative alerts (steeper later in RUN ⇒ profile-learning lag)")
    ax.set_xlabel("minutes since begin_run")
    ax.set_ylabel("cumulative alerts")
    ax.legend(fontsize=8, loc="upper left")
    ax.grid(alpha=0.3)

    # ----- panel 2: stacked bar showing first-half vs second-half split -----
    ax = axes[2]
    if rows_for_table:
        df = pd.DataFrame(rows_for_table)
        x = range(len(df))
        b1 = ax.bar(x, df["first_half"], color="#888888",
                    label="first half of RUN")
        b2 = ax.bar(x, df["second_half"], bottom=df["first_half"],
                    color="#d62728", label="second half of RUN")
        for i, (b, pct) in enumerate(zip(b2, df["second_half_pct"])):
            ax.text(i, df["first_half"].iloc[i] + df["second_half"].iloc[i],
                    f"{pct:.0f}% late",
                    ha="center", va="bottom", fontsize=9, fontweight="bold")
        ax.set_xticks(list(x))
        ax.set_xticklabels(df["name"])
        ax.set_title(
            "Alerts grouped by RUN-half — "
            "% late ≈ how much of the alert mass clusters in the second half "
            "(profile-completion fingerprint)"
        )
        ax.set_ylabel("Σ alerts in window")
        ax.legend(fontsize=9)
        ax.grid(axis="y", alpha=0.3)

    fig.savefig(out_path, dpi=120, bbox_inches="tight")
    plt.close(fig)


# ------------------------------------------------------------------ scaling

# KPI extractors — each returns (mean_during_run, max_during_run) for a single
# RunData. Returning NaN means "missing"; the plot will skip that point.
import math


def _kpi_recorder(r: RunData) -> tuple[float, float]:
    df = r.results[r.results["name"] == "clickhouse_export_rows"]
    df = _filter_run(df, r)
    if df.empty:
        return math.nan, math.nan
    return df["value"].mean(), df["value"].max()


def _kpi_pem_cpu(r: RunData) -> tuple[float, float]:
    df = r.results[
        (r.results["name"] == "cpu_usage")
        & r.results["tag_pod"].fillna("").str.contains("vizier-pem")
    ]
    df = _filter_run(df, r)
    if df.empty:
        return math.nan, math.nan
    return df["value"].mean() * 100, df["value"].max() * 100


def _kpi_kelvin_cpu(r: RunData) -> tuple[float, float]:
    df = r.results[
        (r.results["name"] == "cpu_usage")
        & r.results["tag_pod"].fillna("").str.contains("kelvin")
    ]
    df = _filter_run(df, r)
    if df.empty:
        return math.nan, math.nan
    return df["value"].mean() * 100, df["value"].max() * 100


def _kpi_ch_memory_gb(r: RunData) -> tuple[float, float]:
    df = r.results[r.results["name"] == "clickhouse_memory_tracking_bytes"]
    df = _filter_run(df, r)
    if df.empty:
        return math.nan, math.nan
    return df["value"].mean() / 1e9, df["value"].max() / 1e9


def _kpi_ch_inserts_kpm(r: RunData) -> tuple[float, float]:
    df = _delta_rate(
        r.results[r.results["name"] == "clickhouse_inserted_rows_total"]
    )
    if df.empty:
        return math.nan, math.nan
    return df["rate"].mean() / 1e3, df["rate"].max() / 1e3


def _kpi_alerts(r: RunData) -> tuple[float, float]:
    df = r.results[r.results["name"] == "forensic_alert_count"]
    df = _filter_run(df, r)
    if df.empty:
        return math.nan, math.nan
    return df["value"].mean(), df["value"].max()


def _kpi_ks_cpu(r: RunData) -> tuple[float, float]:
    df = _delta_rate(
        r.results[r.results["name"] == "kubescape_node_agent_cpu_seconds_total"],
        per_seconds=100.0,
    )
    if df.empty:
        return math.nan, math.nan
    return df["rate"].mean(), df["rate"].max()


def _kpi_ks_rss_mb(r: RunData) -> tuple[float, float]:
    df = r.results[r.results["name"] == "kubescape_node_agent_rss"]
    df = _filter_run(df, r)
    if df.empty:
        return math.nan, math.nan
    return df["value"].mean() / (1024 * 1024), df["value"].max() / (1024 * 1024)


def _kpi_ks_goroutines(r: RunData) -> tuple[float, float]:
    df = r.results[r.results["name"] == "kubescape_node_agent_goroutines"]
    df = _filter_run(df, r)
    if df.empty:
        return math.nan, math.nan
    return df["value"].mean(), df["value"].max()


# (extractor, panel title, y-axis unit)
SCALING_KPIS = [
    (_kpi_recorder,       "Recorder rows/tick",           "rows/tick"),
    (_kpi_pem_cpu,        "PEM CPU",                      "% (of one core)"),
    (_kpi_kelvin_cpu,     "Kelvin CPU",                   "% (of one core)"),
    (_kpi_ch_memory_gb,   "CH memory_tracking",           "GB"),
    (_kpi_ch_inserts_kpm, "CH inserted rows/min",         "K rows/min"),
    (_kpi_alerts,         "forensic_alert_count",         "alerts / 1-min window"),
    (_kpi_ks_cpu,         "Kubescape node-agent CPU",     "%"),
    (_kpi_ks_rss_mb,      "Kubescape node-agent RSS",     "MB"),
    (_kpi_ks_goroutines,  "Kubescape goroutines",         "count"),
]


def render_scaling(runs: list[RunData], out_path: Path) -> None:
    """Log-log scaling chart: each panel plots mean+max of a KPI versus
    the load multiplier. Linear-on-log-log = power-law scaling; flat or
    concave shape ⇒ saturation / bottleneck has kicked in.

    Useful for spotting where Pixie / CH / kubescape stop scaling
    linearly with workload, which is the *whole point* of a load sweep.
    """
    if not runs:
        return
    runs = sorted(runs, key=lambda r: r.multiplier)
    multipliers = [r.multiplier for r in runs]

    fig, axes = plt.subplots(3, 3, figsize=(15, 11), constrained_layout=True)
    fig.suptitle(
        "Scaling — log-log: mean (solid) & max (dashed) KPI vs load multiplier "
        "[ideal: straight line, slope ≈ 1 = strict linear]",
        fontsize=12, y=1.02,
    )
    for ax, (extractor, title, unit) in zip(axes.flat, SCALING_KPIS):
        means, maxes = [], []
        for r in runs:
            m, mx = extractor(r)
            means.append(m)
            maxes.append(mx)
        ax.plot(multipliers, means,
                marker="o", linewidth=1.4, color="#1f77b4", label="mean")
        ax.plot(multipliers, maxes,
                marker="s", linewidth=1.0, color="#d62728",
                linestyle="--", label="max")
        # Annotate each point so you can read raw numbers off the chart.
        for x, y in zip(multipliers, means):
            if y is not None and not (isinstance(y, float) and math.isnan(y)):
                ax.annotate(f"{y:.1f}", (x, y),
                            textcoords="offset points", xytext=(4, 4),
                            fontsize=7, color="#1f77b4")
        for x, y in zip(multipliers, maxes):
            if y is not None and not (isinstance(y, float) and math.isnan(y)):
                ax.annotate(f"{y:.1f}", (x, y),
                            textcoords="offset points", xytext=(4, -10),
                            fontsize=7, color="#d62728")
        # log-log axes when feasible; fall back to linear if values are zero
        # or negative on either series (matplotlib refuses log on those).
        all_vals = [v for v in means + maxes if v is not None
                    and not (isinstance(v, float) and math.isnan(v))]
        if all_vals and min(all_vals) > 0:
            ax.set_xscale("log", base=2)
            ax.set_yscale("log")
            # Show the actual multiplier values, not 2^n labels.
            ax.set_xticks(multipliers)
            ax.get_xaxis().set_major_formatter(
                plt.matplotlib.ticker.ScalarFormatter()
            )
        else:
            # Some KPI series have 0s (typical for forensic_alert_count
            # mean ≈ 0 if the kubescape pipeline is broken). Log-scale
            # x, linear y so at least the multiplier axis stays right.
            ax.set_xscale("log", base=2)
            ax.set_xticks(multipliers)
            ax.get_xaxis().set_major_formatter(
                plt.matplotlib.ticker.ScalarFormatter()
            )
        ax.set_xlabel("load multiplier (×)")
        ax.set_ylabel(unit)
        ax.set_title(title, fontsize=10)
        ax.grid(which="both", alpha=0.3)
        ax.legend(fontsize=8, loc="best")

    fig.savefig(out_path, dpi=120, bbox_inches="tight")
    plt.close(fig)


# ------------------------------------------------------------------ main

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("sweep_dir", type=Path, nargs="?",
                   help="path to perf-sweep-<ts> dir; defaults to latest")
    args = p.parse_args()

    if args.sweep_dir is None:
        candidates = sorted(
            Path("/tmp").glob("perf-sweep-*"),
            key=lambda p: p.stat().st_mtime, reverse=True,
        )
        if not candidates:
            print("no /tmp/perf-sweep-* dirs found", file=sys.stderr)
            return 1
        args.sweep_dir = candidates[0]
        print(f"sweep_dir (auto): {args.sweep_dir}", file=sys.stderr)

    runs = find_runs(args.sweep_dir)
    if not runs:
        print("no finished parquets found in", args.sweep_dir, file=sys.stderr)
        return 0

    for r in runs:
        out = args.sweep_dir / f"{r.name}.png"
        render_run(r, out)
        print(f"  {r.name}.png  (results: {len(r.results)} rows, "
              f"{r.spec_tags[:3] if r.spec_tags else '—'})")

    render_summary(runs, args.sweep_dir / "summary.png")
    print(f"  summary.png  ({len(runs)} runs stacked)")

    render_scorecard(runs, args.sweep_dir / "scorecard.png")
    print(f"  scorecard.png  ({len(runs)} runs in bar chart)")

    render_alert_distribution(runs, args.sweep_dir / "alerts.png")
    print(f"  alerts.png    ({len(runs)} runs, alert ramp vs RUN-relative time)")

    render_scaling(runs, args.sweep_dir / "scaling.png")
    print(f"  scaling.png   ({len(runs)} runs, log-log KPI vs multiplier)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
