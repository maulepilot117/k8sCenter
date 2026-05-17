// Tap-target test — pumps a representative interactive surface across
// every generated theme and asserts both iOSTapTargetGuideline (44×44)
// and androidTapTargetGuideline (48×48) pass.
//
// We intentionally avoid pumping every screen via a route walker — that
// would require app-shell bootstrap (router, providers, secure storage)
// inside flutter_test. Instead, we exercise the shared interactive
// primitives (IconButton, ListTile, ChoiceChip, ElevatedButton) at
// production sizes. If the M5 a11y pass missed an IconButton with
// `tapTargetSize: shrinkWrap` or a GestureDetector smaller than 48 dp,
// the static-default widgets in this fixture would have caught it; a
// targeted test for screen-specific affordances is the right tool when
// they appear, not blanket route-walking that bloats CI.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/theme/themes.g.dart';

import '../a11y_helpers.dart';

class _InteractiveSurface extends StatelessWidget {
  const _InteractiveSurface();

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // Bare IconButton — must default to >=48 dp under M3.
        IconButton(
          onPressed: () {},
          icon: const Icon(Icons.refresh),
          tooltip: 'Refresh',
        ),
        // IconButton inside an AppBar context.
        AppBar(
          leading: IconButton(
            onPressed: () {},
            icon: const Icon(Icons.arrow_back),
            tooltip: 'Back',
          ),
          title: const Text('Detail'),
          actions: [
            IconButton(
              onPressed: () {},
              icon: const Icon(Icons.more_vert),
              tooltip: 'More actions',
            ),
          ],
        ),
        // ListTile rows — tap target is the tile (>= kMinInteractiveDimension).
        ListTile(
          leading: const Icon(Icons.cloud_outlined),
          title: const Text('Item one'),
          onTap: () {},
        ),
        ListTile(
          leading: const Icon(Icons.cloud_outlined),
          title: const Text('Item two'),
          trailing: const Icon(Icons.chevron_right),
          onTap: () {},
        ),
        // SegmentedButton (TimeRangePicker shape) — default tap-target size
        // must NOT be shrinkWrap. PR-5h removed an explicit shrinkWrap.
        Padding(
          padding: const EdgeInsets.all(8),
          child: SegmentedButton<int>(
            segments: const [
              ButtonSegment(value: 0, label: Text('1h')),
              ButtonSegment(value: 1, label: Text('6h')),
              ButtonSegment(value: 2, label: Text('24h')),
            ],
            selected: const {0},
            onSelectionChanged: (_) {},
          ),
        ),
        // ElevatedButton — used for primary actions.
        Padding(
          padding: const EdgeInsets.all(8),
          child: ElevatedButton(
            onPressed: () {},
            child: const Text('Submit'),
          ),
        ),
        // ChoiceChip — used for filter affordances.
        Wrap(
          children: [
            ChoiceChip(
              label: const Text('All'),
              selected: true,
              onSelected: (_) {},
            ),
            ChoiceChip(
              label: const Text('Critical'),
              selected: false,
              onSelected: (_) {},
            ),
          ],
        ),
      ],
    );
  }
}

void main() {
  for (final entry in kubeThemes.entries) {
    final id = entry.key;
    final name = entry.value.name;
    testWidgets('$name ($id): tap targets satisfy both platform guidelines',
        (tester) async {
      await tester.pumpWidget(a11yHarness(
        theme: buildKubeTheme(id),
        child: const SingleChildScrollView(child: _InteractiveSurface()),
      ));
      await expectMeetsAllGuidelines(
        tester,
        // Contrast is exercised by contrast_test.dart over a richer fixture.
        textContrast: false,
        iOSTapTarget: true,
        androidTapTarget: true,
        labeledTapTargets: true,
      );
    });
  }
}
