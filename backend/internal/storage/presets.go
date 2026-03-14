package storage

// PresetParam describes a single parameter for a CSI driver preset.
type PresetParam struct {
	Default     string   `json:"default"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	Required    bool     `json:"required,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// PresetInfo describes a driver preset with display name and parameters.
type PresetInfo struct {
	DisplayName string                 `json:"displayName"`
	Parameters  map[string]PresetParam `json:"parameters"`
}

// DriverPresets maps CSI driver names to their parameter presets.
var DriverPresets = map[string]PresetInfo{
	"driver.longhorn.io": {
		DisplayName: "Longhorn",
		Parameters: map[string]PresetParam{
			"numberOfReplicas":    {Default: "3", Description: "Number of replicas", Type: "number"},
			"staleReplicaTimeout": {Default: "2880", Description: "Stale replica timeout (minutes)", Type: "number"},
			"dataLocality":        {Default: "disabled", Description: "Data locality", Type: "enum", Options: []string{"disabled", "best-effort", "strict-local"}},
		},
	},
	"nfs.csi.k8s.io": {
		DisplayName: "NFS CSI",
		Parameters: map[string]PresetParam{
			"server": {Default: "", Description: "NFS server address", Type: "string", Required: true},
			"share":  {Default: "", Description: "NFS share path", Type: "string", Required: true},
		},
	},
	"ebs.csi.aws.com": {
		DisplayName: "AWS EBS",
		Parameters: map[string]PresetParam{
			"type":      {Default: "gp3", Description: "Volume type", Type: "enum", Options: []string{"gp3", "gp2", "io1", "io2", "st1", "sc1"}},
			"encrypted": {Default: "true", Description: "Enable encryption", Type: "boolean"},
			"kmsKeyId":  {Default: "", Description: "KMS key ARN (optional)", Type: "string"},
		},
	},
	"rook-ceph.rbd.csi.ceph.com": {
		DisplayName: "Rook Ceph RBD",
		Parameters: map[string]PresetParam{
			"clusterID":     {Default: "", Description: "Ceph cluster ID", Type: "string", Required: true},
			"pool":          {Default: "", Description: "Ceph pool name", Type: "string", Required: true},
			"imageFormat":   {Default: "2", Description: "RBD image format", Type: "string"},
			"imageFeatures": {Default: "layering", Description: "RBD image features", Type: "string"},
		},
	},
}
