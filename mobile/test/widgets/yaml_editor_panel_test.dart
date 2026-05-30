// Tests for YamlEditorPanel.
//
// Covers:
//   - read-only mode shows JSON-pretty-printed resource and an "Edit" button
//   - tapping Edit reveals the TextField with pre-filled JSON content
//   - Cancel button returns to read-only view
//   - Validate button triggers a POST /api/v1/yaml/validate

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/yaml_apply_controller.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/yaml_editor_panel.dart';

import '../support/mock_dio_adapter.dart';

// ── fixtures ─────────────────────────────────────────────────────────────────

const _resource = <String, dynamic>{
  'kind': 'ConfigMap',
  'metadata': {'name': 'cfg', 'namespace': 'default'},
  'data': {'foo': 'bar'},
};

const _applyKey = YamlApplyKey(
  clusterId: 'local',
  kind: 'configmaps',
  namespace: 'default',
  name: 'cfg',
);

// Secret GET responses arrive with `data` (and any `stringData`) values
// already masked to the literal `"****"` by the backend. Seeding the editor
// with these would let SSA persist the mask over real credential bytes, so
// [YamlEditorPanel.stripSensitiveDataFields] drops them. These fixtures pin
// that defense.
const _secretResource = <String, dynamic>{
  'kind': 'Secret',
  'metadata': {'name': 'creds', 'namespace': 'default'},
  'type': 'Opaque',
  'data': {'username': '****', 'password': '****'},
};

const _secretApplyKey = YamlApplyKey(
  clusterId: 'local',
  kind: 'secrets',
  namespace: 'default',
  name: 'creds',
);

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

Widget _harness(ProviderContainer container) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: Scaffold(
        body: YamlEditorPanel(
          applyKey: _applyKey,
          resource: _resource,
        ),
      ),
    ),
  );
}

Widget _secretHarness(
  ProviderContainer container, {
  required bool stripSensitiveDataFields,
}) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: Scaffold(
        body: YamlEditorPanel(
          applyKey: _secretApplyKey,
          resource: _secretResource,
          stripSensitiveDataFields: stripSensitiveDataFields,
        ),
      ),
    ),
  );
}

// ── tests ─────────────────────────────────────────────────────────────────────

void main() {
  testWidgets('read-only mode shows JSON-pretty resource and Edit button',
      (tester) async {
    final (:container, mock: _) = _makeContainer();
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container));
    await tester.pump();

    // The JSON-pretty text of the resource should appear.
    final pretty = const JsonEncoder.withIndent('  ').convert(_resource);
    expect(find.text(pretty), findsOneWidget);

    // An "Edit" button must be visible.
    expect(find.text('Edit'), findsOneWidget);

    // No TextField while in read-only mode.
    expect(find.byType(TextField), findsNothing);
  });

  testWidgets('tapping Edit reveals TextField pre-filled with JSON content',
      (tester) async {
    final (:container, mock: _) = _makeContainer();
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container));
    await tester.pump();

    await tester.tap(find.text('Edit'));
    await tester.pump();

    // Must switch to edit mode — TextField appears.
    expect(find.byType(TextField), findsOneWidget);

    // Validate and Apply buttons appear.
    expect(find.text('Validate'), findsOneWidget);
    expect(find.text('Apply'), findsOneWidget);

    // The TextField should be pre-filled with the JSON-pretty text.
    final textField = tester.widget<TextField>(find.byType(TextField));
    final pretty = const JsonEncoder.withIndent('  ').convert(_resource);
    expect(textField.controller?.text, pretty);
  });

  testWidgets('Cancel button returns to read-only view', (tester) async {
    final (:container, mock: _) = _makeContainer();
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container));
    await tester.pump();

    // Enter edit mode.
    await tester.tap(find.text('Edit'));
    await tester.pump();
    expect(find.byType(TextField), findsOneWidget);

    // Tap Cancel.
    await tester.tap(find.text('Cancel'));
    await tester.pump();

    // Back to read-only: TextField gone, Edit button visible.
    expect(find.byType(TextField), findsNothing);
    expect(find.text('Edit'), findsOneWidget);
  });

  testWidgets('Validate button triggers POST /api/v1/yaml/validate',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    // Backend wraps response in {data: ...}; validate returns
    // {documents, valid} (different shape from apply).
    mock.onJson(
      'POST',
      '/api/v1/yaml/validate',
      body: {
        'data': {
          'documents': [
            {
              'index': 0,
              'kind': 'ConfigMap',
              'name': 'cfg',
              'namespace': 'default',
              'valid': true,
              'errors': <Map<String, dynamic>>[],
            },
          ],
          'valid': true,
        },
      },
    );

    await tester.pumpWidget(_harness(container));
    await tester.pump();

    // Enter edit mode.
    await tester.tap(find.text('Edit'));
    await tester.pump();

    // Tap Validate.
    await tester.tap(find.text('Validate'));
    await tester.pumpAndSettle();

    // The validate POST must have been made.
    expect(
      mock.requests.any(
        (r) => r.method == 'POST' && r.path == '/api/v1/yaml/validate',
      ),
      isTrue,
    );

    // Dry run panel should appear after a successful validate.
    expect(find.text('Dry run'), findsOneWidget);
  });

  testWidgets(
      'Secret with stripSensitiveDataFields hides data in read-only view',
      (tester) async {
    final (:container, mock: _) = _makeContainer();
    addTearDown(container.dispose);

    await tester.pumpWidget(
      _secretHarness(container, stripSensitiveDataFields: true),
    );
    await tester.pump();

    // The rendered read-only pretty output must omit the masked `data`
    // field entirely — otherwise SSA would later persist `"****"`.
    final view = tester.widget<SelectableText>(find.byType(SelectableText));
    final rendered = view.data!;
    expect(rendered.contains('"data"'), isFalse);
    expect(rendered.contains('****'), isFalse);
    // Non-sensitive fields remain visible.
    expect(rendered.contains('"kind": "Secret"'), isTrue);
  });

  testWidgets(
      'Secret with stripSensitiveDataFields omits data/stringData from editor seed',
      (tester) async {
    final (:container, mock: _) = _makeContainer();
    addTearDown(container.dispose);

    await tester.pumpWidget(
      _secretHarness(container, stripSensitiveDataFields: true),
    );
    await tester.pump();

    await tester.tap(find.text('Edit'));
    await tester.pump();

    // The editor seed must not carry the masked credential fields, so a
    // label-only edit + Apply never overwrites real secret bytes with `****`.
    final textField = tester.widget<TextField>(find.byType(TextField));
    final seed = textField.controller!.text;
    expect(seed.contains('"data"'), isFalse);
    expect(seed.contains('"stringData"'), isFalse);
    expect(seed.contains('****'), isFalse);
    // The rest of the resource is still present and editable.
    expect(seed.contains('"kind": "Secret"'), isTrue);
  });

  testWidgets(
      'Secret without stripSensitiveDataFields keeps data field present',
      (tester) async {
    final (:container, mock: _) = _makeContainer();
    addTearDown(container.dispose);

    await tester.pumpWidget(
      _secretHarness(container, stripSensitiveDataFields: false),
    );
    await tester.pump();

    // Control case: with the flag off the `data` field IS rendered. This
    // proves it is the strip — not an absence of data — that removes it.
    final view = tester.widget<SelectableText>(find.byType(SelectableText));
    expect(view.data!.contains('"data"'), isTrue);

    await tester.tap(find.text('Edit'));
    await tester.pump();

    final textField = tester.widget<TextField>(find.byType(TextField));
    expect(textField.controller!.text.contains('"data"'), isTrue);
  });
}
