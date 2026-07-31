import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/status_pill.dart';
import '../data/config_change_models.dart';

Color configChangeStatusColor(ConfigChange change) => switch (change.status) {
      ConfigChangeStatus.applied => AppTheme.available,
      ConfigChangeStatus.executing => AppTheme.downloading,
      ConfigChangeStatus.failed => AppTheme.error,
      ConfigChangeStatus.outcomeUnknown => AppTheme.requested,
      ConfigChangeStatus.unknown => AppTheme.textSecondary,
    };

IconData configChangeStatusIcon(ConfigChange change) => switch (change.status) {
      ConfigChangeStatus.applied => change.operation == ConfigChangeOperation.revert
          ? Icons.restore_rounded
          : Icons.check_circle_outline,
      ConfigChangeStatus.executing => Icons.sync_rounded,
      ConfigChangeStatus.failed => Icons.error_outline,
      ConfigChangeStatus.outcomeUnknown => Icons.help_outline,
      ConfigChangeStatus.unknown => Icons.info_outline,
    };

class ConfigChangeStatusPill extends StatelessWidget {
  final ConfigChange change;

  const ConfigChangeStatusPill({super.key, required this.change});

  @override
  Widget build(BuildContext context) => StatusPill(
        text: change.statusLabel,
        color: configChangeStatusColor(change),
      );
}

/// Trusted target metadata shared by chat receipts, history, and detail.
class ConfigChangeTarget extends StatelessWidget {
  final ConfigChange change;
  final bool showInstanceId;

  const ConfigChangeTarget({
    super.key,
    required this.change,
    this.showInstanceId = false,
  });

  @override
  Widget build(BuildContext context) {
    final instance = change.instanceName.trim().isEmpty
        ? 'Unnamed instance'
        : change.instanceName.trim();
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Icon(Icons.dns_outlined,
            size: 18, color: AppTheme.textSecondary),
        const SizedBox(width: 8),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                '${change.serviceLabel} · $instance',
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w700,
                ),
              ),
              if (change.resourceName.trim().isNotEmpty) ...[
                const SizedBox(height: 2),
                Text(
                  change.resourceName.trim(),
                  style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 12,
                  ),
                ),
              ],
              if (showInstanceId && change.instanceId.trim().isNotEmpty) ...[
                const SizedBox(height: 3),
                SelectableText(
                  change.instanceId.trim(),
                  style: const TextStyle(
                    color: AppTheme.textMuted,
                    fontSize: 11,
                    fontFamily: 'monospace',
                  ),
                ),
              ],
            ],
          ),
        ),
      ],
    );
  }
}

String configCurrentStatusLabel(ConfigChange change) =>
    switch (change.currentStatus) {
      ConfigCurrentStatus.matchesApplied => 'Current settings match',
      ConfigCurrentStatus.different => 'Current settings changed',
      ConfigCurrentStatus.unavailable => 'Current settings unavailable',
      ConfigCurrentStatus.unknown => 'Current status unknown',
      null => 'Current status not checked',
    };

Color configCurrentStatusColor(ConfigChange change) =>
    switch (change.currentStatus) {
      ConfigCurrentStatus.matchesApplied => AppTheme.available,
      ConfigCurrentStatus.different => AppTheme.requested,
      ConfigCurrentStatus.unavailable => AppTheme.error,
      ConfigCurrentStatus.unknown || null => AppTheme.textSecondary,
    };
