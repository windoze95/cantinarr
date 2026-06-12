import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';

/// Shows the current Radarr download queue with per-item actions.
class RadarrQueueScreen extends ConsumerStatefulWidget {
  const RadarrQueueScreen({super.key});

  @override
  ConsumerState<RadarrQueueScreen> createState() => _RadarrQueueScreenState();
}

class _RadarrQueueScreenState extends ConsumerState<RadarrQueueScreen> {
  List<RadarrQueueItem> _queue = [];
  bool _isLoading = true;
  String? _error;
  Timer? _refreshTimer;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _loadQueue();
      _refreshTimer =
          Timer.periodic(const Duration(seconds: 15), (_) => _autoRefresh());
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

  RadarrApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeRadarrInstance?.id;
    if (instanceId == null) return null;
    return RadarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<void> _loadQueue({bool silent = false}) async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Radarr instance configured';
      });
      return;
    }

    if (!silent) setState(() => _isLoading = true);
    try {
      final queue = await service.getQueueDetailed();
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

  Future<void> _removeItem(RadarrQueueItem item) async {
    final result = await showDialog<({bool removeFromClient, bool blocklist})>(
      context: context,
      builder: (_) => const _RemoveQueueItemDialog(),
    );
    if (result == null || !mounted) return;

    final service = _buildService();
    if (service == null) return;
    try {
      await service.deleteQueueItem(
        item.id,
        removeFromClient: result.removeFromClient,
        blocklist: result.blocklist,
      );
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
    ref.listen(instanceProvider.select((s) => s.activeRadarrInstanceId),
        (_, __) => _loadQueue());

    if (_isLoading) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(_error!,
                style: const TextStyle(color: AppTheme.textSecondary)),
            const SizedBox(height: 16),
            ElevatedButton(onPressed: _loadQueue, child: const Text('Retry')),
          ],
        ),
      );
    }
    if (_queue.isEmpty) {
      return RefreshIndicator(
        onRefresh: _loadQueue,
        color: AppTheme.accent,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: const [
            SizedBox(height: 160),
            Icon(Icons.check_circle_outline,
                size: 48, color: AppTheme.available),
            SizedBox(height: 12),
            Center(
              child: Text('Queue is empty',
                  style:
                      TextStyle(color: AppTheme.textSecondary, fontSize: 16)),
            ),
          ],
        ),
      );
    }

    return RefreshIndicator(
      onRefresh: _loadQueue,
      color: AppTheme.accent,
      child: ListView.builder(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: _queue.length,
        itemBuilder: (context, index) {
          final item = _queue[index];
          return _QueueItemCard(
            primaryTitle: item.movieTitle ?? item.title,
            releaseTitle: item.movieTitle != null ? item.title : null,
            status: item.status,
            trackedDownloadState: item.trackedDownloadState,
            trackedDownloadStatus: item.trackedDownloadStatus,
            protocol: item.protocol,
            indexer: item.indexer,
            downloadClient: item.downloadClient,
            quality: item.quality,
            progress: item.progress,
            downloadedFormatted: item.downloadedFormatted,
            sizeFormatted: item.sizeFormatted,
            timeleft: item.timeleft,
            errorMessage: item.errorMessage,
            statusMessages: item.statusMessages,
            hasIssues: item.hasIssues,
            onRemove: () => _removeItem(item),
          );
        },
      ),
    );
  }
}

/// Confirmation dialog for removing a queue item, with download client and
/// blocklist checkboxes.
class _RemoveQueueItemDialog extends StatefulWidget {
  const _RemoveQueueItemDialog();

  @override
  State<_RemoveQueueItemDialog> createState() => _RemoveQueueItemDialogState();
}

class _RemoveQueueItemDialogState extends State<_RemoveQueueItemDialog> {
  bool _removeFromClient = true;
  bool _blocklist = false;

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      backgroundColor: AppTheme.surface,
      title: const Text('Remove from Queue'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          CheckboxListTile(
            value: _removeFromClient,
            onChanged: (v) => setState(() => _removeFromClient = v ?? true),
            title: const Text('Remove from download client',
                style: TextStyle(color: AppTheme.textPrimary, fontSize: 14)),
            controlAffinity: ListTileControlAffinity.leading,
            contentPadding: EdgeInsets.zero,
            activeColor: AppTheme.accent,
          ),
          CheckboxListTile(
            value: _blocklist,
            onChanged: (v) => setState(() => _blocklist = v ?? false),
            title: const Text('Add to blocklist',
                style: TextStyle(color: AppTheme.textPrimary, fontSize: 14)),
            controlAffinity: ListTileControlAffinity.leading,
            contentPadding: EdgeInsets.zero,
            activeColor: AppTheme.accent,
          ),
        ],
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel')),
        TextButton(
          onPressed: () => Navigator.pop(context,
              (removeFromClient: _removeFromClient, blocklist: _blocklist)),
          style: TextButton.styleFrom(foregroundColor: AppTheme.error),
          child: const Text('Remove'),
        ),
      ],
    );
  }
}

/// A rich queue item card: status, protocol, indexer, download client,
/// progress bar, timeleft and any error/status messages.
class _QueueItemCard extends StatelessWidget {
  final String primaryTitle;
  final String? releaseTitle;
  final String status;
  final String? trackedDownloadState;
  final String? trackedDownloadStatus;
  final String protocol;
  final String? indexer;
  final String? downloadClient;
  final String? quality;
  final double progress;
  final String downloadedFormatted;
  final String sizeFormatted;
  final String? timeleft;
  final String? errorMessage;
  final List<String> statusMessages;
  final bool hasIssues;
  final VoidCallback onRemove;

  const _QueueItemCard({
    required this.primaryTitle,
    this.releaseTitle,
    required this.status,
    this.trackedDownloadState,
    this.trackedDownloadStatus,
    required this.protocol,
    this.indexer,
    this.downloadClient,
    this.quality,
    required this.progress,
    required this.downloadedFormatted,
    required this.sizeFormatted,
    this.timeleft,
    this.errorMessage,
    this.statusMessages = const [],
    this.hasIssues = false,
    required this.onRemove,
  });

  String get _statusLabel {
    if (trackedDownloadStatus == 'error' || status == 'failed') return 'Error';
    switch (trackedDownloadState) {
      case 'importPending':
        return 'Import pending';
      case 'importing':
        return 'Importing';
      case 'imported':
        return 'Imported';
      case 'failedPending':
        return 'Failed';
    }
    if (trackedDownloadStatus == 'warning') return 'Warning';
    switch (status) {
      case 'downloading':
        return 'Downloading';
      case 'paused':
        return 'Paused';
      case 'queued':
        return 'Queued';
      case 'completed':
        return 'Completed';
      case 'delay':
        return 'Delayed';
      case 'downloadClientUnavailable':
        return 'Client unavailable';
      case 'warning':
        return 'Warning';
      default:
        return status.isEmpty ? 'Unknown' : status;
    }
  }

  Color get _statusColor {
    if (trackedDownloadStatus == 'error' ||
        status == 'failed' ||
        trackedDownloadState == 'failedPending') {
      return AppTheme.error;
    }
    if (trackedDownloadStatus == 'warning' || status == 'warning') {
      return AppTheme.requested;
    }
    switch (trackedDownloadState) {
      case 'importPending':
      case 'importing':
        return AppTheme.requested;
      case 'imported':
        return AppTheme.available;
    }
    switch (status) {
      case 'downloading':
        return AppTheme.downloading;
      case 'completed':
        return AppTheme.available;
      case 'paused':
      case 'queued':
      case 'delay':
      case 'downloadClientUnavailable':
        return AppTheme.unavailable;
      default:
        return AppTheme.downloading;
    }
  }

  @override
  Widget build(BuildContext context) {
    final statusColor = _statusColor;
    final issues = <String>[
      if (errorMessage != null && errorMessage!.isNotEmpty) errorMessage!,
      ...statusMessages,
    ];

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
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      primaryTitle,
                      style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontWeight: FontWeight.w600,
                          fontSize: 14),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    if (releaseTitle != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 2),
                        child: Text(
                          releaseTitle!,
                          style: const TextStyle(
                              color: AppTheme.textSecondary, fontSize: 11),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                  ],
                ),
              ),
              PopupMenuButton<String>(
                icon: const Icon(Icons.more_vert,
                    color: AppTheme.textSecondary, size: 20),
                color: AppTheme.surfaceVariant,
                onSelected: (value) {
                  if (value == 'remove') onRemove();
                },
                itemBuilder: (_) => const [
                  PopupMenuItem(
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
                _QueueBadge(text: _statusLabel, color: statusColor),
                _QueueBadge(
                  text: protocol.toUpperCase(),
                  color: protocol == 'torrent'
                      ? AppTheme.downloading
                      : AppTheme.available,
                ),
                if (quality != null)
                  _QueueBadge(text: quality!, color: AppTheme.accent),
                if (indexer != null && indexer!.isNotEmpty)
                  _QueueBadge(text: indexer!, color: AppTheme.textSecondary),
                if (downloadClient != null && downloadClient!.isNotEmpty)
                  _QueueBadge(
                      text: downloadClient!, color: AppTheme.textSecondary),
              ],
            ),
          ),
          const SizedBox(height: 10),
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: ClipRRect(
              borderRadius: BorderRadius.circular(3),
              child: LinearProgressIndicator(
                value: progress,
                minHeight: 5,
                backgroundColor: AppTheme.surfaceVariant,
                valueColor: AlwaysStoppedAnimation(statusColor),
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
                    '${(progress * 100).toStringAsFixed(1)}% • '
                    '$downloadedFormatted of $sizeFormatted',
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 11),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (timeleft != null && timeleft!.isNotEmpty)
                  Text(
                    timeleft!,
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 11),
                  ),
              ],
            ),
          ),
          if (hasIssues && issues.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 8, right: 8),
              child: Container(
                width: double.infinity,
                padding: const EdgeInsets.all(8),
                decoration: BoxDecoration(
                  color: AppTheme.error.withValues(alpha: 0.1),
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Icon(Icons.warning_amber_rounded,
                        size: 16, color: AppTheme.requested),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        issues.join('\n'),
                        style: const TextStyle(
                            color: AppTheme.textSecondary, fontSize: 11),
                      ),
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _QueueBadge extends StatelessWidget {
  final String text;
  final Color color;

  const _QueueBadge({required this.text, required this.color});

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
