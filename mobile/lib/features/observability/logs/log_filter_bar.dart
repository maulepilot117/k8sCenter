// LogQL editor's form surface — cascading namespace/pod/container
// dropdowns, severity chips, free-text contains, mode toggle
// (search/logql), and a Run button. Builds the LogQL string on
// submit so the controller doesn't have to thread filter state
// alongside the query body.
//
// Cascading dropdown timing (mirrors `frontend/islands/LogFilterBar.tsx`):
//   * On mount, fetch namespace values from `/v1/logs/labels/namespace/values`.
//   * When namespace changes, fetch pod values scoped by that namespace.
//   * When pod changes, fetch container values scoped by namespace+pod.
//   * Switching modes preserves form state — search → LogQL seeds the
//     raw text with the constructed query so the operator can edit it
//     directly; LogQL → search keeps the form fields as they were
//     (the LogQL-mode raw edits drop, by design — operators who want
//     to keep their raw edits can stay in LogQL mode).
//
// Admin gate: non-admin users MUST pick a namespace before the Run
// button enables. Admins see an "All namespaces" option which fires
// a cluster-wide query (backend `enforceQueryNamespaces` permits this
// path for admins only).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/loki_repository.dart';
import '../../../auth/auth_repository.dart';
import '../../../auth/auth_state.dart';
import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import '../../../widgets/time_range_picker.dart';
import 'log_search_controller.dart';

/// Severity filter values mirrored from web's LogFilterBar — the
/// backend's `level` label uses a case-insensitive regex match so
/// the values stay lowercase here and the build path emits the
/// `(?i)` regex flag.
enum LogSeverity { error, warn, info, debug }

extension on LogSeverity {
  String get label => switch (this) {
        LogSeverity.error => 'Error',
        LogSeverity.warn => 'Warn',
        LogSeverity.info => 'Info',
        LogSeverity.debug => 'Debug',
      };

  String get value => switch (this) {
        LogSeverity.error => 'error',
        LogSeverity.warn => 'warn',
        LogSeverity.info => 'info',
        LogSeverity.debug => 'debug',
      };
}

/// Callback invoked when the operator presses Run on the filter bar.
typedef LogSearchSubmit = void Function(LogSearchParams params);

class LogFilterBar extends ConsumerStatefulWidget {
  const LogFilterBar({
    required this.onSubmit,
    this.initialNamespace,
    this.inFlight = false,
    super.key,
  });

  /// Wired to [LogSearchController.submit] from the screen.
  final LogSearchSubmit onSubmit;

  /// Optional seed when the screen receives a namespace deep-link
  /// (e.g. notification opens "view logs in namespace X").
  final String? initialNamespace;

  /// `true` while the parent controller is still resolving a prior
  /// submission. Disables the Run button so rapid taps cannot stack
  /// concurrent fetches at the backend — cancellation handles the
  /// already-fired requests correctly, but the bandwidth + Loki load
  /// from 10 supersedes per second is wasted regardless.
  final bool inFlight;

  @override
  ConsumerState<LogFilterBar> createState() => _LogFilterBarState();
}

class _LogFilterBarState extends ConsumerState<LogFilterBar> {
  LogQueryMode _mode = LogQueryMode.search;
  String? _namespace;
  String? _pod;
  String? _container;
  // PR-5f: explicit admin-only toggle that emits a query without the
  // namespace selector. The dropdown's null option still works for
  // backwards-compat; this checkbox is the discoverable surface.
  bool _allNamespaces = false;
  LogSeverity? _severity;
  String _freeText = '';
  String _rawLogql = '';
  TimeRange _range = timeRangeFromPreset(TimePreset.last1h);
  final TextEditingController _freeTextCtrl = TextEditingController();
  final TextEditingController _rawCtrl = TextEditingController();

  List<String> _namespaces = const [];
  List<String> _pods = const [];
  List<String> _containers = const [];

  bool _loadingNamespaces = false;
  bool _loadingPods = false;
  bool _loadingContainers = false;

  @override
  void initState() {
    super.initState();
    _namespace = widget.initialNamespace;
    WidgetsBinding.instance.addPostFrameCallback((_) => _fetchNamespaces());
    if (_namespace != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _fetchPods());
    }
  }

  @override
  void dispose() {
    _freeTextCtrl.dispose();
    _rawCtrl.dispose();
    super.dispose();
  }

  bool get _isAdmin {
    // ref.watch — not ref.read — so a mid-session auth state change
    // (token refresh, role promotion via OIDC re-auth) rebuilds the
    // filter bar with the new admin posture rather than leaving a
    // stale gate.
    final auth = ref.watch(authRepositoryProvider);
    if (auth is AuthAuthenticated) return auth.user.isAdmin;
    return false;
  }

  Future<void> _fetchNamespaces() async {
    if (!mounted) return;
    setState(() => _loadingNamespaces = true);
    final clusterId = ref.read(activeClusterProvider);
    try {
      final values = await ref
          .read(lokiRepositoryProvider)
          .labelValues(name: 'namespace', clusterIdOverride: clusterId);
      if (!mounted) return;
      setState(() {
        _namespaces = values;
      });
    } finally {
      // Reset the loading flag even on non-graceful errors (401, 500,
      // network timeout). Without this, the repository's narrow
      // 403/502/503 soft-degrade leaves the dropdown stuck on
      // "Loading…" indefinitely for any other failure mode.
      if (mounted) {
        setState(() => _loadingNamespaces = false);
      }
    }
  }

  Future<void> _fetchPods() async {
    final ns = _namespace;
    if (ns == null || ns.isEmpty) {
      setState(() {
        _pods = const [];
        _containers = const [];
      });
      return;
    }
    if (!mounted) return;
    setState(() => _loadingPods = true);
    final clusterId = ref.read(activeClusterProvider);
    try {
      final values = await ref.read(lokiRepositoryProvider).labelValues(
            name: 'pod',
            namespace: ns,
            scopeQuery: '{namespace="$ns"}',
            clusterIdOverride: clusterId,
          );
      if (!mounted) return;
      // Cascade race-protection: if the operator switched namespace
      // between when we issued the labelValues call and when its
      // response arrived, drop this result on the floor — the newer
      // namespace's fetch (already in flight or queued) is authoritative.
      if (_namespace != ns) return;
      setState(() {
        _pods = values;
        _containers = const [];
      });
    } finally {
      if (mounted && _namespace == ns) {
        setState(() => _loadingPods = false);
      }
    }
  }

  Future<void> _fetchContainers() async {
    final ns = _namespace;
    final pod = _pod;
    if (ns == null || ns.isEmpty || pod == null || pod.isEmpty) {
      setState(() => _containers = const []);
      return;
    }
    if (!mounted) return;
    setState(() => _loadingContainers = true);
    final clusterId = ref.read(activeClusterProvider);
    try {
      final values = await ref.read(lokiRepositoryProvider).labelValues(
            name: 'container',
            namespace: ns,
            scopeQuery: '{namespace="$ns",pod="$pod"}',
            clusterIdOverride: clusterId,
          );
      if (!mounted) return;
      // Same cascade race-protection as _fetchPods: a fast operator
      // who switches pod (or namespace) before the container fetch
      // returns must not see the stale list under the new selection.
      if (_namespace != ns || _pod != pod) return;
      setState(() {
        _containers = values;
      });
    } finally {
      if (mounted && _namespace == ns && _pod == pod) {
        setState(() => _loadingContainers = false);
      }
    }
  }

  /// Builds the LogQL query string from the current form state.
  /// In `logql` mode passes the raw textarea contents through.
  String _buildQuery() {
    if (_mode == LogQueryMode.logql) return _rawLogql;

    final matchers = <String>[];
    // The all-namespaces checkbox takes precedence — when on, the
    // namespace matcher is skipped regardless of what the dropdown
    // value happens to be (we also force it to null when the checkbox
    // toggles, but defending against drift is cheap).
    if (!_allNamespaces &&
        _namespace != null &&
        _namespace!.isNotEmpty) {
      matchers.add('namespace="${_namespace!}"');
    }
    if (_pod != null && _pod!.isNotEmpty) {
      matchers.add('pod=~"${_pod!}.*"');
    }
    if (_container != null && _container!.isNotEmpty) {
      matchers.add('container="${_container!}"');
    }
    var q = '{${matchers.join(',')}}';
    if (_severity != null) {
      q += ' | level=~"(?i)${_severity!.value}"';
    }
    if (_freeText.isNotEmpty) {
      q += ' |= "$_freeText"';
    }
    return q;
  }

  void _swapMode(LogQueryMode next) {
    if (next == _mode) return;
    setState(() {
      if (next == LogQueryMode.logql) {
        // Seed the raw textarea with the current built query so the
        // operator can edit it directly instead of starting from a
        // blank prompt.
        _rawLogql = _buildQuery();
        _rawCtrl.text = _rawLogql;
      } else {
        // Going back to search drops the raw edits; the structured
        // form fields stay intact so the operator's prior filters
        // are right where they left them.
        _rawLogql = '';
        _rawCtrl.text = '';
      }
      _mode = next;
    });
  }

  bool get _runEnabled {
    // While a prior submission is still in flight, gate further taps
    // so the operator can't stack concurrent queries with a fast double
    // tap on slow networks.
    if (widget.inFlight) return false;
    // Non-admin without a namespace can't submit — backend hard-403s
    // and the UX is clearer with a disabled button than a
    // post-submit error card.
    if (!_isAdmin && (_namespace == null || _namespace!.isEmpty)) {
      return false;
    }
    // LogQL mode requires non-empty query text.
    if (_mode == LogQueryMode.logql && _rawLogql.trim().isEmpty) {
      return false;
    }
    return true;
  }

  void _onRunPressed() {
    if (!_runEnabled) return;
    widget.onSubmit(LogSearchParams(
      namespace: _namespace,
      query: _buildQuery(),
      range: _range,
    ));
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: TimeRangePicker(
                  initial: _range,
                  onChanged: (r) => setState(() => _range = r),
                ),
              ),
              const SizedBox(width: 8),
              _ModeToggle(
                mode: _mode,
                onChanged: _swapMode,
              ),
            ],
          ),
          const SizedBox(height: 12),
          if (_mode == LogQueryMode.search) ...[
            if (_isAdmin)
              _AllNamespacesCheckbox(
                value: _allNamespaces,
                onChanged: (v) {
                  setState(() {
                    _allNamespaces = v;
                    if (v) {
                      // Clearing namespace and the cascaded selectors
                      // when switching to all-NS prevents a stale pod
                      // dropdown from leaking into the next un-toggle
                      // — the operator's intent is "ignore namespace
                      // scope", not "remember it under the toggle".
                      _namespace = null;
                      _pod = null;
                      _container = null;
                      _pods = const [];
                      _containers = const [];
                    }
                  });
                },
              ),
            if (_isAdmin) const SizedBox(height: 4),
            _NamespaceDropdown(
              namespaces: _namespaces,
              value: _namespace,
              loading: _loadingNamespaces,
              isAdmin: _isAdmin,
              enabled: !_allNamespaces,
              onChanged: (v) {
                setState(() {
                  _namespace = v;
                  _pod = null;
                  _container = null;
                });
                _fetchPods();
              },
            ),
            const SizedBox(height: 8),
            _PodDropdown(
              pods: _pods,
              value: _pod,
              loading: _loadingPods,
              enabled: _namespace != null && _namespace!.isNotEmpty,
              onChanged: (v) {
                setState(() {
                  _pod = v;
                  _container = null;
                });
                _fetchContainers();
              },
            ),
            const SizedBox(height: 8),
            _ContainerDropdown(
              containers: _containers,
              value: _container,
              loading: _loadingContainers,
              enabled: _pod != null && _pod!.isNotEmpty,
              onChanged: (v) => setState(() => _container = v),
            ),
            const SizedBox(height: 12),
            _SeverityChips(
              value: _severity,
              onChanged: (v) => setState(() => _severity = v),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _freeTextCtrl,
              key: const ValueKey('logFilter-freeText'),
              decoration: InputDecoration(
                labelText: 'Contains',
                hintText: 'e.g. timeout',
                border: const OutlineInputBorder(),
                isDense: true,
                fillColor: colors.bgElevated,
                filled: true,
              ),
              onChanged: (v) => setState(() => _freeText = v),
              onSubmitted: (_) => _onRunPressed(),
            ),
          ] else ...[
            // LogQL mode — single multiline textarea. Submit on Run
            // button only (no Enter shortcut since LogQL queries
            // legitimately span lines).
            TextField(
              controller: _rawCtrl,
              key: const ValueKey('logFilter-rawLogql'),
              maxLines: 4,
              minLines: 2,
              decoration: InputDecoration(
                labelText: 'LogQL',
                hintText:
                    '{namespace="app",pod=~"web-.*"} |= "error" | json',
                border: const OutlineInputBorder(),
                fillColor: colors.bgElevated,
                filled: true,
              ),
              style: const TextStyle(fontFamily: 'monospace', fontSize: 13),
              onChanged: (v) => setState(() => _rawLogql = v),
            ),
            const SizedBox(height: 8),
            Text(
              'Max ${kLokiMaxQueryChars.toString()} characters · '
              '${_rawLogql.length} used',
              style: TextStyle(color: colors.textMuted, fontSize: 11),
            ),
          ],
          const SizedBox(height: 12),
          Row(
            children: [
              if (!_isAdmin &&
                  (_namespace == null || _namespace!.isEmpty)) ...[
                Icon(Icons.info_outline, size: 14, color: colors.warning),
                const SizedBox(width: 4),
                Expanded(
                  child: Text(
                    'Pick a namespace to run a log query.',
                    style: TextStyle(color: colors.warning, fontSize: 12),
                  ),
                ),
              ] else
                const Spacer(),
              FilledButton.icon(
                key: const ValueKey('logFilter-runButton'),
                onPressed: _runEnabled ? _onRunPressed : null,
                icon: const Icon(Icons.search, size: 18),
                label: const Text('Run'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Private subwidgets — kept inline so the filter-bar layout is readable
// without jumping between files.
// ---------------------------------------------------------------------------

class _ModeToggle extends StatelessWidget {
  const _ModeToggle({required this.mode, required this.onChanged});

  final LogQueryMode mode;
  final ValueChanged<LogQueryMode> onChanged;

  @override
  Widget build(BuildContext context) {
    return SegmentedButton<LogQueryMode>(
      segments: const [
        ButtonSegment(value: LogQueryMode.search, label: Text('Search')),
        ButtonSegment(value: LogQueryMode.logql, label: Text('LogQL')),
      ],
      selected: {mode},
      onSelectionChanged: (s) => onChanged(s.first),
      showSelectedIcon: false,
      style: const ButtonStyle(
        visualDensity: VisualDensity.compact,
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
      ),
    );
  }
}

class _NamespaceDropdown extends StatelessWidget {
  const _NamespaceDropdown({
    required this.namespaces,
    required this.value,
    required this.loading,
    required this.isAdmin,
    required this.onChanged,
    this.enabled = true,
  });

  final List<String> namespaces;
  final String? value;
  final bool loading;
  final bool isAdmin;
  final ValueChanged<String?> onChanged;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    // Build the items set — guards against the initial-namespace seed
    // (e.g. from a deep link) not being in the label-values response
    // yet, which would otherwise crash DropdownButtonFormField.
    final items = <String>{
      ...namespaces,
      if (value != null && value!.isNotEmpty) value!,
    }.toList()
      ..sort();
    return DropdownButtonFormField<String?>(
      key: const ValueKey('logFilter-namespace'),
      initialValue: value,
      isExpanded: true,
      decoration: InputDecoration(
        labelText: 'Namespace${isAdmin ? '' : ' *'}',
        helperText: loading
            ? 'Loading…'
            : (!enabled ? 'Disabled — using all namespaces' : null),
        border: const OutlineInputBorder(),
        isDense: true,
      ),
      hint: Text(isAdmin ? 'All namespaces' : 'Pick a namespace'),
      items: [
        if (isAdmin)
          const DropdownMenuItem<String?>(
            value: null,
            child: Text('All namespaces'),
          ),
        for (final ns in items)
          DropdownMenuItem<String?>(value: ns, child: Text(ns)),
      ],
      onChanged: enabled ? onChanged : null,
    );
  }
}

class _AllNamespacesCheckbox extends StatelessWidget {
  const _AllNamespacesCheckbox({
    required this.value,
    required this.onChanged,
  });

  final bool value;
  final ValueChanged<bool> onChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return InkWell(
      key: const ValueKey('logFilter-allNamespaces'),
      onTap: () => onChanged(!value),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Row(
          children: [
            SizedBox(
              width: 24,
              height: 24,
              child: Checkbox(
                value: value,
                onChanged: (v) => onChanged(v ?? false),
                visualDensity: VisualDensity.compact,
                materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                'All namespaces (admin)',
                style: TextStyle(
                  color: colors.textPrimary,
                  fontSize: 14,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _PodDropdown extends StatelessWidget {
  const _PodDropdown({
    required this.pods,
    required this.value,
    required this.loading,
    required this.enabled,
    required this.onChanged,
  });

  final List<String> pods;
  final String? value;
  final bool loading;
  final bool enabled;
  final ValueChanged<String?> onChanged;

  @override
  Widget build(BuildContext context) {
    final items = <String>{
      ...pods,
      if (value != null && value!.isNotEmpty) value!,
    }.toList()
      ..sort();
    return DropdownButtonFormField<String?>(
      key: const ValueKey('logFilter-pod'),
      initialValue: value,
      isExpanded: true,
      decoration: InputDecoration(
        labelText: 'Pod (optional)',
        helperText: loading
            ? 'Loading…'
            : (!enabled ? 'Pick a namespace first' : null),
        border: const OutlineInputBorder(),
        isDense: true,
      ),
      hint: const Text('All pods'),
      items: [
        const DropdownMenuItem<String?>(value: null, child: Text('All pods')),
        for (final p in items)
          DropdownMenuItem<String?>(value: p, child: Text(p)),
      ],
      onChanged: enabled ? onChanged : null,
    );
  }
}

class _ContainerDropdown extends StatelessWidget {
  const _ContainerDropdown({
    required this.containers,
    required this.value,
    required this.loading,
    required this.enabled,
    required this.onChanged,
  });

  final List<String> containers;
  final String? value;
  final bool loading;
  final bool enabled;
  final ValueChanged<String?> onChanged;

  @override
  Widget build(BuildContext context) {
    final items = <String>{
      ...containers,
      if (value != null && value!.isNotEmpty) value!,
    }.toList()
      ..sort();
    return DropdownButtonFormField<String?>(
      key: const ValueKey('logFilter-container'),
      initialValue: value,
      isExpanded: true,
      decoration: InputDecoration(
        labelText: 'Container (optional)',
        helperText:
            loading ? 'Loading…' : (!enabled ? 'Pick a pod first' : null),
        border: const OutlineInputBorder(),
        isDense: true,
      ),
      hint: const Text('All containers'),
      items: [
        const DropdownMenuItem<String?>(
            value: null, child: Text('All containers')),
        for (final c in items)
          DropdownMenuItem<String?>(value: c, child: Text(c)),
      ],
      onChanged: enabled ? onChanged : null,
    );
  }
}

class _SeverityChips extends StatelessWidget {
  const _SeverityChips({required this.value, required this.onChanged});

  final LogSeverity? value;
  final ValueChanged<LogSeverity?> onChanged;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Wrap(
      spacing: 6,
      children: [
        FilterChip(
          key: const ValueKey('logFilter-severity-any'),
          label: const Text('Any'),
          selected: value == null,
          onSelected: (_) => onChanged(null),
        ),
        for (final s in LogSeverity.values)
          FilterChip(
            key: ValueKey('logFilter-severity-${s.value}'),
            label: Text(s.label),
            selected: value == s,
            selectedColor: _severityTone(colors, s).withAlpha(48),
            onSelected: (_) => onChanged(value == s ? null : s),
          ),
      ],
    );
  }

  Color _severityTone(KubeColors colors, LogSeverity s) {
    return switch (s) {
      LogSeverity.error => colors.error,
      LogSeverity.warn => colors.warning,
      LogSeverity.info => colors.accent,
      LogSeverity.debug => colors.textMuted,
    };
  }
}
