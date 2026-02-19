resource "helm_release" "cert_manager" {
  name             = "cert-manager"
  repository       = "https://charts.jetstack.io"
  namespace        = "cert-manager"
  chart            = "cert-manager"
  version          = "v1.19.3"
  create_namespace = true

  set = [
    {
      name  = "crds.enabled"
      value = "true"
    },
  ]
}
