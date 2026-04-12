package certmanager

import (
	"math"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// computeStatus derives a Certificate Status from the Ready condition and notAfter time.
func computeStatus(readyStatus, reason string, notAfter *time.Time) Status {
	now := time.Now()

	// Expired check takes priority regardless of the Ready condition.
	if notAfter != nil && notAfter.Before(now) {
		return StatusExpired
	}

	switch readyStatus {
	case "True":
		if notAfter != nil {
			days := int(math.Floor(time.Until(*notAfter).Hours() / 24))
			if days <= WarningThresholdDays {
				return StatusExpiring
			}
		}
		return StatusReady
	case "False":
		if reason == "Issuing" || reason == "InProgress" {
			return StatusIssuing
		}
		return StatusFailed
	default:
		return StatusUnknown
	}
}

// normalizeCertificate converts an unstructured cert-manager Certificate into our
// typed Certificate struct.
func normalizeCertificate(u *unstructured.Unstructured) (Certificate, error) {
	obj := u.Object

	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}

	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}

	// IssuerRef
	issuerRefRaw, _ := spec["issuerRef"].(map[string]any)
	issuerRef := IssuerRef{
		Name:  stringFrom(issuerRefRaw, "name"),
		Kind:  stringFrom(issuerRefRaw, "kind"),
		Group: stringFrom(issuerRefRaw, "group"),
	}

	// DNS names from spec
	dnsNames, _, _ := unstructured.NestedStringSlice(obj, "spec", "dnsNames")

	// Time fields
	notBefore := parseTimeField(status, "notBefore")
	notAfter := parseTimeField(status, "notAfter")
	renewalTime := parseTimeField(status, "renewalTime")

	// DaysRemaining
	var daysRemaining *int
	if notAfter != nil {
		d := int(math.Floor(time.Until(*notAfter).Hours() / 24))
		daysRemaining = &d
	}

	// Ready condition
	readyStatus, reason, message := readReadyCondition(status)

	cert := Certificate{
		Name:          u.GetName(),
		Namespace:     u.GetNamespace(),
		Status:        computeStatus(readyStatus, reason, notAfter),
		Reason:        reason,
		Message:       message,
		IssuerRef:     issuerRef,
		SecretName:    stringFrom(spec, "secretName"),
		DNSNames:      dnsNames,
		CommonName:    stringFrom(spec, "commonName"),
		Duration:      stringFrom(spec, "duration"),
		RenewBefore:   stringFrom(spec, "renewBefore"),
		NotBefore:     notBefore,
		NotAfter:      notAfter,
		RenewalTime:   renewalTime,
		DaysRemaining: daysRemaining,
		UID:           string(u.GetUID()),
	}

	return cert, nil
}

// normalizeIssuer converts an unstructured Issuer or ClusterIssuer.
// scope should be "Namespaced" or "Cluster".
func normalizeIssuer(u *unstructured.Unstructured, scope string) Issuer {
	obj := u.Object

	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}

	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}

	issuerType := detectIssuerType(spec)
	readyStatus, reason, message := readReadyCondition(status)

	var acmeEmail, acmeServer string
	if acme, ok := spec["acme"].(map[string]any); ok {
		acmeEmail = stringFrom(acme, "email")
		acmeServer = stringFrom(acme, "server")
	}

	// UpdatedAt: use the last condition's lastTransitionTime if available.
	updatedAt := time.Time{}
	if conditions, ok := status["conditions"].([]any); ok && len(conditions) > 0 {
		for _, c := range conditions {
			if cm, ok := c.(map[string]any); ok {
				if t := parseTimeField(cm, "lastTransitionTime"); t != nil {
					if t.After(updatedAt) {
						updatedAt = *t
					}
				}
			}
		}
	}

	return Issuer{
		Name:       u.GetName(),
		Namespace:  u.GetNamespace(),
		Scope:      scope,
		Type:       issuerType,
		Ready:      readyStatus == "True",
		Reason:     reason,
		Message:    message,
		ACMEEmail:  acmeEmail,
		ACMEServer: acmeServer,
		UID:        string(u.GetUID()),
		UpdatedAt:  updatedAt,
	}
}

// detectIssuerType returns the issuer type string by inspecting spec keys.
func detectIssuerType(spec map[string]any) string {
	if spec == nil {
		return "Unknown"
	}
	if _, ok := spec["acme"]; ok {
		return "ACME"
	}
	if _, ok := spec["ca"]; ok {
		return "CA"
	}
	if _, ok := spec["vault"]; ok {
		return "Vault"
	}
	if _, ok := spec["selfSigned"]; ok {
		return "SelfSigned"
	}
	return "Unknown"
}

// normalizeCertRequest converts an unstructured CertificateRequest.
func normalizeCertRequest(u *unstructured.Unstructured) CertificateRequest {
	obj := u.Object

	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}

	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}

	issuerRefRaw, _ := spec["issuerRef"].(map[string]any)
	issuerRef := IssuerRef{
		Name:  stringFrom(issuerRefRaw, "name"),
		Kind:  stringFrom(issuerRefRaw, "kind"),
		Group: stringFrom(issuerRefRaw, "group"),
	}

	readyStatus, reason, message := readReadyCondition(status)

	var createdAt time.Time
	if t := parseTimeField(u.Object["metadata"].(map[string]any), "creationTimestamp"); t != nil {
		createdAt = *t
	}

	finishedAt := parseTimeField(status, "completionTime")

	return CertificateRequest{
		Name:       u.GetName(),
		Namespace:  u.GetNamespace(),
		Status:     computeStatus(readyStatus, reason, nil),
		Reason:     reason,
		Message:    message,
		IssuerRef:  issuerRef,
		CreatedAt:  createdAt,
		FinishedAt: finishedAt,
		UID:        string(u.GetUID()),
	}
}

// normalizeOrder converts an unstructured ACME Order.
func normalizeOrder(u *unstructured.Unstructured) Order {
	obj := u.Object

	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}

	// Owning Certificate name from ownerReferences.
	crName := ""
	if owners, ok := obj["metadata"].(map[string]any)["ownerReferences"].([]any); ok {
		for _, o := range owners {
			if om, ok := o.(map[string]any); ok {
				if kind, _ := om["kind"].(string); kind == "CertificateRequest" {
					crName, _ = om["name"].(string)
					break
				}
			}
		}
	}

	var createdAt time.Time
	if meta, ok := obj["metadata"].(map[string]any); ok {
		if t := parseTimeField(meta, "creationTimestamp"); t != nil {
			createdAt = *t
		}
	}

	return Order{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		State:     stringFrom(status, "state"),
		Reason:    stringFrom(status, "reason"),
		CreatedAt: createdAt,
		UID:       string(u.GetUID()),
		CRName:    crName,
	}
}

// normalizeChallenge converts an unstructured ACME Challenge.
func normalizeChallenge(u *unstructured.Unstructured) Challenge {
	obj := u.Object

	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}

	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}

	// Owning Order name from ownerReferences.
	orderName := ""
	if meta, ok := obj["metadata"].(map[string]any); ok {
		if owners, ok := meta["ownerReferences"].([]any); ok {
			for _, o := range owners {
				if om, ok := o.(map[string]any); ok {
					if kind, _ := om["kind"].(string); kind == "Order" {
						orderName, _ = om["name"].(string)
						break
					}
				}
			}
		}
	}

	var createdAt time.Time
	if meta, ok := obj["metadata"].(map[string]any); ok {
		if t := parseTimeField(meta, "creationTimestamp"); t != nil {
			createdAt = *t
		}
	}

	return Challenge{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		Type:      stringFrom(spec, "type"),
		State:     stringFrom(status, "state"),
		Reason:    stringFrom(status, "reason"),
		DNSName:   stringFrom(spec, "dnsName"),
		CreatedAt: createdAt,
		UID:       string(u.GetUID()),
		OrderName: orderName,
	}
}

// readReadyCondition iterates status.conditions looking for type=Ready and returns
// (status, reason, message). Returns empty strings if not found or malformed.
func readReadyCondition(obj map[string]any) (status, reason, message string) {
	conditions, ok := obj["conditions"].([]any)
	if !ok {
		return "", "", ""
	}
	for _, c := range conditions {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cm["type"].(string); t == "Ready" {
			status, _ = cm["status"].(string)
			reason, _ = cm["reason"].(string)
			message, _ = cm["message"].(string)
			return
		}
	}
	return "", "", ""
}

// stringFrom safely extracts a string value from a map by key.
// Returns "" if the map is nil or the key is absent / not a string.
func stringFrom(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// parseTimeField parses an RFC3339 timestamp from a nested map path.
// Returns nil on any error or missing value.
func parseTimeField(obj map[string]any, fields ...string) *time.Time {
	s, found, err := unstructured.NestedString(obj, fields...)
	if err != nil || !found || s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
