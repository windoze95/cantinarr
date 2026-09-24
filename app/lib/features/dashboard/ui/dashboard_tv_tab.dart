import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/featured_media_hero.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/logic/browse_query.dart';
import '../../discover/logic/library_snapshot_provider.dart';
import '../../discover/logic/search_library_status.dart';
import '../../discover/ui/category_row.dart';
import '../../discover/ui/discover_refresh.dart';
import '../../discover/logic/discover_session.dart';
import '../../../core/widgets/error_banner.dart';
import '../../discover/ui/genre_chip_strip.dart';
import '../../sonarr/data/sonarr_models.dart';
import '../../sonarr/logic/tv_discover_provider.dart';
import 'tv_library_link.dart';

/// Dashboard TV tab: discovery rows + Sonarr library rows.
class DashboardTvTab extends ConsumerStatefulWidget {
  const DashboardTvTab({super.key});

  @override
  ConsumerState<DashboardTvTab> createState() => _DashboardTvTabState();
}

class _DashboardTvTabState extends ConsumerState<DashboardTvTab> {
  LibrarySnapshot get _library => ref.read(librarySnapshotProvider);
  bool get _isLoadingLibrary => _library.seriesLoading;
  List<SonarrSeries> get _recentlyDownloaded => _library.recentSeries;
  List<SonarrSeries> get _airingNext => _library.airingSeries;
  String get _recentInstanceId => _library.sonarrInstanceId;
  String get _airingInstanceId => _library.sonarrInstanceId;
  List<SonarrSeries> get _librarySeries => _library.series;

  Future<void> _refresh() async {
    await Future.wait([
      ref.read(tvDiscoverProvider.notifier).bootstrap(),
      ref.read(librarySnapshotProvider.notifier)
          .refresh(force: true, type: MediaType.tv),
    ]);
  }

  Future<void> _onRefresh() async {
    ref.read(libraryRefreshTickProvider.notifier).state++;
    await _refresh();
  }

  /// Opens a discovery row's feed as a full grid.
  void _seeAll(BrowseFeed feed, String title) => context.push(
        BrowseQuery(type: MediaType.tv, feed: feed, title: title).toLocation(),
      );

  @override
  Widget build(BuildContext context) {
    final library = ref.watch(librarySnapshotProvider);
    final scope = ref.watch(discoverSessionProvider);
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

    return PageStorage(
      bucket: ref.watch(discoverPageStorageProvider),
      child: DiscoverRefresh(
        key: ValueKey(scope),
        path: '/dashboard/tv',
        onRefresh: _refresh,
        child: RefreshIndicator(
          onRefresh: _onRefresh,
          color: AppTheme.accent,
          child: Stack(children: [
            ListView(
            key: PageStorageKey(('discover-tv', scope)),
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
                paginationRevision: discover.refreshRevision,
                key: const PageStorageKey('featured'),
                title: discover.featuredTitle,
                items: discover.featured.skip(1).toList(growable: false),
                isLoading: discover.isLoadingFeatured,
                isTvRow: true,
                resolveTVStatus: true,
                libraryStatus: libraryStatus,
                onSeeAll: discover.featuredSource.isEmpty
                    ? null
                    : () => _seeAll(BrowseFeed.featured, discover.featuredTitle),
              ),
              // Every row below grows as it is scrolled toward its end; the
              // headline row above is the one server-capped page.
              if (discover.onTheAir.isNotEmpty)
                CategoryRow(
                  paginationRevision: discover.refreshRevision,
                  key: const PageStorageKey('Airing This Week'),
                  title: 'Airing This Week',
                  items: discover.onTheAir,
                  isLoading: discover.isLoadingOnTheAir,
                  isTvRow: true,
                  resolveTVStatus: true,
                  libraryStatus: libraryStatus,
                  onLoadMore: (_) => discoverNotifier.loadMoreOnTheAir(),
                  onSeeAll: () => _seeAll(BrowseFeed.onTheAir, 'Airing This Week'),
                ),
              if (discover.topRated.isNotEmpty)
                CategoryRow(
                  paginationRevision: discover.refreshRevision,
                  key: const PageStorageKey('Top Rated'),
                  title: 'Top Rated',
                  items: discover.topRated,
                  isLoading: discover.isLoadingTopRated,
                  isTvRow: true,
                  resolveTVStatus: true,
                  libraryStatus: libraryStatus,
                  onLoadMore: (_) => discoverNotifier.loadMoreTopRated(),
                  onSeeAll: () => _seeAll(BrowseFeed.topRated, 'Top Rated'),
                ),
              if (discover.upcoming.isNotEmpty)
                CategoryRow(
                  paginationRevision: discover.refreshRevision,
                  key: const PageStorageKey('Coming Soon'),
                  title: 'Coming Soon',
                  items: discover.upcoming,
                  isLoading: discover.isLoadingUpcoming,
                  isTvRow: true,
                  resolveTVStatus: true,
                  libraryStatus: libraryStatus,
                  onLoadMore: (_) => discoverNotifier.loadMoreUpcoming(),
                  onSeeAll: () => _seeAll(BrowseFeed.upcoming, 'Coming Soon'),
                ),
              if (discover.anticipated.isNotEmpty)
                CategoryRow(
                  paginationRevision: discover.refreshRevision,
                  key: const PageStorageKey('Most Anticipated'),
                  title: 'Most Anticipated',
                  items: discover.anticipated,
                  isLoading: discover.isLoadingAnticipated,
                  isTvRow: true,
                  resolveTVStatus: true,
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
                  instanceId: _recentInstanceId,
                  statusLabel: 'Downloaded',
                  statusColor: AppTheme.available,
                ),
              if (_airingNext.isNotEmpty || _isLoadingLibrary)
                _buildRow(
                  title: 'Airing Next',
                  items: _airingNext,
                  instanceId: _airingInstanceId,
                  statusLabel: 'Airing',
                  statusColor: AppTheme.downloading,
                ),
            ],
          ),
            if (discover.failedRows.isNotEmpty || library.seriesFailed)
              Align(
                alignment: Alignment.bottomCenter,
                child: SafeArea(child: ErrorBanner(
                  message: 'Some titles could not be loaded. Retry to check for updates.',
                  onRetry: _refresh,
                )),
              ),
          ]),
        ),
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
    required String instanceId,
    required String statusLabel,
    required Color statusColor,
  }) {
    final viewportWidth = MediaQuery.sizeOf(context).width;
    final cardWidth =
        viewportWidth >= 900 ? 124.0 : (viewportWidth >= 600 ? 116.0 : 108.0);

    return Padding(
      key: PageStorageKey(title),
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
            itemKey: (item) => item.id,
            itemExtent: cardWidth + 14,
            isLoading: _isLoadingLibrary,
            height: cardWidth * 1.5 + MediaCard.rowExtraHeight(context, withSubtitle: true),
            itemBuilder: (series) => TVLibraryLink(
              instanceId: instanceId,
              seriesId: series.id,
              tmdbId: series.tmdbId,
              builder: (onTap) => MediaCard(
                id: series.id,
                title: series.title,
                posterPath: series.posterUrl,
                statusLabel: statusLabel,
                statusColor: statusColor,
                subtitle: _availabilityLine(series),
                width: cardWidth,
                onTap: onTap,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
