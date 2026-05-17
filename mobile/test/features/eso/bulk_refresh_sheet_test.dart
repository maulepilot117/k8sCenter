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
      theme: buildKubeTheme('nexus'),
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

  group('BulkRefreshSheet.confirm phase', () {
    testWidgets('gates Refresh button on the REFRESH type-to-confirm token',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.confirm,
          scope: const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
          scopeResponse: _scopeOk(visible: 47),
        ),
        mock: mock,
      ));
      await _openSheet(tester);

      expect(find.textContaining('47'), findsAtLeastNWidgets(1));
      // Without typing REFRESH the action button is disabled.
      final refreshBtn = find.widgetWithText(FilledButton, 'Refresh 47');
      expect(refreshBtn, findsOneWidget);
      expect(tester.widget<FilledButton>(refreshBtn).onPressed, isNull);

      // Type the wrong token first.
      await tester.enterText(find.byType(TextField), 'refresh');
      await tester.pump();
      expect(tester.widget<FilledButton>(refreshBtn).onPressed, isNull,
          reason: 'lowercase must NOT satisfy the gate (case-sensitive)');

      // Type the correct token.
      await tester.enterText(find.byType(TextField), kBulkRefreshConfirmToken);
      await tester.pump();
      expect(tester.widget<FilledButton>(refreshBtn).onPressed, isNotNull);
    });

    testWidgets('zero-visible scope renders no-permission copy + Close',
        (tester) async {
      final mock = MockDioAdapter();
      await tester.pumpWidget(_wrap(
        seed: BulkRefreshSheetState(
          phase: BulkRefreshPhase.confirm,
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
      expect(find.widgetWithText(FilledButton, 'Close'), findsOneWidget);
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
