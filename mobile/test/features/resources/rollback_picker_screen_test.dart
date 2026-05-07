// Tests for RollbackPickerScreen.
//
// Covers:
//   - renders revision tiles for ReplicaSets owned by the deployment, newest-first
//   - skips ReplicaSets whose ownerReferences uid doesn't match the deployment uid
//   - empty state when no owned ReplicaSets exist
//   - tapping a revision tile opens the confirm sheet

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/resources/rollback_picker_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

// ── fixtures ─────────────────────────────────────────────────────────────────

const _namespace = 'default';
const _name = 'my-app';
const _depUid = 'dep-uid-abc';

/// Deployment GET response.
final _deploymentBody = {
  'data': {
    'kind': 'Deployment',
    'metadata': {
      'name': _name,
      'namespace': _namespace,
      'uid': _depUid,
      'creationTimestamp': '2026-01-01T00:00:00Z',
    },
    'spec': {'replicas': 3},
  },
};

/// RS owned by this deployment, revision 5 (the latest).
final _rs5 = {
  'kind': 'ReplicaSet',
  'metadata': {
    'name': 'my-app-5',
    'namespace': _namespace,
    'uid': 'rs-uid-5',
    'creationTimestamp': '2026-04-05T00:00:00Z',
    'ownerReferences': [
      {'kind': 'Deployment', 'uid': _depUid, 'name': _name},
    ],
    'annotations': {
      'deployment.kubernetes.io/revision': '5',
    },
  },
};

/// RS owned by this deployment, revision 3.
final _rs3 = {
  'kind': 'ReplicaSet',
  'metadata': {
    'name': 'my-app-3',
    'namespace': _namespace,
    'uid': 'rs-uid-3',
    'creationTimestamp': '2026-03-03T00:00:00Z',
    'ownerReferences': [
      {'kind': 'Deployment', 'uid': _depUid, 'name': _name},
    ],
    'annotations': {
      'deployment.kubernetes.io/revision': '3',
    },
  },
};

/// RS with a *different* deployment uid — must be filtered out.
final _rsOther = {
  'kind': 'ReplicaSet',
  'metadata': {
    'name': 'other-app-1',
    'namespace': _namespace,
    'uid': 'rs-uid-other',
    'creationTimestamp': '2026-02-01T00:00:00Z',
    'ownerReferences': [
      {'kind': 'Deployment', 'uid': 'other-dep-uid', 'name': 'other-app'},
    ],
    'annotations': {
      'deployment.kubernetes.io/revision': '1',
    },
  },
};

// ── helpers ───────────────────────────────────────────────────────────────────

({ProviderContainer container, MockDioAdapter mock}) _makeContainer() {
  final mock = MockDioAdapter();
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
  );
  container.read(dioProvider).httpClientAdapter = mock;
  container.read(refreshDioProvider).httpClientAdapter = mock;
  return (container: container, mock: mock);
}

void _mockDeployment(MockDioAdapter mock) {
  mock.onJson(
    'GET',
    '/api/v1/resources/deployments/$_namespace/$_name',
    body: _deploymentBody,
  );
}

void _mockReplicaSets(MockDioAdapter mock, List<Map<String, dynamic>> items) {
  mock.onJson(
    'GET',
    '/api/v1/resources/replicasets/$_namespace',
    body: {
      'data': items,
      'metadata': {'total': items.length},
    },
  );
}

Widget _harness(ProviderContainer container) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: const RollbackPickerScreen(
        namespace: _namespace,
        name: _name,
      ),
    ),
  );
}

// ── tests ─────────────────────────────────────────────────────────────────────

void main() {
  testWidgets(
      'renders revision tiles for owned ReplicaSets, sorted newest-first',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    _mockDeployment(mock);
    _mockReplicaSets(mock, [_rs3, _rs5]); // unsorted input

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    // Both owned revisions should appear.
    expect(find.text('Revision 5'), findsOneWidget);
    expect(find.text('Revision 3'), findsOneWidget);

    // Verify visual order: revision 5 must appear before revision 3.
    final rev5Pos = tester.getTopLeft(find.text('Revision 5')).dy;
    final rev3Pos = tester.getTopLeft(find.text('Revision 3')).dy;
    expect(rev5Pos, lessThan(rev3Pos));
  });

  testWidgets(
      'skips ReplicaSets not owned by this deployment (different uid)',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    _mockDeployment(mock);
    _mockReplicaSets(mock, [_rs5, _rsOther]);

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    // Only the owned RS (revision 5) should be rendered.
    expect(find.text('Revision 5'), findsOneWidget);
    // The other-app RS must be absent.
    expect(find.text('Revision 1'), findsNothing);
    expect(find.text('other-app-1'), findsNothing);
  });

  testWidgets('shows empty-state message when no owned ReplicaSets exist',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    _mockDeployment(mock);
    _mockReplicaSets(mock, [_rsOther]); // nothing owned by dep-uid

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    // No revision tiles.
    expect(find.textContaining('Revision '), findsNothing);

    // Empty-state hint mentions revision history.
    expect(find.textContaining('No prior revisions'), findsOneWidget);
  });

  testWidgets('tapping a revision tile opens the confirm sheet', (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    _mockDeployment(mock);
    _mockReplicaSets(mock, [_rs5, _rs3]);

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    // Tap the revision 5 tile.
    await tester.tap(find.text('Revision 5'));
    await tester.pumpAndSettle();

    // Confirm sheet must appear — the ConfirmSheet renders a FilledButton
    // labelled 'Rollback'. The title also appears in the sheet but is shared
    // with the AppBar, so we identify the sheet by the action button.
    expect(find.widgetWithText(FilledButton, 'Rollback'), findsOneWidget);
    // At least one instance of the title text is visible.
    expect(find.text('Rollback $_name'), findsAtLeastNWidgets(1));
  });
}
