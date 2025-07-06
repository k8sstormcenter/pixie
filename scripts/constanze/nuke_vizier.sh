kubectl rollout restart -n pl statefulset vizier-metadata
kubectl delete -n pl pvc  metadata-pv-claim
pvname=$(kubectl get pv | cut -d " " -f 1 |grep pvc)
kubectl delete pv $pvname