# Mobile performance — baseline + targets

Status: PR-5i foundation. The `DataTable2` eager-row materialization
fix landed; baseline measurements are captured manually during the
homelab smoke pass (Flutter cannot run in CI on a real device under
profile mode, so the numbers in §Measurements are filled in by the
person running smoke and committed in a follow-up).

## Frame budget targets

| Surface | Target | Reason |
|---|---|---|
| Cold start (time to first frame) | ≤ 800ms (iPhone 14 sim), ≤ 1200ms (Pixel 6 emu) | Industry baseline for "responsive feel" on launch. Sentry init is gated behind opt-in (`sentry_init.dart`), so the off path is a single `shared_preferences` read. |
| Dashboard scroll | ≤ 16ms / frame (60fps) | The default-route surface on every cold start. |
| 1000-row resource list scroll | ≤ 16ms / frame | Tablet `PaginatedDataTable2` page size is 50; the source materializes only the visible page. |
| 6000-row vulnerability list scroll | ≤ 16ms / frame | Already `SliverChildBuilderDelegate` — no DataTable involved. |
| Metrics tab chart render (5 series × 200 points) | ≤ 16ms initial paint, ≤ 16ms / frame on pinch-zoom | `fl_chart` is the renderer; zoom is the heavy path. |

Anything that exceeds the target by more than 50% (`> 24ms` on a frame
budget, `> 1200ms` on iPhone cold start) is a release blocker. Within
target is shippable.

## The bug PR-5i fixed

Before PR-5i, `mobile/lib/widgets/resource_table.dart` constructed
every `DataRow2` up-front:

```dart
DataTable2(
  rows: [
    for (final item in items)
      DataRow2(cells: [...]),  // built for ALL items, regardless of viewport
  ],
)
```

On a 500-pod cluster, every cell's `Text` widget materialized on the
first frame — dominant cost before any of them scrolled into view.
PR-5i switches the tablet branch to `PaginatedDataTable2(source:
KubeDataTableSource(...))`, which calls `getRow(index)` lazily for
just the indices the paginator needs (≤ `rowsPerPage`, defaulted to
50 here). Verified via the `rowCallCount` counter in
`mobile/test/widgets/kube_data_table_source_test.dart` — pumping a
6000-row source through `PaginatedDataTable2` materializes < 60 rows.

The phone branch (`< 768px`) was already `ListView.separated`, which
virtualizes natively. Unchanged.

The scanning vulnerability list
(`mobile/lib/features/scanning/vulnerabilities_list_screen.dart`) is
not a `ResourceTable` consumer — it uses `SliverChildBuilderDelegate`
directly, so its virtualization is independent of PR-5i.

## Capture methodology

1. Build a profile-mode binary:
   ```bash
   cd mobile
   flutter run --profile -d <device>
   ```
   (`<device>` is `iPhone 14 Pro Max` simulator on macOS, `Pixel 6 API
   34` emulator on Android, or a real device by UDID.)

2. Open the Flutter DevTools timeline tab. Connect to the running
   instance.

3. For each surface in §Measurements:
   - Navigate to the surface.
   - Start a recording.
   - Perform the action under measurement (cold start = full app
     relaunch; scroll = fast flick + settle, ~10 seconds; chart render
     = open Metrics tab and pinch-zoom once).
   - Stop the recording.
   - Record the worst-case frame ms over the capture (look for the red
     bars in the frame chart).

4. For cold start specifically:
   ```bash
   flutter run --profile --trace-startup
   ```
   Then read `time_to_first_frame_microseconds` from
   `<app>/build/start_up_info.json`.

5. Drop the numbers in §Measurements. Commit on the same PR if
   captured during the same session as the code change; otherwise as
   a follow-up commit referencing PR-5i.

## Measurements

Captured by: TBD
Cluster: TBD (homelab, populated with ~100 pods / ~5 namespaces / ~5
ExternalSecrets / Trivy report on at least one namespace)
Date: TBD
Flutter version: TBD (output of `flutter --version`)

| Surface | Device | Result | Target | Pass? |
|---|---|---|---|---|
| Cold start | iPhone 14 Pro Max sim | TBD | ≤ 800ms | TBD |
| Cold start | Pixel 6 API 34 emu | TBD | ≤ 1200ms | TBD |
| Dashboard scroll | iPhone 14 Pro Max sim | TBD | ≤ 16ms | TBD |
| Dashboard scroll | Pixel 6 API 34 emu | TBD | ≤ 16ms | TBD |
| 1000-row resource list (Pods) scroll | iPhone 14 Pro Max sim | TBD | ≤ 16ms | TBD |
| 1000-row resource list (Pods) scroll | Pixel 6 API 34 emu | TBD | ≤ 16ms | TBD |
| 6000-row vulnerability list scroll | iPhone 14 Pro Max sim | TBD | ≤ 16ms | TBD |
| 6000-row vulnerability list scroll | Pixel 6 API 34 emu | TBD | ≤ 16ms | TBD |
| Metrics tab chart render (5×200) | iPhone 14 Pro Max sim | TBD | ≤ 16ms | TBD |
| Metrics tab chart render (5×200) | Pixel 6 API 34 emu | TBD | ≤ 16ms | TBD |

## Follow-up policy

If a row in §Measurements fails its target:

- **Within 50% of target**: file a follow-up issue, document the
  hotspot in §Known regressions below. Not a release blocker for M5.
- **Beyond 50% of target**: release blocker. Identify the offending
  widget via the DevTools timeline's PaintingContext / MeasureContext
  hotspot, apply the standard mitigations (`const` constructors,
  `RepaintBoundary` around the heavy subtree, image caching), and
  re-measure.

PR-5i scope is the `DataTable2` fix + this doc. Optimisations beyond
the DataTable fix are explicitly out of scope unless §Measurements
surfaces a release blocker.

## Known regressions

None at PR-5i baseline. Populate as §Measurements is filled in.
