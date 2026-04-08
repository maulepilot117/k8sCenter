package k8sdisallowlatesttag

violation[{"msg": msg}] {
  c := input_containers[_]
  endswith(c.image, ":latest")
  msg := sprintf("Container %v uses ':latest' tag: %v", [c.name, c.image])
}

violation[{"msg": msg}] {
  c := input_containers[_]
  not contains(c.image, ":")
  msg := sprintf("Container %v has no explicit tag: %v", [c.name, c.image])
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
