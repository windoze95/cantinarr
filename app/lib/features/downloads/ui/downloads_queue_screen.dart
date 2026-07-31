import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/network/websocket_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/downloads_api_service.dart';
import '../data/downloads_models.dart';

/// Shows the download client queue with global and per-item controls.
///
/// With the aggregate "All" view active every download client is a source:
/// each client's queue is fetched (and pushed over the WebSocket) separately,
/// rendered as one list in client-menu order (usenet clients first) with a
/// client badge per item, and the global toggle pauses or resumes every
/// client at once. A client that fails to load is named in a banner instead
/// of silently missing from the list.
class DownloadsQueueScreen extends ConsumerStatefulWidget {
  const DownloadsQueueScreen({super.key});

  @override
  ConsumerState<DownloadsQueueScreen> createState() =>
      _DownloadsQueueScreenState();
}

class _DownloadsQueueScreenState extends ConsumerState<DownloadsQueueScreen> {
  /// Last known queue per source instance id.
  final Map<String, DownloadsQueue> _queues = {};

  /// Load failure per source instance id; cleared by any successful read.
  final Map<String, String> _sourceErrors = {};

  bool _isLoading = true;
  Timer? _refreshTimer;

  /// Bumped by every load; in-flight results from a superseded load (e.g.
  /// started before a selection switch) are dropped instead of applied.
  int _loadGeneration = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _loadQueue();
      // Fallback poll only — live updates arrive over the WebSocket
      // (downloads_queue events); this covers gaps when the socket is down.
      _refreshTimer =
          Timer.periodic(const Duration(seconds: 30), (_) => _autoRefresh());
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    super.dispose();
  }

  void _autoRefresh() {
    if (!mounted) return;
    // Skip silent refreshes when another route is on top of this screen.
    final route = ModalRoute.of(context);
    if (route != null && !route.isCurrent) return;
    _loadQueue(silent: true);
  }

  /// The clients this screen currently shows: every download client in the
  /// aggregate "All" view, otherwise just the active one.
  List<ServiceInstance> _sourcesFrom(InstanceState state) {
    if (state.allDownloadsActive) return state.downloadInstances;
    final active = state.activeDownloadInstance;
    return active == null ? const [] : [active];
  }

  DownloadsApiService _serviceFor(ServiceInstance source) =>
      DownloadsApiService(
        backendDio: ref.read(backendClientProvider),
        instanceId: source.id,
      );

  Future<void> _loadQueue({bool silent = false}) async {
    final generation = ++_loadGeneration;
    final sources = _sourcesFrom(ref.read(instanceProvider));
    if (sources.isEmpty) {
      if (!mounted) return;
      setState(() => _isLoading = false);
      return;
    }

    if (!silent) setState(() => _isLoading = true);
    await Future.wait(sources.map((source) async {
      try {
        final queue = await _serviceFor(source).getQueue();
        if (!mounted || generation != _loadGeneration) return;
        setState(() {
          _queues[source.id] = queue;
          _sourceErrors.remove(source.id);
        });
      } catch (e) {
        if (!mounted || generation != _loadGeneration) return;
        // Keep showing the last known data; the banner names the client.
        setState(() => _sourceErrors[source.id] = '$e');
      }
    }));
    if (!mounted || generation != _loadGeneration) return;
    setState(() => _isLoading = false);
  }

  /// Applies a full queue snapshot pushed over the WebSocket — no REST
  /// roundtrip needed; the event data matches the REST queue payload.
  void _applyQueueEvent(String instanceId, WsEvent event) {
    if (!mounted) return;
    try {
      final queue = DownloadsQueue.fromJson(event.data);
      setState(() {
        _queues[instanceId] = queue;
        _sourceErrors.remove(instanceId);
        _isLoading = false;
      });
    } catch (_) {
      // Malformed payload (e.g. server/app version skew); the polling
      // fallback will correct any drift.
    }
  }

  Future<void> _runAction(Future<void> Function() action,
      {String? failureLabel}) async {
    try {
      await action();
      if (!mounted) return;
      await _loadQueue(silent: true);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('${failureLabel ?? 'Action failed'}: $e')));
    }
  }

  /// Whether every source with known data reports its queue paused.
  bool _allPaused(List<ServiceInstance> sources) {
    final known = [
      for (final source in sources)
        if (_queues[source.id] != null) _queues[source.id]!,
    ];
    return known.isNotEmpty && known.every((queue) => queue.paused);
  }

  /// Pauses or resumes every shown client. Failures are named per client;
  /// the rest proceed regardless.
  Future<void> _toggleGlobalPause() async {
    final sources = _sourcesFrom(ref.read(instanceProvider));
    if (sources.isEmpty) return;
    final resume = _allPaused(sources);
    final failures = <String>[];
    await Future.wait(sources.map((source) async {
      try {
        final service = _serviceFor(source);
        resume ? await service.resumeAll() : await service.pauseAll();
      } catch (_) {
        failures.add(source.name);
      }
    }));
    if (!mounted) return;
    if (failures.isNotEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text('Failed to ${resume ? 'resume' : 'pause'}: '
              '${failures.join(', ')}')));
    }
    await _loadQueue(silent: true);
  }

  Future<void> _togglePauseItem(
      ServiceInstance source, DownloadQueueItem item) async {
    final service = _serviceFor(source);
    await _runAction(
      () => item.isPaused
          ? service.resumeItem(item.id)
          : service.pauseItem(item.id),
      failureLabel: item.isPaused ? 'Failed to resume' : 'Failed to pause',
    );
  }

  Future<void> _removeItem(
      ServiceInstance source, DownloadQueueItem item) async {
    final deleteData = await showDialog<bool>(
      context: context,
      builder: (_) => _RemoveDownloadDialog(
          name: item.name, serviceType: source.serviceType),
    );
    if (deleteData == null || !mounted) return;

    try {
      await _serviceFor(source).deleteItem(item.id, deleteData: deleteData);
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Removed from queue')));
      _loadQueue(silent: true);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to remove: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    final instanceState = ref.watch(instanceProvider);
    final sources = _sourcesFrom(instanceState);

    // Reset and reload when the selection changes (client <-> All).
    ref.listen(instanceProvider.select((s) => s.activeDownloadInstanceId),
        (_, __) {
      setState(() {
        _queues.clear();
        _sourceErrors.clear();
        _isLoading = true;
      });
      _loadQueue();
    });

    // Live queue snapshots over the WebSocket for every shown client; the
    // periodic poll remains as a fallback when the socket is down.
    for (final source in sources) {
      ref.listen(downloadsQueueEventsProvider(source.id), (_, next) {
        final event = next.valueOrNull;
        if (event != null) _applyQueueEvent(source.id, event);
      });
    }

    if (_isLoading) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }

    final failedSources = [
      for (final source in sources)
        if (_sourceErrors.containsKey(source.id)) source,
    ];
    final nothingLoaded =
        sources.every((source) => _queues[source.id] == null);
    String? fatalError;
    if (sources.isEmpty) {
      fatalError = 'No download client configured';
    } else if (nothingLoaded && failedSources.length == sources.length) {
      fatalError = sources.length == 1
          ? 'Failed to load queue: ${_sourceErrors[sources.single.id]}'
          : 'Failed to load queues:\n${[
              for (final source in failedSources)
                '${source.name}: ${_sourceErrors[source.id]}'
            ].join('\n')}';
    }
    if (fatalError != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 24),
              child: Text(fatalError,
                  style: const TextStyle(color: AppTheme.textSecondary),
                  textAlign: TextAlign.center),
            ),
            const SizedBox(height: 16),
            ElevatedButton(onPressed: _loadQueue, child: const Text('Retry')),
          ],
        ),
      );
    }

    // One list in source order (usenet clients first, matching the client
    // menu); each client's own queue order is preserved within its block.
    final entries = [
      for (final source in sources)
        for (final item
            in _queues[source.id]?.items ?? const <DownloadQueueItem>[])
          (source: source, item: item),
    ];
    final showClient = sources.length > 1;
    final speedBps = sources.fold<int>(
        0, (sum, source) => sum + (_queues[source.id]?.speedBps ?? 0));

    return Column(
      children: [
        _GlobalQueueHeader(
          paused: _allPaused(sources),
          speedFormatted: formatSpeed(speedBps),
          itemCount: entries.length,
          multiClient: showClient,
          onTogglePause: _toggleGlobalPause,
        ),
        if (failedSources.isNotEmpty)
          _SourceErrorBanner(
              names: [for (final source in failedSources) source.name]),
        Expanded(
          child: RefreshIndicator(
            onRefresh: _loadQueue,
            color: AppTheme.accent,
            child: entries.isEmpty
                ? ListView(
                    physics: const AlwaysScrollableScrollPhysics(),
                    children: const [
                      SizedBox(height: 160),
                      Icon(Icons.check_circle_outline,
                          size: 48, color: AppTheme.available),
                      SizedBox(height: 12),
                      Center(
                        child: Text('Queue is empty',
                            style: TextStyle(
                                color: AppTheme.textSecondary, fontSize: 16)),
                      ),
                    ],
                  )
                : ListView.builder(
                    physics: const AlwaysScrollableScrollPhysics(),
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    itemCount: entries.length,
                    itemBuilder: (context, index) {
                      final entry = entries[index];
                      return _DownloadItemCard(
                        item: entry.item,
                        clientName: showClient ? entry.source.name : null,
                        onTogglePause: () =>
                            _togglePauseItem(entry.source, entry.item),
                        onRemove: () => _removeItem(entry.source, entry.item),
                      );
                    },
                  ),
          ),
        ),
      ],
    );
  }
}

/// Header row with total speed and a global pause/resume toggle. In the
/// aggregate view the speed is the sum across clients and the toggle acts
/// on every client.
class _GlobalQueueHeader extends StatelessWidget {
  final bool paused;
  final String speedFormatted;
  final int itemCount;
  final bool multiClient;
  final VoidCallback onTogglePause;

  const _GlobalQueueHeader({
    required this.paused,
    required this.speedFormatted,
    required this.itemCount,
    required this.multiClient,
    required this.onTogglePause,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 10, 8, 10),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        border: Border(
          bottom: BorderSide(color: AppTheme.border, width: 0.5),
        ),
      ),
      child: Row(
        children: [
          Icon(
            paused ? Icons.pause_circle_outline : Icons.speed,
            size: 18,
            color: paused ? AppTheme.unavailable : AppTheme.downloading,
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              paused
                  ? (multiClient ? 'All queues paused' : 'Queue paused')
                  : '$speedFormatted • $itemCount item${itemCount == 1 ? '' : 's'}',
              style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w500),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          TextButton.icon(
            onPressed: onTogglePause,
            icon: Icon(paused ? Icons.play_arrow : Icons.pause, size: 18),
            label: Text(paused ? 'Resume all' : 'Pause all'),
            style: TextButton.styleFrom(
              foregroundColor: paused ? AppTheme.available : AppTheme.accent,
              textStyle:
                  const TextStyle(fontSize: 12.5, fontWeight: FontWeight.w600),
            ),
          ),
        ],
      ),
    );
  }
}

/// Names the clients whose last load failed, so partial aggregate data is
/// never mistaken for the whole picture. Pull to refresh retries them.
class _SourceErrorBanner extends StatelessWidget {
  final List<String> names;

  const _SourceErrorBanner({required this.names});

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

/// Confirmation dialog for removing a download, with an optional checkbox to
/// also delete downloaded data (default OFF).
///
/// NZBGet has no way to remove downloaded files together with the queue item,
/// so for NZBGet instances the checkbox is replaced by a factual hint and the
/// dialog always resolves to `false`.
class _RemoveDownloadDialog extends StatefulWidget {
  final String name;
  final String serviceType;

  const _RemoveDownloadDialog({required this.name, required this.serviceType});

  @override
  State<_RemoveDownloadDialog> createState() => _RemoveDownloadDialogState();
}

class _RemoveDownloadDialogState extends State<_RemoveDownloadDialog> {
  bool _deleteData = false;

  bool get _supportsDeleteData => widget.serviceType != 'nzbget';

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      backgroundColor: AppTheme.surface,
      title: const Text('Remove Download'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            widget.name,
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
          ),
          const SizedBox(height: 12),
          if (_supportsDeleteData)
            CheckboxListTile(
              value: _deleteData,
              onChanged: (v) => setState(() => _deleteData = v ?? false),
              title: const Text('Also delete downloaded data',
                  style: TextStyle(color: AppTheme.textPrimary, fontSize: 14)),
              controlAffinity: ListTileControlAffinity.leading,
              contentPadding: EdgeInsets.zero,
              activeColor: AppTheme.accent,
            )
          else
            const Text(
              'NZBGet removes the queue item only; '
              'downloaded files stay on disk.',
              style:
                  TextStyle(color: AppTheme.textSecondary, fontSize: 12.5),
            ),
        ],
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel')),
        TextButton(
          onPressed: () =>
              Navigator.pop(context, _supportsDeleteData && _deleteData),
          style: TextButton.styleFrom(foregroundColor: AppTheme.error),
          child: const Text('Remove'),
        ),
      ],
    );
  }
}

({String label, Color color}) _statusStyle(String status) {
  final s = status.toLowerCase();
  if (s.contains('error') || s.contains('fail') || s.contains('missing')) {
    return (label: 'Error', color: AppTheme.error);
  }
  if (s.contains('stalled')) {
    return (label: 'Stalled', color: AppTheme.requested);
  }
  if (s.contains('paused') || s.contains('stopped')) {
    return (label: 'Paused', color: AppTheme.unavailable);
  }
  if (s.contains('queued') ||
      s.contains('alloc') ||
      s.contains('meta') ||
      s.contains('checking') ||
      s.contains('fetching') ||
      s.contains('grabbing') ||
      s.contains('propagating')) {
    return (label: 'Queued', color: AppTheme.unavailable);
  }
  if (s.contains('upload') || s.contains('seed') || s.contains('complet')) {
    return (label: 'Completed', color: AppTheme.available);
  }
  if (s.contains('extract') ||
      s.contains('repair') ||
      s.contains('verify') ||
      s.contains('moving')) {
    return (label: 'Processing', color: AppTheme.requested);
  }
  if (s.contains('download') || s.contains('running')) {
    return (label: 'Downloading', color: AppTheme.downloading);
  }
  return (
    label: status.isEmpty ? 'Unknown' : status,
    color: AppTheme.textSecondary
  );
}

/// One download in the queue: name, status chip, progress bar, sizes,
/// per-item speed (torrents), ETA, category badge, the owning client
/// (aggregate view only) and an actions menu.
class _DownloadItemCard extends StatelessWidget {
  final DownloadQueueItem item;

  /// Owning client name; shown as a badge only in the aggregate view.
  final String? clientName;
  final VoidCallback onTogglePause;
  final VoidCallback onRemove;

  const _DownloadItemCard({
    required this.item,
    this.clientName,
    required this.onTogglePause,
    required this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    final style = _statusStyle(item.status);
    final eta = item.etaFormatted;
    final client = clientName;

    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      padding: const EdgeInsets.fromLTRB(12, 10, 4, 12),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border, width: 0.5),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                child: Text(
                  item.name,
                  style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontWeight: FontWeight.w600,
                      fontSize: 14),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              PopupMenuButton<String>(
                icon: const Icon(Icons.more_vert,
                    color: AppTheme.textSecondary, size: 20),
                color: AppTheme.surfaceVariant,
                onSelected: (value) {
                  if (value == 'toggle') onTogglePause();
                  if (value == 'remove') onRemove();
                },
                itemBuilder: (_) => [
                  PopupMenuItem(
                    value: 'toggle',
                    child: Row(
                      children: [
                        Icon(
                          item.isPaused ? Icons.play_arrow : Icons.pause,
                          size: 18,
                          color: AppTheme.textSecondary,
                        ),
                        const SizedBox(width: 8),
                        Text(item.isPaused ? 'Resume' : 'Pause'),
                      ],
                    ),
                  ),
                  const PopupMenuItem(
                    value: 'remove',
                    child: Row(
                      children: [
                        Icon(Icons.delete_outline,
                            size: 18, color: AppTheme.error),
                        SizedBox(width: 8),
                        Text('Remove'),
                      ],
                    ),
                  ),
                ],
              ),
            ],
          ),
          const SizedBox(height: 6),
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: Wrap(
              spacing: 6,
              runSpacing: 4,
              children: [
                _DownloadBadge(text: style.label, color: style.color),
                if (item.category.isNotEmpty)
                  _DownloadBadge(text: item.category, color: AppTheme.accent),
                if (client != null)
                  _DownloadBadge(text: client, color: AppTheme.textSecondary),
              ],
            ),
          ),
          const SizedBox(height: 10),
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: ClipRRect(
              borderRadius: BorderRadius.circular(3),
              child: LinearProgressIndicator(
                value: item.progressFraction,
                minHeight: 5,
                backgroundColor: AppTheme.surfaceVariant,
                valueColor: AlwaysStoppedAnimation(style.color),
              ),
            ),
          ),
          const SizedBox(height: 6),
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    '${item.progress.toStringAsFixed(1)}% • '
                    '${item.downloadedFormatted} of ${item.sizeFormatted}',
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 11),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (item.speedBps > 0)
                  Padding(
                    padding: const EdgeInsets.only(left: 8),
                    child: Text(
                      item.speedFormatted,
                      style: const TextStyle(
                          color: AppTheme.downloading, fontSize: 11),
                    ),
                  ),
                if (eta.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(left: 8),
                    child: Text(
                      eta,
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 11),
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _DownloadBadge extends StatelessWidget {
  final String text;
  final Color color;

  const _DownloadBadge({required this.text, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        text,
        style: TextStyle(
            color: color, fontSize: 10.5, fontWeight: FontWeight.w500),
      ),
    );
  }
}
