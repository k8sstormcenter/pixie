exp="try2"
kubectl logs -n pl -l name=vizier-pem -f >pemlogs$exp.log &
kubectl logs -n pl -l name=vizier-metadata -f >metadatalogs$exp.log &
kubectl logs -n pl -l name=vizier-cloud-connector -f >cloudconnectorlogs$exp.log &