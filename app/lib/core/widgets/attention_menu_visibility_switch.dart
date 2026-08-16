import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../storage/preferences.dart';
import '../theme/app_theme.dart';

/// Admin queues that can stay pinned in the menu or appear only while active.
enum AttentionMenuItem { approvals, issues, agentFixes, profileApprovals }

/// The shared control used both on each queue screen and in Settings.
///
/// Keeping the copy and provider wiring in one widget prevents the recovery
/// control in Settings from drifting from the switch that can hide the item.
/// With [opensQueue] the row itself opens the queue — Settings uses that so
/// each row is the queue's stable doorway even while its menu entry hides;
/// the queue screens leave it off (the row would only push themselves).
class AttentionMenuVisibilitySwitch extends ConsumerWidget {
  const AttentionMenuVisibilitySwitch({
    super.key,
    required this.item,
    this.opensQueue = false,
  });

  final AttentionMenuItem item;

  /// Whether tapping the row navigates to the queue it governs.
  final bool opensQueue;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final value = switch (item) {
      AttentionMenuItem.approvals =>
        ref.watch(approvalsMenuOnlyWhenPendingProvider),
      AttentionMenuItem.issues => ref.watch(issuesMenuOnlyWhenActiveProvider),
      AttentionMenuItem.agentFixes =>
        ref.watch(agentFixesMenuOnlyWhenAwaitingReviewProvider),
      AttentionMenuItem.profileApprovals =>
        ref.watch(profileApprovalsMenuOnlyWhenPendingProvider),
    };

    return ListTile(
      leading: Icon(_icon, color: AppTheme.textSecondary),
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
      trailing: Switch(
        key: ValueKey('${item.name}-conditional-menu-visibility'),
        value: value,
        onChanged: (next) => _set(ref, next),
        activeThumbColor: AppTheme.accent,
      ),
      onTap: opensQueue ? () => context.push(_route) : null,
    );
  }

  String get _route => switch (item) {
        AttentionMenuItem.approvals => '/approvals',
        AttentionMenuItem.issues => '/issues',
        AttentionMenuItem.agentFixes => '/agent-actions',
        AttentionMenuItem.profileApprovals => '/settings/profile-approvals',
      };

  Future<void> _set(WidgetRef ref, bool value) => switch (item) {
        AttentionMenuItem.approvals =>
          ref.read(approvalsMenuOnlyWhenPendingProvider.notifier).set(value),
        AttentionMenuItem.issues =>
          ref.read(issuesMenuOnlyWhenActiveProvider.notifier).set(value),
        AttentionMenuItem.agentFixes => ref
            .read(agentFixesMenuOnlyWhenAwaitingReviewProvider.notifier)
            .set(value),
        AttentionMenuItem.profileApprovals => ref
            .read(profileApprovalsMenuOnlyWhenPendingProvider.notifier)
            .set(value),
      };

  String get _title => switch (item) {
        AttentionMenuItem.approvals => 'Approvals',
        AttentionMenuItem.issues => 'Issues',
        AttentionMenuItem.agentFixes => 'Agent fixes',
        AttentionMenuItem.profileApprovals => 'Profile approvals',
      };

  String get _subtitle => switch (item) {
        AttentionMenuItem.approvals =>
          'Only show in the menu when requests are pending',
        AttentionMenuItem.issues =>
          'Only show in the menu when something needs attention or tracking',
        AttentionMenuItem.agentFixes =>
          'Only show in the menu when proposals await review',
        AttentionMenuItem.profileApprovals =>
          'Only show in the menu when changes await a decision',
      };

  IconData get _icon => switch (item) {
        AttentionMenuItem.approvals => Icons.fact_check_outlined,
        AttentionMenuItem.issues => Icons.flag_outlined,
        AttentionMenuItem.agentFixes => Icons.build_circle_outlined,
        AttentionMenuItem.profileApprovals => Icons.tune,
      };
}
