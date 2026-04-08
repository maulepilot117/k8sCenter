package k8sdisallowroot

violation[{"msg": msg}] {
  c := input_containers[_]
  not c.securityContext.runAsNonRoot
  msg := sprintf("Container must run as non-root: %v", [c.name])
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
