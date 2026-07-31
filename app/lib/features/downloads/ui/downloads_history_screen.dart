import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/downloads_api_service.dart';
import '../data/downloads_models.dart';

/// Recent download client history: completed and failed downloads.
///
/// With the aggregate "All" view active every client's history is fetched
/// and merged newest-first (each tile names its client); a client that
/// fails to load is named in a banner instead of silently missing.
class DownloadsHistoryScreen extends ConsumerStatefulWidget {
  const DownloadsHistoryScreen({super.key});

  @override
  ConsumerState<DownloadsHistoryScreen> createState() =>
      _DownloadsHistoryScreenState();
}

class _DownloadsHistoryScreenState
    extends ConsumerState<DownloadsHistoryScreen> {
  /// Last known history per source instance id.
  final Map<String, List<DownloadHistoryItem>> _histories = {};

  /// Load failure per source instance id; cleared by a successful read.
  final Map<String, String> _sourceErrors = {};

  bool _isLoading = true;

  /// Bumped by every load; in-flight results from a superseded load (e.g.
  /// started before a selection switch) are dropped instead of applied.
  int _loadGeneration = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  /// The clients this screen currently shows: every download client in the
  /// aggregate "All" view, otherwise just the active one.
  List<ServiceInstance> _sourcesFrom(InstanceState state) {
    if (state.allDownloadsActive) return state.downloadInstances;
    final active = state.activeDownloadInstance;
    return active == null ? const [] : [active];
  }

  Future<void> _load() async {
    final generation = ++_loadGeneration;
    final sources = _sourcesFrom(ref.read(instanceProvider));
    if (sources.isEmpty) {
      if (!mounted) return;
      setState(() => _isLoading = false);
      return;
    }

    setState(() => _isLoading = true);
    final dio = ref.read(backendClientProvider);
    await Future.wait(sources.map((source) async {
      final service =
          DownloadsApiService(backendDio: dio, instanceId: source.id);
      try {
        final items = await service.getHistory(limit: 50);
        if (!mounted || generation != _loadGeneration) return;
        setState(() {
          _histories[source.id] = items;
          _sourceErrors.remove(source.id);
        });
      } catch (e) {
        if (!mounted || generation != _loadGeneration) return;
        setState(() => _sourceErrors[source.id] = '$e');
      }
    }));
    if (!mounted || generation != _loadGeneration) return;
    setState(() => _isLoading = false);
  }

  @override
  Widget build(BuildContext context) {
    final instanceState = ref.watch(instanceProvider);
    final sources = _sourcesFrom(instanceState);

    // Reset and reload when the selection changes (client <-> All).
    ref.listen(instanceProvider.select((s) => s.activeDownloadInstanceId),
        (_, __) {
      setState(() {
        _histories.clear();
        _sourceErrors.clear();
        _isLoading = true;
      });
      _load();
    });

    if (_isLoading) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }

    final failedSources = [
      for (final source in sources)
        if (_sourceErrors.containsKey(source.id)) source,
    ];
    final nothingLoaded =
        sources.every((source) => _histories[source.id] == null);
    if (sources.isEmpty) {
      return FullScreenError(
          message: 'No download client configured', onRetry: _load);
    }
    if (nothingLoaded && failedSources.length == sources.length) {
      final message = sources.length == 1
          ? 'Failed to load history: ${_sourceErrors[sources.single.id]}'
          : 'Failed to load history:\n${[
              for (final source in failedSources)
                '${source.name}: ${_sourceErrors[source.id]}'
            ].join('\n')}';
      return FullScreenError(message: message, onRetry: _load);
    }

    // One list in source order; in the aggregate view the lists are merged
    // newest-first (undated entries keep their client's reported position).
    final showClient = sources.length > 1;
    final entries = [
      for (final source in sources)
        for (final item
            in _histories[source.id] ?? const <DownloadHistoryItem>[])
          (source: source, item: item),
    ];
    if (showClient) _sortNewestFirst(entries);

    final banner = failedSources.isNotEmpty
        ? _HistoryErrorBanner(
            names: [for (final source in failedSources) source.name])
        : null;

    if (entries.isEmpty) {
      return Column(
        children: [
          if (banner != null) banner,
          Expanded(
            child: RefreshIndicator(
              onRefresh: _load,
              color: AppTheme.accent,
              child: ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                children: const [
                  SizedBox(height: 160),
                  Icon(Icons.history, size: 48, color: AppTheme.textSecondary),
                  SizedBox(height: 12),
                  Center(
                    child: Text('No history yet',
                        style: TextStyle(
                            color: AppTheme.textSecondary, fontSize: 16)),
                  ),
                ],
              ),
            ),
          ),
        ],
      );
    }

    return Column(
      children: [
        if (banner != null) banner,
        Expanded(
          child: RefreshIndicator(
            onRefresh: _load,
            color: AppTheme.accent,
            child: ListView.builder(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.symmetric(vertical: 8),
              itemCount: entries.length,
              itemBuilder: (context, index) {
                final entry = entries[index];
                return _HistoryTile(
                  item: entry.item,
                  clientName: showClient ? entry.source.name : null,
                );
              },
            ),
          ),
        ),
      ],
    );
  }
}

/// Sorts merged history entries by completion time, newest first. Undated
/// entries sink below dated ones; the sort is stable so each client's own
/// reported order is preserved among equals.
void _sortNewestFirst(
    List<({ServiceInstance source, DownloadHistoryItem item})> entries) {
  final indexed = [
    for (var i = 0; i < entries.length; i++) (index: i, entry: entries[i]),
  ];
  indexed.sort((a, b) {
    final at = a.entry.item.completedAt;
    final bt = b.entry.item.completedAt;
    if (at == null && bt == null) return a.index - b.index;
    if (at == null) return 1;
    if (bt == null) return -1;
    final byTime = bt.compareTo(at);
    return byTime != 0 ? byTime : a.index - b.index;
  });
  for (var i = 0; i < indexed.length; i++) {
    entries[i] = indexed[i].entry;
  }
}

/// Names the clients whose last load failed, so a partial aggregate list is
/// never mistaken for the whole picture. Pull to refresh retries them.
class _HistoryErrorBanner extends StatelessWidget {
  final List<String> names;

  const _HistoryErrorBanner({required this.names});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      color: AppTheme.error.withValues(alpha: 0.12),
      child: Row(
        children: [
          const Icon(Icons.warning_amber_rounded,
              size: 15, color: AppTheme.error),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Not responding: ${names.join(', ')}',
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 12),
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}

String _relativeTime(DateTime? date) {
  if (date == null) return '';
  final local = date.toLocal();
  final diff = DateTime.now().difference(local);
  if (diff.inMinutes < 1) return 'just now';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
  if (diff.inHours < 24) return '${diff.inHours}h ago';
  if (diff.inDays < 7) return '${diff.inDays}d ago';
  if (local.year == DateTime.now().year) {
    return DateFormat('MMM d').format(local);
  }
  return DateFormat('MMM d, yyyy').format(local);
}

({IconData icon, Color color, String label}) _historyStyle(
    DownloadHistoryItem item) {
  if (item.isFailed) {
    return (icon: Icons.error_outline, color: AppTheme.error, label: 'Failed');
  }
  if (item.isCompleted) {
    return (
      icon: Icons.check_circle_outline,
      color: AppTheme.available,
      label: 'Completed'
    );
  }
  return (
    icon: Icons.history,
    color: AppTheme.textSecondary,
    label: item.status.isEmpty ? 'Unknown' : item.status
  );
}

class _HistoryTile extends StatelessWidget {
  final DownloadHistoryItem item;

  /// Owning client name; named in the subtitle only in the aggregate view.
  final String? clientName;

  const _HistoryTile({required this.item, this.clientName});

  @override
  Widget build(BuildContext context) {
    final style = _historyStyle(item);
    final subtitleParts = [
      style.label,
      if (item.sizeBytes > 0) item.sizeFormatted,
      if (item.category.isNotEmpty) item.category,
      if (clientName != null) clientName!,
    ];

    final leading = Container(
      width: 36,
      height: 36,
      decoration: BoxDecoration(
        color: style.color.withValues(alpha: 0.15),
        shape: BoxShape.circle,
      ),
      child: Icon(style.icon, color: style.color, size: 20),
    );
    final title = Text(
      item.name,
      style: const TextStyle(
          color: AppTheme.textPrimary,
          fontSize: 13.5,
          fontWeight: FontWeight.w500),
      maxLines: 2,
      overflow: TextOverflow.ellipsis,
    );
    final subtitle = Text(
      subtitleParts.join(' • '),
      style: TextStyle(color: style.color, fontSize: 12),
    );
    final trailing = Text(
      _relativeTime(item.completedAt),
      style: const TextStyle(color: AppTheme.textSecondary, fontSize: 11),
    );

    // Failed items with an error message expand to show it.
    if (item.isFailed && item.error.isNotEmpty) {
      return ExpansionTile(
        tilePadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 2),
        leading: leading,
        title: title,
        subtitle: subtitle,
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            trailing,
            const SizedBox(width: 4),
            const Icon(Icons.expand_more,
                size: 16, color: AppTheme.textSecondary),
          ],
        ),
        iconColor: AppTheme.textSecondary,
        collapsedIconColor: AppTheme.textSecondary,
        shape: const Border(),
        collapsedShape: const Border(),
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
            child: Container(
              width: double.infinity,
              padding: const EdgeInsets.all(8),
              decoration: BoxDecoration(
                color: AppTheme.error.withValues(alpha: 0.1),
                borderRadius: BorderRadius.circular(6),
              ),
              child: Text(
                item.error,
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 12),
              ),
            ),
          ),
        ],
      );
    }

    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 2),
      leading: leading,
      title: title,
      subtitle: subtitle,
      trailing: trailing,
    );
  }
}
