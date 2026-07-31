import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../storage/preferences.dart';
import '../theme/app_theme.dart';

/// Admin queues that can stay pinned in the menu or appear only while active.
enum AttentionMenuItem { approvals, issues, agentFixes }

/// The shared control used both on each queue screen and in Settings.
///
/// Keeping the copy and provider wiring in one widget prevents the recovery
/// control in Settings from drifting from the switch that can hide the item.
class AttentionMenuVisibilitySwitch extends ConsumerWidget {
  const AttentionMenuVisibilitySwitch({
    super.key,
    required this.item,
  });

  final AttentionMenuItem item;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final value = switch (item) {
      AttentionMenuItem.approvals =>
        ref.watch(approvalsMenuOnlyWhenPendingProvider),
      AttentionMenuItem.issues => ref.watch(issuesMenuOnlyWhenActiveProvider),
      AttentionMenuItem.agentFixes =>
        ref.watch(agentFixesMenuOnlyWhenAwaitingReviewProvider),
    };

    return SwitchListTile(
      key: ValueKey('${item.name}-conditional-menu-visibility'),
      value: value,
      onChanged: (next) => _set(ref, next),
      activeThumbColor: AppTheme.accent,
      secondary: Icon(_icon, color: AppTheme.textSecondary),
      title: Text(
        _title,
        style: const TextStyle(
          color: AppTheme.textPrimary,
          fontWeight: FontWeight.w500,
        ),
      ),
      subtitle: Text(
        _subtitle,
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 13,
        ),
      ),
    );
  }

  Future<void> _set(WidgetRef ref, bool value) => switch (item) {
        AttentionMenuItem.approvals =>
          ref.read(approvalsMenuOnlyWhenPendingProvider.notifier).set(value),
        AttentionMenuItem.issues =>
          ref.read(issuesMenuOnlyWhenActiveProvider.notifier).set(value),
        AttentionMenuItem.agentFixes => ref
            .read(agentFixesMenuOnlyWhenAwaitingReviewProvider.notifier)
            .set(value),
      };

  String get _title => switch (item) {
        AttentionMenuItem.approvals => 'Only show Approvals when pending',
        AttentionMenuItem.issues => 'Only show Issues when active',
        AttentionMenuItem.agentFixes =>
          'Only show Agent fixes when awaiting review',
      };

  String get _subtitle => switch (item) {
        AttentionMenuItem.approvals =>
          'Hide Approvals from the menu when no requests are pending.',
        AttentionMenuItem.issues =>
          'Hide Issues when nothing needs attention or tracking.',
        AttentionMenuItem.agentFixes =>
          'Hide Agent fixes when no proposals await review.',
      };

  IconData get _icon => switch (item) {
        AttentionMenuItem.approvals => Icons.fact_check_outlined,
        AttentionMenuItem.issues => Icons.flag_outlined,
        AttentionMenuItem.agentFixes => Icons.build_circle_outlined,
      };
}
