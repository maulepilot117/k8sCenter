// Widget tests for ProbeFormSection. Confirms the handler picker
// hides/shows the path field, and that toJson omits zero-valued
// timing fields.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/probe_form.dart';

Widget _harness({
  required ProbeData? initial,
  required ValueChanged<ProbeData?> onChanged,
}) {
  return MaterialApp(
    theme: buildKubeTheme('nexus'),
    home: Scaffold(
      body: _Stateful(initial: initial, onChanged: onChanged),
    ),
  );
}

class _Stateful extends StatefulWidget {
  const _Stateful({required this.initial, required this.onChanged});
  final ProbeData? initial;
  final ValueChanged<ProbeData?> onChanged;

  @override
  State<_Stateful> createState() => _StatefulState();
}

class _StatefulState extends State<_Stateful> {
  late ProbeData? _probe = widget.initial;

  @override
  Widget build(BuildContext context) {
    return ProbeFormSection(
      label: 'Liveness',
      probe: _probe,
      onChanged: (next) {
        setState(() => _probe = next);
        widget.onChanged(next);
      },
    );
  }
}

void main() {
  group('ProbeData', () {
    test('http toJson includes path; tcp omits it', () {
      const http = ProbeData(
          type: 'http', path: '/healthz', port: 8080, periodSeconds: 10);
      const tcp = ProbeData(type: 'tcp', port: 6379, periodSeconds: 5);
      expect(http.toJson()['path'], '/healthz');
      expect(tcp.toJson().containsKey('path'), false);
    });

    test('toJson omits zero initialDelay/period', () {
      const p = ProbeData(
        type: 'http',
        path: '/healthz',
        port: 8080,
        initialDelaySeconds: 0,
        periodSeconds: 0,
      );
      final j = p.toJson();
      expect(j.containsKey('initialDelaySeconds'), false);
      expect(j.containsKey('periodSeconds'), false);
    });
  });

  group('ProbeFormSection', () {
    testWidgets('switch off emits null', (tester) async {
      ProbeData? captured;
      await tester.pumpWidget(_harness(
        initial: const ProbeData(),
        onChanged: (p) => captured = p,
      ));
      await tester.tap(find.byType(Switch));
      await tester.pumpAndSettle();
      expect(captured, isNull);
    });

    testWidgets('switch on from null emits a default ProbeData',
        (tester) async {
      ProbeData? captured;
      await tester.pumpWidget(_harness(
        initial: null,
        onChanged: (p) => captured = p,
      ));
      await tester.tap(find.byType(Switch));
      await tester.pumpAndSettle();
      expect(captured, isNotNull);
      expect(captured!.type, 'http');
    });

    testWidgets('http handler renders Path label', (tester) async {
      await tester.pumpWidget(_harness(
        initial: const ProbeData(type: 'http'),
        onChanged: (_) {},
      ));
      expect(find.text('Path'), findsOneWidget);
    });

    testWidgets('tcp handler hides Path label', (tester) async {
      await tester.pumpWidget(_harness(
        initial: const ProbeData(type: 'tcp'),
        onChanged: (_) {},
      ));
      expect(find.text('Path'), findsNothing);
    });
  });
}
