import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/watch_history_api_service.dart';
import '../data/watch_history_models.dart';

/// Watch statistics: top movies, shows and users over a selectable period,
/// with a note on what the ranking was computed from.
class MonitoringStatsScreen extends ConsumerStatefulWidget {
  const MonitoringStatsScreen({super.key});

  @override
  ConsumerState<MonitoringStatsScreen> createState() =>
      _MonitoringStatsScreenState();
}

class _MonitoringStatsScreenState
    extends ConsumerState<MonitoringStatsScreen> {
  StatsSnapshot? _stats;
  bool _isLoading = true;
  String? _error;
  int _days = 30;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  WatchHistoryApiService? _buildService() {
    final instance = ref.read(instanceProvider).activeWatchHistoryInstance;
    if (instance == null) return null;
    return WatchHistoryApiService(
      backendDio: ref.read(backendClientProvider),
      instance: instance,
    );
  }

  Future<void> _load() async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Tautulli or Tracearr instance configured. Add one from Settings > Add Instance.';
      });
      return;
    }

    // Capture the request parameters so a slow response for a previously
    // selected period (or instance) can't overwrite the current selection.
    final days = _days;
    final instanceId =
        ref.read(instanceProvider.select((s) => s.activeWatchHistoryInstanceId));
    bool stale() =>
        !mounted ||
        days != _days ||
        instanceId !=
            ref.read(
                instanceProvider.select((s) => s.activeWatchHistoryInstanceId));

    setState(() => _isLoading = true);
    try {
      final stats = await service.getStats(days: days);
      if (stale()) return;
      setState(() {
        _stats = stats;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (stale()) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load stats: $e';
      });
    }
  }

  void _setDays(int days) {
    if (days == _days) return;
    setState(() => _days = days);
    _load();
  }

  @override
  Widget build(BuildContext context) {
    // Reload when the active instance changes.
    ref.listen(instanceProvider.select((s) => s.activeWatchHistoryInstanceId),
        (_, __) => _load());

    final daysSelector = Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
      child: SegmentedButton<int>(
        showSelectedIcon: false,
        segments: const [
          ButtonSegment(value: 7, label: Text('7 days')),
          ButtonSegment(value: 30, label: Text('30 days')),
          ButtonSegment(value: 90, label: Text('90 days')),
        ],
        selected: {_days},
        onSelectionChanged: (value) => _setDays(value.first),
      ),
    );

    if (_isLoading && _stats == null) {
      return Column(
        children: [
          daysSelector,
          const Expanded(
            child: Center(
                child: CircularProgressIndicator(color: AppTheme.accent)),
          ),
        ],
      );
    }
    if (_error != null && _stats == null) {
      return Column(
        children: [
          daysSelector,
          Expanded(child: FullScreenError(message: _error!, onRetry: _load)),
        ],
      );
    }

    final stats = _stats ?? const StatsSnapshot();
    final isEmpty = stats.topMovies.isEmpty &&
        stats.topShows.isEmpty &&
        stats.topUsers.isEmpty;

    return Column(
      children: [
        daysSelector,
        // What the ranking was computed from: which provider, which window,
        // and for Tracearr how many plays were read.
        if (stats.coverage.note.isNotEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 4),
            child: Text(
              stats.coverage.note,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
          ),
        Expanded(
          child: RefreshIndicator(
            onRefresh: _load,
            color: AppTheme.accent,
            child: isEmpty
                ? ListView(
                    physics: const AlwaysScrollableScrollPhysics(),
                    children: const [
                      SizedBox(height: 140),
                      Icon(Icons.insights_outlined,
                          size: 48, color: AppTheme.textSecondary),
                      SizedBox(height: 12),
                      Center(
                        child: Text('No stats for this period',
                            style: TextStyle(
                                color: AppTheme.textSecondary, fontSize: 16)),
                      ),
                    ],
                  )
                : ListView(
                    physics: const AlwaysScrollableScrollPhysics(),
                    padding: const EdgeInsets.only(bottom: 24),
                    children: [
                      if (_isLoading)
                        const LinearProgressIndicator(
                          minHeight: 2,
                          color: AppTheme.accent,
                          backgroundColor: Colors.transparent,
                        ),
                      _StatSection(
                        title: 'Top Movies',
                        icon: Icons.movie_outlined,
                        entries: stats.topMovies,
                      ),
                      _StatSection(
                        title: 'Top Shows',
                        icon: Icons.tv_outlined,
                        entries: stats.topShows,
                      ),
                      _StatSection(
                        title: 'Top Users',
                        icon: Icons.person_outline,
                        entries: stats.topUsers,
                      ),
                    ],
                  ),
          ),
        ),
      ],
    );
  }
}

/// A titled, ranked list of play-count entries.
class _StatSection extends StatelessWidget {
  final String title;
  final IconData icon;
  final List<StatEntry> entries;

  const _StatSection({
    required this.title,
    required this.icon,
    required this.entries,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
          child: Row(
            children: [
              Icon(icon, size: 18, color: AppTheme.accent),
              const SizedBox(width: 8),
              Text(
                title,
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 15,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
        if (entries.isEmpty)
          const Padding(
            padding: EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: Text('No plays in this period',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
          )
        else
          ...entries.asMap().entries.map((entry) => _StatTile(
                rank: entry.key + 1,
                entry: entry.value,
              )),
      ],
    );
  }
}

class _StatTile extends StatelessWidget {
  final int rank;
  final StatEntry entry;

  const _StatTile({required this.rank, required this.entry});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 5),
      child: Row(
        children: [
          Container(
            width: 26,
            height: 26,
            alignment: Alignment.center,
            decoration: BoxDecoration(
              color: rank <= 3
                  ? AppTheme.accent.withValues(alpha: 0.15)
                  : AppTheme.surfaceVariant,
              shape: BoxShape.circle,
            ),
            child: Text(
              '$rank',
              style: TextStyle(
                color: rank <= 3 ? AppTheme.accent : AppTheme.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              entry.label,
              style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 13.5,
                  fontWeight: FontWeight.w500),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          const SizedBox(width: 8),
          Text(
            '${entry.plays} play${entry.plays == 1 ? '' : 's'}',
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
          ),
        ],
      ),
    );
  }
}
