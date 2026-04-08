package k8sdisallowprivilegeescalation

violation[{"msg": msg}] {
  c := input_containers[_]
  c.securityContext.allowPrivilegeEscalation
  msg := sprintf("Privilege escalation is not allowed: %v", [c.name])
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
