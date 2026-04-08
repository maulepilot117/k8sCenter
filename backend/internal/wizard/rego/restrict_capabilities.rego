package k8srestrictcapabilities

violation[{"msg": msg}] {
  c := input_containers[_]
  not has_drop_all(c)
  msg := sprintf("Container must drop ALL capabilities: %v", [c.name])
}

violation[{"msg": msg}] {
  c := input_containers[_]
  cap := c.securityContext.capabilities.add[_]
  not allowed_cap(cap)
  msg := sprintf("Capability %v is not allowed for container %v", [cap, c.name])
}

has_drop_all(c) {
  c.securityContext.capabilities.drop[_] == "ALL"
}

allowed_cap(cap) {
  input.parameters.allowedCapabilities[_] == cap
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
