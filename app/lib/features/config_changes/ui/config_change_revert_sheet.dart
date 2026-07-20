import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../data/config_change_models.dart';
import 'config_change_visuals.dart';

/// Shows a trusted, fixed confirmation for restoring one configuration change.
///
/// [change] must be a freshly fetched detail record. The sheet never accepts or
/// edits values; a positive result only authorizes the server-owned revert.
Future<bool> showConfigChangeRevertConfirmation(
  BuildContext context,
  ConfigChange change,
) async {
  final confirmed = await showModalBottomSheet<bool>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    builder: (sheetContext) => Align(
      alignment: Alignment.bottomCenter,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 640),
        child: Material(
          color: AppTheme.surfaceRaised,
          borderRadius: const BorderRadius.vertical(
            top: Radius.circular(AppTheme.radiusXLarge),
          ),
          clipBehavior: Clip.antiAlias,
          child: SafeArea(
            top: false,
            child: SingleChildScrollView(
              padding: EdgeInsets.fromLTRB(
                20,
                12,
                20,
                20 + MediaQuery.viewInsetsOf(sheetContext).bottom,
              ),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Center(
                    child: Container(
                      width: 40,
                      height: 4,
                      decoration: BoxDecoration(
                        color: AppTheme.textMuted,
                        borderRadius: BorderRadius.circular(2),
                      ),
                    ),
                  ),
                  const SizedBox(height: 18),
                  Text(
                    change.canRevert == true
                        ? 'Restore previous settings?'
                        : 'Restore unavailable',
                    style: Theme.of(sheetContext).textTheme.titleLarge,
                  ),
                  const SizedBox(height: 8),
                  Text(
                    change.canRevert == true
                        ? 'Cantinarr will restore only the settings recorded below. '
                            'The restore will be added to change history.'
                        : _unavailableReason(change),
                    style: const TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 14,
                      height: 1.4,
                    ),
                  ),
                  const SizedBox(height: 16),
                  Container(
                    width: double.infinity,
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceVariant,
                      borderRadius:
                          BorderRadius.circular(AppTheme.radiusMedium),
                      border: Border.all(color: AppTheme.border),
                    ),
                    child: ConfigChangeTarget(
                      change: change,
                      showInstanceId: true,
                    ),
                  ),
                  if (change.changes.isNotEmpty) ...[
                    const SizedBox(height: 18),
                    const Text(
                      'Settings to restore',
                      style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 13,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(height: 6),
                    for (final field in change.changes)
                      _ReverseDiffRow(field: field),
                  ],
                  const SizedBox(height: 20),
                  if (change.canRevert == true)
                    LayoutBuilder(builder: (context, constraints) {
                      final narrow = constraints.maxWidth < 390;
                      final cancel = OutlinedButton(
                        onPressed: () => Navigator.of(sheetContext).pop(false),
                        child: const Text('Cancel'),
                      );
                      final restore = ElevatedButton.icon(
                        onPressed: () => Navigator.of(sheetContext).pop(true),
                        icon: const Icon(Icons.restore_rounded, size: 18),
                        label: const Text('Restore previous settings'),
                      );
                      if (narrow) {
                        return Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [restore, const SizedBox(height: 8), cancel],
                        );
                      }
                      return Row(
                        mainAxisAlignment: MainAxisAlignment.end,
                        children: [cancel, const SizedBox(width: 10), restore],
                      );
                    })
                  else
                    Align(
                      alignment: Alignment.centerRight,
                      child: ElevatedButton(
                        onPressed: () => Navigator.of(sheetContext).pop(false),
                        child: const Text('Done'),
                      ),
                    ),
                ],
              ),
            ),
          ),
        ),
      ),
    ),
  );
  return confirmed == true;
}

String _unavailableReason(ConfigChange change) {
  if (change.currentError != null) return change.currentError!;
  return switch (change.currentStatus) {
    ConfigCurrentStatus.different =>
      'These settings changed after Cantinarr applied them. Review the live '
          'values before deciding what to keep.',
    ConfigCurrentStatus.unavailable =>
      'Cantinarr could not read the connected app, so it cannot safely restore '
          'the recorded values.',
    _ => 'This change cannot be restored safely.',
  };
}

class _ReverseDiffRow extends StatelessWidget {
  final ConfigFieldChange field;

  const _ReverseDiffRow({required this.field});

  @override
  Widget build(BuildContext context) {
    final current = field.hasCurrent ? field.current : field.after;
    return Padding(
      padding: const EdgeInsets.only(top: 10),
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(
          color: AppTheme.background.withValues(alpha: 0.45),
          borderRadius: BorderRadius.circular(AppTheme.radiusSmall),
          border: Border.all(color: AppTheme.border),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              field.label,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 12,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 5),
            SelectableText(
              '${formatConfigValue(current)}  →  ${formatConfigValue(field.before)}',
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                height: 1.35,
                fontFamily: 'monospace',
              ),
            ),
          ],
        ),
      ),
    );
  }
}
