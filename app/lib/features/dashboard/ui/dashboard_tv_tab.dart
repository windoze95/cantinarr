import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/featured_media_hero.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
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

class _DashboardTvTabState extends ConsumerState<DashboardTvTab>
    with WidgetsBindingObserver {
  List<SonarrSeries> _recentlyDownloaded = [];
  List<SonarrSeries> _airingNext = [];
  bool _isLoadingLibrary = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(tvDiscoverProvider.notifier).bootstrap();
      _loadLibraryPreview();
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // The library may have changed while the app was backgrounded (downloads
    // finishing, an admin working directly in the arr) — otherwise these rows
    // only refresh on pull-to-refresh and this tab is the landing screen.
    if (state == AppLifecycleState.resumed && !_isLoadingLibrary) {
      _loadLibraryPreview();
    }
  }

  Future<void> _loadLibraryPreview() async {
    final auth = ref.read(authProvider).valueOrNull;
    final defaultSonarr = auth?.connection?.defaultSonarrInstance;
    if (defaultSonarr == null) return;

    setState(() => _isLoadingLibrary = true);

    final backendDio = ref.read(backendClientProvider);
    final service =
        SonarrApiService(backendDio: backendDio, instanceId: defaultSonarr.id);

    List<SonarrSeries> series = [];
    try {
      series = await service.getSeries();
      if (!mounted) return;

      // "Recently Downloaded" = series with downloaded episodes, sorted by percent complete
      final downloaded = series.where((s) => s.percentComplete > 0).toList()
        ..sort((a, b) => b.percentComplete.compareTo(a.percentComplete));

      setState(() {
        _recentlyDownloaded = downloaded.take(10).toList();
      });
    } catch (_) {
      // Series fetch failed; leave _recentlyDownloaded empty.
    }

    try {
      final now = DateTime.now();
      final calendarEntries = await service.getCalendar(
        start: now.toIso8601String(),
        end: now.add(const Duration(days: 7)).toIso8601String(),
      );
      if (!mounted) return;

      // "Airing Next" = unique series from calendar entries
      final airingSeriesIds = calendarEntries
          .map((e) => e['seriesId'] as int?)
          .whereType<int>()
          .toSet();
      final airingNext =
          series.where((s) => airingSeriesIds.contains(s.id)).toList();

      setState(() {
        _airingNext = airingNext.take(10).toList();
      });
    } catch (_) {
      // Calendar fetch failed; leave _airingNext empty.
    }

    if (mounted) setState(() => _isLoadingLibrary = false);
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
          if (discover.popularTV.isNotEmpty)
            FeaturedMediaHero(
              item: discover.popularTV.first,
              eyebrow: 'Series spotlight',
              onTap: () => context.push(
                '/detail/tv/${discover.popularTV.first.id}',
              ),
            ),
          CategoryRow(
            title: 'Popular TV Shows',
            items: discover.popularTV.skip(1).toList(growable: false),
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

  /// All-seasons availability line for a TV card, e.g. "18/24 eps". Returns null
  /// when Sonarr reported no episode statistics for the series.
  String? _availabilityLine(SonarrSeries series) {
    final stats = series.statistics;
    if (stats == null || stats.episodeCount == 0) return null;
    return '${stats.episodeFileCount}/${stats.episodeCount} eps';
  }

  Widget _buildRow({
    required String title,
    required List<SonarrSeries> items,
    required String statusLabel,
    required Color statusColor,
  }) {
    final viewportWidth = MediaQuery.sizeOf(context).width;
    final cardWidth =
        viewportWidth >= 900 ? 124.0 : (viewportWidth >= 600 ? 116.0 : 108.0);

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.symmetric(
              horizontal: MediaQuery.sizeOf(context).width >= 900 ? 24 : 16,
            ),
            child: SectionHeader(title: title),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<SonarrSeries>(
            items: items,
            isLoading: _isLoadingLibrary,
            height: cardWidth * 1.5 + 68,
            itemBuilder: (series) => MediaCard(
              id: series.id,
              title: series.title,
              posterPath: series.posterUrl,
              statusLabel: statusLabel,
              statusColor: statusColor,
              subtitle: _availabilityLine(series),
              width: cardWidth,
              onTap: series.tmdbId != null
                  ? () => context.push('/detail/tv/${series.tmdbId}')
                  : null,
            ),
          ),
        ],
      ),
    );
  }
}
