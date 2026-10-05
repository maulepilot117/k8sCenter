// Package incidents holds the Release D incident domain: captured, redacted
// evidence that is owned by its creator and shared by explicit grants.
//
// Access policy (Q1, docs/plans/2026-09-10-release-d-incidents-impl.md §2):
// an incident is visible to its owner and to users holding an explicit
// grant, and a grant conveys standing to ask, never Kubernetes authority.
// Every read of every evidence item re-checks the caller's current
// Kubernetes authorization for that item's stored scope (cluster, API group,
// resource, namespace); an item whose payload was derived from a Secret
// additionally requires `get` on secrets in that namespace, for the owner
// too. Secret values and Secret key names are never persisted: redaction
// runs at capture, before anything is measured or written, and there is no
// reveal path. Incidents are retained for a configurable window, 30 days by
// default, and swept whole.
//
// This file is the capture-time half of that policy. The behavioural
// reference is resources.maskedSecret (backend/internal/k8s/resources/
// secrets.go), which is unexported and Secret-typed and so cannot be
// imported; its two non-obvious rules are carried over and extended here:
// StringData is covered as well as Data (both are dropped outright), and
// kubectl.kubernetes.io/last-applied-configuration is stripped from every
// object kind, because it carries the plaintext stringData of the original
// kubectl apply on any resource kubectl has applied.
package incidents

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// DefaultMaxBytes is the per-item ceiling (Q1 P14: 1 MiB per evidence item).
const DefaultMaxBytes = 1 << 20

// MinMaxBytes is the smallest bound NewRedactor accepts. The reduction
// ladder bottoms out at an empty projection, so any bound at or above this
// can be met; a smaller one is a configuration error, not a runtime one.
const MinMaxBytes = 64

// LastAppliedConfigAnnotation carries the full original manifest, including
// plaintext stringData. It is removed from every object.
const LastAppliedConfigAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// Rule ids recorded in RedactionMeta.Rules. Stable wire values; U24a's
// frontend types mirror them.
const (
	// RuleSecretValues: the object is a Secret and was projected to metadata
	// only. No data, no stringData, and no key names survive (Q1 P11.3: a key
	// name such as PROD_DB_PASSWORD discloses credential structure; storing
	// none is stricter than gating them and is the chosen design).
	RuleSecretValues = "secret-values"
	// RuleLastAppliedConfig: metadata.annotations carried
	// LastAppliedConfigAnnotation and it was removed.
	RuleLastAppliedConfig = "last-applied-config"
	// RuleAnnotationAllowlist: at least one annotation outside the allowlist
	// (kubecenter.io/*, deployment.kubernetes.io/revision) was removed.
	RuleAnnotationAllowlist = "annotation-allowlist"
	// RuleFieldAllowlist: at least one field outside the projection allowlist
	// was removed. Annotation removals and Secret data removals are counted
	// in FieldsRemoved but attributed to their own rules.
	RuleFieldAllowlist = "field-allowlist"
	// RuleTextSanitized: at least one string lost control characters or
	// invalid UTF-8 sequences.
	RuleTextSanitized = "text-sanitized"
	// RuleTruncated: something was cut for size: a string beyond its field
	// bound, a list or map beyond its count bound, or the whole projection
	// reduced to fit maxBytes. Mirrors RedactionMeta.Truncated.
	RuleTruncated = "truncated"
)

// Per-field and per-collection bounds applied during projection. Exceeding
// one is a truncation and is flagged.
const (
	maxFieldBytes      = 4096
	minFieldBytes      = 16 // floor of the size-reduction ladder
	maxLabels          = 64
	maxAnnotations     = 32
	maxConditions      = 32
	maxContainers      = 64
	maxOwnerReferences = 16
)

// RedactionMeta is the capture-time redaction record stored beside every
// evidence item (plan §3.2). U22b's envelope embeds it.
type RedactionMeta struct {
	// Applied is true when at least one rule fired.
	Applied bool `json:"applied"`
	// Rules lists the rule ids that fired, in declaration order, once each.
	Rules []string `json:"rules,omitempty"`
	// FieldsRemoved counts keys present in the input that the projection
	// dropped: one per dropped key at every level the projection walks (a
	// dropped subtree counts once), one per dropped list element that was not
	// a map, and one per annotation removed.
	FieldsRemoved int `json:"fieldsRemoved"`
	// Truncated is true when anything was cut for size. Never silent (P14).
	Truncated bool `json:"truncated"`
	// SecretDerived is true when the source was a Secret or referenced one
	// (Q1 P11.4). Reading the item then also requires `get` on secrets.
	SecretDerived bool `json:"secretDerived"`
}

// Redactor projects captured objects onto an allowlist and bounds them
// BEFORE they are measured, marshalled, or written. It is pure and safe for
// concurrent use.
type Redactor struct{ maxBytes int }

// NewRedactor returns a Redactor whose RedactObject output marshals to at
// most maxBytes and whose RedactText output is at most maxBytes long.
func NewRedactor(maxBytes int) (*Redactor, error) {
	if maxBytes < MinMaxBytes {
		return nil, fmt.Errorf("incidents: maxBytes must be at least %d, got %d", MinMaxBytes, maxBytes)
	}
	return &Redactor{maxBytes: maxBytes}, nil
}

// RedactObject projects obj (an unstructured object's content) down to the
// allowlist, sanitizes and bounds every string that survives, and enforces
// len(json.Marshal(projection)) <= maxBytes. sourceResource is the plural
// resource the object was read from ("secrets" marks it Secret-derived).
//
// Allowlist: apiVersion, kind, metadata.{name,namespace,uid,resourceVersion,
// creationTimestamp,labels,annotations(allowlisted),ownerReferences
// {apiVersion,kind,name,uid,controller}}, spec.replicas, container
// {name,image} for containers and initContainers at spec (Pod),
// spec.template.spec (workloads) and spec.jobTemplate.spec.template.spec
// (CronJob), status.conditions[]{type,status,reason,message,
// lastTransitionTime}, status.{replicas,readyReplicas,availableReplicas,
// phase}. A Secret keeps apiVersion, kind and metadata only. Everything
// else is dropped and counted.
//
// SecretDerived is decided on the ORIGINAL object: kind Secret,
// sourceResource "secrets", or any secretKeyRef / envFrom.secretRef in any
// container list, imagePullSecrets, volumes[].secret, or
// volumes[].projected.sources[].secret in any pod-spec location.
//
// When the projection exceeds maxBytes it is reduced deterministically
// (conditions, labels, annotations, ownerReferences and containers lose
// their tails by halving, then every string bound is halved down to 16
// bytes, then the projection becomes empty) and Truncated is set. The input
// is never mutated.
func (r *Redactor) RedactObject(obj map[string]any, sourceResource string) (map[string]any, RedactionMeta) {
	p := &projector{bound: maxFieldBytes}
	isSecret := asString(obj["kind"]) == "Secret"
	out := p.object(obj, isSecret)

	for {
		b, err := json.Marshal(out)
		if err == nil && len(b) <= r.maxBytes {
			break
		}
		p.truncated = true
		if !p.shrink(out) {
			out = map[string]any{}
			break
		}
	}

	meta := RedactionMeta{
		FieldsRemoved: p.removed,
		Truncated:     p.truncated,
		SecretDerived: isSecret || sourceResource == "secrets" || referencesSecret(obj),
	}
	if isSecret {
		meta.Rules = append(meta.Rules, RuleSecretValues)
	}
	if p.lastApplied {
		meta.Rules = append(meta.Rules, RuleLastAppliedConfig)
	}
	if p.annotationHits > 0 {
		meta.Rules = append(meta.Rules, RuleAnnotationAllowlist)
	}
	if p.fieldHits > 0 {
		meta.Rules = append(meta.Rules, RuleFieldAllowlist)
	}
	if p.sanitized {
		meta.Rules = append(meta.Rules, RuleTextSanitized)
	}
	if p.truncated {
		meta.Rules = append(meta.Rules, RuleTruncated)
	}
	meta.Applied = len(meta.Rules) > 0
	return out, meta
}

// RedactText sanitizes a controller-authored string: invalid UTF-8 becomes
// U+FFFD, C0 and C1 control characters (and DEL) other than '\n' and '\t'
// are removed, and the result is cut to maxBytes at a rune boundary. The
// second result reports whether it was cut.
func (r *Redactor) RedactText(s string) (string, bool) {
	out, _, truncated := sanitize(s, r.maxBytes)
	return out, truncated
}

// sanitize is the one text path: every string that reaches a projection or
// RedactText goes through it. changed reports a sanitization edit (not a
// cut); truncated reports a cut.
func sanitize(s string, bound int) (out string, changed, truncated bool) {
	out = strings.ToValidUTF8(s, "�")
	out = strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, out)
	changed = out != s
	if len(out) > bound {
		out = truncateRunes(out, bound)
		truncated = true
	}
	return out, changed, truncated
}

// truncateRunes cuts s to at most n bytes without splitting a rune.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// projector carries the counters for one RedactObject call.
type projector struct {
	bound          int // current per-string byte bound
	removed        int // FieldsRemoved
	fieldHits      int // removals attributed to RuleFieldAllowlist
	annotationHits int // removals attributed to RuleAnnotationAllowlist
	lastApplied    bool
	sanitized      bool
	truncated      bool
}

// str sanitizes and bounds v when it is a string. ok is false for any other
// type; callers count that as a removed field.
func (p *projector) str(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	out, changed, truncated := sanitize(s, p.bound)
	p.sanitized = p.sanitized || changed
	p.truncated = p.truncated || truncated
	return out, true
}

// drop records a removed field attributed to the field allowlist.
func (p *projector) drop() { p.removed++; p.fieldHits++ }

// copyStrings copies the named string keys from in to out; a key present
// with a non-string value is dropped.
func (p *projector) copyStrings(in, out map[string]any, keys ...string) {
	for _, k := range keys {
		v, present := in[k]
		if !present {
			continue
		}
		if s, ok := p.str(v); ok {
			out[k] = s
		} else {
			p.drop()
		}
	}
}

// copyNumbers copies the named numeric keys; any other type is dropped.
func (p *projector) copyNumbers(in, out map[string]any, keys ...string) {
	for _, k := range keys {
		v, present := in[k]
		if !present {
			continue
		}
		if isNumber(v) {
			out[k] = v
		} else {
			p.drop()
		}
	}
}

// dropOthers counts every key of in that is not in keep.
func (p *projector) dropOthers(in map[string]any, keep ...string) {
	for k := range in {
		if !slices.Contains(keep, k) {
			p.drop()
		}
	}
}

// child returns in[key] as a map when present. A present non-map value is
// dropped and counted.
func (p *projector) child(in map[string]any, key string) (map[string]any, bool) {
	v, present := in[key]
	if !present {
		return nil, false
	}
	m, ok := v.(map[string]any)
	if !ok {
		p.drop()
		return nil, false
	}
	return m, true
}

func (p *projector) object(obj map[string]any, isSecret bool) map[string]any {
	out := map[string]any{}
	if obj == nil {
		return out
	}
	p.copyStrings(obj, out, "apiVersion", "kind")
	if md, ok := p.child(obj, "metadata"); ok {
		out["metadata"] = p.metadata(md)
	}
	if isSecret {
		// Metadata only. data/stringData are attributed to RuleSecretValues,
		// everything else (type, immutable, ...) to the field allowlist.
		for k := range obj {
			switch k {
			case "apiVersion", "kind", "metadata":
			case "data", "stringData":
				p.removed++
			default:
				p.drop()
			}
		}
		return out
	}
	if spec, ok := p.child(obj, "spec"); ok {
		out["spec"] = p.spec(spec)
	}
	if st, ok := p.child(obj, "status"); ok {
		out["status"] = p.status(st)
	}
	p.dropOthers(obj, "apiVersion", "kind", "metadata", "spec", "status")
	return out
}

func (p *projector) metadata(md map[string]any) map[string]any {
	out := map[string]any{}
	p.copyStrings(md, out, "name", "namespace", "uid", "resourceVersion", "creationTimestamp")
	if labels, ok := p.child(md, "labels"); ok {
		out["labels"] = p.stringMap(labels, maxLabels, nil)
	}
	if ann, ok := p.child(md, "annotations"); ok {
		out["annotations"] = p.stringMap(ann, maxAnnotations, p.annotationAllowed)
	}
	if v, present := md["ownerReferences"]; present {
		if refs, ok := v.([]any); ok {
			out["ownerReferences"] = p.ownerReferences(refs)
		} else {
			p.drop()
		}
	}
	p.dropOthers(md, "name", "namespace", "uid", "resourceVersion", "creationTimestamp", "labels", "annotations", "ownerReferences")
	return out
}

// annotationAllowed decides on the raw key, before sanitization, so a key
// smuggling control characters into an allowlisted prefix is judged as
// written. Returns ok=false for a removed key.
func (p *projector) annotationAllowed(key string) bool {
	switch {
	case key == LastAppliedConfigAnnotation:
		p.lastApplied = true
		p.removed++
		return false
	case strings.HasPrefix(key, "kubecenter.io/"), key == "deployment.kubernetes.io/revision":
		return true
	default:
		p.annotationHits++
		p.removed++
		return false
	}
}

// stringMap projects a labels/annotations map: allowed keys with string
// values, sanitized, kept in sorted-key order up to max entries.
func (p *projector) stringMap(in map[string]any, max int, allowed func(string) bool) map[string]any {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := map[string]any{}
	for _, k := range keys {
		if allowed != nil && !allowed(k) {
			continue
		}
		v, ok := p.str(in[k])
		if !ok {
			p.drop()
			continue
		}
		if len(out) >= max {
			p.truncated = true
			break
		}
		sk, _ := p.str(k)
		out[sk] = v
	}
	return out
}

func (p *projector) ownerReferences(refs []any) []any {
	out := make([]any, 0, min(len(refs), maxOwnerReferences))
	for _, v := range refs {
		ref, ok := v.(map[string]any)
		if !ok {
			p.drop()
			continue
		}
		if len(out) >= maxOwnerReferences {
			p.truncated = true
			break
		}
		o := map[string]any{}
		p.copyStrings(ref, o, "apiVersion", "kind", "name", "uid")
		if c, present := ref["controller"]; present {
			if b, ok := c.(bool); ok {
				o["controller"] = b
			} else {
				p.drop()
			}
		}
		p.dropOthers(ref, "apiVersion", "kind", "name", "uid", "controller")
		out = append(out, o)
	}
	return out
}

// spec projects the top-level spec: replicas, the pod-spec container lists
// at the Pod location, and the workload and CronJob template paths.
func (p *projector) spec(spec map[string]any) map[string]any {
	out := map[string]any{}
	p.copyNumbers(spec, out, "replicas")
	p.containerLists(spec, out)
	if tpl, ok := p.child(spec, "template"); ok {
		out["template"] = p.template(tpl)
	}
	if jt, ok := p.child(spec, "jobTemplate"); ok {
		jo := map[string]any{}
		if js, ok := p.child(jt, "spec"); ok {
			so := map[string]any{}
			if tpl, ok := p.child(js, "template"); ok {
				so["template"] = p.template(tpl)
			}
			p.dropOthers(js, "template")
			jo["spec"] = so
		}
		p.dropOthers(jt, "spec")
		out["jobTemplate"] = jo
	}
	p.dropOthers(spec, "replicas", "containers", "initContainers", "template", "jobTemplate")
	return out
}

// template projects a pod template: only its spec's container lists.
func (p *projector) template(tpl map[string]any) map[string]any {
	out := map[string]any{}
	if ps, ok := p.child(tpl, "spec"); ok {
		so := map[string]any{}
		p.containerLists(ps, so)
		p.dropOthers(ps, "containers", "initContainers")
		out["spec"] = so
	}
	p.dropOthers(tpl, "spec")
	return out
}

func (p *projector) containerLists(podSpec, out map[string]any) {
	for _, key := range []string{"containers", "initContainers"} {
		v, present := podSpec[key]
		if !present {
			continue
		}
		list, ok := v.([]any)
		if !ok {
			p.drop()
			continue
		}
		out[key] = p.containers(list)
	}
}

func (p *projector) containers(list []any) []any {
	out := make([]any, 0, min(len(list), maxContainers))
	for _, v := range list {
		c, ok := v.(map[string]any)
		if !ok {
			p.drop()
			continue
		}
		if len(out) >= maxContainers {
			p.truncated = true
			break
		}
		o := map[string]any{}
		p.copyStrings(c, o, "name", "image")
		p.dropOthers(c, "name", "image")
		out = append(out, o)
	}
	return out
}

func (p *projector) status(st map[string]any) map[string]any {
	out := map[string]any{}
	p.copyNumbers(st, out, "replicas", "readyReplicas", "availableReplicas")
	p.copyStrings(st, out, "phase")
	if v, present := st["conditions"]; present {
		if list, ok := v.([]any); ok {
			out["conditions"] = p.conditions(list)
		} else {
			p.drop()
		}
	}
	p.dropOthers(st, "replicas", "readyReplicas", "availableReplicas", "phase", "conditions")
	return out
}

func (p *projector) conditions(list []any) []any {
	out := make([]any, 0, min(len(list), maxConditions))
	for _, v := range list {
		c, ok := v.(map[string]any)
		if !ok {
			p.drop()
			continue
		}
		if len(out) >= maxConditions {
			p.truncated = true
			break
		}
		o := map[string]any{}
		p.copyStrings(c, o, "type", "status", "reason", "message", "lastTransitionTime")
		p.dropOthers(c, "type", "status", "reason", "message", "lastTransitionTime")
		out = append(out, o)
	}
	return out
}

// shrink performs one deterministic size reduction on a projection and
// reports whether it did anything. The caller re-measures after each step.
func (p *projector) shrink(out map[string]any) bool {
	if halveList(mapAt(out, "status"), "conditions") {
		return true
	}
	md := mapAt(out, "metadata")
	if halveMap(md, "labels") || halveMap(md, "annotations") || halveList(md, "ownerReferences") {
		return true
	}
	shrunk := false
	for _, path := range podSpecPaths {
		ps := mapAt(out, path...)
		if halveList(ps, "containers") {
			shrunk = true
		}
		if halveList(ps, "initContainers") {
			shrunk = true
		}
	}
	if shrunk {
		return true
	}
	if p.bound > minFieldBytes {
		p.bound /= 2
		retruncate(out, p.bound)
		return true
	}
	return false
}

// podSpecPaths are the pod-spec locations the projection carries containers
// for, relative to the projection root.
var podSpecPaths = [][]string{
	{"spec"},
	{"spec", "template", "spec"},
	{"spec", "jobTemplate", "spec", "template", "spec"},
}

func mapAt(m map[string]any, path ...string) map[string]any {
	for _, key := range path {
		if m == nil {
			return nil
		}
		next, _ := m[key].(map[string]any)
		m = next
	}
	return m
}

// halveList keeps the first half of the list at m[key]; an emptied list is
// removed. Reports whether anything changed.
func halveList(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	list, ok := m[key].([]any)
	if !ok || len(list) == 0 {
		delete(m, key)
		return ok
	}
	m[key] = list[:len(list)/2]
	return true
}

// halveMap keeps the first half of the map at m[key] in sorted-key order; an
// emptied map is removed. Reports whether anything changed.
func halveMap(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	sm, ok := m[key].(map[string]any)
	if !ok || len(sm) == 0 {
		delete(m, key)
		return ok
	}
	keys := make([]string, 0, len(sm))
	for k := range sm {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys[len(keys)/2:] {
		delete(sm, k)
	}
	return true
}

// retruncate cuts every string in a projection to bound. The projection is
// this package's own shape (maps, lists, scalars) of fixed depth, so the
// recursion is bounded by construction.
func retruncate(v any, bound int) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if s, ok := e.(string); ok {
				x[k] = truncateRunes(s, bound)
			} else {
				retruncate(e, bound)
			}
		}
	case []any:
		for i, e := range x {
			if s, ok := e.(string); ok {
				x[i] = truncateRunes(s, bound)
			} else {
				retruncate(e, bound)
			}
		}
	}
}

// referencesSecret scans the ORIGINAL object (never the projection) for any
// Secret reference in any pod-spec location. A malformed sibling is skipped,
// never fatal, and never clears a detection made elsewhere.
func referencesSecret(obj map[string]any) bool {
	for _, path := range podSpecPaths {
		ps := mapAt(obj, path...)
		if ps == nil {
			continue
		}
		if present(ps, "imagePullSecrets") {
			return true
		}
		for _, key := range []string{"containers", "initContainers", "ephemeralContainers"} {
			for _, c := range listOfMaps(ps[key]) {
				for _, e := range listOfMaps(c["env"]) {
					if vf, _ := e["valueFrom"].(map[string]any); present(vf, "secretKeyRef") {
						return true
					}
				}
				for _, e := range listOfMaps(c["envFrom"]) {
					if present(e, "secretRef") {
						return true
					}
				}
			}
		}
		for _, vol := range listOfMaps(ps["volumes"]) {
			if present(vol, "secret") {
				return true
			}
			if proj, _ := vol["projected"].(map[string]any); proj != nil {
				for _, src := range listOfMaps(proj["sources"]) {
					if present(src, "secret") {
						return true
					}
				}
			}
		}
	}
	return false
}

// present reports whether m[key] exists with a value that references
// something: not nil, and not an empty list. Any other value, including a
// wrong-typed one, counts as a reference (over-marking is the safe side).
func present(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	if list, isList := v.([]any); isList && len(list) == 0 {
		return false
	}
	return true
}

// listOfMaps returns the map elements of v when v is a list; nil otherwise.
func listOfMaps(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// isNumber reports whether v is a numeric value json.Marshal will accept.
// Non-finite floats are rejected: they do not marshal, and letting one
// through would collapse the whole projection via the size ladder.
func isNumber(v any) bool {
	switch x := v.(type) {
	case float64:
		return !math.IsNaN(x) && !math.IsInf(x, 0)
	case float32:
		return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case json.Number:
		f, err := x.Float64()
		return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return false
}
