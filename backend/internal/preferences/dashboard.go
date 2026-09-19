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

// allowedWidgetIDs is the server-side half of the widget catalog.
//
// The registry itself is TypeScript and unreadable from here, so the two are
// kept honest by a pinned-list test on each side -- the same discipline
// parity_test.go already applies to the saved-view allowlists. Adding a widget
// means adding it here AND to the registry, and forgetting either fails a test
// rather than producing a layout the server silently refuses.
//
// Ids are never removed from this map when a widget is retired. A retired id
// must stay acceptable on read so a stored layout is not bricked; the client
// drops it with a notice (spec D-7).
var allowedWidgetIDs = map[string]struct{}{
	"cluster-health": {}, "cpu-tile": {}, "memory-tile": {}, "pods-tile": {},
	"network-tile": {}, "resource-utilization": {}, "pod-status": {},
	"nodes": {}, "recent-events": {}, "active-alerts": {},
}

// MaxDashboardLayoutsPerUser is the per-user, per-cluster ceiling the store
// enforces inside its INSERT. One layout per scope and the scope set is
// closed, so the ceiling IS the size of that set -- deriving it means adding a
// scope cannot leave a stale constant behind that refuses the new layout.
var MaxDashboardLayoutsPerUser = len(allowedDashboardScopes)

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
		if _, ok := allowedWidgetIDs[it.ID]; !ok {
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
		if it.W < 1 || it.W > cfg.Columns {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] is %d columns wide; the range is 1-%d", i, it.W, cfg.Columns)
		}
		if it.H < 1 || it.H > maxDashboardRows {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] is %d rows tall; the range is 1-%d", i, it.H, maxDashboardRows)
		}
		if it.X < 0 || it.X > cfg.Columns {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] starts at column %d, outside 0-%d", i, it.X, cfg.Columns)
		}
		if it.X+it.W > cfg.Columns {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] spans columns %d-%d, outside 0-%d", i, it.X, it.X+it.W, cfg.Columns)
		}
		if it.Y < 0 || it.Y > maxDashboardRows {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] starts at row %d, outside 0-%d", i, it.Y, maxDashboardRows)
		}
		if it.Y+it.H > maxDashboardRows {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] spans rows %d-%d, outside 0-%d", i, it.Y, it.Y+it.H, maxDashboardRows)
		}

		if len(it.Params) > maxDashboardParams {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] carries %d params; the maximum is %d", i, len(it.Params), maxDashboardParams)
		}
		for k, v := range it.Params {
			// No URLs, no queries, no scripts -- KTD4. Params are short,
			// control-free scalars and nothing else.
			if k == "" || utf8.RuneCountInString(k) > maxParamKeyLen || hasControlChars(k) {
				return cfg, nil, invalidf("invalid_config", "items[%d] has an invalid param name", i)
			}
			if utf8.RuneCountInString(v) > maxParamValueLen || hasControlChars(v) {
				return cfg, nil, invalidf("invalid_config",
					"items[%d] param %q has an invalid value", i, k)
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

	normalized, err := json.Marshal(cfg)
	if err != nil {
		return cfg, nil, invalidf("invalid_config", "config could not be normalized")
	}
	return cfg, normalized, nil
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
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(p[k])
		b.WriteByte('\x00')
	}
	return b.String()
}
