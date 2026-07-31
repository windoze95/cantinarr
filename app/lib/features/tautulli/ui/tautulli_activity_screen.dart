import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/tautulli_api_service.dart';
import '../data/tautulli_models.dart';

/// Now-playing view: one card per active Plex stream, with a header showing
/// total stream count and bandwidth. Auto-refreshes every 10 seconds.
class TautulliActivityScreen extends ConsumerStatefulWidget {
  const TautulliActivityScreen({super.key});

  @override
  ConsumerState<TautulliActivityScreen> createState() =>
      _TautulliActivityScreenState();
}

class _TautulliActivityScreenState
    extends ConsumerState<TautulliActivityScreen> {
  TautulliActivity? _activity;
  bool _isLoading = true;
  String? _error;
  Timer? _refreshTimer;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _load();
      _refreshTimer =
          Timer.periodic(const Duration(seconds: 10), (_) => _autoRefresh());
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
    _load(silent: true);
  }

  TautulliApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeTautulliInstance?.id;
    if (instanceId == null) return null;
    return TautulliApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<void> _load({bool silent = false}) async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Tautulli instance configured';
      });
      return;
    }

    if (!silent) setState(() => _isLoading = true);
    try {
      final activity = await service.getActivity();
      if (!mounted) return;
      setState(() {
        _activity = activity;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      // Keep showing the last known data on silent refresh failures.
      if (silent) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load activity: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    // Reload when the active instance changes.
    ref.listen(instanceProvider.select((s) => s.activeTautulliInstanceId),
        (_, __) => _load());

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
            ElevatedButton(onPressed: _load, child: const Text('Retry')),
          ],
        ),
      );
    }

    final activity = _activity ?? const TautulliActivity();

    return Column(
      children: [
        _ActivityHeader(
          streamCount: activity.streamCount,
          bandwidthFormatted: activity.totalBandwidthFormatted,
        ),
        Expanded(
          child: RefreshIndicator(
            onRefresh: _load,
            color: AppTheme.accent,
            child: activity.streams.isEmpty
                ? ListView(
                    physics: const AlwaysScrollableScrollPhysics(),
                    children: const [
                      SizedBox(height: 160),
                      Icon(Icons.tv_off_outlined,
                          size: 48, color: AppTheme.textSecondary),
                      SizedBox(height: 12),
                      Center(
                        child: Text('Nothing is playing',
                            style: TextStyle(
                                color: AppTheme.textSecondary, fontSize: 16)),
                      ),
                    ],
                  )
                : ListView.builder(
                    physics: const AlwaysScrollableScrollPhysics(),
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    itemCount: activity.streams.length,
                    itemBuilder: (context, index) =>
                        _StreamCard(stream: activity.streams[index]),
                  ),
          ),
        ),
      ],
    );
  }
}

/// Header row with active stream count and total bandwidth.
class _ActivityHeader extends StatelessWidget {
  final int streamCount;
  final String bandwidthFormatted;

  const _ActivityHeader({
    required this.streamCount,
    required this.bandwidthFormatted,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        border: Border(
          bottom: BorderSide(color: AppTheme.border, width: 0.5),
        ),
      ),
      child: Row(
        children: [
          Icon(
            streamCount > 0 ? Icons.play_circle_outline : Icons.tv_outlined,
            size: 18,
            color:
                streamCount > 0 ? AppTheme.downloading : AppTheme.unavailable,
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              streamCount > 0
                  ? '$streamCount stream${streamCount == 1 ? '' : 's'} • $bandwidthFormatted'
                  : 'No active streams',
              style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w500),
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}

({IconData icon, Color color, String label}) _stateStyle(
    TautulliStream stream) {
  if (stream.isPaused) {
    return (
      icon: Icons.pause_circle_outline,
      color: AppTheme.unavailable,
      label: 'Paused'
    );
  }
  if (stream.isBuffering) {
    return (
      icon: Icons.downloading_outlined,
      color: AppTheme.requested,
      label: 'Buffering'
    );
  }
  return (
    icon: Icons.play_circle_outline,
    color: AppTheme.downloading,
    label: 'Playing'
  );
}

/// One active stream: user, title, player/product, state, progress bar,
/// quality, stream-decision badge and bandwidth.
class _StreamCard extends StatelessWidget {
  final TautulliStream stream;

  const _StreamCard({required this.stream});

  @override
  Widget build(BuildContext context) {
    final state = _stateStyle(stream);
    final playerLine = [
      if (stream.player.isNotEmpty) stream.player,
      if (stream.product.isNotEmpty && stream.product != stream.player)
        stream.product,
    ].join(' • ');

    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      padding: const EdgeInsets.fromLTRB(12, 10, 12, 12),
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
              Icon(state.icon, size: 22, color: state.color),
              const SizedBox(width: 8),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      stream.displayTitle,
                      style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontWeight: FontWeight.w600,
                          fontSize: 14),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 2),
                    Text(
                      [
                        if (stream.user.isNotEmpty) stream.user,
                        if (playerLine.isNotEmpty) playerLine,
                      ].join(' • '),
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          ClipRRect(
            borderRadius: BorderRadius.circular(3),
            child: LinearProgressIndicator(
              value: stream.progressFraction,
              minHeight: 5,
              backgroundColor: AppTheme.surfaceVariant,
              valueColor: AlwaysStoppedAnimation(state.color),
            ),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: Wrap(
                  spacing: 6,
                  runSpacing: 4,
                  children: [
                    _StreamBadge(text: state.label, color: state.color),
                    _StreamBadge(
                      text: stream.streamTypeLabel,
                      color: stream.isTranscode
                          ? AppTheme.requested
                          : AppTheme.available,
                    ),
                    if (stream.quality.isNotEmpty)
                      _StreamBadge(
                          text: stream.quality, color: AppTheme.accent),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.only(left: 8),
                child: Text(
                  '${stream.progressPercent}% • ${stream.bandwidthFormatted}',
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 11),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _StreamBadge extends StatelessWidget {
  final String text;
  final Color color;

  const _StreamBadge({required this.text, required this.color});

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
