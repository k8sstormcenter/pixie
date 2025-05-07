sed -i '' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
00_secrets.yaml

sed -i '' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
01_nats.yaml

sed -i '' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
02_etcd.yaml

sed -i '' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
03_vizier_etcd.yaml


sed -i '' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
04_vizier_persistent.yaml

sed -i '' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
05_vizier_etcd_ap.yaml

sed -i '' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io-pixie-oss-pixie-prod-vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
06_vizier_persistent_ap.yaml




sed -i '' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
00_secrets.yaml

sed -i '' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
01_nats.yaml

sed -i '' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
02_etcd.yaml

sed -i '' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
03_vizier_etcd.yaml


sed -i '' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
04_vizier_persistent.yaml

sed -i '' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
05_vizier_etcd_ap.yaml

sed -i '' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-pem_image:0.14.15|ghcr.io/k8sstormcenter/vizier-pem_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-kelvin_image:0.14.15|ghcr.io/k8sstormcenter/vizier-kelvin_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-metadata_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-metadata_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-query_broker_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-query_broker_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cloud_connector_server_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cloud_connector_server_image:{{ .Values.imageTag }}|g' \
-e 's|gcr.io/pixie-oss/pixie-prod/vizier-cert_provisioner_image:0.14.15|ghcr.io/k8sstormcenter/vizier-cert_provisioner_image:{{ .Values.imageTag }}|g' \
06_vizier_persistent_ap.yaml