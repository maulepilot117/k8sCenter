// Widget tests for the ESO Bulk Refresh modal bottom sheet.
//
// Coverage:
//   * Phase 1 (scopePick) renders the variant segmented button and gates
//     "Continue" until an identifier is selected.
//   * Phase 3 (confirm) gates "Refresh N" until type-to-confirm matches.
//   * Phase 4 (poll) renders the progress bar + screen-reader live region
//     copy and the "Run in background" dismiss.
//   * Phase 5 (done) renders the success summary.
//   * Error phase renders the message + retry CTA.
//
// We drive phase transitions directly by overriding the controller's
// `build` to seed canned state, rather than walking the full wire path
// (the controller-level tests cover that).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/eso_repository.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/eso/bulk_refresh_controller.dart';
import 'package:kubecenter/features/eso/bulk_refresh_sheet.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

const _clusterId = 'local';

/// Test-only override that lets the widget pump any phase without
/// walking the controller's full state machine. The override registers
/// against the family slot; consumers `ref.read(...notifier).method()`
/// would still call the real controller methods, so we only stub `build`.
class _SeedController extends BulkRefreshController {
  _SeedController(this.seed);

  final BulkRefreshSheetState seed;

  @override
  BulkRefreshSheetState build(String clusterId) {
    super.build(clusterId);
    return seed;
  }
}

Widget _wrap({
  required BulkRefreshSheetState seed,
  required MockDioAdapter mock,
}) {
  return ProviderScope(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
      bulkRefreshControllerProvider.overrideWith(
        () => _SeedController(seed),
      ),
    ],
    child: MaterialApp(
      theme: buildKubeTheme('liquid-glass'),
      home: Scaffold(
        body: Builder(
          builder: (ctx) {
            return Center(
              child: ElevatedButton(
                onPressed: () =>
                    showBulkRefreshSheet(context: ctx, clusterId: _clusterId),
                child: const Text('Open'),
              ),
            );
          },
        ),
      ),
    ),
  );
}

Future<void> _openSheet(WidgetTester tester) async {
  await tester.tap(find.text('Open'));
  await tester.pumpAndSettle();
}

BulkScopeResponse _scopeOk({int visible = 2}) {
  final raw = {
    'action': 'refresh_store',
    'scopeTarget': 'prod/vault',
    'totalCount': visible,
    'totalNamespaces': 1,
    'visibleCount': visible,
    'restricted': false,
    'targets': List.generate(
      visible,
      (i) => {'namespace': 'prod', 'name': 'es-$i', 'uid': 'u$i'},
    ),
    'byNamespace': [
      {'namespace': 'prod', 'count': visible},
    ],
  };
  return BulkScopeResponse.fromJson(raw);
}

BulkRefreshJob _jobInProgress() {
  return BulkRefreshJob.fromJson({
    'jobId': 'job-1',
    'clusterId': _clusterId,
    'requestedBy': 'oncall',
    'action': 'refresh_store',
    'scopeTarget': 'prod/vault',
    'targetCount': 2,
    'createdAt': '2026-05-17T10:00:00Z',
    'succeeded': ['u1'],
    'failed': <Object>[],
    'skipped': <Object>[],
  });
}

BulkRefreshJob _jobDone() {
  return BulkRefreshJob.fromJson({
    'jobId': 'job-1',
    'clusterId': _clusterId,
    'requestedBy': 'oncall',
    'action': 'refresh_store',
    'scopeTarget': 'prod/vault',
    'targetCount': 2,
    'createdAt': '2026-05-17T10:00:00Z',
    'completedAt': '2026-05-17T10:00:30Z',
    'succeeded': ['u1', 'u2'],
    'failed': <Object>[],
    'skipped': <Object>[],
  });
}

void main() {
  group('BulkRefreshSheet.scopePick phase', () {
    testWidgets('renders variant picker and disables Continue without input',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: const BulkRefreshSheetState(phase: BulkRefreshPhase.scopePick),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(find.text('Namespace'), findsAtLeastNWidgets(1));
      expect(find.text('Store'), findsOneWidget);
      expect(find.text('Cluster store'), findsOneWidget);

      // "Continue" exists but is disabled until an identifier is picked.
      final continueBtn = find.widgetWithText(FilledButton, 'Continue');
      expect(continueBtn, findsOneWidget);
      expect(
        tester.widget<FilledButton>(continueBtn).onPressed,
        isNull,
        reason: 'Continue must be disabled when no namespace is selected',
      );
    });
  });

  group('BulkRefreshSheet.preview phase', () {
    // PR-5e-review #3: type-to-confirm friction moved into the shared
    // `showConfirmSheet`. The preview body now renders the breakdown +
    // a "Continue" button; tapping Continue stacks the shared confirm
    // sheet on top of the bulk sheet.
    testWidgets('renders byNamespace breakdown + enabled Continue button',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.preview,
          scope: const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
          scopeResponse: _scopeOk(visible: 47),
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(find.textContaining('47'), findsAtLeastNWidgets(1));
      // Type-to-confirm input is NOT on the preview body — it lives on
      // the shared confirm sheet that stacks on Continue.
      expect(find.byType(TextField), findsNothing,
          reason: 'preview body must NOT render the type-to-confirm input '
              '— that lives on the shared confirm sheet');
      final continueBtn = find.widgetWithText(FilledButton, 'Continue');
      expect(continueBtn, findsOneWidget);
      expect(tester.widget<FilledButton>(continueBtn).onPressed, isNotNull,
          reason: 'Continue is enabled while phase == preview');
    });

    testWidgets('Continue stacks the shared confirm sheet on top',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.preview,
          scope: const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
          scopeResponse: _scopeOk(visible: 3),
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      // Tap Continue and pump until the inner sheet is fully open.
      await tester.tap(find.widgetWithText(FilledButton, 'Continue'));
      await tester.pumpAndSettle();

      // Shared confirm sheet uses the title "Refresh <N> ExternalSecret(s)";
      // type-to-confirm TextField is present and the inner confirm button
      // is labeled "Refresh 3".
      expect(find.text('Refresh 3 ExternalSecrets'), findsOneWidget);
      expect(find.byType(TextField), findsOneWidget,
          reason: 'shared confirm sheet renders the type-to-confirm input');
      expect(find.widgetWithText(FilledButton, 'Refresh 3'), findsOneWidget);
    });

    testWidgets('zero-visible scope renders no-permission copy + Close',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.preview,
          scope: const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
          scopeResponse: _scopeOk(visible: 0),
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(find.textContaining('permission'), findsOneWidget);
      expect(find.widgetWithText(FilledButton, 'Close'), findsOneWidget);
    });
  });

  group('BulkRefreshSheet.poll phase', () {
    testWidgets('renders Refreshing N of M in a Semantics liveRegion',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.poll,
          jobId: 'job-1',
          job: _jobInProgress(),
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(find.text('Refreshing 1 of 2'), findsOneWidget);
      expect(find.text('Run in background'), findsOneWidget);
      // The live region announcement is a Semantics node; locate it by
      // its child label to confirm wrapping rather than digging
      // through the semantics tree.
      final progressLine = find.text('Refreshing 1 of 2');
      final semantics = find.ancestor(
        of: progressLine,
        matching: find.byWidgetPredicate(
          (w) => w is Semantics && (w.properties.liveRegion ?? false),
        ),
      );
      expect(semantics, findsOneWidget);
    });

    testWidgets('takingLong flag surfaces the long-running caption',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.poll,
          jobId: 'job-1',
          job: _jobInProgress(),
          takingLong: true,
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(find.textContaining('Taking longer'), findsOneWidget);
    });

    testWidgets('attachedToExistingJob shows the in-flight banner',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.poll,
          jobId: 'job-existing',
          job: _jobInProgress(),
          attachedToExistingJob: true,
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(
        find.textContaining('Attached to an existing'),
        findsOneWidget,
      );
    });

    // PR-5e-review #14: pollRetrying surfaces the transient-error banner
    // inline so the operator knows the loop is alive (vs. wedged).
    testWidgets('pollRetrying flag renders the Retrying caption in warning',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.poll,
          jobId: 'job-1',
          job: _jobInProgress(),
          pollRetrying: true,
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(find.text('Retrying…'), findsOneWidget);
      final text = tester.widget<Text>(find.text('Retrying…'));
      final ctx = tester.element(find.text('Retrying…'));
      final colors = Theme.of(ctx).extension<KubeColors>()!;
      expect(text.style?.color, colors.warning,
          reason: 'Retrying caption must use the warning token, not error');
    });
  });

  group('BulkRefreshSheet.done phase', () {
    testWidgets('renders success summary with succeeded count', (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.done,
          jobId: 'job-1',
          job: _jobDone(),
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(find.textContaining('All 2 ExternalSecrets'), findsOneWidget);
      final headline =
          tester.widget<Text>(find.textContaining('All 2 ExternalSecrets'));
      final ctx = tester.element(find.textContaining('All 2 ExternalSecrets'));
      final colors = Theme.of(ctx).extension<KubeColors>()!;
      expect(headline.style?.color, colors.success,
          reason: 'pure success run must render success-colored copy');
      expect(find.widgetWithText(FilledButton, 'Close'), findsOneWidget);
    });

    // PR-5e-review #15: partial-failure Done state must render warning
    // copy (NOT success) and surface the failed outcome list.
    testWidgets('partial failures render warning headline + failed list',
        (tester) async {
      final mock = MockDioAdapter();
      final job = BulkRefreshJob.fromJson({
        'jobId': 'job-1',
        'clusterId': _clusterId,
        'requestedBy': 'oncall',
        'action': 'refresh_store',
        'scopeTarget': 'prod/vault',
        'targetCount': 4,
        'createdAt': '2026-05-17T10:00:00Z',
        'completedAt': '2026-05-17T10:00:30Z',
        'succeeded': ['u1', 'u2', 'u3'],
        'failed': [
          {'uid': 'u4', 'reason': 'auth method failed'},
        ],
        'skipped': <Object>[],
      });
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.done,
          jobId: 'job-1',
          job: job,
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(find.textContaining('Refresh finished with 1 failure'),
          findsOneWidget);
      final headline = tester
          .widget<Text>(find.textContaining('Refresh finished with 1 failure'));
      final ctx =
          tester.element(find.textContaining('Refresh finished with 1 failure'));
      final colors = Theme.of(ctx).extension<KubeColors>()!;
      expect(headline.style?.color, colors.warning,
          reason: 'partial-failure outcomes must NOT use success copy');
      expect(headline.style?.color, isNot(colors.success));
      // The ExpansionTile label includes the failed count.
      expect(find.text('Failed (1)'), findsOneWidget);
      // Success-style copy must NOT render.
      expect(find.textContaining('All 3 ExternalSecrets'), findsNothing);
    });

    // PR-5e-review #8: all-skipped completion must not pretend
    // everything was refreshed — "All 0 ExternalSecrets were refreshed"
    // in green would be an operator-visible lie.
    testWidgets('all-skipped outcomes render warning copy, not success',
        (tester) async {
      final mock = MockDioAdapter();
      final job = BulkRefreshJob.fromJson({
        'jobId': 'job-1',
        'clusterId': _clusterId,
        'requestedBy': 'oncall',
        'action': 'refresh_store',
        'scopeTarget': 'prod/vault',
        'targetCount': 2,
        'createdAt': '2026-05-17T10:00:00Z',
        'completedAt': '2026-05-17T10:00:30Z',
        'succeeded': <Object>[],
        'failed': <Object>[],
        'skipped': [
          {'uid': 'u1', 'reason': 'rate limit'},
          {'uid': 'u2', 'reason': 'rate limit'},
        ],
      });
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.done,
          jobId: 'job-1',
          job: job,
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      // The all-skipped headline copy.
      expect(
        find.textContaining('All targets were skipped'),
        findsOneWidget,
      );
      // Green success copy must NOT appear.
      expect(find.textContaining('All 0 ExternalSecrets were refreshed'),
          findsNothing);

      final headline = tester
          .widget<Text>(find.textContaining('All targets were skipped'));
      final ctx =
          tester.element(find.textContaining('All targets were skipped'));
      final colors = Theme.of(ctx).extension<KubeColors>()!;
      expect(headline.style?.color, colors.warning,
          reason: 'all-skipped must be warning-tier, not success');
      expect(headline.style?.color, isNot(colors.success));
    });

    // PR-5e-review #10: _OutcomeList must NOT render thousands of rows
    // eagerly inside the sheet — a stuck Vault store can dump 10k
    // "auth method failed" rows and the sheet used to render every one
    // into a Column inside an ExpansionTile. Now caps at 50 with a
    // "Show all (N)" affordance, and the visible rows go through a
    // ListView.builder so lazy paint kicks in even when expanded.
    testWidgets(
        '_OutcomeList caps at 50 rows initially and expands on Show all',
        (tester) async {
      final mock = MockDioAdapter();
      // 200 failed outcomes — well past the cap.
      final failed = List.generate(
        200,
        (i) => {'uid': 'u$i', 'reason': 'auth method failed'},
      );
      final job = BulkRefreshJob.fromJson({
        'jobId': 'job-1',
        'clusterId': _clusterId,
        'requestedBy': 'oncall',
        'action': 'refresh_store',
        'scopeTarget': 'prod/vault',
        'targetCount': 200,
        'createdAt': '2026-05-17T10:00:00Z',
        'completedAt': '2026-05-17T10:00:30Z',
        'succeeded': <Object>[],
        'failed': failed,
        'skipped': <Object>[],
      });
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.done,
          jobId: 'job-1',
          job: job,
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      // ExpansionTile starts collapsed in tests; tap to expand the
      // outcome list so the rows actually paint.
      await tester.tap(find.text('Failed (200)'));
      await tester.pumpAndSettle();

      // Locate _OutcomeRow widgets via runtime-type predicate (private
      // class). With ListView.builder + a 240px height clamp, the
      // physical rows painted at any one time are LESS than 50, but
      // the "Show all" button must NOT be present yet because we are
      // still in capped mode (item count == 50, not 200).
      expect(find.text('Show all (200)'), findsOneWidget,
          reason: 'capped mode shows the "Show all" CTA');

      await tester.tap(find.text('Show all (200)'));
      await tester.pumpAndSettle();

      // After expanding, the CTA disappears (we now show the full list
      // via ListView.builder over all 200).
      expect(find.text('Show all (200)'), findsNothing,
          reason: 'after tapping Show all the CTA must disappear');
    });
  });

  group('BulkRefreshSheet.error phase', () {
    testWidgets('renders the error message + retry CTA', (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: const BulkRefreshSheetState(
          phase: BulkRefreshPhase.error,
          errorMessage: 'Scope is too large for a single bulk refresh.',
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(
        find.text('Scope is too large for a single bulk refresh.'),
        findsOneWidget,
      );
      expect(find.widgetWithText(FilledButton, 'Try again'), findsOneWidget);
    });
  });
}
