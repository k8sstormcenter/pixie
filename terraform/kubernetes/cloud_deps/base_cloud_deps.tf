data "kustomization_build" "elastic" {
  # TODO(ddelnano): This will need to be updated for the terraform Azure pipeline
  path = "../../../k8s/cloud_deps/base/elastic/operator"
}

# first loop through resources in ids_prio[0]
resource "kustomization_resource" "elastic_p0" {
  for_each = data.kustomization_build.elastic.ids_prio[0]

  manifest = (
    contains(["_/Secret"], regex("(?P<group_kind>.*/.*)/.*/.*", each.value)["group_kind"])
    ? sensitive(data.kustomization_build.elastic.manifests[each.value])
    : data.kustomization_build.elastic.manifests[each.value]
  )
}

# then loop through resources in ids_prio[1]
# and set an explicit depends_on on kustomization_resource.elastic_p0
# wait 2 minutes for any deployment or daemonset to become ready
resource "kustomization_resource" "elastic_p1" {
  for_each = data.kustomization_build.elastic.ids_prio[1]

  manifest = (
    contains(["_/Secret"], regex("(?P<group_kind>.*/.*)/.*/.*", each.value)["group_kind"])
    ? sensitive(data.kustomization_build.elastic.manifests[each.value])
    : data.kustomization_build.elastic.manifests[each.value]
  )
  wait = true
  timeouts {
    create = "2m"
    update = "2m"
  }

  depends_on = [kustomization_resource.elastic_p0]
}

# finally, loop through resources in ids_prio[2]
# and set an explicit depends_on on kustomization_resource.elastic_p1
resource "kustomization_resource" "p2" {
  for_each = data.kustomization_build.elastic.ids_prio[2]

  manifest = (
    contains(["_/Secret"], regex("(?P<group_kind>.*/.*)/.*/.*", each.value)["group_kind"])
    ? sensitive(data.kustomization_build.elastic.manifests[each.value])
    : data.kustomization_build.elastic.manifests[each.value]
  )

  depends_on = [kustomization_resource.elastic_p1]
}
