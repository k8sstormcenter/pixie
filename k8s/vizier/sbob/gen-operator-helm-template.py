#!/usr/bin/env python3
"""Regenerate the operator helm chart's SBoB template from the canonical profiles.

k8s/vizier/sbob/profiles/ is the single source for every shipped ContainerProfile.
The kustomize overlays reference those files directly; the helm chart cannot (helm
only reads files under its own chart directory), so the three operator/OLM profiles
are rendered into a template by this script. Run it after editing a profile:

    python3 k8s/vizier/sbob/gen-operator-helm-template.py

and commit the result. CI compares the two; a drift means the script was not run.
"""
import pathlib, sys

ROOT = pathlib.Path(__file__).resolve().parents[3]
SRC = ROOT / "k8s/vizier/sbob/profiles"
OUT = ROOT / "k8s/operator/helm/templates/05_sbob.yaml"
NAMES = ["cp-catalog-operator.yaml", "cp-olm-operator.yaml", "cp-vizier-operator.yaml"]

parts = ["{{- if .Values.sbob }}"]
for n in NAMES:
    body = (SRC / n).read_text().rstrip("\n")
    if body.startswith("---\n"):
        body = body[4:]
    parts.append("---")
    parts.append(body)
parts.append("{{- end }}")
text = "\n".join(parts) + "\n"

if "--check" in sys.argv:
    cur = OUT.read_text() if OUT.exists() else ""
    if cur != text:
        sys.exit(f"{OUT} is stale: run python3 {pathlib.Path(__file__).relative_to(ROOT)}")
    print("05_sbob.yaml matches the canonical profiles")
else:
    OUT.write_text(text)
    print(f"wrote {OUT.relative_to(ROOT)} from {len(NAMES)} canonical profiles")
