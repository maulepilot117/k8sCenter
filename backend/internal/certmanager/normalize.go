package certmanager

import (
	"math"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// computeStatus derives a Certificate's BASE Status (Ready / Issuing /
// Failed / Expired / Unknown) from the Ready condition and notAfter
// time. It deliberately never returns StatusExpiring — the warning-band
// transition depends on the resolved per-cert threshold, which isn't
// available here. ApplyThresholds (in thresholds.go) overlays Expiring
// onto the base status using DeriveStatus.
func computeStatus(readyStatus, reason string, notAfter *time.Time) Status {
	now := time.Now()

	// Expired is independent of the configurable threshold — past
	// notAfter is always Expired regardless of operator settings.
	if notAfter != nil && notAfter.Before(now) {
		return StatusExpired
	}

	switch readyStatus {
	case "True":
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

// DeriveStatus returns a Certificate's effective Status given the
// already-populated base Status and resolved WarningThresholdDays.
// Caller is responsible for having run ApplyThresholds first so the
// threshold field is non-zero; if the field is zero (resolution didn't
// run, or threshold isn't applicable) the package default applies.
//
// Status precedence: Expired > Failed > Issuing > Expiring > Ready >
// Unknown. Expiring only overlays the Ready band — a Failed cert with a
// near-expiry NotAfter stays Failed (the operator's bigger problem).
func DeriveStatus(cert Certificate) Status {
	if cert.Status != StatusReady {
		return cert.Status
	}
	if cert.DaysRemaining == nil {
		return cert.Status
	}
	warn := cert.WarningThresholdDays
	if warn <= 0 {
		warn = WarningThresholdDays
	}
	if *cert.DaysRemaining <= warn {
		return StatusExpiring
	}
	return cert.Status
}

// parseThresholdAnnotation parses an annotation value as a positive
// integer (days). Returns (n, true) only when the value is a clean
// positive integer; empty / malformed / non-positive values return
// (0, false) so callers can fall through to the next layer of the
// resolution chain. The strict-positive constraint matches the
// "thresholds are days, days are >= 1" invariant; a "0" annotation is
// treated as "not set" rather than "warn instantly," which is the more
// useful operator semantic.
func parseThresholdAnnotation(val string) (int, bool) {
	if val == "" {
		return 0, false
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// readThresholdAnnotations extracts both threshold annotations from a
// resource's annotation map. Each key is parsed independently, so a
// resource can set warn alone or crit alone. Returns (warn, warnOK,
// crit, critOK).
func readThresholdAnnotations(annotations map[string]string) (warn int, warnOK bool, crit int, critOK bool) {
	if annotations == nil {
		return 0, false, 0, false
	}
	warn, warnOK = parseThresholdAnnotation(annotations[AnnotationWarnThreshold])
	crit, critOK = parseThresholdAnnotation(annotations[AnnotationCriticalThreshold])
	return
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

	// Cert-level threshold annotations. ApplyThresholds resolves the
	// final values via the inheritance chain; here we just parse what
	// the cert itself declares. Zero means "not set at the cert level"
	// — the resolver fills in the inherited / default value later.
	warn, _, crit, _ := readThresholdAnnotations(u.GetAnnotations())

	cert := Certificate{
		Name:                  u.GetName(),
		Namespace:             u.GetNamespace(),
		Status:                computeStatus(readyStatus, reason, notAfter),
		Reason:                reason,
		Message:               message,
		IssuerRef:             issuerRef,
		SecretName:            stringFrom(spec, "secretName"),
		DNSNames:              dnsNames,
		CommonName:            stringFrom(spec, "commonName"),
		Duration:              stringFrom(spec, "duration"),
		RenewBefore:           stringFrom(spec, "renewBefore"),
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		RenewalTime:           renewalTime,
		DaysRemaining:         daysRemaining,
		WarningThresholdDays:  warn,
		CriticalThresholdDays: crit,
		UID:                   string(u.GetUID()),
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

	// Issuer-level threshold annotations. Pointer fields so the resolver
	// can distinguish "issuer didn't set this" (nil) from "issuer set
	// it to the same value as the default" (non-nil).
	warn, warnOK, crit, critOK := readThresholdAnnotations(u.GetAnnotations())
	var warnPtr, critPtr *int
	if warnOK {
		warnPtr = &warn
	}
	if critOK {
		critPtr = &crit
	}

	return Issuer{
		Name:                  u.GetName(),
		Namespace:             u.GetNamespace(),
		Scope:                 scope,
		Type:                  issuerType,
		Ready:                 readyStatus == "True",
		Reason:                reason,
		Message:               message,
		ACMEEmail:             acmeEmail,
		ACMEServer:            acmeServer,
		WarningThresholdDays:  warnPtr,
		CriticalThresholdDays: critPtr,
		UID:                   string(u.GetUID()),
		UpdatedAt:             updatedAt,
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
