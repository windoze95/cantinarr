import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import 'import_doctor_sheet.dart';
import 'widgets/queue_item_card.dart';

/// Shows the current Sonarr download queue with per-item actions.
class SonarrQueueScreen extends ConsumerStatefulWidget {
  const SonarrQueueScreen({super.key});

  @override
  ConsumerState<SonarrQueueScreen> createState() => _SonarrQueueScreenState();
}

class _SonarrQueueScreenState extends ConsumerState<SonarrQueueScreen> {
  List<SonarrQueueItem> _queue = [];
  bool _isLoading = true;
  String? _error;
  Timer? _refreshTimer;
  Timer? _wsRefetchDebounce;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _loadQueue();
      // Fallback poll only — queue changes arrive as arr_queue_changed
      // pings over the WebSocket; this covers gaps when the socket is down.
      _refreshTimer =
          Timer.periodic(const Duration(seconds: 45), (_) => _autoRefresh());
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    _wsRefetchDebounce?.cancel();
    super.dispose();
  }

  void _autoRefresh() {
    if (!mounted) return;
    // Skip silent refreshes when another route is on top of this screen.
    final route = ModalRoute.of(context);
    if (route != null && !route.isCurrent) return;
    _loadQueue(silent: true);
  }

  /// Debounced refetch triggered by WebSocket invalidation pings, so a
  /// burst of changes only causes one REST roundtrip.
  void _scheduleWsRefetch() {
    _wsRefetchDebounce?.cancel();
    _wsRefetchDebounce = Timer(const Duration(milliseconds: 500), _autoRefresh);
  }

  SonarrApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeSonarrInstance?.id;
    if (instanceId == null) return null;
    return SonarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<void> _loadQueue({bool silent = false}) async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Sonarr instance configured';
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

  void _showDoctor(SonarrQueueItem item) {
    final instanceId = ref.read(instanceProvider).activeSonarrInstance?.id;
    if (instanceId == null) return;
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (_) => ImportDoctorSheet(
        instanceId: instanceId,
        item: item,
        onChanged: () => _loadQueue(silent: true),
      ),
    );
  }

  Future<void> _removeItem(SonarrQueueItem item) async {
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
    ref.listen(instanceProvider.select((s) => s.activeSonarrInstanceId),
        (_, __) => _loadQueue());

    // Refetch on server-pushed queue-change pings for the active instance;
    // the periodic poll remains as a fallback when the socket is down.
    final wsInstanceId =
        ref.watch(instanceProvider.select((s) => s.activeSonarrInstance?.id));
    if (wsInstanceId != null) {
      ref.listen(
          arrQueueChangedProvider(
              (instanceId: wsInstanceId, serviceType: 'sonarr')), (_, next) {
        if (next.valueOrNull != null) _scheduleWsRefetch();
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
          final episodeLabel = item.episodeLabel;
          final primaryTitle = item.seriesTitle != null
              ? (episodeLabel != null
                  ? '${item.seriesTitle} • $episodeLabel'
                  : item.seriesTitle!)
              : item.title;
          return QueueItemCard(
            item: item,
            primaryTitle: primaryTitle,
            releaseTitle: item.seriesTitle != null ? item.title : null,
            onRemove: () => _removeItem(item),
            onShowIssues:
                item.hasIssues ? () => _showDoctor(item) : null,
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
