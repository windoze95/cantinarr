import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/ui/category_row.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/data/sonarr_models.dart';
import '../../sonarr/logic/tv_discover_provider.dart';

/// Dashboard TV tab: discovery rows + Sonarr library rows.
class DashboardTvTab extends ConsumerStatefulWidget {
  const DashboardTvTab({super.key});

  @override
  ConsumerState<DashboardTvTab> createState() => _DashboardTvTabState();
}

class _DashboardTvTabState extends ConsumerState<DashboardTvTab> {
  List<SonarrSeries> _recentlyDownloaded = [];
  List<SonarrSeries> _airingNext = [];
  bool _isLoadingLibrary = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(tvDiscoverProvider.notifier).bootstrap();
      _loadLibraryPreview();
    });
  }

  Future<void> _loadLibraryPreview() async {
    final auth = ref.read(authProvider).valueOrNull;
    final defaultSonarr = auth?.connection?.defaultSonarrInstance;
    if (defaultSonarr == null) return;

    setState(() => _isLoadingLibrary = true);
    try {
      final backendDio = ref.read(backendClientProvider);
      final service = SonarrApiService(
          backendDio: backendDio, instanceId: defaultSonarr.id);

      final series = await service.getSeries();

      final now = DateTime.now();
      final calendarEntries = await service.getCalendar(
        start: now.toIso8601String(),
        end: now.add(const Duration(days: 7)).toIso8601String(),
      );

      // "Airing Next" = unique series from calendar entries
      final airingSeriesIds = calendarEntries
          .map((e) => e['seriesId'] as int?)
          .whereType<int>()
          .toSet();
      final airingNext = series
          .where((s) => airingSeriesIds.contains(s.id))
          .toList();

      // "Recently Downloaded" = series with downloaded episodes, sorted by percent complete
      final downloaded = series
          .where((s) => s.percentComplete > 0)
          .toList()
        ..sort((a, b) =>
            b.percentComplete.compareTo(a.percentComplete));

      setState(() {
        _recentlyDownloaded = downloaded.take(10).toList();
        _airingNext = airingNext.take(10).toList();
        _isLoadingLibrary = false;
      });
    } catch (_) {
      setState(() => _isLoadingLibrary = false);
    }
  }

  Future<void> _onRefresh() async {
    await Future.wait([
      ref.read(tvDiscoverProvider.notifier).bootstrap(),
      _loadLibraryPreview(),
    ]);
  }

  @override
  Widget build(BuildContext context) {
    final discover = ref.watch(tvDiscoverProvider);

    return RefreshIndicator(
      onRefresh: _onRefresh,
      color: AppTheme.accent,
      child: ListView(
        padding: const EdgeInsets.only(bottom: 24),
        children: [
          CategoryRow(
            title: 'Popular TV Shows',
            items: discover.popularTV,
            isLoading: discover.isLoadingPopular,
          ),
          if (discover.anticipated.isNotEmpty)
            CategoryRow(
              title: 'Most Anticipated',
              items: discover.anticipated,
              isLoading: discover.isLoadingAnticipated,
            ),

          // Sonarr library rows (same style as discovery)
          if (_recentlyDownloaded.isNotEmpty || _isLoadingLibrary)
            _buildRow(
              title: 'Recently Downloaded',
              items: _recentlyDownloaded,
              statusLabel: 'Downloaded',
              statusColor: AppTheme.available,
            ),
          if (_airingNext.isNotEmpty || _isLoadingLibrary)
            _buildRow(
              title: 'Airing Next',
              items: _airingNext,
              statusLabel: 'Airing',
              statusColor: AppTheme.downloading,
            ),
        ],
      ),
    );
  }

  Widget _buildRow({
    required String title,
    required List<SonarrSeries> items,
    required String statusLabel,
    required Color statusColor,
  }) {
    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Text(
              title,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 20,
                fontWeight: FontWeight.bold,
              ),
            ),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<SonarrSeries>(
            items: items,
            isLoading: _isLoadingLibrary,
            itemBuilder: (series) => MediaCard(
              id: series.id,
              title: series.title,
              posterPath: series.posterUrl,
              statusLabel: statusLabel,
              statusColor: statusColor,
              width: 100,
              onTap: series.tvdbId != null
                  ? () => context.push('/detail/tv/${series.tvdbId}')
                  : null,
            ),
          ),
        ],
      ),
    );
  }
}
