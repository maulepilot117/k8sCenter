// PageView shell that hosts the three onboarding cards and drives
// completion. The Skip button on every card and the "Enable
// notifications" / "Get started" CTA on the last card both go through
// `_complete`, so flag flips are routed through a single path.
//
// After `complete()`, the screen navigates explicitly to `/login`. The
// router redirect re-evaluates and now passes (`onboarded == true`)
// so there is no flicker back to `/onboarding`. If the user happens
// to already be authenticated when they complete onboarding (an
// internal-beta upgrade that somehow slipped past
// [migrateOnboardingFlagForUpgrade]), the redirect catches it and
// sends them to `/` instead.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../theme/kube_theme_builder.dart';
import 'onboarding_cards.dart';
import 'onboarding_controller.dart';

class OnboardingScreen extends ConsumerStatefulWidget {
  const OnboardingScreen({super.key});

  @override
  ConsumerState<OnboardingScreen> createState() => _OnboardingScreenState();
}

class _OnboardingScreenState extends ConsumerState<OnboardingScreen> {
  static const int _kPageCount = 3;

  final PageController _pageController = PageController();
  int _page = 0;

  @override
  void dispose() {
    _pageController.dispose();
    super.dispose();
  }

  Future<void> _advance() async {
    if (_page < _kPageCount - 1) {
      await _pageController.nextPage(
        duration: const Duration(milliseconds: 240),
        curve: Curves.easeOut,
      );
    } else {
      await _complete();
    }
  }

  Future<void> _complete() async {
    await ref.read(onboardingControllerProvider.notifier).complete();
    if (!mounted) return;
    context.go('/login');
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final cards = <Widget>[
      IntroCard(onAdvance: _advance, onSkip: _complete),
      ClusterPinCard(onAdvance: _advance, onSkip: _complete),
      NotificationsCard(onAdvance: _advance, onSkip: _complete),
    ];

    return Scaffold(
      body: Column(
        children: [
          Expanded(
            child: PageView.builder(
              controller: _pageController,
              itemCount: _kPageCount,
              onPageChanged: (i) => setState(() => _page = i),
              itemBuilder: (_, i) => cards[i],
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 0, 24, 24),
            child: _PageIndicator(
              count: _kPageCount,
              activeIndex: _page,
              colors: colors,
            ),
          ),
        ],
      ),
    );
  }
}

class _PageIndicator extends StatelessWidget {
  const _PageIndicator({
    required this.count,
    required this.activeIndex,
    required this.colors,
  });

  final int count;
  final int activeIndex;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      key: const ValueKey('onboarding-page-indicator'),
      label: 'Step ${activeIndex + 1} of $count',
      container: true,
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          for (var i = 0; i < count; i++) ...[
            if (i > 0) const SizedBox(width: 8),
            AnimatedContainer(
              key: ValueKey('onboarding-dot-$i'),
              duration: const Duration(milliseconds: 200),
              width: i == activeIndex ? 16 : 8,
              height: 8,
              decoration: BoxDecoration(
                color: i == activeIndex
                    ? colors.accent
                    : colors.textMuted.withValues(alpha: 0.4),
                borderRadius: BorderRadius.circular(4),
              ),
            ),
          ],
        ],
      ),
    );
  }
}
