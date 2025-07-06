kubectl rollout restart -n pl statefulset vizier-metadata
kubectl delete -n pl pvc  metadata-pv-claim
pvname=$(kubectl get pv | cut -d " " -f 1 |grep pvc)
kubectl delete pv $pvname

#better not to use this script - I m not sure what cause one of the GKE nodes to become unschedulable but I had to delete the node and wait for a new one