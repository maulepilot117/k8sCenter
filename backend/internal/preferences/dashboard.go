package preferences

// dashboard.go -- validation for personal dashboard layouts (Release G).
//
// Kept beside types.go and under the same rule: pure, no net/http. Split into
// its own file because types.go is already the home of two kinds and a third
// would bury all three.
//
// The containment discipline is types.go's: nothing the caller sent is stored.
// The validator re-marshals its own typed struct, so an unlisted field cannot
// survive a round trip even if a future decoder stops rejecting it.

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// DashboardLayoutSchemaVersion is the only envelope version this release
	// accepts. A stored record carrying anything else is schema drift and is
	// surfaced to the caller rather than coerced.
	DashboardLayoutSchemaVersion = 1

	// dashboardColumns is the fixed grid width. Stored in the config as well
	// so a future change is detectable in existing rows rather than silently
	// reinterpreting them.
	dashboardColumns = 12

	maxDashboardItems = 40
	maxWidgetIDLen    = 64
	maxInstanceIDLen  = 64

	maxDashboardParams = 8
	maxParamKeyLen     = 32
	maxParamValueLen   = 253

	// maxDashboardRows bounds vertical growth. A row is 40px on the client, so
	// 200 rows is an 8000px dashboard -- far past anything usable, which is
	// the point: it is a containment bound, not a design limit.
	maxDashboardRows = 200
)

// allowedDashboardScopes is the set of dashboards a layout can target. P6 adds
// more; each addition here must also be added to DASHBOARD_SCOPES in
// frontend/lib/dashboard/types.ts.
var allowedDashboardScopes = map[string]struct{}{"overview": {}}

// widgetSpec is the server's copy of what a widget will accept.
//
// It carries the two rules spec §7 states the server must enforce and that an
// id alone cannot express: the widget's minimum size, and the parameters it
// declares. Both have to live here rather than being read from the registry,
// because the registry is TypeScript.
type widgetSpec struct {
	// MinW and MinH mirror the registry's minW/minH -- "smallest the editor
	// will let the user resize this widget". The editor refuses to drag below
	// them; without them here, a request that bypasses the editor does not.
	MinW int
	MinH int

	// Params is the widget's declared parameter surface: key -> the closed set
	// of values that key accepts. A nil or empty map means the widget takes no
	// parameters at all, which is every widget shipped today, so any param on
	// any of them is a client bug rather than a value to range-check.
	//
	// An empty value slice means "any value within the generic bounds" -- for
	// a parameter whose legal values are not knowable ahead of time (a
	// namespace name). That is deliberately NOT the same as an absent key.
	Params map[string][]string
}

// allowedWidgets is the server-side half of the widget catalog.
//
// The registry itself is TypeScript and unreadable from here, so the two are
// kept honest by a pinned test on each side -- the same discipline
// parity_test.go already applies to the saved-view allowlists. Adding a widget
// means adding it here AND to the registry, and forgetting either fails a test
// rather than producing a layout the server silently refuses.
//
// The minimums must equal the registry's. TestContractParity pins every pair
// to a literal and names the widget file to edit, so a size changed on one
// side and not the other is a red test rather than a layout the editor builds
// and the server rejects.
//
// Ids are never removed from this map when a widget is retired. A retired id
// must stay acceptable on read so a stored layout is not bricked; the client
// drops it with a notice (spec D-7). A retired entry keeps whatever minimums
// it shipped with, because stored layouts still carry placements sized to them.
var allowedWidgets = map[string]widgetSpec{
	"cluster-health":       {MinW: 3, MinH: 4},
	"cpu-tile":             {MinW: 2, MinH: 2},
	"memory-tile":          {MinW: 2, MinH: 2},
	"pods-tile":            {MinW: 2, MinH: 2},
	"network-tile":         {MinW: 2, MinH: 2},
	"resource-utilization": {MinW: 4, MinH: 4},
	"pod-status":           {MinW: 3, MinH: 4},
	"nodes":                {MinW: 3, MinH: 4},
	"recent-events":        {MinW: 3, MinH: 3},
	"active-alerts":        {MinW: 2, MinH: 3},
	"workload-health":      {MinW: 3, MinH: 3},
	"pending-pods":         {MinW: 3, MinH: 3},
	"pod-restarts":         {MinW: 3, MinH: 3},
	"hpa-status":           {MinW: 3, MinH: 3},
	"pdb-risk":             {MinW: 3, MinH: 3},
	"quota-pressure":       {MinW: 3, MinH: 3},
	// Wider than its neighbours for the same reason top-consumers is: a row
	// carries a node name plus up to four condition chips, and a node name is
	// routinely `ip-10-0-42-118.eu-west-1.compute.internal`.
	"node-conditions": {MinW: 4, MinH: 3},
	// Wider AND taller: the card stacks a StorageClass inventory above a
	// ranked volume list, so it needs the height for two sections and the
	// width for a claim name beside its namespace.
	"storage-capacity": {MinW: 4, MinH: 4},
	// Wider than its neighbours: each row carries a pod name, its namespace
	// and a value, and three columns inside three grid columns truncate the
	// names to uselessness. The card's CPU/memory switch is NOT a parameter
	// -- it is component state, so nothing about it is stored and there is
	// nothing here to validate.
	"top-consumers": {MinW: 4, MinH: 4},
	// The first parameterized widget. The empty value slice is load-bearing
	// and is NOT the same as omitting the key: it says the legal values are
	// not knowable from a catalog -- they are whatever namespaces this cluster
	// has -- so only the generic length and control-character bounds apply.
	//
	// The key is `paramKeyNamespace` exactly, which is what makes a stored
	// value re-authorized on every read (see the const's docstring and
	// `withholdUnauthorized`). Any other spelling would store and serve
	// identically while silently opting the widget out of that.
	"diagnostics-summary": {
		MinW:   3,
		MinH:   3,
		Params: map[string][]string{paramKeyNamespace: {}},
	},
	// The security family. All three read CRD-discovered features and declare
	// a discovery status client-side, which is a render-contract concern and
	// therefore invisible here -- this map validates placements, not sources.
	//
	// The compliance card is taller than it is wide because it stacks a gauge
	// over a pass/fail row; the other two are wider because each row carries a
	// resource name beside a cluster of severity chips.
	"policy-compliance": {MinW: 3, MinH: 4},
	"policy-violations": {MinW: 4, MinH: 3},
	// The second parameterized widget, and the first whose parameter is
	// MANDATORY rather than a scoping choice: the backing route
	// (`/v1/scanning/vulnerabilities`) answers 400 without `?namespace=`, so
	// there is no unparameterized form of this card to fall back to. Same key
	// and same empty value slice as diagnostics-summary above, for the same
	// reasons -- see that entry's comment.
	"vulnerability-severity": {
		MinW:   4,
		MinH:   3,
		Params: map[string][]string{paramKeyNamespace: {}},
	},
	// The data-protection family. Like the security three above, all four read
	// CRD-discovered features and declare a discovery status client-side --
	// invisible here, because this map validates placements, not sources.
	//
	// All four are four columns wide for the same reason: each row carries a
	// resource name beside a state badge and an attribution line, and a
	// certificate, ExternalSecret, Velero backup or VolumeSnapshot name is
	// routinely long enough (`daily-full-20260920010000`) that three columns
	// truncate it to uselessness. None of them takes parameters: every backing
	// route is cluster-wide and already RBAC-filtered, so there is no scope
	// for the user to choose.
	"certs-expiring":  {MinW: 4, MinH: 3},
	"eso-health":      {MinW: 4, MinH: 3},
	"velero-backups":  {MinW: 4, MinH: 3},
	"snapshot-health": {MinW: 4, MinH: 3},
}

// MaxDashboardLayoutsPerUser is the per-user, per-cluster ceiling. One layout
// per scope and the scope set is closed, so the ceiling IS the size of that
// set -- deriving it means adding a scope cannot leave a stale constant behind
// that refuses the new layout.
//
// "Per cluster" is load-bearing and is the caller's job to honour: the handler
// saves through PreferenceStore.CreateInCluster, whose count is taken inside
// one cluster. Passing this number to plain Create instead would count every
// cluster's rows against a ceiling sized for one, and the user's first layout
// anywhere would refuse their first layout everywhere else.
var MaxDashboardLayoutsPerUser = len(allowedDashboardScopes)

// paramKeyNamespace is the param key whose value names a Kubernetes namespace.
//
// It is a single well-known key rather than a per-widget declaration because
// the read path has to recognise a namespace without knowing which widget it
// came from: a parameter naming a namespace is re-authorized on every read
// (spec 7), and a widget that spelled the key differently would quietly opt
// out of that. A widget declaring this key in its spec is declaring that its
// value is a namespace.
const paramKeyNamespace = "namespace"

// SaveLayoutRequest is the wire shape for PUT /preferences/layouts/{scope}.
//
// It carries no name and no scope, for the reason CreateRequest carries no
// ownerId: a request type that cannot express a server-derived value is a
// stronger guarantee than a handler that remembers to ignore one. The scope is
// the path, the name is the scope, the owner is the session and the cluster is
// the middleware's. The decoder rejects unknown fields, so a client that sends
// any of them is told which one rather than having it silently dropped.
//
// Revision drives the optimistic-concurrency check. Zero means "I believe no
// layout exists here", which is a claim like any other and is refused when it
// turns out to be wrong.
type SaveLayoutRequest struct {
	Revision int64           `json:"revision"`
	Config   json.RawMessage `json:"config"`
}

// DashboardLayoutItem is one widget placement inside a stored layout.
//
// InstanceID exists because a parameterized widget may legitimately appear
// twice -- one filtered to prod beside one filtered to staging -- so identity
// cannot be the widget id.
type DashboardLayoutItem struct {
	InstanceID string            `json:"instanceId"`
	ID         string            `json:"id"`
	X          int               `json:"x"`
	Y          int               `json:"y"`
	W          int               `json:"w"`
	H          int               `json:"h"`
	Params     map[string]string `json:"params,omitempty"`
}

// DashboardLayoutConfig is the stored envelope for one user's arrangement of
// one dashboard.
type DashboardLayoutConfig struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Scope         string                `json:"scope"`
	Columns       int                   `json:"columns"`
	Items         []DashboardLayoutItem `json:"items"`
}

// DashboardLayoutDedupKey folds a layout to its identity. The scope IS the
// identity: one layout per owner per cluster per scope, enforced by the unique
// index migration 000018 already created.
func DashboardLayoutDedupKey(scope string) string {
	return scope
}

// ValidateDashboardLayout decodes, validates and normalizes a dashboard-layout
// envelope. It returns the typed config and the exact bytes to store -- a
// re-marshal of the typed struct, never the caller's input.
func ValidateDashboardLayout(raw json.RawMessage) (DashboardLayoutConfig, json.RawMessage, error) {
	var cfg DashboardLayoutConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, nil, invalidf("invalid_config", "config is not a valid dashboard-layout object")
	}

	if cfg.SchemaVersion != DashboardLayoutSchemaVersion {
		return cfg, nil, invalidf("unsupported_schema_version",
			"unsupported schemaVersion %d; this server accepts %d",
			cfg.SchemaVersion, DashboardLayoutSchemaVersion)
	}
	if _, ok := allowedDashboardScopes[cfg.Scope]; !ok {
		return cfg, nil, invalidf("invalid_config", "unsupported scope %q", cfg.Scope)
	}
	if cfg.Columns != dashboardColumns {
		return cfg, nil, invalidf("invalid_config",
			"columns is %d; this server lays out %d", cfg.Columns, dashboardColumns)
	}
	if len(cfg.Items) > maxDashboardItems {
		return cfg, nil, invalidf("invalid_config",
			"layout has %d widgets; the maximum is %d", len(cfg.Items), maxDashboardItems)
	}

	seenInstance := make(map[string]struct{}, len(cfg.Items))
	seenIdentity := make(map[string]struct{}, len(cfg.Items))

	for i, it := range cfg.Items {
		if it.InstanceID == "" || utf8.RuneCountInString(it.InstanceID) > maxInstanceIDLen ||
			!isDNS1123Subdomain(it.InstanceID) {
			return cfg, nil, invalidf("invalid_config",
				"items[%d].instanceId is not a valid identifier", i)
		}
		if _, dup := seenInstance[it.InstanceID]; dup {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] repeats instanceId %q", i, it.InstanceID)
		}
		seenInstance[it.InstanceID] = struct{}{}

		if utf8.RuneCountInString(it.ID) > maxWidgetIDLen {
			return cfg, nil, invalidf("invalid_config", "items[%d].id is too long", i)
		}
		// A write carrying an unknown id is a client bug, not a stored-layout
		// problem, so it is refused with the field named (spec D-7). Reads are
		// the forgiving direction and are the client's job.
		spec, ok := allowedWidgets[it.ID]
		if !ok {
			return cfg, nil, invalidf("unknown_widget_id",
				"items[%d].id %q is not a widget this server knows", i, it.ID)
		}

		// Geometry is checked one component at a time, and nothing is added to
		// anything else until both operands are known to be in range.
		//
		// This ordering is the whole point, not style. `x+w` on unchecked ints
		// wraps: x=math.MaxInt64 with w=1 folds to a large NEGATIVE, which
		// sails through a naive "x+w > columns" test and stores a widget
		// positioned quintillions of columns off a twelve-column grid. Once
		// each component is bounded by the grid, every sum below is safe --
		// including the ones in the error messages and in itemsOverlap.
		// Sizes are bounded from below by the widget's own declared minimum,
		// not merely by 1. The editor will not let a user drag below it, so a
		// smaller placement can only arrive from a client that bypassed the
		// editor -- and it would render the widget below the size it was
		// designed for on every subsequent load (spec §7, `w < minW`).
		//
		// The absolute floor of 1 is kept independently of the catalog. A
		// zero-area placement is meaningless whatever a spec claims, and this
		// is also what keeps the sums below un-wrappable: without it, a
		// catalog entry that someone gave a zero or negative minimum would
		// silently reopen that hole in a file nowhere near this one.
		minW, minH := spec.MinW, spec.MinH
		if minW < 1 {
			minW = 1
		}
		if minH < 1 {
			minH = 1
		}
		if it.W < minW || it.W > cfg.Columns {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] is %d columns wide; %s accepts %d-%d",
				i, it.W, it.ID, minW, cfg.Columns)
		}
		if it.H < minH || it.H > maxDashboardRows {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] is %d rows tall; %s accepts %d-%d",
				i, it.H, it.ID, minH, maxDashboardRows)
		}
		// Positions are stated inclusively, because that is how a reader counts
		// cells: with 12 columns the legal starts are 0-11, and a widget at
		// x=10 w=3 occupies 10, 11 and 12. Printing the exclusive end would
		// name a column that is not legal and a cell the widget does not hold.
		if it.X < 0 || it.X >= cfg.Columns {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] starts at column %d, outside 0-%d", i, it.X, cfg.Columns-1)
		}
		if it.X+it.W > cfg.Columns {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] spans columns %d-%d, outside 0-%d",
				i, it.X, it.X+it.W-1, cfg.Columns-1)
		}
		if it.Y < 0 || it.Y >= maxDashboardRows {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] starts at row %d, outside 0-%d", i, it.Y, maxDashboardRows-1)
		}
		if it.Y+it.H > maxDashboardRows {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] spans rows %d-%d, outside 0-%d",
				i, it.Y, it.Y+it.H-1, maxDashboardRows-1)
		}

		if len(it.Params) > maxDashboardParams {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] carries %d params; the maximum is %d", i, len(it.Params), maxDashboardParams)
		}
		// A widget that declares no parameters accepts none. Every widget
		// shipped today is in that case, so this is the whole param surface
		// for now: params on any of them are a client bug, and refusing them
		// is what keeps caller-chosen text out of the stored column entirely
		// rather than merely length-bounded (spec §7, "no executable queries,
		// no URLs").
		if len(spec.Params) == 0 && len(it.Params) > 0 {
			return cfg, nil, invalidf("invalid_config",
				"items[%d]: %s takes no parameters", i, it.ID)
		}
		for k, v := range it.Params {
			// Generic bounds first, so an unparseable key is reported as such
			// rather than as an unknown one.
			if k == "" || utf8.RuneCountInString(k) > maxParamKeyLen || hasControlChars(k) {
				return cfg, nil, invalidf("invalid_config", "items[%d] has an invalid param name", i)
			}
			if utf8.RuneCountInString(v) > maxParamValueLen || hasControlChars(v) {
				return cfg, nil, invalidf("invalid_config",
					"items[%d] param %q has an invalid value", i, k)
			}
			// The empty string is not a value. It passed every bound above,
			// and three layers below here read it differently: the read path
			// treats a namespace of "" as naming no namespace and skips the
			// re-authorization the key exists for, the browser's key encoding
			// drops the pair so the placement collapses onto the unscoped
			// source, and the fetcher issues a path with an empty segment
			// that the resource layer accepts as "every namespace". The
			// dialog cannot produce this, but the dialog is not what makes it
			// safe.
			if v == "" {
				return cfg, nil, invalidf("invalid_config",
					"items[%d] param %q has an invalid value", i, k)
			}

			// Then the widget's own declaration. No URLs, no queries, no
			// scripts -- KTD4 -- is enforced by the value being drawn from a
			// closed set the widget named, not by pattern-matching what a
			// dangerous value looks like.
			allowed, declared := spec.Params[k]
			if !declared {
				return cfg, nil, invalidf("invalid_config",
					"items[%d]: %s does not take a parameter named %q", i, it.ID, k)
			}
			// An empty value set means the legal values are not knowable here
			// (a namespace name), so the generic bounds above are the whole
			// check. A non-empty set is closed.
			if len(allowed) > 0 && !containsString(allowed, v) {
				return cfg, nil, invalidf("invalid_config",
					"items[%d]: %q is not a value %s accepts for %q", i, v, it.ID, k)
			}
		}

		// The loop above checks every parameter that was sent. This checks
		// the other direction -- that a widget declaring a parameter actually
		// carries one -- which nothing did, so a placement naming a namespace
		// and omitting the service it also declares was stored happily and
		// then failed on every read for the life of the layout. The browser
		// refuses the same case before it sends; this is its missing twin.
		//
		// Every declared key is mandatory today, which is true of all four
		// parameterized widgets: their backing routes refuse without the
		// value. An optional parameter would need a per-key flag rather than
		// this blanket rule.
		for k := range spec.Params {
			if _, ok := it.Params[k]; !ok {
				return cfg, nil, invalidf("invalid_config",
					"items[%d]: %s requires a value for %q", i, it.ID, k)
			}
		}

		// Two placements of the same widget with the same params are a
		// duplicate; with different params they are two legitimate views
		// (prod beside staging), which is why instanceId exists.
		identity := it.ID + "\x00" + canonicalParams(it.Params)
		if _, dup := seenIdentity[identity]; dup {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] duplicates an identical widget already in the layout", i)
		}
		seenIdentity[identity] = struct{}{}
	}

	// Overlap is checked after every item is individually sound, so the error
	// a caller sees names the real first problem.
	for a := range cfg.Items {
		for b := a + 1; b < len(cfg.Items); b++ {
			if itemsOverlap(cfg.Items[a], cfg.Items[b]) {
				return cfg, nil, invalidf("invalid_config",
					"items[%d] and items[%d] overlap", a, b)
			}
		}
	}

	// A layout with no items is a real state (the user cleared the dashboard),
	// but a nil slice and an empty one are not interchangeable here: Items
	// carries no omitempty, so nil marshals to `"items":null` while an empty
	// slice marshals to `"items":[]`. The TypeScript mirror declares
	// `items: LayoutItem[]` -- required and non-nullable -- so a stored null
	// is a value every client consumer is entitled to assume cannot occur, and
	// iterating it throws on the user's own saved layout at every load.
	//
	// Normalizing here rather than rejecting keeps `len(cfg.Items) == 0`
	// meaning exactly what it meant above, so no check changes behaviour.
	if cfg.Items == nil {
		cfg.Items = []DashboardLayoutItem{}
	}

	normalized, err := json.Marshal(cfg)
	if err != nil {
		return cfg, nil, invalidf("invalid_config", "config could not be normalized")
	}
	return cfg, normalized, nil
}

// containsString reports whether v is in the closed set vs. The sets are a
// widget's declared parameter values -- a handful of short strings -- so a
// scan is the right shape and keeps the catalog literal readable.
func containsString(vs []string, v string) bool {
	for _, s := range vs {
		if s == v {
			return true
		}
	}
	return false
}

// itemsOverlap reports whether two placements share any cell. Adjacency is not
// overlap: a packed grid is mostly widgets sitting edge to edge.
//
// The sums here cannot overflow because every caller runs after the per-item
// geometry checks above have bounded x, y, w and h to the grid. Moving this
// call earlier would reintroduce the wrap those checks exist to prevent.
func itemsOverlap(a, b DashboardLayoutItem) bool {
	return !(a.X+a.W <= b.X || b.X+b.W <= a.X || a.Y+a.H <= b.Y || b.Y+b.H <= a.Y)
}

// canonicalParams renders a param map order-independently so two placements
// that differ only in map iteration order compare equal.
//
// The result is the duplicate-detection identity, so it has to be injective:
// two different maps must never fold to one string, or a legitimate second
// placement is refused as a duplicate of the first.
//
// Each key and value is length-prefixed rather than separated by a delimiter,
// which makes that hold for ANY bytes. A delimiter only works while no input
// can contain it, and that is a precondition living in another function:
// '=' was the original choice and it made {"a=b":"c"} and {"a":"b=c"} identical,
// while NUL merely moves the same assumption onto hasControlChars running
// first. Length prefixes depend on nothing, so this stays correct if the
// param rules above are ever loosened.
func canonicalParams(p map[string]string) string {
	if len(p) == 0 {
		return ""
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	writeLenPrefixed := func(s string) {
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
	}
	for _, k := range keys {
		writeLenPrefixed(k)
		writeLenPrefixed(p[k])
	}
	return b.String()
}
