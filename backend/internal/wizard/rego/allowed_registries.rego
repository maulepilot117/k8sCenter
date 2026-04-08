package k8sallowedregistries

violation[{"msg": msg}] {
  c := input_containers[_]
  not startswith_any(c.image, input.parameters.registries)
  msg := sprintf("Container %v image %v is not from an allowed registry", [c.name, c.image])
}

startswith_any(str, prefixes) {
  startswith(str, prefixes[_])
}

input_containers[c] {
  c := input.review.object.spec.containers[_]
}

input_containers[c] {
  c := input.review.object.spec.initContainers[_]
}

input_containers[c] {
  c := input.review.object.spec.ephemeralContainers[_]
}
