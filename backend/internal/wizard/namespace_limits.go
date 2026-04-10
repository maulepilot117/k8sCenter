package wizard

import (
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/kubecenter/kubecenter/internal/limits"
)

// ResourcePair holds CPU and memory values for limit range configuration.
type ResourcePair struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// QuotaConfig holds ResourceQuota configuration.
type QuotaConfig struct {
	CPUHard    string `json:"cpuHard"`
	MemoryHard string `json:"memoryHard"`
	PodsHard   int    `json:"podsHard"`

	// Advanced (optional)
	SecretsHard       int    `json:"secretsHard,omitempty"`
	ConfigMapsHard    int    `json:"configMapsHard,omitempty"`
	ServicesHard      int    `json:"servicesHard,omitempty"`
	PVCsHard          int    `json:"pvcsHard,omitempty"`
	GPUHard           string `json:"gpuHard,omitempty"`
	WarnThreshold     int    `json:"warnThreshold,omitempty"`
	CriticalThreshold int    `json:"criticalThreshold,omitempty"`
}

// LimitConfig holds LimitRange configuration.
type LimitConfig struct {
	// Container limits (required)
	ContainerDefault        ResourcePair `json:"containerDefault"`
	ContainerDefaultRequest ResourcePair `json:"containerDefaultRequest"`
	ContainerMax            ResourcePair `json:"containerMax"`
	ContainerMin            ResourcePair `json:"containerMin"`

	// Advanced (optional)
	PodMax        *ResourcePair `json:"podMax,omitempty"`
	PVCMinStorage string        `json:"pvcMinStorage,omitempty"`
	PVCMaxStorage string        `json:"pvcMaxStorage,omitempty"`
}

// NamespaceLimitsInput represents the wizard form data for creating
// both a ResourceQuota and a LimitRange in a single flow.
type NamespaceLimitsInput struct {
	Namespace      string      `json:"namespace"`
	QuotaName      string      `json:"quotaName"`
	LimitRangeName string      `json:"limitRangeName"`
	Quota          QuotaConfig `json:"quota"`
	Limits         LimitConfig `json:"limits"`
}

// Validate checks the NamespaceLimitsInput and returns field-level errors.
func (n *NamespaceLimitsInput) Validate() []FieldError {
	var errs []FieldError

	// Namespace validation
	if n.Namespace == "" {
		errs = append(errs, FieldError{Field: "namespace", Message: "is required"})
	} else if !dnsLabelRegex.MatchString(n.Namespace) {
		errs = append(errs, FieldError{Field: "namespace", Message: "must be a valid DNS label"})
	}

	// ResourceQuota name validation
	if !dnsLabelRegex.MatchString(n.QuotaName) {
		errs = append(errs, FieldError{Field: "quotaName", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	// LimitRange name validation
	if !dnsLabelRegex.MatchString(n.LimitRangeName) {
		errs = append(errs, FieldError{Field: "limitRangeName", Message: "must be a valid DNS label (lowercase alphanumeric and hyphens, 1-63 chars)"})
	}

	// Quota values
	if n.Quota.CPUHard == "" {
		errs = append(errs, FieldError{Field: "quota.cpuHard", Message: "is required"})
	} else if _, err := resource.ParseQuantity(n.Quota.CPUHard); err != nil {
		errs = append(errs, FieldError{Field: "quota.cpuHard", Message: "must be a valid resource quantity (e.g., 4 or 4000m)"})
	}

	if n.Quota.MemoryHard == "" {
		errs = append(errs, FieldError{Field: "quota.memoryHard", Message: "is required"})
	} else if _, err := resource.ParseQuantity(n.Quota.MemoryHard); err != nil {
		errs = append(errs, FieldError{Field: "quota.memoryHard", Message: "must be a valid resource quantity (e.g., 16Gi)"})
	}

	if n.Quota.PodsHard < 1 {
		errs = append(errs, FieldError{Field: "quota.podsHard", Message: "must be at least 1"})
	}

	// Validate optional quota values
	if n.Quota.GPUHard != "" {
		if _, err := resource.ParseQuantity(n.Quota.GPUHard); err != nil {
			errs = append(errs, FieldError{Field: "quota.gpuHard", Message: "must be a valid resource quantity"})
		}
	}

	if n.Quota.WarnThreshold > 0 && (n.Quota.WarnThreshold < 1 || n.Quota.WarnThreshold > 100) {
		errs = append(errs, FieldError{Field: "quota.warnThreshold", Message: "must be between 1 and 100"})
	}

	if n.Quota.CriticalThreshold > 0 && (n.Quota.CriticalThreshold < 1 || n.Quota.CriticalThreshold > 100) {
		errs = append(errs, FieldError{Field: "quota.criticalThreshold", Message: "must be between 1 and 100"})
	}

	// Validate threshold ordering: warn must be less than critical
	if n.Quota.WarnThreshold > 0 && n.Quota.CriticalThreshold > 0 {
		if n.Quota.WarnThreshold >= n.Quota.CriticalThreshold {
			errs = append(errs, FieldError{Field: "quota.warnThreshold", Message: "must be less than critical threshold"})
		}
	}

	// LimitRange values
	errs = append(errs, n.validateResourcePair("limits.containerDefault", n.Limits.ContainerDefault, true)...)
	errs = append(errs, n.validateResourcePair("limits.containerDefaultRequest", n.Limits.ContainerDefaultRequest, true)...)
	errs = append(errs, n.validateResourcePair("limits.containerMax", n.Limits.ContainerMax, true)...)
	errs = append(errs, n.validateResourcePair("limits.containerMin", n.Limits.ContainerMin, true)...)

	// Optional pod limits
	if n.Limits.PodMax != nil {
		errs = append(errs, n.validateResourcePair("limits.podMax", *n.Limits.PodMax, false)...)
	}

	// Optional PVC limits
	if n.Limits.PVCMinStorage != "" {
		if _, err := resource.ParseQuantity(n.Limits.PVCMinStorage); err != nil {
			errs = append(errs, FieldError{Field: "limits.pvcMinStorage", Message: "must be a valid storage quantity"})
		}
	}
	if n.Limits.PVCMaxStorage != "" {
		if _, err := resource.ParseQuantity(n.Limits.PVCMaxStorage); err != nil {
			errs = append(errs, FieldError{Field: "limits.pvcMaxStorage", Message: "must be a valid storage quantity"})
		}
	}

	// Validate LimitRange ordering: min <= defaultRequest <= default <= max
	errs = append(errs, n.validateLimitRangeOrdering()...)

	return errs
}

// validateLimitRangeOrdering checks semantic relationships between LimitRange values.
func (n *NamespaceLimitsInput) validateLimitRangeOrdering() []FieldError {
	var errs []FieldError

	// Parse all quantities (skip if any failed basic validation above)
	minCPU, errMinCPU := resource.ParseQuantity(n.Limits.ContainerMin.CPU)
	minMem, errMinMem := resource.ParseQuantity(n.Limits.ContainerMin.Memory)
	maxCPU, errMaxCPU := resource.ParseQuantity(n.Limits.ContainerMax.CPU)
	maxMem, errMaxMem := resource.ParseQuantity(n.Limits.ContainerMax.Memory)
	defCPU, errDefCPU := resource.ParseQuantity(n.Limits.ContainerDefault.CPU)
	defMem, errDefMem := resource.ParseQuantity(n.Limits.ContainerDefault.Memory)
	reqCPU, errReqCPU := resource.ParseQuantity(n.Limits.ContainerDefaultRequest.CPU)
	reqMem, errReqMem := resource.ParseQuantity(n.Limits.ContainerDefaultRequest.Memory)

	// CPU ordering: min <= defaultRequest <= default <= max
	if errMinCPU == nil && errMaxCPU == nil && minCPU.Cmp(maxCPU) > 0 {
		errs = append(errs, FieldError{Field: "limits.containerMin.cpu", Message: "must be less than or equal to max"})
	}
	if errReqCPU == nil && errMinCPU == nil && reqCPU.Cmp(minCPU) < 0 {
		errs = append(errs, FieldError{Field: "limits.containerDefaultRequest.cpu", Message: "must be greater than or equal to min"})
	}
	if errDefCPU == nil && errMaxCPU == nil && defCPU.Cmp(maxCPU) > 0 {
		errs = append(errs, FieldError{Field: "limits.containerDefault.cpu", Message: "must be less than or equal to max"})
	}
	if errReqCPU == nil && errDefCPU == nil && reqCPU.Cmp(defCPU) > 0 {
		errs = append(errs, FieldError{Field: "limits.containerDefaultRequest.cpu", Message: "must be less than or equal to default limit"})
	}

	// Memory ordering: min <= defaultRequest <= default <= max
	if errMinMem == nil && errMaxMem == nil && minMem.Cmp(maxMem) > 0 {
		errs = append(errs, FieldError{Field: "limits.containerMin.memory", Message: "must be less than or equal to max"})
	}
	if errReqMem == nil && errMinMem == nil && reqMem.Cmp(minMem) < 0 {
		errs = append(errs, FieldError{Field: "limits.containerDefaultRequest.memory", Message: "must be greater than or equal to min"})
	}
	if errDefMem == nil && errMaxMem == nil && defMem.Cmp(maxMem) > 0 {
		errs = append(errs, FieldError{Field: "limits.containerDefault.memory", Message: "must be less than or equal to max"})
	}
	if errReqMem == nil && errDefMem == nil && reqMem.Cmp(defMem) > 0 {
		errs = append(errs, FieldError{Field: "limits.containerDefaultRequest.memory", Message: "must be less than or equal to default limit"})
	}

	return errs
}

func (n *NamespaceLimitsInput) validateResourcePair(prefix string, rp ResourcePair, required bool) []FieldError {
	var errs []FieldError

	if rp.CPU == "" {
		if required {
			errs = append(errs, FieldError{Field: prefix + ".cpu", Message: "is required"})
		}
	} else if _, err := resource.ParseQuantity(rp.CPU); err != nil {
		errs = append(errs, FieldError{Field: prefix + ".cpu", Message: "must be a valid CPU quantity"})
	}

	if rp.Memory == "" {
		if required {
			errs = append(errs, FieldError{Field: prefix + ".memory", Message: "is required"})
		}
	} else if _, err := resource.ParseQuantity(rp.Memory); err != nil {
		errs = append(errs, FieldError{Field: prefix + ".memory", Message: "must be a valid memory quantity"})
	}

	return errs
}

// ToYAML implements WizardInput by converting to ResourceQuota + LimitRange and marshaling to YAML.
func (n *NamespaceLimitsInput) ToYAML() (string, error) {
	quotaYAML, err := n.buildResourceQuotaYAML()
	if err != nil {
		return "", fmt.Errorf("failed to generate ResourceQuota YAML: %w", err)
	}

	limitRangeYAML, err := n.buildLimitRangeYAML()
	if err != nil {
		return "", fmt.Errorf("failed to generate LimitRange YAML: %w", err)
	}

	// Combine both YAMLs with separator
	return quotaYAML + "---\n" + limitRangeYAML, nil
}

func (n *NamespaceLimitsInput) buildResourceQuotaYAML() (string, error) {
	hard := corev1.ResourceList{}

	// Parse required quotas with error handling (defense-in-depth, Validate should catch these)
	cpuQty, err := resource.ParseQuantity(n.Quota.CPUHard)
	if err != nil {
		return "", fmt.Errorf("invalid CPU quota: %w", err)
	}
	hard[corev1.ResourceCPU] = cpuQty

	memQty, err := resource.ParseQuantity(n.Quota.MemoryHard)
	if err != nil {
		return "", fmt.Errorf("invalid memory quota: %w", err)
	}
	hard[corev1.ResourceMemory] = memQty

	podsQty, err := resource.ParseQuantity(strconv.Itoa(n.Quota.PodsHard))
	if err != nil {
		return "", fmt.Errorf("invalid pods quota: %w", err)
	}
	hard[corev1.ResourcePods] = podsQty

	// Optional count quotas (integers always parse successfully)
	if n.Quota.SecretsHard > 0 {
		hard[corev1.ResourceSecrets] = resource.MustParse(strconv.Itoa(n.Quota.SecretsHard))
	}
	if n.Quota.ConfigMapsHard > 0 {
		hard[corev1.ResourceConfigMaps] = resource.MustParse(strconv.Itoa(n.Quota.ConfigMapsHard))
	}
	if n.Quota.ServicesHard > 0 {
		hard[corev1.ResourceServices] = resource.MustParse(strconv.Itoa(n.Quota.ServicesHard))
	}
	if n.Quota.PVCsHard > 0 {
		hard[corev1.ResourcePersistentVolumeClaims] = resource.MustParse(strconv.Itoa(n.Quota.PVCsHard))
	}
	if n.Quota.GPUHard != "" {
		gpuQty, err := resource.ParseQuantity(n.Quota.GPUHard)
		if err != nil {
			return "", fmt.Errorf("invalid GPU quota: %w", err)
		}
		hard["nvidia.com/gpu"] = gpuQty
	}

	// Annotations for thresholds
	annotations := make(map[string]string)
	if n.Quota.WarnThreshold > 0 {
		annotations[limits.AnnotationWarnThreshold] = strconv.Itoa(n.Quota.WarnThreshold)
	}
	if n.Quota.CriticalThreshold > 0 {
		annotations[limits.AnnotationCriticalThreshold] = strconv.Itoa(n.Quota.CriticalThreshold)
	}

	rq := &corev1.ResourceQuota{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ResourceQuota",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      n.QuotaName,
			Namespace: n.Namespace,
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: hard,
		},
	}

	if len(annotations) > 0 {
		rq.ObjectMeta.Annotations = annotations
	}

	yamlBytes, err := sigsyaml.Marshal(rq)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

func (n *NamespaceLimitsInput) buildLimitRangeYAML() (string, error) {
	lrItems := []corev1.LimitRangeItem{}

	// Parse container limits with error handling
	containerDefault, err := parseResourcePair(n.Limits.ContainerDefault, "containerDefault")
	if err != nil {
		return "", err
	}
	containerDefaultRequest, err := parseResourcePair(n.Limits.ContainerDefaultRequest, "containerDefaultRequest")
	if err != nil {
		return "", err
	}
	containerMax, err := parseResourcePair(n.Limits.ContainerMax, "containerMax")
	if err != nil {
		return "", err
	}
	containerMin, err := parseResourcePair(n.Limits.ContainerMin, "containerMin")
	if err != nil {
		return "", err
	}

	containerLimit := corev1.LimitRangeItem{
		Type:           corev1.LimitTypeContainer,
		Default:        containerDefault,
		DefaultRequest: containerDefaultRequest,
		Max:            containerMax,
		Min:            containerMin,
	}
	lrItems = append(lrItems, containerLimit)

	// Optional pod limits
	if n.Limits.PodMax != nil && (n.Limits.PodMax.CPU != "" || n.Limits.PodMax.Memory != "") {
		podLimit := corev1.LimitRangeItem{
			Type: corev1.LimitTypePod,
			Max:  corev1.ResourceList{},
		}
		if n.Limits.PodMax.CPU != "" {
			cpuQty, err := resource.ParseQuantity(n.Limits.PodMax.CPU)
			if err != nil {
				return "", fmt.Errorf("invalid podMax CPU: %w", err)
			}
			podLimit.Max[corev1.ResourceCPU] = cpuQty
		}
		if n.Limits.PodMax.Memory != "" {
			memQty, err := resource.ParseQuantity(n.Limits.PodMax.Memory)
			if err != nil {
				return "", fmt.Errorf("invalid podMax memory: %w", err)
			}
			podLimit.Max[corev1.ResourceMemory] = memQty
		}
		lrItems = append(lrItems, podLimit)
	}

	// Optional PVC limits
	if n.Limits.PVCMinStorage != "" || n.Limits.PVCMaxStorage != "" {
		pvcLimit := corev1.LimitRangeItem{
			Type: corev1.LimitTypePersistentVolumeClaim,
		}
		if n.Limits.PVCMinStorage != "" {
			minStorage, err := resource.ParseQuantity(n.Limits.PVCMinStorage)
			if err != nil {
				return "", fmt.Errorf("invalid PVC min storage: %w", err)
			}
			pvcLimit.Min = corev1.ResourceList{
				corev1.ResourceStorage: minStorage,
			}
		}
		if n.Limits.PVCMaxStorage != "" {
			maxStorage, err := resource.ParseQuantity(n.Limits.PVCMaxStorage)
			if err != nil {
				return "", fmt.Errorf("invalid PVC max storage: %w", err)
			}
			pvcLimit.Max = corev1.ResourceList{
				corev1.ResourceStorage: maxStorage,
			}
		}
		lrItems = append(lrItems, pvcLimit)
	}

	lr := &corev1.LimitRange{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "LimitRange",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      n.LimitRangeName,
			Namespace: n.Namespace,
		},
		Spec: corev1.LimitRangeSpec{
			Limits: lrItems,
		},
	}

	yamlBytes, err := sigsyaml.Marshal(lr)
	if err != nil {
		return "", err
	}

	// Remove empty status fields from output
	yamlStr := string(yamlBytes)
	yamlStr = strings.ReplaceAll(yamlStr, "status: {}\n", "")
	return yamlStr, nil
}

// parseResourcePair converts a ResourcePair to a corev1.ResourceList with error handling.
func parseResourcePair(rp ResourcePair, name string) (corev1.ResourceList, error) {
	cpuQty, err := resource.ParseQuantity(rp.CPU)
	if err != nil {
		return nil, fmt.Errorf("invalid %s CPU: %w", name, err)
	}
	memQty, err := resource.ParseQuantity(rp.Memory)
	if err != nil {
		return nil, fmt.Errorf("invalid %s memory: %w", name, err)
	}
	return corev1.ResourceList{
		corev1.ResourceCPU:    cpuQty,
		corev1.ResourceMemory: memQty,
	}, nil
}
