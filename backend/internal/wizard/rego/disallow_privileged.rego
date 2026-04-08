package k8sdisallowprivileged

violation[{"msg": msg}] {
  c := input_containers[_]
  c.securityContext.privileged
  msg := sprintf("Privileged container is not allowed: %v", [c.name])
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
