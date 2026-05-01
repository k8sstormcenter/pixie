# Resume — poc/otel-profiles-sbob

## Active branch
`poc/otel-profiles-sbob` off `demo/adaptive-export-config` (fork: `k8sstormcenter/pixie`).

## PoC goal
Feasibility: extend SBOB with abstracted OTel Profiles emitted by Pixie. Target = redis under bobctl test vs bobctl attack (bmlv-demo). Deadline Mon 2026-05-04.

## Docs / fixtures live separately
`~/biz/PoC/OTel/` — `state.yaml` is the authoritative status file. Markdown, mermaid PNGs, slides go there. Pixie tree only carries code.

## Task tracker
9 tasks total. #1 done. Sequence + parallelism in `~/biz/PoC/OTel/state.yaml` under `day_status`. Tracks run in 3 swimlanes Fri–Sun:
- A (research): #2 capture → #3 userspace prototype (Q1 gate) → #4 UDAs
- B (plumbing): #5 Profiles sink ‖ #6 planner builder → #7 PxL script → #8 e2e validation
- C (write-up): #9 rolling draft Fri–Sun, finalize Mon AM

## Staged but uncommitted
- `bazel/repository_locations.bzl` — OTel proto pin bumped v1.3.2 → v1.10.0
  - tarball sha256: `52c85df79badc45da7e6a8735e8090b05a961b0208756187e1492a40db2d1f5f`
  - tag SHA: `ca839c51f706f5d53bfb46f06c3e90c3af3a52c6`
- `bazel/external/opentelemetry.BUILD` — added `profiles_proto`, `profiles_service_proto`, `profiles_service_grpc_cc` targets matching existing trace/metric/log pattern.

Bump rationale: profiles signal proto only present from v1.4.0 onward; v1development path is alpha. Risk: if existing trace/metric/log code references removed/renamed fields, build breaks. Mitigation if it bites: dual-pin with separate `com_github_opentelemetry_proto_dev` repo for profiles only.

## Build verification not yet run
Have not invoked `bazel build` against the bumped pin. First build attempt will surface any breakage.

## Pending decisions awaiting user
1. Commit checkpoints — user has a standing no-commit-without-ask rule. Currently nothing on this branch beyond the base.
2. bmlv-demo cluster state — task #2 needs redis + pixie + bobctl up on local k3s. Status unknown.

## Hard rules in force
- No Co-Authored-By Claude / "Generated with Claude Code" on any commit, patch, or PR body.
- No fabricated metrics in any user-facing artifact — empirical numbers from actual bmlv runs only, otherwise placeholders.
- No upstream PRs — fork strategy. This branch lives on `k8sstormcenter/pixie`.
