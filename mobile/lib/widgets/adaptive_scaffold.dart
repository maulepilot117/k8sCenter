// Adaptive scaffold with a single 768px breakpoint. Phone (< 768px) gets a
// drawer + single-pane body. Tablet (>= 768px) gets a fixed left rail and
// the body in a flexible right pane — the master-detail shape mirrored
// from the web's split-pane layout.
//
// Screens are not aware of the breakpoint — they pass content via [body]
// and optional [detail]. On tablet, both render side-by-side; on phone,
// only [body] renders and [detail] (when non-null) is expected to be
// reachable via Navigator.push from the body.

import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';

const double tabletBreakpoint = 768;

class AdaptiveScaffold extends StatelessWidget {
  const AdaptiveScaffold({
    super.key,
    required this.title,
    required this.body,
    this.detail,
    this.actions,
    this.drawer,
    this.floatingActionButton,
  });

  final String title;
  final Widget body;
  final Widget? detail;
  final List<Widget>? actions;
  final Widget? drawer;
  final Widget? floatingActionButton;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final isTablet = constraints.maxWidth >= tabletBreakpoint;
        if (isTablet) {
          return _buildTabletLayout(context);
        }
        return _buildPhoneLayout(context);
      },
    );
  }

  Widget _buildPhoneLayout(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(title), actions: actions),
      drawer: drawer,
      body: body,
      floatingActionButton: floatingActionButton,
    );
  }

  Widget _buildTabletLayout(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Scaffold(
      appBar: AppBar(title: Text(title), actions: actions),
      body: Row(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (drawer != null)
            SizedBox(width: 280, child: drawer)
          else
            const SizedBox.shrink(),
          if (drawer != null)
            Container(width: 1, color: colors.borderSubtle),
          Expanded(
            flex: 4,
            child: body,
          ),
          if (detail != null) ...[
            Container(width: 1, color: colors.borderSubtle),
            Expanded(
              flex: 6,
              child: detail!,
            ),
          ],
        ],
      ),
      floatingActionButton: floatingActionButton,
    );
  }
}
