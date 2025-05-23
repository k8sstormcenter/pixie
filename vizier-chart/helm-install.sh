
#First you install pixie via px deploy, then run this script
kubectl get secret -n pl -o json | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl --overwrite secret {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get svc  -n pl -o json   | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl --overwrite svc {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get sa -n pl -o json     | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl sa --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get cm -n pl -o json     | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl cm --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get pvc -n pl -o json    | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl pvc --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get clusterrole -n pl -o json    | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl clusterrole --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get clusterrolebinding -n pl -o json    | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl clusterrolebinding --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get role -n pl -o json    | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl role --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get rolebinding -n pl -o json    | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl rolebinding --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get ds -n pl -o json     | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl ds --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get deployment -n pl -o json     | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl deployment --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 
kubectl get statefulset -n pl -o json     | jq -r '.items[] | select(.metadata.annotations | has("helm.sh/release-name") | not) | .metadata.name' | xargs -I {} kubectl annotate -n pl statefulset --overwrite {} meta.helm.sh/release-name=pixie meta.helm.sh/release-namespace=pl 



kubectl get sa -n pl -o json     | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl sa     --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get svc -n pl -o json    | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl svc    --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get secret -n pl -o json | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl secret --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get cm -n pl -o json     | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl cm     --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get pvc -n pl -o json    | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl pvc    --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get clusterrole -n pl -o json    | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl clusterrole    --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get clusterrolebinding -n pl -o json    | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl clusterrolebinding    --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get role -n pl -o json    | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl role    --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get rolebinding -n pl -o json    | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl rolebinding    --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get ds -n pl -o json     | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl ds     --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get deployment -n pl -o json     | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl deployment     --overwrite {} app.kubernetes.io/managed-by=Helm
kubectl get statefulset -n pl -o json     | jq '.items[] | .metadata | select(.labels."app.kubernetes.io/managed-by=Helm" | not) | .name' | xargs -I {} kubectl label -n pl statefulset     --overwrite {} app.kubernetes.io/managed-by=Helm

keyid=f60a3c55-91fe-4dbc-b984-bf6ed4fdc323  
key=$(px api-key get $keyid)
if [ ! -f myvalues.yaml ]; then
  echo "Error: myvalues.yaml not found"
  exit 1
fi

helm upgrade --install pixie . --namespace pl --create-namespace --values myvalues.yaml


