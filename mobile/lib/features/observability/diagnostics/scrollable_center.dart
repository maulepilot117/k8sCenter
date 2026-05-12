// `RefreshIndicator` requires a scrollable child or pull-to-refresh
// can't be triggered. The loading / error / empty paths on the
// diagnostics + namespace-summary screens otherwise render a centered
// non-scrollable widget. This helper wraps any child in a
// `SingleChildScrollView` constrained to at least the viewport
// height so pull-to-refresh works in those states too.

import 'package:flutter/material.dart';

class ScrollableCenter extends StatelessWidget {
  const ScrollableCenter({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        return SingleChildScrollView(
          physics: const AlwaysScrollableScrollPhysics(),
          child: ConstrainedBox(
            constraints: BoxConstraints(minHeight: constraints.maxHeight),
            child: Center(child: child),
          ),
        );
      },
    );
  }
}
