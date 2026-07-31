import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/section_header.dart';
import '../data/config_change_models.dart';
import '../data/config_changes_service.dart';
import 'config_change_revert_sheet.dart';
import 'config_change_visuals.dart';

/// Live comparison and safe restore surface for one durable change record.
class ConfigChangeDetailScreen extends ConsumerStatefulWidget {
  final int changeId;

  const ConfigChangeDetailScreen({
    super.key,
    required this.changeId,
  });

  @override
  ConsumerState<ConfigChangeDetailScreen> createState() =>
      _ConfigChangeDetailScreenState();
}

class _ConfigChangeDetailScreenState
    extends ConsumerState<ConfigChangeDetailScreen>
    with WidgetsBindingObserver {
  ConfigChange? _change;
  bool _loading = true;
  bool _restoring = false;
  String? _error;
  int _loadEpoch = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _load();
  }

  @override
  void didUpdateWidget(ConfigChangeDetailScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.changeId == widget.changeId) return;
    _loadEpoch++;

    // GoRouter can retain this State when replacing one detail ID with
    // another. Preserve a restore response that already matches the new ID;
    // otherwise avoid showing the previous record while the new one loads.
    if (_change?.id != widget.changeId) {
      _change = null;
      _loading = true;
      _error = null;
    }
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  Future<void> _load() async {
    if (!mounted) return;
    final epoch = ++_loadEpoch;
    setState(() {
      _loading = _change == null;
      if (_change == null) _error = null;
    });
    try {
      final change = await ref
          .read(configChangesServiceProvider)
          .getChange(widget.changeId);
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _change = change;
        _loading = false;
        _error = null;
      });
    } catch (error) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _loading = false;
        _error = _friendlyError(error);
      });
    }
  }

  Future<void> _restore() async {
    if (_restoring || _change == null) return;
    setState(() => _restoring = true);
    final service = ref.read(configChangesServiceProvider);
    try {
      // Refresh immediately before confirmation. The backend performs its own
      // final compare when the POST arrives, closing the remaining race.
      final fresh = await service.getChange(widget.changeId);
      if (!mounted) return;
      setState(() {
        _change = fresh;
        _error = null;
      });
      final confirmed =
          await showConfigChangeRevertConfirmation(context, fresh);
      if (!confirmed || !mounted) return;
      final restored = await service.revertChange(fresh.id);
      if (!mounted) return;
      setState(() {
        _change = restored;
        _loading = false;
        _error = null;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Previous settings restored.')),
      );
      context.replace('/settings/change-history/${restored.id}');
    } catch (_) {
      if (!mounted) return;
      // The write may have reached the server even if the response did not.
      // Reconcile the original before another restore can be attempted.
      await _load();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'Could not confirm whether settings were restored. The current state was refreshed.',
          ),
        ),
      );
    } finally {
      if (mounted) setState(() => _restoring = false);
    }
  }

  String _friendlyError(Object error) {
    final match = RegExp(r'"error":"([^"]+)"').firstMatch(error.toString());
    return match?.group(1) ?? 'Could not load this change.';
  }

  @override
  Widget build(BuildContext context) {
    final change = _change;
    return Scaffold(
      appBar: AppBar(title: const Text('Change details')),
      body: _loading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent),
            )
          : change == null
              ? FullScreenError(
                  title: 'Change unavailable',
                  message: _error ?? 'Could not load this change.',
                  onRetry: _load,
                )
              : RefreshIndicator(
                  color: AppTheme.accent,
                  onRefresh: _load,
                  child: CenteredContent(
                    child: ListView(
                      physics: const AlwaysScrollableScrollPhysics(),
                      padding: const EdgeInsets.fromLTRB(16, 10, 16, 32),
                      children: [
                        _ChangeHeader(change: change),
                        if (_error != null) ...[
                          const SizedBox(height: 12),
                          ErrorBanner(
                            message:
                                "Couldn't refresh this change. Current settings may be out of date.",
                            onRetry: _load,
                          ),
                        ],
                        const SizedBox(height: 16),
                        _CurrentStatusBanner(change: change),
                        const SizedBox(height: 22),
                        SectionHeader(
                          eyebrow: 'Comparison',
                          title: change.comparisonTitle,
                        ),
                        const SizedBox(height: 12),
                        if (change.changes.isEmpty)
                          const _NoComparison()
                        else
                          for (final field in change.changes)
                            _FieldComparisonCard(
                              field: field,
                              change: change,
                            ),
                        const SizedBox(height: 20),
                        _ChangeMetadata(change: change),
                        if (change.supportsRestore ||
                            change.isAppliedRestore) ...[
                          const SizedBox(height: 20),
                          SizedBox(
                            width: double.infinity,
                            child: change.canRestore
                                ? ElevatedButton.icon(
                                    onPressed: _restoring ? null : _restore,
                                    icon: _restoring
                                        ? const SizedBox(
                                            width: 18,
                                            height: 18,
                                            child: CircularProgressIndicator(
                                              strokeWidth: 2,
                                              color: AppTheme.onAccent,
                                            ),
                                          )
                                        : const Icon(Icons.restore_rounded),
                                    label: const Text(
                                      'Restore previous settings',
                                    ),
                                  )
                                : change.isAppliedRestore
                                    ? ElevatedButton.icon(
                                        onPressed: null,
                                        icon: const Icon(
                                          Icons.history_rounded,
                                        ),
                                        label: const Text(
                                          'Previous settings restored',
                                        ),
                                      )
                                    : OutlinedButton.icon(
                                        onPressed:
                                            _restoring ? null : _restore,
                                        icon: const Icon(Icons.lock_outline),
                                        label: const Text(
                                          'Why restore is unavailable',
                                        ),
                                      ),
                          ),
                        ],
                      ],
                    ),
                  ),
                ),
    );
  }
}

class _ChangeHeader extends StatelessWidget {
  final ConfigChange change;

  const _ChangeHeader({required this.change});

  @override
  Widget build(BuildContext context) {
    final color = configChangeStatusColor(change);
    return AppPanel(
      accentColor: color,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 42,
                height: 42,
                decoration: BoxDecoration(
                  color: color.withValues(alpha: 0.13),
                  borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
                ),
                child: Icon(configChangeStatusIcon(change), color: color),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      change.displaySummary,
                      style: Theme.of(context).textTheme.titleMedium?.copyWith(
                            color: AppTheme.textPrimary,
                            fontWeight: FontWeight.w700,
                          ),
                    ),
                    const SizedBox(height: 7),
                    ConfigChangeTarget(
                      change: change,
                      showInstanceId: true,
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              ConfigChangeStatusPill(change: change),
            ],
          ),
          if (change.errorText != null) ...[
            const SizedBox(height: 12),
            Text(
              change.errorText!,
              style: const TextStyle(
                color: AppTheme.error,
                fontSize: 12,
                height: 1.4,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _CurrentStatusBanner extends StatelessWidget {
  final ConfigChange change;

  const _CurrentStatusBanner({required this.change});

  @override
  Widget build(BuildContext context) {
    final color = configCurrentStatusColor(change);
    final icon = switch (change.currentStatus) {
      ConfigCurrentStatus.matchesApplied => Icons.verified_outlined,
      ConfigCurrentStatus.different => Icons.warning_amber_rounded,
      ConfigCurrentStatus.unavailable => Icons.cloud_off_outlined,
      ConfigCurrentStatus.unknown || null => Icons.help_outline,
    };
    return Semantics(
      liveRegion: true,
      label: configCurrentStatusLabel(change),
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.1),
          borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
          border: Border.all(color: color.withValues(alpha: 0.32)),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(icon, color: color, size: 20),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    configCurrentStatusLabel(change),
                    style: TextStyle(
                      color: color,
                      fontSize: 13,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  if (change.currentError != null) ...[
                    const SizedBox(height: 4),
                    Text(
                      change.currentError!,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12,
                        height: 1.35,
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _FieldComparisonCard extends StatelessWidget {
  final ConfigFieldChange field;
  final ConfigChange change;

  const _FieldComparisonCard({
    required this.field,
    required this.change,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 10),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant.withValues(alpha: 0.82),
        borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            field.label,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 13,
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 12),
          LayoutBuilder(builder: (context, constraints) {
            final currentValue = field.hasCurrent
                ? formatConfigValue(field.current)
                : change.currentStatus == ConfigCurrentStatus.matchesApplied
                    ? formatConfigValue(field.after)
                    : 'Unavailable';
            final currentNote =
                field.currentStateLabelFor(change.recordedValueLabel) ??
                    switch (change.currentStatus) {
                  ConfigCurrentStatus.matchesApplied =>
                    change.recordedValueMatchLabel,
                  ConfigCurrentStatus.different => 'Different',
                  ConfigCurrentStatus.unavailable => 'Could not verify',
                  _ => 'Not verified',
                };
            final values = [
              _ComparisonValue(
                label: 'Before',
                value: formatConfigValue(field.before),
              ),
              _ComparisonValue(
                label: change.recordedValueLabel,
                value: formatConfigValue(field.after),
              ),
              _ComparisonValue(
                label: 'Current',
                value: currentValue,
                note: currentNote,
              ),
            ];
            if (constraints.maxWidth < 580) {
              return Column(
                children: [
                  for (int i = 0; i < values.length; i++) ...[
                    values[i],
                    if (i < values.length - 1)
                      const Divider(height: 18, color: AppTheme.border),
                  ],
                ],
              );
            }
            return Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                for (int i = 0; i < values.length; i++) ...[
                  Expanded(child: values[i]),
                  if (i < values.length - 1) const SizedBox(width: 16),
                ],
              ],
            );
          }),
        ],
      ),
    );
  }
}

class _ComparisonValue extends StatelessWidget {
  final String label;
  final String value;
  final String? note;

  const _ComparisonValue({
    required this.label,
    required this.value,
    this.note,
  });

  @override
  Widget build(BuildContext context) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label.toUpperCase(),
            style: const TextStyle(
              color: AppTheme.textMuted,
              fontSize: 10,
              fontWeight: FontWeight.w800,
              letterSpacing: 1,
            ),
          ),
          const SizedBox(height: 5),
          SelectableText(
            value,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 12,
              height: 1.4,
              fontFamily: 'monospace',
            ),
          ),
          if (note != null) ...[
            const SizedBox(height: 3),
            Text(
              note!,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 11,
              ),
            ),
          ],
        ],
      );
}

class _ChangeMetadata extends StatelessWidget {
  final ConfigChange change;

  const _ChangeMetadata({required this.change});

  @override
  Widget build(BuildContext context) {
    final when = change.completedAt ?? change.createdAt;
    final actor = change.actorName.trim().isEmpty
        ? change.sourceLabel
        : '${change.actorName.trim()} through ${change.sourceLabel}';
    return AppPanel(
      padding: const EdgeInsets.all(14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            'Change record',
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 13,
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 9),
          _MetadataRow(label: 'Initiated by', value: actor),
          if (when != null)
            _MetadataRow(
              label: 'Recorded',
              value: DateFormat('MMM d, y · h:mm a').format(when),
            ),
          _MetadataRow(label: 'Change ID', value: '#${change.id}'),
          if (change.parentId != null)
            _MetadataRow(
              label: 'Restores change',
              value: '#${change.parentId}',
            ),
        ],
      ),
    );
  }
}

class _MetadataRow extends StatelessWidget {
  final String label;
  final String value;

  const _MetadataRow({required this.label, required this.value});

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.only(top: 5),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 112,
              child: Text(
                label,
                style: const TextStyle(
                  color: AppTheme.textMuted,
                  fontSize: 12,
                ),
              ),
            ),
            Expanded(
              child: SelectableText(
                value,
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 12,
                ),
              ),
            ),
          ],
        ),
      );
}

class _NoComparison extends StatelessWidget {
  const _NoComparison();

  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: AppTheme.surfaceVariant,
          borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
          border: Border.all(color: AppTheme.border),
        ),
        child: const Text(
          'No field comparison was recorded for this change.',
          style: TextStyle(color: AppTheme.textSecondary),
        ),
      );
}
