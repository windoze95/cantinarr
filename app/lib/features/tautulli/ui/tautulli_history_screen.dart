import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/tautulli_api_service.dart';
import '../data/tautulli_models.dart';

/// Recent Plex watch history: who watched what, when and how much of it.
class TautulliHistoryScreen extends ConsumerStatefulWidget {
  const TautulliHistoryScreen({super.key});

  @override
  ConsumerState<TautulliHistoryScreen> createState() =>
      _TautulliHistoryScreenState();
}

class _TautulliHistoryScreenState extends ConsumerState<TautulliHistoryScreen> {
  List<TautulliHistoryItem> _items = [];
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  TautulliApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeTautulliInstance?.id;
    if (instanceId == null) return null;
    return TautulliApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<void> _load() async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Tautulli instance configured';
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
    ref.listen(instanceProvider.select((s) => s.activeTautulliInstanceId),
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

class _HistoryTile extends StatelessWidget {
  final TautulliHistoryItem item;

  const _HistoryTile({required this.item});

  @override
  Widget build(BuildContext context) {
    final watched = item.percentComplete >= 85;
    final color = watched ? AppTheme.available : AppTheme.textSecondary;
    final subtitleParts = [
      if (item.user.isNotEmpty) item.user,
      '${item.percentComplete}%',
      if (item.platform.isNotEmpty) item.platform,
    ];

    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 2),
      leading: Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.15),
          shape: BoxShape.circle,
        ),
        child: Icon(
          watched ? Icons.check_circle_outline : Icons.play_arrow_outlined,
          color: color,
          size: 20,
        ),
      ),
      title: Text(
        item.fullTitle,
        style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 13.5,
            fontWeight: FontWeight.w500),
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        subtitleParts.join(' • '),
        style: TextStyle(color: color, fontSize: 12),
      ),
      trailing: Text(
        _relativeTime(item.date),
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 11),
      ),
    );
  }
}
