import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/theme/app_theme.dart';
import '../data/config_change_models.dart';
import '../data/config_changes_service.dart';
import 'config_change_revert_sheet.dart';
import 'config_change_visuals.dart';

/// Trusted receipt emitted by the server after Cantinarr changes settings in a
/// connected app. Assistant-authored prose is never turned into a control.
class ConfigChangeReceiptCard extends ConsumerStatefulWidget {
  final ConfigChange change;

  const ConfigChangeReceiptCard({super.key, required this.change});

  @override
  ConsumerState<ConfigChangeReceiptCard> createState() =>
      _ConfigChangeReceiptCardState();
}

class _ConfigChangeReceiptCardState
    extends ConsumerState<ConfigChangeReceiptCard> {
  late ConfigChange _change;
  bool _restoring = false;

  @override
  void initState() {
    super.initState();
    _change = widget.change;
  }

  @override
  void didUpdateWidget(ConfigChangeReceiptCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!_restoring && !identical(oldWidget.change, widget.change)) {
      _change = widget.change;
    }
  }

  Future<void> _restore() async {
    if (_restoring) return;
    setState(() => _restoring = true);
    final service = ref.read(configChangesServiceProvider);
    try {
      // The SSE receipt is a summary. Refresh the live comparison before the
      // admin sees a restore control or confirms anything.
      final fresh = await service.getChange(_change.id);
      if (!mounted) return;
      setState(() => _change = fresh);
      final confirmed =
          await showConfigChangeRevertConfirmation(context, fresh);
      if (!confirmed || !mounted) return;
      final restored = await service.revertChange(fresh.id);
      if (!mounted) return;
      setState(() => _change = restored);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Previous settings restored.')),
      );
    } catch (_) {
      if (!mounted) return;
      // A lost POST response can hide a successful remote write. Re-read before
      // the card can offer restore again, then direct the admin to the detail.
      try {
        final current = await service.getChange(_change.id);
        if (mounted) setState(() => _change = current);
      } catch (_) {
        // Keep the durable summary. The detail route will retry independently.
      }
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'Could not confirm whether settings were restored. Review the current state.',
          ),
        ),
      );
    } finally {
      if (mounted) setState(() => _restoring = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final change = _change;
    final color = configChangeStatusColor(change);
    final canOfferRestore = change.status == ConfigChangeStatus.applied &&
        change.resourceType == 'quality_profile' &&
        (change.operation == ConfigChangeOperation.update ||
            change.canRevert == true);
    return Semantics(
      container: true,
      label:
          '${change.statusLabel} configuration change in ${change.serviceLabel}',
      child: Container(
        width: double.infinity,
        margin: const EdgeInsets.only(top: 8),
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
            colors: [
              AppTheme.surfaceRaised.withValues(alpha: 0.94),
              AppTheme.surfaceVariant.withValues(alpha: 0.94),
            ],
          ),
          borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
          border: Border.all(color: color.withValues(alpha: 0.42)),
          boxShadow: [
            BoxShadow(
              color: color.withValues(alpha: 0.05),
              blurRadius: 18,
            ),
          ],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(configChangeStatusIcon(change), size: 20, color: color),
                const SizedBox(width: 8),
                const Expanded(
                  child: Text(
                    'Connected-app settings',
                    style: TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 13,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
                ConfigChangeStatusPill(change: change),
              ],
            ),
            const SizedBox(height: 12),
            Text(
              change.displaySummary,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 15,
                height: 1.35,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 10),
            ConfigChangeTarget(change: change),
            if (change.changes.isNotEmpty) ...[
              const SizedBox(height: 10),
              Text(
                '${change.changes.length} setting${change.changes.length == 1 ? '' : 's'} changed',
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 12,
                ),
              ),
            ],
            if (change.errorText != null) ...[
              const SizedBox(height: 10),
              Text(
                change.errorText!,
                maxLines: 4,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                  color: AppTheme.error,
                  fontSize: 12,
                  height: 1.35,
                ),
              ),
            ],
            const SizedBox(height: 12),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                OutlinedButton.icon(
                  onPressed: () => context.push(
                    '/settings/change-history/${change.id}',
                  ),
                  icon: const Icon(Icons.compare_arrows_rounded, size: 18),
                  label: const Text('Review change'),
                ),
                if (canOfferRestore)
                  TextButton.icon(
                    onPressed: _restoring ? null : _restore,
                    icon: _restoring
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.restore_rounded, size: 18),
                    label: const Text('Restore previous settings'),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
