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

mkdir -p "${repo_root}/scripts/constanze/$heap_profile_dir"

pxl_heap_output_file="${heap_profile_dir}/tmp.json"
nodes=()

px run -o json  -f "${repo_root}/src/pxl_scripts/px/collect_heap_dumps.pxl"  > "${repo_root}/scripts/constanze/$pxl_heap_output_file"
while IFS= read -r line; do
    hostname=$(echo "$line" | jq -r '.hostname')
    if [[ -z "$hostname" || "$hostname" == "null" ]]; then
        continue
    else
        echo "$hostname"
        nodes+=("$hostname")
    fi
done < "${repo_root}/scripts/constanze/$pxl_heap_output_file"



#lets practise with only tetragon since it seems the primary issue

## Schema construction
# Discussion with Dom: use .json extension to ensure the mappings for time_ and string exist and
#"If you use a JSON file, it will work properly. The thing is that the query broker can't handle receiving an empty schema."
#Question to self: what happens if this Max is hit:
# constexpr int kMaxLines = 1000;
create_cmd=$(cat << EOF
 echo "{"time_": 1750851088000000000, "node_name": "init", "type": "process_exec", "payload": {"init": true}}" > /tmp/tetragon.json;
EOF
)

for node in "${nodes[@]}"; do
  gcloud compute ssh --zone "$zone" --command="$create_cmd" "$node" "${@:3}" --tunnel-through-iap --project "$project"
done





