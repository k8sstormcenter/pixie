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
#in the /tmp/tetragon.json file the full line reads
# {"node_name":"gke-k8s-caas-0009-beta-user-pool-5a7e10f2-vx28","payload":{"action":"KPROBE_ACTION_POST","dedup":"2ed665a540dc4059d075068794641e3ckpro","function_name":"__x64_sys_setns","parent":{"arguments":"-c \"nsenter -t 1 -a /bin/bash && sleep infinity;\"","auid":4294967295,"binary":"/bin/sh","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3NTg3ODQwNTk2NjE6MTQxOTEzNw==","pid":1420598,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.849692112Z","tid":1420598,"uid":0},"policy_name":"detect-ce-nsenter","process":{"arguments":"-t 1 -a /bin/bash","auid":4294967295,"binary":"/usr/bin/nsenter","cwd":"/","docker":"e7caa949ba34c37a13a8cb30d3931e2","exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3OTI3Mjk4MTA6MTQyMDYzNA==","flags":"execve rootcwd clone","in_init_tree":false,"parent_exec_id":"Z2tlLWs4cy1jYWFzLTAwMDktYmV0YS11c2VyLXBvb2wtNWE3ZTEwZjItdngyODoxMjg3Nzc3ODc2OTk5MTI6MTQyMDU5OA==","pid":1420634,"pod":{"container":{"id":"containerd://e7caa949ba34c37a13a8cb30d3931e294c61919e4efe7e5c27df009149c0ca02","image":{"id":"docker.io/library/ubuntu@sha256:440dcf6a5640b2ae5c77724e68787a906afb8ddee98bf86db94eea8528c2c076","name":"docker.io/library/ubuntu:latest"},"name":"kh-calibration-ce-1-pod","start_time":"2025-07-06T16:17:05Z"},"name":"kh-calibration-ce-1","namespace":"default","pod_labels":{"app":"kubehound-edge-test"},"workload":"kh-calibration-ce-1","workload_kind":"Pod"},"start_time":"2025-07-06T16:17:05.854721770Z","tid":1420634,"uid":0},"return_action":"KPROBE_ACTION_POST"},"time":"2025-07-06T16:17:05.860498389Z","type":"process_kprobe"}


# ls -alh /tmp/tetragon2.json 
# -rw-r--r-- 1 croedig croedig 17M Jul  6 16:19 /tmp/tetragon2.json

#  wc -l /tmp/tetragon2.json 
# 6055 /tmp/tetragon2.json