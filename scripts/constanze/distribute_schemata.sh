# License Apache 2.0
# Author: Constanze Roedig
# Intended Usage: debug - no merge - not maintained

# This script is for me to understand how exactly https://github.com/ddelnano/pixie/blob/310eb744bfc1423e67c985ba840410b78a76b9bf/src/stirling/source_connectors/file_source/file_source_connector.cc works

usage() {
  echo "This script distributes a schema-init-file to all k8s nodes"
  echo ""
  echo "Usage: $0 <source_type> "
  exit 1
}

source_type="$1" # one of tetragon kubescape
project="adls-152q5v4ovh2urxs17pnkwnlwl"
zone="europe-west1-b" 
heap_profile_dir="tmppx"

script_dir=$(dirname "$(realpath "$0")")

repo_root=$(git rev-parse --show-toplevel)

if [ -z "$heap_profile_dir" ] || [ -z "$source_type" ] ; then
  usage
fi
echo $repo_root

# mkdir -p "${repo_root}/scripts/constanze/$heap_profile_dir"

# pxl_heap_output_file="${heap_profile_dir}/tmp.json"
# nodes=()

# px run -o json  -f "${repo_root}/src/pxl_scripts/px/collect_heap_dumps.pxl"  > "${repo_root}/scripts/constanze/$pxl_heap_output_file"
# while IFS= read -r line; do
#     hostname=$(echo "$line" | jq -r '.hostname')
#     if [[ -z "$hostname" || "$hostname" == "null" ]]; then
#         continue
#     else
#         echo "$hostname"
#         nodes+=("$hostname")
#     fi
# done < "${repo_root}/scripts/constanze/$pxl_heap_output_file"
nodes=(
  "gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28"
  "gke-k8s-caas-0009-be-default-node-poo-08c84be6-bnif"
)

#lets practise with only tetragon since it seems the primary issue

## Schema construction
# Discussion with Dom: use .json extension to ensure the mappings for time_ and string exist and
#"If you use a JSON file, it will work properly. The thing is that the query broker can't handle receiving an empty schema."
#Question to self: what happens if this Max is hit:
# constexpr int kMaxLines = 1000; Answer: pagination
#THIS CREATES A CRASH IN THE PEMs
# create_cmd=$(cat << EOF
#  echo "{\"time_\": \"1750851088000000000\", \"node_name\": \"init\", \"type\": \"process_kprobe\", \"payload\": {\"init\": true}}" > /tmp/tetragon4.json;
# EOF
# )

create_cmd=$(cat << EOF
 echo "{\"time\": \"2025-04-23T10:22:32.068683462Z\", \"node_name\": \"init\", \"type\": \"process_kprobe\", \"payload\": {\"init\": true}}" > /tmp/tetragon2.json;
EOF
)




for node in "${nodes[@]}"; do
  gcloud compute ssh  --zone "$zone" --command="$create_cmd" "$node" "${@:3}" --tunnel-through-iap  --project $project
done

#rm -r "${repo_root}/scripts/constanze/$heap_profile_dir"


# import px
# import pxlog
# import pxtrace

# glob = "/tmp/tetragon.json"
# table = "tetragon.json"
# pxlog.FileSource(glob, table, "4h")

# df = px.DataFrame(table)

# px.display(df)

# kubectl rollout restart -n pl statefulset vizier-metadata
# kubectl delete -n pl pvc  metadata-pv-claim
# pvname=$(kubectl get pv | cut -d " " -f 1 |grep pvc)
# kubectl delete pv $pvname

#use branch 177 - do not convert timestamps to unix in vector - only use kprobes in tetragon

#E20250706 16:19:47.393940 1390044 file_source_connector.cc:176] Failed to parse JSON: 5a540dc4059d075068794641e3ckpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.860498389Z","type":"process_kprobe"} The document root must not be followed by other values.
#in the /tmp/tetragon.json file the full line (line number 2, after schema_init) reads
# {"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"2ed665a540dc4059d075068794641e3ckpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.860498389Z","type":"process_kprobe"}


# ls -alh /tmp/tetragon2.json 
# -rw-r--r-- 1 croedig croedig 17M Jul  6 16:19 /tmp/tetragon2.json

#  wc -l /tmp/tetragon2.json 
# 6055 /tmp/tetragon2.json


#experiment repeat
constanze@gke-k8s-caas-0009-be-default-node-poo-08c84be6-bnif ~ $ cat /tmp/tetragon2.json 
{"time": "2025-04-23T10:22:32.068683462Z", "node_name": "init", "type": "process_kprobe", "payload": {"init": true}}
constanze@gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28 ~ $ cat /tmp/tetragon2.json 
{"time": "2025-04-23T10:22:32.068683462Z", "node_name": "init", "type": "process_kprobe", "payload": {"init": true}}


# at t=01:05 AM # cluster is in good state

define files_source_init.pxl via UI
looking good- pems at 1.1Gi and 657Mib

stable for 5 min


# at t=01:09 AM # cluster is in good state
import px

df = px.DataFrame(table="tetragon2.json")

px.display(df)
via UI




# copy first 10 lines ONLY on the user-pool, not on the default-pool
#constanze@gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28 ~ $ head -n 10 /tmp/tetragon.json
{"time": "2025-04-23T10:22:32.068683462Z", "node_name": "init", "type": "process_kprobe", "payload": {"init": true}}
{"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"2ed665a540dc4059d075068794641e3ckpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.860498389Z","type":"process_kprobe"}
{"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"b210e2c38bca24f4984382bcac92a21akpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.860748354Z","type":"process_kprobe"}
{"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"09aee1cb800ede5b5528738dfdad7407kpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.860966640Z","type":"process_kprobe"}
{"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"b32c1052f7c165bc8ece3613f5c2179ckpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.861114609Z","type":"process_kprobe"}
{"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"c031e40c78e9e599a7fa5072cccc7184kpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.861174143Z","type":"process_kprobe"}
{"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"d90ecf69b5ee89a1bdd56fd610d6decakpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.861228049Z","type":"process_kprobe"}
{"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"dfae86bf5e405d4d831b98fd3e849038kpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.861395729Z","type":"process_kprobe"}
{"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"471da1af5a2427fc597a9acbb84fa868kpro","function_name":"__x64_sys_mount","parent":{"arguments":"-c \"mount -t proc proc /proc && sleep infinity\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"bc01edd353e7852c9a4b7155cfae35c","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc5NDEzMjYxMzI6MTQyMDYyMA==","flags":"execve rootcwd clone inInitTree","in_init_tree":true,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTk3ODMzMzA1OTE6MTQxOTI4Ng==","pid":1420620,"pod":{"container":{"id":"containerd://bc01edd353e7852c9a4b7155cfae35c77b08671ac571ca47ad69037ac8e02220","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"priv-mount-pod","pid":1,"start_time":"2025-07-06T16:17:06Z"},"name":"priv-mount-pod","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"priv-mount-pod","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:06.003318804Z","tid":1420620,"uid":0},"policy_name":"detect-ce-priv-mount","process":{"arguments":"-t proc proc /proc","auid":4294967295,"binary":"/usr/bin/mount","cwd":"/","docker":"bc01edd353e7852c9a4b7155cfae35c","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc5NTQyNzI3MDI6MTQyMDY0NA==","flags":"execve rootcwd clone inInitTree","in_init_tree":true,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc5NDEzMjYxMzI6MTQyMDYyMA==","pid":1420644,"pod":{"container":{"id":"containerd://bc01edd353e7852c9a4b7155cfae35c77b08671ac571ca47ad69037ac8e02220","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"priv-mount-pod","pid":7,"start_time":"2025-07-06T16:17:06Z"},"name":"priv-mount-pod","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"priv-mount-pod","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:06.016264488Z","tid":1420644,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:06.020391190Z","type":"process_kprobe"}
{"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"9bd34c6030fa8ec634b8cf3caba04e4dkpro","function_name":"__x64_sys_execve","parent":{"arguments":"-c \"gdb && sleep infinity\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"6f1d3d83de65e86bb14488d043b2af5","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3OTU1Mjc1MDI4NTY6MTQyMDgzMg==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NjAyOTE1NTA4NzA6MTQxOTMzNw==","pid":1420832,"pod":{"container":{"id":"containerd://6f1d3d83de65e86bb14488d043b2af5c283b9169eb70c8328f0038f17564ec28","image":{"id":"docker.io/andyneff/hello-world-gdb@sha256:e9ac79efe4818bacf7c4c6114f3f4f51839b5d1a53bfc98ae5bc4125d0020b4a","name":"docker.io/andyneff/hello-world-gdb:latest"},"name":"kh-calibration-ptrace","start_time":"2025-07-06T16:17:23Z"},"name":"kh-calibration-ptrace","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ptrace","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:23.589495253Z","tid":1420832,"uid":0},"policy_name":"detect-ce-sys-ptrace","process":{"arguments":"-c \"gdb && sleep infinity\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"6f1d3d83de65e86bb14488d043b2af5","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3OTU1MzI4MzI1Mzk6MTQyMDg1Mg==","flags":"execve","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3OTU1Mjc1MDI4NTY6MTQyMDgzMg==","pid":1420852,"pod":{"container":{"id":"containerd://6f1d3d83de65e86bb14488d043b2af5c283b9169eb70c8328f0038f17564ec28","image":{"id":"docker.io/andyneff/hello-world-gdb@sha256:e9ac79efe4818bacf7c4c6114f3f4f51839b5d1a53bfc98ae5bc4125d0020b4a","name":"docker.io/andyneff/hello-world-gdb:latest"},"name":"kh-calibration-ptrace","start_time":"2025-07-06T16:17:23Z"},"name":"kh-calibration-ptrace","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ptrace","workload_kind":"Pod"},"refcnt":1,"start_time":"2025-07-06T16:17:23.594824593Z","tid":1420852,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:23.596136571Z","type":"process_kprobe"}

# at t=1:15 AM 
head -n 10 tetragon_userpool.json >/tmp/tetragon2.json 

everything fine, RAM stable, logs all fine

# at t=1:20 AM 
cp tetragon_userpool.json /tmp/tetragon2.json 
constanze@gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28 ~ $ wc -l /tmp/tetragon2.json 
6061 /tmp/tetragon2.json

everything fine, RAM stable, logs all fine - no sign of the JSON deserialisation error - its the identical file as before // there are a few more lines in the end, given the error was in line1 , it mayb have been a newline...maybe

# at t=1:25 AM 

query table in UI


all stable, Ram still at 1.1GiB and 651MiB 
only the query broker was hit by that retrieval and went from 48MiB to 57MiB. and then back to 49MiB

clear case of Layer8 error

massive facepalm, Happy Sunday... and Nighty Night