import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/config_change_models.dart';
import '../data/config_changes_service.dart';
import 'config_change_visuals.dart';

/// Durable, admin-only history of supported AI/MCP settings mutations.
class ConfigChangeHistoryScreen extends ConsumerStatefulWidget {
  const ConfigChangeHistoryScreen({super.key});

  @override
  ConsumerState<ConfigChangeHistoryScreen> createState() =>
      _ConfigChangeHistoryScreenState();
}

class _ConfigChangeHistoryScreenState
    extends ConsumerState<ConfigChangeHistoryScreen>
    with WidgetsBindingObserver {
  static const _pageSize = 100;

  List<ConfigChange>? _changes;
  bool _loading = true;
  bool _loadingOlder = false;
  bool _hasOlder = false;
  String? _error;
  int _loadEpoch = 0;
  Timer? _poll;

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
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    if (!mounted) return;
    final epoch = ++_loadEpoch;
    setState(() {
      _loading = _changes == null;
      if (_changes == null) _error = null;
    });
    try {
      final loaded = (await ref
              .read(configChangesServiceProvider)
              .listChanges(limit: _pageSize))
          .toList();
      loaded.sort(_compareChanges);
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _changes = loaded;
        _loading = false;
        _hasOlder = loaded.length == _pageSize;
        _error = null;
      });
      _syncPolling(loaded.any((change) => change.isLive));
    } catch (error) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _loading = false;
        _error = _friendlyError(error);
      });
    }
  }

  Future<void> _loadOlder() async {
    final current = _changes;
    if (_loadingOlder || !_hasOlder || current == null || current.isEmpty) {
      return;
    }
    setState(() => _loadingOlder = true);
    try {
      final oldestId = current
          .map((change) => change.id)
          .reduce((value, id) => id < value ? id : value);
      final older = (await ref.read(configChangesServiceProvider).listChanges(
                limit: _pageSize,
                beforeId: oldestId,
              ))
          .toList();
      if (!mounted) return;
      final byId = {
        for (final change in current) change.id: change,
        for (final change in older) change.id: change,
      };
      final merged = byId.values.toList()
        ..sort(_compareChanges);
      setState(() {
        _changes = merged;
        _hasOlder = older.length == _pageSize;
      });
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not load older changes.')),
      );
    } finally {
      if (mounted) setState(() => _loadingOlder = false);
    }
  }

  void _syncPolling(bool hasLiveChange) {
    if (hasLiveChange) {
      _poll ??= Timer.periodic(const Duration(seconds: 5), (_) => _load());
    } else {
      _poll?.cancel();
      _poll = null;
    }
  }

  DateTime _sortDate(ConfigChange change) =>
      change.completedAt ?? change.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);

  int _compareChanges(ConfigChange a, ConfigChange b) {
    final byTime = _sortDate(b).compareTo(_sortDate(a));
    return byTime != 0 ? byTime : b.id.compareTo(a.id);
  }

  String _friendlyError(Object error) {
    final match = RegExp(r'"error":"([^"]+)"').firstMatch(error.toString());
    return match?.group(1) ?? 'Could not load change history.';
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Configuration history')),
      body: _loading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent),
            )
          : _changes == null
              ? FullScreenError(
                  title: 'Configuration history unavailable',
                  message: _error ?? 'Could not load change history.',
                  onRetry: _load,
                )
              : RefreshIndicator(
                  color: AppTheme.accent,
                  onRefresh: _load,
                  child: LayoutBuilder(builder: (context, constraints) {
                    final hPad = AppBreakpoints.centeredContentPadding(
                      constraints.maxWidth,
                    );
                    return ListView(
                      physics: const AlwaysScrollableScrollPhysics(),
                      padding: EdgeInsets.fromLTRB(hPad, 12, hPad, 32),
                      children: [
                        AppPanel(
                          padding: const EdgeInsets.all(16),
                          accentColor: AppTheme.signal,
                          child: Row(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Container(
                                width: 42,
                                height: 42,
                                decoration: BoxDecoration(
                                  color:
                                      AppTheme.signal.withValues(alpha: 0.13),
                                  borderRadius: BorderRadius.circular(
                                    AppTheme.radiusMedium,
                                  ),
                                ),
                                child: const Icon(
                                  Icons.manage_history_outlined,
                                  color: AppTheme.signal,
                                ),
                              ),
                              const SizedBox(width: 12),
                              const Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(
                                      'Connected-app changes',
                                      style: TextStyle(
                                        color: AppTheme.textPrimary,
                                        fontSize: 15,
                                        fontWeight: FontWeight.w700,
                                      ),
                                    ),
                                    SizedBox(height: 4),
                                    Text(
                                      'AI and MCP quality-profile and custom-format writes are recorded here for review, live comparison, and troubleshooting. Applied profile updates can also be safely restored once while nothing has drifted.',
                                      style: TextStyle(
                                        color: AppTheme.textSecondary,
                                        fontSize: 13,
                                        height: 1.4,
                                      ),
                                    ),
                                  ],
                                ),
                              ),
                            ],
                          ),
                        ),
                        if (_error != null) ...[
                          const SizedBox(height: 12),
                          ErrorBanner(
                            message:
                                "Couldn't refresh change history. Showing the last update.",
                            onRetry: _load,
                          ),
                        ],
                        const SizedBox(height: 18),
                        if (_changes!.isEmpty)
                          const _EmptyHistory()
                        else ...[
                          ..._buildSections(_changes!),
                          if (_hasOlder || _loadingOlder) ...[
                            const SizedBox(height: 12),
                            Center(
                              child: OutlinedButton.icon(
                                onPressed: _loadingOlder ? null : _loadOlder,
                                icon: _loadingOlder
                                    ? const SizedBox(
                                        width: 16,
                                        height: 16,
                                        child: CircularProgressIndicator(
                                          strokeWidth: 2,
                                        ),
                                      )
                                    : const Icon(Icons.expand_more_rounded),
                                label: const Text('Load older changes'),
                              ),
                            ),
                          ],
                        ],
                      ],
                    );
                  }),
                ),
    );
  }

  List<Widget> _buildSections(List<ConfigChange> changes) {
    final sections = <DateTime, List<ConfigChange>>{};
    for (final change in changes) {
      final date = _sortDate(change);
      final day = DateTime(date.year, date.month, date.day);
      sections.putIfAbsent(day, () => []).add(change);
    }

    final widgets = <Widget>[];
    for (final entry in sections.entries) {
      widgets.add(_DayHeader(day: entry.key));
      for (final change in entry.value) {
        widgets.add(
          _HistoryEntry(
            change: change,
            onTap: () async {
              await context.push('/settings/change-history/${change.id}');
              if (mounted) _load();
            },
          ),
        );
      }
    }
    return widgets;
  }
}

class _DayHeader extends StatelessWidget {
  final DateTime day;

  const _DayHeader({required this.day});

  @override
  Widget build(BuildContext context) {
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    final difference = today.difference(day).inDays;
    final label = switch (difference) {
      0 => 'TODAY',
      1 => 'YESTERDAY',
      _ => DateFormat('MMM d, y').format(day).toUpperCase(),
    };
    return Padding(
      padding: const EdgeInsets.fromLTRB(2, 14, 2, 8),
      child: Text(
        label,
        style: const TextStyle(
          color: AppTheme.textMuted,
          fontSize: 10,
          fontWeight: FontWeight.w800,
          letterSpacing: 1.2,
        ),
      ),
    );
  }
}

class _HistoryEntry extends StatelessWidget {
  final ConfigChange change;
  final VoidCallback onTap;

  const _HistoryEntry({required this.change, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final color = configChangeStatusColor(change);
    final time = change.completedAt ?? change.createdAt;
    final by = change.actorName.trim().isEmpty
        ? change.sourceLabel
        : '${change.sourceLabel} · ${change.actorName.trim()}';
    return Semantics(
      button: true,
      label:
          '${change.displaySummary}. ${change.statusLabel}. ${change.serviceLabel}, ${change.instanceName}',
      child: Stack(
        children: [
          Positioned(
            left: 11,
            top: 0,
            bottom: 0,
            child: Container(
              width: 2,
              color: AppTheme.border,
            ),
          ),
          Padding(
            padding: const EdgeInsets.only(left: 32, bottom: 8),
            child: Material(
              color: AppTheme.surfaceVariant.withValues(alpha: 0.82),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
                side: const BorderSide(color: AppTheme.border),
              ),
              clipBehavior: Clip.antiAlias,
              child: InkWell(
                onTap: onTap,
                child: Padding(
                  padding: const EdgeInsets.all(14),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Expanded(
                            child: Text(
                              change.displaySummary,
                              style: const TextStyle(
                                color: AppTheme.textPrimary,
                                fontSize: 14,
                                height: 1.3,
                                fontWeight: FontWeight.w700,
                              ),
                            ),
                          ),
                          const SizedBox(width: 8),
                          ConfigChangeStatusPill(change: change),
                        ],
                      ),
                      const SizedBox(height: 8),
                      ConfigChangeTarget(change: change),
                      const SizedBox(height: 9),
                      Text(
                        '$by${time == null ? '' : ' · ${DateFormat.jm().format(time)}'}',
                        style: const TextStyle(
                          color: AppTheme.textMuted,
                          fontSize: 11,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
          Positioned(
            left: 3,
            top: 18,
            child: Container(
              width: 18,
              height: 18,
              decoration: BoxDecoration(
                color: AppTheme.surface,
                shape: BoxShape.circle,
                border: Border.all(color: color, width: 2),
              ),
              child: Icon(
                configChangeStatusIcon(change),
                color: color,
                size: 11,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _EmptyHistory extends StatelessWidget {
  const _EmptyHistory();

  @override
  Widget build(BuildContext context) => const Padding(
        padding: EdgeInsets.symmetric(vertical: 64, horizontal: 24),
        child: Column(
          children: [
            Icon(
              Icons.history_toggle_off_outlined,
              size: 48,
              color: AppTheme.textMuted,
            ),
            SizedBox(height: 14),
            Text(
              'No connected-app changes yet',
              style: TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 16,
                fontWeight: FontWeight.w700,
              ),
            ),
            SizedBox(height: 6),
            Text(
              'Changes made by the AI Assistant and other Cantinarr tools will appear here.',
              textAlign: TextAlign.center,
              style: TextStyle(color: AppTheme.textSecondary, height: 1.4),
            ),
          ],
        ),
      );
}
