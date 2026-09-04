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
import '../../discover/data/tmdb_models.dart';
import '../../discover/logic/browse_query.dart';
import '../../discover/logic/library_snapshot_provider.dart';
import '../../discover/logic/search_library_status.dart';
import '../../discover/ui/category_row.dart';
import '../../discover/ui/genre_chip_strip.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/data/sonarr_models.dart';
import '../../sonarr/logic/tv_discover_provider.dart';
import '../logic/library_rows.dart';

/// How far back the "Recently Downloaded" row looks. Sonarr writes one import
/// record per episode, so a season pack spends a dozen of these on one series —
/// the page has to be deep enough that a single big import does not crowd every
/// other show out of the row.
const _importHistoryPageSize = 100;

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

  /// The full Sonarr library, retained so Discover browse-row posters can be
  /// badged Available/Partial/Requested from the same fetch this
  /// tab already makes — no second Sonarr call.
  List<SonarrSeries> _librarySeries = [];

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

    // Both rows are the library joined to a second source, so a failed series
    // fetch leaves nothing to join and the dependent calls are skipped. The
    // rows keep what they are already showing rather than blanking the landing
    // screen over one transient error.
    final List<SonarrSeries> series;
    try {
      series = await service.getSeries();
    } catch (_) {
      if (mounted) setState(() => _isLoadingLibrary = false);
      return;
    }
    if (!mounted) return;

    // Retained before the history/calendar follow-up fetches: those two can
    // each fail independently, and browse-row badges must survive either
    // outage since the series list they depend on already arrived.
    setState(() => _librarySeries = series);
    // A grid opened from this tab badges its posters from the same list.
    ref.read(librarySnapshotProvider.notifier).seed(series: series);

    try {
      final imports = await service.getHistory(
        pageSize: _importHistoryPageSize,
        eventType: SonarrHistoryRecord.importedEventTypeId,
      );
      if (!mounted) return;

      setState(() {
        _recentlyDownloaded = recentlyDownloadedSeries(series, imports.records);
      });
    } catch (_) {
      // History fetch failed. A series record carries no import date, so there
      // is no second source to fall back to — leave the row as it is rather
      // than ordering it by something that is not recency.
    }

    try {
      final now = DateTime.now();
      final calendarEntries = await service.getCalendar(
        start: now.toIso8601String(),
        end: now.add(const Duration(days: 7)).toIso8601String(),
      );
      if (!mounted) return;

      setState(() {
        _airingNext = airingNextSeries(series, calendarEntries);
      });
    } catch (_) {
      // Calendar fetch failed; leave _airingNext as it is.
    }

    if (mounted) setState(() => _isLoadingLibrary = false);
  }

  Future<void> _onRefresh() async {
    await Future.wait([
      ref.read(tvDiscoverProvider.notifier).bootstrap(),
      _loadLibraryPreview(),
    ]);
  }

  /// Opens a discovery row's feed as a full grid.
  void _seeAll(BrowseFeed feed, String title) => context.push(
        BrowseQuery(type: MediaType.tv, feed: feed, title: title).toLocation(),
      );

  @override
  Widget build(BuildContext context) {
    final discover = ref.watch(tvDiscoverProvider);
    final discoverNotifier = ref.watch(tvDiscoverProvider.notifier);
    // Unlike the Movies tab, searchResults is load-bearing here: the TV
    // branch of buildSearchLibraryStatus looks each result up by tmdbId
    // (falling back to a title+year match), so it needs every discovery
    // row's items to key against; a row left out renders unbadged. The
    // whole featured list is passed, not `.skip(1)` — the hero's extra entry
    // is one unread map key, and keeping the lists aligned avoids an
    // off-by-one.
    final libraryStatus = buildSearchLibraryStatus(
      searchResults: [
        ...discover.featured,
        ...discover.onTheAir,
        ...discover.topRated,
        ...discover.upcoming,
        ...discover.anticipated,
      ],
      movies: const [],
      series: _librarySeries,
    );

    return RefreshIndicator(
      onRefresh: _onRefresh,
      color: AppTheme.accent,
      child: ListView(
        padding: const EdgeInsets.only(bottom: 24),
        children: [
          if (discover.featured.isNotEmpty)
            FeaturedMediaHero(
              item: discover.featured.first,
              eyebrow: 'Series spotlight',
              onTap: () => context.push(
                '/detail/tv/${discover.featured.first.id}',
              ),
            ),
          CategoryRow(
            title: discover.featuredTitle,
            items: discover.featured.skip(1).toList(growable: false),
            isLoading: discover.isLoadingFeatured,
            isTvRow: true,
            libraryStatus: libraryStatus,
            onSeeAll: discover.featuredSource.isEmpty
                ? null
                : () => _seeAll(BrowseFeed.featured, discover.featuredTitle),
          ),
          // Every row below grows as it is scrolled toward its end; the
          // headline row above is the one server-capped page.
          if (discover.onTheAir.isNotEmpty)
            CategoryRow(
              title: 'Airing This Week',
              items: discover.onTheAir,
              isLoading: discover.isLoadingOnTheAir,
              isTvRow: true,
              libraryStatus: libraryStatus,
              onLoadMore: (_) => discoverNotifier.loadMoreOnTheAir(),
              onSeeAll: () => _seeAll(BrowseFeed.onTheAir, 'Airing This Week'),
            ),
          if (discover.topRated.isNotEmpty)
            CategoryRow(
              title: 'Top Rated',
              items: discover.topRated,
              isLoading: discover.isLoadingTopRated,
              isTvRow: true,
              libraryStatus: libraryStatus,
              onLoadMore: (_) => discoverNotifier.loadMoreTopRated(),
              onSeeAll: () => _seeAll(BrowseFeed.topRated, 'Top Rated'),
            ),
          if (discover.upcoming.isNotEmpty)
            CategoryRow(
              title: 'Coming Soon',
              items: discover.upcoming,
              isLoading: discover.isLoadingUpcoming,
              isTvRow: true,
              libraryStatus: libraryStatus,
              onLoadMore: (_) => discoverNotifier.loadMoreUpcoming(),
              onSeeAll: () => _seeAll(BrowseFeed.upcoming, 'Coming Soon'),
            ),
          if (discover.anticipated.isNotEmpty)
            CategoryRow(
              title: 'Most Anticipated',
              items: discover.anticipated,
              isLoading: discover.isLoadingAnticipated,
              isTvRow: true,
              libraryStatus: libraryStatus,
              onLoadMore: (_) => discoverNotifier.loadMoreAnticipated(),
              onSeeAll: () =>
                  _seeAll(BrowseFeed.anticipated, 'Most Anticipated'),
            ),
          GenreChipStrip(genres: discover.genres, mediaType: MediaType.tv),

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
            height: cardWidth * 1.5 + MediaCard.subtitleRowExtraHeight,
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
