import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/radarr_api_service.dart';

/// Shows the current Radarr download queue.
class RadarrQueueScreen extends ConsumerStatefulWidget {
  const RadarrQueueScreen({super.key});

  @override
  ConsumerState<RadarrQueueScreen> createState() => _RadarrQueueScreenState();
}

class _RadarrQueueScreenState extends ConsumerState<RadarrQueueScreen> {
  List<Map<String, dynamic>> _queue = [];
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _loadQueue());
  }

  Future<void> _loadQueue() async {
    final instanceState = ref.read(instanceProvider);
    final instanceId = instanceState.activeRadarrInstance?.id;
    if (instanceId == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Radarr instance configured';
      });
      return;
    }

    setState(() => _isLoading = true);
    try {
      final backendDio = ref.read(backendClientProvider);
      final service =
          RadarrApiService(backendDio: backendDio, instanceId: instanceId);
      final queue = await service.getQueue();
      setState(() {
        _queue = queue;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      setState(() {
        _isLoading = false;
        _error = 'Failed to load queue: $e';
      });
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
      return const Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.check_circle_outline,
                size: 48, color: AppTheme.available),
            SizedBox(height: 12),
            Text('Queue is empty',
                style: TextStyle(
                    color: AppTheme.textSecondary, fontSize: 16)),
          ],
        ),
      );
    }

    return RefreshIndicator(
      onRefresh: _loadQueue,
      color: AppTheme.accent,
      child: ListView.builder(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: _queue.length,
        itemBuilder: (context, index) {
          final item = _queue[index];
          final title = item['title'] as String? ?? 'Unknown';
          final status = item['status'] as String? ?? '';
          final size = (item['size'] as num?)?.toDouble() ?? 0;
          final sizeLeft = (item['sizeleft'] as num?)?.toDouble() ?? 0;
          final progress = size > 0 ? ((size - sizeLeft) / size) : 0.0;

          return ListTile(
            leading: CircularProgressIndicator(
              value: progress,
              color: AppTheme.accent,
              backgroundColor: AppTheme.border,
              strokeWidth: 3,
            ),
            title: Text(title,
                style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w500)),
            subtitle: Text(
              '${(progress * 100).toStringAsFixed(1)}% - $status',
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 13),
            ),
          );
        },
      ),
    );
  }
}
