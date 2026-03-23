package wizard

import (
	"fmt"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

// ScheduledSnapshotInput represents the wizard form data for creating a
// CronJob-based scheduled VolumeSnapshot workflow.
type ScheduledSnapshotInput struct {
	Name                    string `json:"name"`
	Namespace               string `json:"namespace"`
	SourcePVC               string `json:"sourcePVC"`
	VolumeSnapshotClassName string `json:"volumeSnapshotClassName"`
	Schedule                string `json:"schedule"`
	RetentionCount          int    `json:"retentionCount"`
}

// Validate checks the ScheduledSnapshotInput and returns field-level errors.
func (s *ScheduledSnapshotInput) Validate() []FieldError {
	var errs []FieldError

	if !dnsLabelRegex.MatchString(s.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	if s.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(s.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	if s.SourcePVC == "" {
		errs = append(errs, FieldError{Field: "sourcePVC", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(s.SourcePVC) {
		errs = append(errs, FieldError{Field: "sourcePVC", Message: "must be a valid DNS label"})
	}

	if s.VolumeSnapshotClassName == "" {
		errs = append(errs, FieldError{Field: "volumeSnapshotClassName", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(s.VolumeSnapshotClassName) {
		errs = append(errs, FieldError{Field: "volumeSnapshotClassName", Message: "must be a valid DNS label"})
	}

	if strings.TrimSpace(s.Schedule) == "" {
		errs = append(errs, FieldError{Field: "schedule", Message: "is required"})
	}

	if s.RetentionCount < 1 || s.RetentionCount > 100 {
		errs = append(errs, FieldError{Field: "retentionCount", Message: "must be between 1 and 100"})
	}

	return errs
}

// ToMultiDocYAML generates a multi-document YAML string containing 4 resources:
// ServiceAccount, Role, RoleBinding, and CronJob for scheduled VolumeSnapshot creation.
func (s *ScheduledSnapshotInput) ToMultiDocYAML() (string, error) {
	saName := s.Name + "-snapshotter"

	// 1. ServiceAccount
	sa := map[string]any{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata": map[string]any{
			"name":      saName,
			"namespace": s.Namespace,
			"labels": map[string]any{
				"k8scenter.io/managed-by": "scheduled-snapshot",
				"k8scenter.io/schedule":   s.Name,
			},
		},
	}

	// 2. Role
	role := map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "Role",
		"metadata": map[string]any{
			"name":      saName,
			"namespace": s.Namespace,
			"labels": map[string]any{
				"k8scenter.io/managed-by": "scheduled-snapshot",
				"k8scenter.io/schedule":   s.Name,
			},
		},
		"rules": []map[string]any{
			{
				"apiGroups": []string{"snapshot.storage.k8s.io"},
				"resources": []string{"volumesnapshots"},
				"verbs":     []string{"create", "get", "list", "delete"},
			},
			{
				"apiGroups": []string{""},
				"resources": []string{"persistentvolumeclaims"},
				"verbs":     []string{"get", "list"},
			},
		},
	}

	// 3. RoleBinding
	rb := map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "RoleBinding",
		"metadata": map[string]any{
			"name":      saName,
			"namespace": s.Namespace,
			"labels": map[string]any{
				"k8scenter.io/managed-by": "scheduled-snapshot",
				"k8scenter.io/schedule":   s.Name,
			},
		},
		"roleRef": map[string]any{
			"apiGroup": "rbac.authorization.k8s.io",
			"kind":     "Role",
			"name":     saName,
		},
		"subjects": []map[string]any{
			{
				"kind":      "ServiceAccount",
				"name":      saName,
				"namespace": s.Namespace,
			},
		},
	}

	// 4. CronJob with snapshot creation + retention cleanup script
	script := fmt.Sprintf(`set -e
SNAP_NAME="%s-$(date +%%Y%%m%%d-%%H%%M%%S)"
cat <<SNAPEOF | kubectl apply -f -
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: ${SNAP_NAME}
  namespace: %s
  labels:
    k8scenter.io/scheduled: "true"
    k8scenter.io/source-pvc: "%s"
    k8scenter.io/schedule: "%s"
spec:
  volumeSnapshotClassName: %s
  source:
    persistentVolumeClaimName: %s
SNAPEOF
echo "Created snapshot ${SNAP_NAME}"
# Retention: delete oldest snapshots beyond retention count
SNAPSHOTS=$(kubectl get volumesnapshots -n %s -l k8scenter.io/schedule=%s --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[*].metadata.name}')
COUNT=0
TOTAL=0
for s in ${SNAPSHOTS}; do
  TOTAL=$((TOTAL + 1))
done
for s in ${SNAPSHOTS}; do
  COUNT=$((COUNT + 1))
  if [ $((TOTAL - COUNT)) -ge %d ]; then
    echo "Deleting old snapshot ${s}"
    kubectl delete volumesnapshot -n %s "${s}"
  fi
done`,
		s.SourcePVC,
		s.Namespace,
		s.SourcePVC,
		s.Name,
		s.VolumeSnapshotClassName,
		s.SourcePVC,
		s.Namespace,
		s.Name,
		s.RetentionCount,
		s.Namespace,
	)

	cronjob := map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "CronJob",
		"metadata": map[string]any{
			"name":      s.Name,
			"namespace": s.Namespace,
			"labels": map[string]any{
				"k8scenter.io/managed-by": "scheduled-snapshot",
				"k8scenter.io/schedule":   s.Name,
				"k8scenter.io/source-pvc": s.SourcePVC,
			},
		},
		"spec": map[string]any{
			"schedule":          s.Schedule,
			"concurrencyPolicy": "Forbid",
			"jobTemplate": map[string]any{
				"spec": map[string]any{
					"template": map[string]any{
						"spec": map[string]any{
							"serviceAccountName": saName,
							"restartPolicy":      "OnFailure",
							"containers": []map[string]any{
								{
									"name":    "snapshotter",
									"image":   "bitnami/kubectl:1.31",
									"command": []string{"/bin/sh", "-c", script},
								},
							},
						},
					},
				},
			},
		},
	}

	// Marshal each resource and join with ---
	docs := make([]string, 0, 4)
	for _, obj := range []map[string]any{sa, role, rb, cronjob} {
		yamlBytes, err := sigsyaml.Marshal(obj)
		if err != nil {
			return "", fmt.Errorf("failed to marshal resource to YAML: %w", err)
		}
		docs = append(docs, string(yamlBytes))
	}

	return strings.Join(docs, "---\n"), nil
}
