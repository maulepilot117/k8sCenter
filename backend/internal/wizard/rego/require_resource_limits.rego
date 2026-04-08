package k8srequireresourcelimits

violation[{"msg": msg}] {
  c := input.review.object.spec.containers[_]
  input.parameters.requireCpu
  not c.resources.limits.cpu
  msg := sprintf("Container %v has no CPU limit", [c.name])
}

violation[{"msg": msg}] {
  c := input.review.object.spec.containers[_]
  input.parameters.requireMemory
  not c.resources.limits.memory
  msg := sprintf("Container %v has no memory limit", [c.name])
}
