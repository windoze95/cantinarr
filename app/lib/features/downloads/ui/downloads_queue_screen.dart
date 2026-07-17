import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/network/websocket_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/downloads_api_service.dart';
import '../data/downloads_models.dart';

/// Shows the download client queue with global and per-item controls.
class DownloadsQueueScreen extends ConsumerStatefulWidget {
  const DownloadsQueueScreen({super.key});

  @override
  ConsumerState<DownloadsQueueScreen> createState() =>
      _DownloadsQueueScreenState();
}

class _DownloadsQueueScreenState extends ConsumerState<DownloadsQueueScreen> {
  DownloadsQueue? _queue;
  bool _isLoading = true;
  String? _error;
  Timer? _refreshTimer;

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

  DownloadsApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeDownloadInstance?.id;
    if (instanceId == null) return null;
    return DownloadsApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<void> _loadQueue({bool silent = false}) async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No download client configured';
      });
      return;
    }

    if (!silent) setState(() => _isLoading = true);
    try {
      final queue = await service.getQueue();
      if (!mounted) return;
      setState(() {
        _queue = queue;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      // Keep showing the last known data on silent refresh failures.
      if (silent) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load queue: $e';
      });
    }
  }

  /// Applies a full queue snapshot pushed over the WebSocket — no REST
  /// roundtrip needed; the event data matches the REST queue payload.
  void _applyQueueEvent(WsEvent event) {
    if (!mounted) return;
    try {
      final queue = DownloadsQueue.fromJson(event.data);
      setState(() {
        _queue = queue;
        _isLoading = false;
        _error = null;
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

  Future<void> _toggleGlobalPause() async {
    final service = _buildService();
    final queue = _queue;
    if (service == null || queue == null) return;
    await _runAction(
      queue.paused ? service.resumeAll : service.pauseAll,
      failureLabel:
          queue.paused ? 'Failed to resume queue' : 'Failed to pause queue',
    );
  }

  Future<void> _togglePauseItem(DownloadQueueItem item) async {
    final service = _buildService();
    if (service == null) return;
    await _runAction(
      () => item.isPaused
          ? service.resumeItem(item.id)
          : service.pauseItem(item.id),
      failureLabel: item.isPaused ? 'Failed to resume' : 'Failed to pause',
    );
  }

  Future<void> _removeItem(DownloadQueueItem item) async {
    final serviceType =
        ref.read(instanceProvider).activeDownloadInstance?.serviceType ?? '';
    final deleteData = await showDialog<bool>(
      context: context,
      builder: (_) =>
          _RemoveDownloadDialog(name: item.name, serviceType: serviceType),
    );
    if (deleteData == null || !mounted) return;

    final service = _buildService();
    if (service == null) return;
    try {
      await service.deleteItem(item.id, deleteData: deleteData);
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
    // Rebuild when instance changes
    ref.listen(instanceProvider.select((s) => s.activeDownloadInstanceId),
        (_, __) => _loadQueue());

    // Live queue snapshots over the WebSocket for the active instance;
    // the periodic poll remains as a fallback when the socket is down.
    final wsInstanceId =
        ref.watch(instanceProvider.select((s) => s.activeDownloadInstance?.id));
    if (wsInstanceId != null) {
      ref.listen(downloadsQueueEventsProvider(wsInstanceId), (_, next) {
        final event = next.valueOrNull;
        if (event != null) _applyQueueEvent(event);
      });
    }

    if (_isLoading) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 24),
              child: Text(_error!,
                  style: const TextStyle(color: AppTheme.textSecondary),
                  textAlign: TextAlign.center),
            ),
            const SizedBox(height: 16),
            ElevatedButton(onPressed: _loadQueue, child: const Text('Retry')),
          ],
        ),
      );
    }

    final queue = _queue ?? const DownloadsQueue();

    return Column(
      children: [
        _GlobalQueueHeader(
          paused: queue.paused,
          speedFormatted: queue.speedFormatted,
          itemCount: queue.items.length,
          onTogglePause: _toggleGlobalPause,
        ),
        Expanded(
          child: RefreshIndicator(
            onRefresh: _loadQueue,
            color: AppTheme.accent,
            child: queue.items.isEmpty
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
                    itemCount: queue.items.length,
                    itemBuilder: (context, index) {
                      final item = queue.items[index];
                      return _DownloadItemCard(
                        item: item,
                        onTogglePause: () => _togglePauseItem(item),
                        onRemove: () => _removeItem(item),
                      );
                    },
                  ),
          ),
        ),
      ],
    );
  }
}

/// Header row with total speed and a global pause/resume toggle.
class _GlobalQueueHeader extends StatelessWidget {
  final bool paused;
  final String speedFormatted;
  final int itemCount;
  final VoidCallback onTogglePause;

  const _GlobalQueueHeader({
    required this.paused,
    required this.speedFormatted,
    required this.itemCount,
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
                  ? 'Queue paused'
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
/// per-item speed (torrents), ETA, category badge and an actions menu.
class _DownloadItemCard extends StatelessWidget {
  final DownloadQueueItem item;
  final VoidCallback onTogglePause;
  final VoidCallback onRemove;

  const _DownloadItemCard({
    required this.item,
    required this.onTogglePause,
    required this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    final style = _statusStyle(item.status);
    final eta = item.etaFormatted;

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
