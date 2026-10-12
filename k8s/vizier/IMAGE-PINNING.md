# How images are pinned and updated here

Two different mechanisms live side by side in `k8s/vizier/`, because the
components no longer all come from the same build. Read this before bumping
anything.

## 1. Vizier images (pem, kelvin, query_broker, metadata, cloud_connector,
##    cert_provisioner)

Cut as a set by the `vizier-release` workflow, which fires on an **annotated**
tag matching `release/vizier/**`:

    git tag -a release/vizier/v0.14.19-<name>NN -m "<what changed>"
    git push origin release/vizier/v0.14.19-<name>NN

It builds every vizier image at that one commit, pushes them to GHCR under the
same version string, and publishes a **public GitHub release** carrying
`vizier_yamls.tar` and `vizier_template_yamls.tar`. Those tarballs are the
authority on what a deploy pins: they contain the fully-qualified image
reference for every component. To see what a given release actually shipped:

    gh release download release/vizier/v<version> -p 'vizier_template_yamls.tar' -O v.tar
    tar xf v.tar && grep -rhoE '(ghcr|gcr)\.io/[^"]+:[A-Za-z0-9._-]+' . | sort -u

The tag must be annotated. A lightweight tag still publishes images but fails
the manifest step, so the release is incomplete and CI goes red.

**These images are currently frozen.** The last complete vizier release is
`v0.14.19-aeprod95` (2026-09-07). Nothing has been cut since, so every vizier
image — the PEM included — is pinned at that version until someone cuts a new
`release/vizier/**` tag. That is deliberate, not drift: see §2.

Note the naming, which is easy to get wrong when querying the registry
directly. The repositories are `vizier-pem_image`, `vizier-kelvin_image`,
`vizier-cert_provisioner_image`, but `vizier-query_broker_server_image`,
`vizier-metadata_server_image` and `vizier-cloud_connector_server_image` — the
control-plane three carry `_server`. Probing the wrong name returns a 404 that
looks exactly like a missing image. Prefer the release tarball above over
registry probing.

## 2. adaptive_export

No longer built by `vizier-release`. Its image is produced by its own module and
pinned independently, so bumping AE does **not** require cutting a vizier
release, and cutting a vizier release does **not** move AE.

The pin lives in `adaptive_export/kustomization.yaml` as a kustomize `images:`
entry, so the deployment manifest carries only the logical name
`vizier-adaptive_export_image` and the version is changed in one place:

    cd k8s/vizier/adaptive_export
    kustomize edit set image vizier-adaptive_export_image=<ref>:<version>

This is why the vizier set can sit at `aeprod95` while AE moves on: the
`vizier-adaptive_export_image` that `aeprod95` published is overridden by this
entry and is not what runs.

## 3. dx

`dx/dx-daemon.yaml` hardcodes its image reference inline; there is no kustomize
`images:` entry, so a dx bump means editing the DaemonSet manifest itself. This
is inconsistent with §2 and worth unifying, but changing it moves no deployed
artifact either way.

## Verifying a pin took effect

Rendering is the only proof. The kustomization and the manifest disagree often
enough that reading either alone is unreliable:

    kubectl kustomize k8s/vizier/adaptive_export | grep -m1 'image:'
    kubectl kustomize k8s/vizier/dx              | grep -m1 'image:'

## What `skaffold/skaffold_vizier.yaml` does

It does **not** pin. It declares the vizier images as bazel artifacts and builds
them from the current working tree, which is what you want for local
development and what you do not want when reproducing a released version. To
run a released version, deploy the release tarball from §1 instead.

## Deploy order

adaptive_export before dx: the dx skaffold's before-hook reads the AE secret out
of `pl` and mirrors it into `honey`. Reversed, dx comes up without an API key.
