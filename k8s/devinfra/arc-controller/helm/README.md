## install instructions

```
kubectl apply -f https://openebs.github.io/charts/openebs-operator-lite.yaml

kubectl apply -f https://openebs.github.io/charts/openebs-lite-sc.yaml

helm install arc --namespace arc-systems --create-namespace oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller

kubectl create ns arc-runners

kubectl create secret generic pre-defined-secret --namespace=arc-runners --from-literal=github_app_id=2794533 --from-literal=github_app_installation_id=107954104 --from-literal=github_app_private_key="$(cat github_app_private_key.pem)"

helm install oracle-vm-16cpu-64gb-x86-64 oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set --namespace arc-runners --set githubConfigUrl="https://github.com/k8sstormcenter/pixie" --set githubConfigSecret=pre-defined-secret --set containerMode.type="dind" -f values.yaml

$ sudo sysctl vm.mmap_rnd_bits=28
```

## Multi-label runner (TODO: roll this out)

The upstream pixie workflows reference both `oracle-vm-16cpu-64gb-x86-64` and `oracle-16cpu-64gb-x86-64` in `runs-on`. Rather than running two scale sets (or patching the workflows on every fork sync), register a single scale set that advertises both labels via `scaleSetLabels` (requires `gha-runner-scale-set` chart v0.14.0+):

```
helm upgrade oracle-vm-16cpu-64gb-x86-64 \
  oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set \
  --namespace arc-runners \
  --reuse-values \
  --set scaleSetLabels[0]=oracle-vm-16cpu-64gb-x86-64 \
  --set scaleSetLabels[1]=oracle-16cpu-64gb-x86-64
```

Verify: `kubectl get autoscalingrunnerset -n arc-runners -o jsonpath='{.items[*].spec.runnerScaleSetLabels}'`

apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: runner-hostpath
  annotations:
    openebs.io/cas-type: local
    cas.openebs.io/config: |
       - name: BasePath
         value: "/mnt/runner-storage"
provisioner: openebs.io/local
volumeBindingMode: WaitForFirstConsumer
reclaimPolicy: Delete
