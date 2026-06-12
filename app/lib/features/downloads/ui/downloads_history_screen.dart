import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/downloads_api_service.dart';
import '../data/downloads_models.dart';

/// Recent download client history: completed and failed downloads.
class DownloadsHistoryScreen extends ConsumerStatefulWidget {
  const DownloadsHistoryScreen({super.key});

  @override
  ConsumerState<DownloadsHistoryScreen> createState() =>
      _DownloadsHistoryScreenState();
}

class _DownloadsHistoryScreenState
    extends ConsumerState<DownloadsHistoryScreen> {
  List<DownloadHistoryItem> _items = [];
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  DownloadsApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeDownloadInstance?.id;
    if (instanceId == null) return null;
    return DownloadsApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<void> _load() async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No download client configured';
      });
      return;
    }

    setState(() => _isLoading = true);
    try {
      final items = await service.getHistory(limit: 50);
      if (!mounted) return;
      setState(() {
        _items = items;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load history: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    // Reload when the active instance changes.
    ref.listen(instanceProvider.select((s) => s.activeDownloadInstanceId),
        (_, __) => _load());

    if (_isLoading && _items.isEmpty) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null && _items.isEmpty) {
      return FullScreenError(message: _error!, onRetry: _load);
    }
    if (_items.isEmpty) {
      return RefreshIndicator(
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
                  style:
                      TextStyle(color: AppTheme.textSecondary, fontSize: 16)),
            ),
          ],
        ),
      );
    }

    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView.builder(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: _items.length,
        itemBuilder: (context, index) => _HistoryTile(item: _items[index]),
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

  const _HistoryTile({required this.item});

  @override
  Widget build(BuildContext context) {
    final style = _historyStyle(item);
    final subtitleParts = [
      style.label,
      if (item.sizeBytes > 0) item.sizeFormatted,
      if (item.category.isNotEmpty) item.category,
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
