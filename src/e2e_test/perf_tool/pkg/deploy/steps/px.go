/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package steps

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"px.dev/pixie/src/e2e_test/perf_tool/experimentpb"
	"px.dev/pixie/src/e2e_test/perf_tool/pkg/cluster"
	"px.dev/pixie/src/e2e_test/perf_tool/pkg/pixie"
)

type pxDeployImpl struct {
	pxCtx *pixie.Context
	spec  *experimentpb.PxCLIDeploy
}

var _ DeployStep = &pxDeployImpl{}

// NewPxDeploy creates a new DeployStep that deploys some part of a workload to the cluster using the PX CLI.
func NewPxDeploy(pxCtx *pixie.Context, spec *experimentpb.PxCLIDeploy) DeployStep {
	return &pxDeployImpl{
		pxCtx: pxCtx,
		spec:  spec,
	}
}

// Name returns a printable name for this deploy step.
func (px *pxDeployImpl) Name() string {
	return fmt.Sprintf("px %s", strings.Join(px.spec.Args, " "))
}

// Prepare doesn't do anything for the px deploy step.
func (px *pxDeployImpl) Prepare() error {
	// px deploy / px demo deploy can't do anything without the clusterCtx.
	return nil
}

func hasElem(args []string, arg string) bool {
	for _, a := range args {
		if a == arg {
			return true
		}
	}
	return false
}

// Deploy runs makes sure `px` is auth'd, then runs the `px` command specified in the spec.
func (px *pxDeployImpl) Deploy(clusterCtx *cluster.Context) ([]string, error) {
	if _, err := px.pxCtx.RunPXCmd(clusterCtx, "auth", "login", "--use_api_key=true"); err != nil {
		return nil, err
	}
	args := px.spec.Args
	// Make sure that the px deploy command doesn't prompt for user input.
	if hasElem(args, "deploy") && !hasElem(args, "-y") {
		args = append(args, "-y")
	}
	// Empty Args is used by callers that only want SetClusterID against a
	// pre-existing Pixie deployment (e.g. the SOC_VIZIER_EXISTING path in
	// the sovereign-soc suite). Skip the bare `px` invocation in that case
	// — it would otherwise just print help and clutter the trace log.
	if len(args) > 0 {
		if _, err := px.pxCtx.RunPXCmd(clusterCtx, args...); err != nil {
			return nil, err
		}
	}
	if px.spec.SetClusterID {
		// Direct env override — useful when callers know the UUID up front
		// (rare for fresh deploys; cluster_id is dynamic).
		if override := strings.TrimSpace(os.Getenv("SOC_VIZIER_CLUSTER_ID")); override != "" {
			id, err := uuid.FromString(override)
			if err != nil {
				return nil, fmt.Errorf("SOC_VIZIER_CLUSTER_ID %q is not a valid UUID: %w", override, err)
			}
			log.WithField("source", "env").WithField("cluster_id", id.String()).Info("Binding existing Vizier cluster ID")
			px.pxCtx.SetClusterID(id)
		} else {
			// Vizier registers with Pixie Cloud asynchronously after a fresh
			// skaffold deploy. The cluster_id is assigned by cloud-connector
			// and persisted to the in-cluster secret `pl/pl-cluster-secrets`
			// under key `cluster-id`. Poll for up to 5 minutes, trying TWO
			// sources each round:
			//
			//   a) `px get cluster --id` — works once the px CLI session has
			//      a cluster selected (typically after a px-driven deploy).
			//   b) k8s Secret `pl/pl-cluster-secrets[cluster-id]` — works as
			//      soon as cloud-connector finishes its registration
			//      handshake, even in CI where the px CLI has no implicit
			//      cluster selection.
			//
			// Whichever yields a non-zero UUID first wins. This keeps the
			// cluster_id dynamic (no hardcoded UUIDs in code or config) and
			// works with both the legacy px-deploy path and the
			// skaffold-only path used by this fork (px deploy hits the OLM
			// Subscription bug, so we deliberately avoid it).
			deadline := time.Now().Add(5 * time.Minute)
			attempt := 0
			var id uuid.UUID
			for {
				attempt++

				// (a) Try the px CLI path first; cheap if already populated.
				if clusterIDBytes, err := px.pxCtx.RunPXCmd(clusterCtx, "get", "cluster", "--id"); err == nil {
					if parsed, perr := uuid.FromString(strings.Trim(string(clusterIDBytes), " \n")); perr == nil && parsed != (uuid.UUID{}) {
						id = parsed
						log.WithField("source", "px get cluster --id").WithField("cluster_id", id.String()).WithField("attempts", attempt).Info("Resolved Vizier cluster ID")
						break
					}
				}

				// (b) Fall back to the in-cluster Secret cloud-connector
				// writes after registration.
				if cs := clusterCtx.Clientset(); cs != nil {
					sec, err := cs.CoreV1().Secrets("pl").Get(context.Background(), "pl-cluster-secrets", metav1.GetOptions{})
					if err == nil {
						if raw, ok := sec.Data["cluster-id"]; ok && len(raw) > 0 {
							if parsed, perr := uuid.FromString(strings.TrimSpace(string(raw))); perr == nil && parsed != (uuid.UUID{}) {
								id = parsed
								log.WithField("source", "secret pl/pl-cluster-secrets").WithField("cluster_id", id.String()).WithField("attempts", attempt).Info("Resolved Vizier cluster ID")
								break
							}
						}
					}
				}

				if time.Now().After(deadline) {
					return nil, fmt.Errorf("cluster_id not resolvable after %d attempts over 5m; neither `px get cluster --id` nor the pl/pl-cluster-secrets Secret yielded a non-zero UUID. cloud-connector registration likely stuck — check `kubectl -n pl logs deploy/vizier-cloud-connector`", attempt)
				}
				if attempt%6 == 1 {
					log.WithField("attempt", attempt).Info("Waiting for cloud-connector to register the cluster (zero UUID so far)")
				}
				time.Sleep(10 * time.Second)
			}
			px.pxCtx.SetClusterID(id)
		}
	}
	// We don't know what namespaces a given `px` command will create, so we rely on the user to set them in the spec.
	return px.spec.Namespaces, nil
}
