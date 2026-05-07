// Stateless step-progress indicator. Mirrors the contract of
// `frontend/components/wizard/WizardStepper.tsx`:
//
//   - Completed steps are clickable (via [onStepClick])
//   - The current step is highlighted in the accent color
//   - Future steps are disabled
//
// The widget owns no state — the parent passes `currentStep` and
// receives back a tap. Validation, form state, preview, apply all live
// in the per-wizard controller, not here.
//
// Phone (<768px width): vertical layout with subtitle. Tablet (≥768):
// horizontal chips. The 768 breakpoint mirrors PR-1c's adaptive shell.

import 'package:flutter/material.dart';

import '../../theme/kube_theme_builder.dart';
import '../wizard_step.dart';

const double _tabletBreakpoint = 768;

class WizardStepperMobile extends StatelessWidget {
  const WizardStepperMobile({
    super.key,
    required this.steps,
    required this.currentStep,
    this.onStepClick,
  });

  final List<WizardStep> steps;
  final int currentStep;
  final ValueChanged<int>? onStepClick;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (ctx, constraints) {
        final isTablet = constraints.maxWidth >= _tabletBreakpoint;
        return isTablet ? _Horizontal(this) : _Vertical(this);
      },
    );
  }
}

class _Horizontal extends StatelessWidget {
  const _Horizontal(this.parent);
  final WizardStepperMobile parent;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      child: Row(
        children: [
          for (var i = 0; i < parent.steps.length; i++) ...[
            _StepChip(
              index: i,
              label: parent.steps[i].title,
              currentStep: parent.currentStep,
              onTap: parent.onStepClick,
            ),
            if (i != parent.steps.length - 1)
              Expanded(
                child: Container(
                  height: 2,
                  margin: const EdgeInsets.symmetric(horizontal: 8),
                  color: i < parent.currentStep
                      ? colors.accent
                      : colors.borderSubtle,
                ),
              ),
          ],
        ],
      ),
    );
  }
}

class _Vertical extends StatelessWidget {
  const _Vertical(this.parent);
  final WizardStepperMobile parent;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  'Step ${parent.currentStep + 1} of ${parent.steps.length}',
                  style: TextStyle(
                    color: colors.textMuted,
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    letterSpacing: 0.6,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  parent.steps[parent.currentStep].title,
                  style: TextStyle(
                    color: colors.textPrimary,
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                if (parent.steps[parent.currentStep].description != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Text(
                      parent.steps[parent.currentStep].description!,
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                  ),
              ],
            ),
          ),
          // Compact dot row so the operator can see total progress without
          // sacrificing the dominant title-and-description on phone.
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (var i = 0; i < parent.steps.length; i++)
                Container(
                  width: 8,
                  height: 8,
                  margin: const EdgeInsets.symmetric(horizontal: 3),
                  decoration: BoxDecoration(
                    color: i <= parent.currentStep
                        ? colors.accent
                        : colors.borderSubtle,
                    shape: BoxShape.circle,
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _StepChip extends StatelessWidget {
  const _StepChip({
    required this.index,
    required this.label,
    required this.currentStep,
    this.onTap,
  });

  final int index;
  final String label;
  final int currentStep;
  final ValueChanged<int>? onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final isCurrent = index == currentStep;
    final isCompleted = index < currentStep;
    final isFuture = index > currentStep;

    final fg = isCurrent
        ? colors.accent
        : isCompleted
            ? colors.textPrimary
            : colors.textMuted;
    final bg = isCurrent
        ? colors.accent.withValues(alpha: 0.16)
        : isCompleted
            ? colors.borderSubtle.withValues(alpha: 0.4)
            : Colors.transparent;

    final chip = Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(
          color: isCurrent ? colors.accent : colors.borderSubtle,
        ),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 20,
            height: 20,
            margin: const EdgeInsets.only(right: 6),
            decoration: BoxDecoration(
              color: isCurrent
                  ? colors.accent
                  : isCompleted
                      ? colors.accent.withValues(alpha: 0.6)
                      : Colors.transparent,
              shape: BoxShape.circle,
              border: Border.all(
                color:
                    isFuture ? colors.borderSubtle : Colors.transparent,
              ),
            ),
            child: Center(
              child: isCompleted
                  ? const Icon(Icons.check, size: 14, color: Colors.white)
                  : Text(
                      '${index + 1}',
                      style: TextStyle(
                        color: isCurrent ? Colors.white : colors.textMuted,
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
            ),
          ),
          Text(
            label,
            style: TextStyle(
              color: fg,
              fontSize: 13,
              fontWeight: FontWeight.w500,
            ),
          ),
        ],
      ),
    );

    if (isCompleted && onTap != null) {
      return InkWell(
        borderRadius: BorderRadius.circular(20),
        onTap: () => onTap!(index),
        child: chip,
      );
    }
    return chip;
  }
}
