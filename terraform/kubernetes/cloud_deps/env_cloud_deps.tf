data "kustomization_build" "env_deps" {
  # TODO(ddelnano): This will need to be updated for the terraform Azure pipeline 
  path = "../../../k8s/cloud_deps/public"
}

# first loop through resources in ids_prio[0]
resource "kustomization_resource" "env_deps_p0" {
  for_each = data.kustomization_build.env_deps.ids_prio[0]

  manifest = (
    contains(["_/Secret"], regex("(?P<group_kind>.*/.*)/.*/.*", each.value)["group_kind"])
    ? sensitive(data.kustomization_build.env_deps.manifests[each.value])
    : data.kustomization_build.env_deps.manifests[each.value]
  )
}

# then loop through resources in ids_prio[1]
# and set an explicit depends_on on kustomization_resource.env_deps_p0
# wait 2 minutes for any deployment or daemonset to become ready
resource "kustomization_resource" "env_deps_p1" {
  for_each = data.kustomization_build.env_deps.ids_prio[1]

  manifest = (
    contains(["_/Secret"], regex("(?P<group_kind>.*/.*)/.*/.*", each.value)["group_kind"])
    ? sensitive(data.kustomization_build.env_deps.manifests[each.value])
    : data.kustomization_build.env_deps.manifests[each.value]
  )
  wait = true
  timeouts {
    create = "2m"
    update = "2m"
  }

  depends_on = [kustomization_resource.env_deps_p0]
}

# finally, loop through resources in ids_prio[2]
# and set an explicit depends_on on kustomization_resource.env_deps_p1
resource "kustomization_resource" "env_deps_p2" {
  for_each = data.kustomization_build.env_deps.ids_prio[2]

  manifest = (
    contains(["_/Secret"], regex("(?P<group_kind>.*/.*)/.*/.*", each.value)["group_kind"])
    ? sensitive(data.kustomization_build.env_deps.manifests[each.value])
    : data.kustomization_build.env_deps.manifests[each.value]
  )

  depends_on = [kustomization_resource.env_deps_p1]
}
