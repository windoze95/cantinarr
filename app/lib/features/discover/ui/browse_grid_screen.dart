import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_sort_menu.dart';
import '../data/discover_api_service.dart';
import '../data/tmdb_models.dart';
import '../logic/browse_grid_notifier.dart';
import '../logic/browse_session_provider.dart';
import '../logic/discover_session.dart';
import 'discover_refresh.dart';
import 'package:flutter/rendering.dart';
import '../logic/browse_query.dart';
import '../logic/cover_4k_badges_provider.dart';
import '../logic/library_snapshot_provider.dart';
import '../logic/search_library_status.dart';
import 'catalog_status_builder.dart';
import 'filter_sheet.dart';

/// A feed as a full-page poster grid that keeps loading: the "See all" behind
/// every discovery row, and the Browse page when the feed is the filterable
/// one. Posters carry the same Available / Requested badges the rows do.
class BrowseGridScreen extends ConsumerWidget {
  const BrowseGridScreen({super.key, required this.query});

  final BrowseQuery query;

  /// The narrowest a poster column may be; the grid fits as many columns as
  /// the width allows, between [minColumns] and [maxColumns].
  static const double minCardWidth = 132;
  static const int minColumns = 2;
  static const int maxColumns = 8;
  static const double columnSpacing = 14;
  static const double rowSpacing = 16;

  /// How close to the end the next page is asked for, in logical pixels.
  static const double loadMoreThreshold = 400;

  @override
  Widget build(BuildContext context, WidgetRef ref) => _BrowseGridBody(
    key: ValueKey(ref.watch(discoverSessionProvider)),
    query: query,
    session: ref.watch(browseSessionProvider),
  );
}

class _BrowseGridBody extends ConsumerStatefulWidget {
  const _BrowseGridBody({super.key, required this.query, required this.session});
  final BrowseQuery query;
  final BrowseSession session;
  @override
  ConsumerState<_BrowseGridBody> createState() => _BrowseGridScreenState();
}

class _BrowseGridScreenState extends ConsumerState<_BrowseGridBody> {
  late BrowseGridNotifier _notifier;
  final _gridKey = GlobalKey();
  List<MediaItem> _displayedItems = const [];
  int _columns = 1;
  double _rowExtent = 1;
  late final ScrollController _scrollController;
  List<Genre> _genres = const [];
  List<TmdbLanguage> _languages = const [];
  List<WatchRegion> _regions = const [];

  /// Service lists by region, kept for the life of the screen so a region
  /// picked in the sheet is loaded once and the empty state can name a
  /// service whatever region it came from.
  final Map<String, List<WatchProvider>> _providersByRegion = {};

  /// The region the sheet opens on when the query names none: the device's
  /// own country, or the US.
  late final String _deviceRegion = watchRegionFor(
    WidgetsBinding.instance.platformDispatcher.locale.countryCode,
  );

  @override
  void initState() {
    super.initState();
    _notifier = widget.session.acquire(widget.query)..addListener(_onFeedChanged);
    _scrollController = ScrollController(
        initialScrollOffset: _notifier.scrollOffset, keepScrollOffset: false)
      ..addListener(_maybeLoadMore);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && widget.query.feed.isFilterable) _loadFilterLists();
    });
  }

  @override
  void didUpdateWidget(covariant _BrowseGridBody oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.query.toLocation() != oldWidget.query.toLocation() &&
        widget.query.toLocation() != _notifier.query.toLocation()) {
      _selectQuery(widget.query, updateLocation: false);
    }
  }

  Future<void> _refresh() async {
    widget.session.touch(_notifier);
    await Future.wait([
      _notifier.refresh(),
      ref.read(librarySnapshotProvider.notifier)
          .refresh(force: true, type: _notifier.query.type),
    ]);
  }

  void _selectQuery(BrowseQuery query, {bool updateLocation = true}) {
    _notifier.removeListener(_onFeedChanged);
    widget.session.release(_notifier);
    setState(() {
      _notifier = widget.session.acquire(query)..addListener(_onFeedChanged);
      _displayedItems = const [];
    });
    final entry = _notifier;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !identical(entry, _notifier)) return;
      if (_scrollController.hasClients) {
        _scrollController.jumpTo(entry.scrollOffset.clamp(
            0.0, _scrollController.position.maxScrollExtent));
      }
      _refresh();
    });
    if (updateLocation) GoRouter.maybeOf(context)?.replace(query.toLocation());
  }

  @override
  void dispose() {
    _notifier.removeListener(_onFeedChanged);
    widget.session.release(_notifier);
    _scrollController
      ..removeListener(_maybeLoadMore)
      ..dispose();
    super.dispose();
  }

  void _onFeedChanged() {
    if (!mounted) return;
    double? anchorOffset;
    final oldOffset = _scrollController.hasClients ? _scrollController.offset : 0.0;
    // Use the laid-out grid's scroll offset, which excludes filters/padding.
    final grid = _gridKey.currentContext?.findRenderObject();
    if (grid is RenderSliverGrid && _displayedItems.isNotEmpty &&
        !identical(_displayedItems, _notifier.items)) {
      final index = (grid.constraints.scrollOffset / _rowExtent).floor() * _columns;
      if (index < _displayedItems.length) {
        final anchor = _displayedItems[index];
        final next = _notifier.items.indexWhere(
            (item) => item.id == anchor.id && item.mediaType == anchor.mediaType);
        if (next >= 0) {
          anchorOffset = oldOffset +
              ((next ~/ _columns) - (index ~/ _columns)) * _rowExtent;
        }
      }
    }
    setState(() {});
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      if (anchorOffset != null && _scrollController.hasClients &&
          _scrollController.offset == oldOffset) {
        _scrollController.jumpTo(anchorOffset.clamp(
            0.0, _scrollController.position.maxScrollExtent));
      }
      _maybeLoadMore();
    });
  }

  void _maybeLoadMore() {
    if (!_scrollController.hasClients) return;
    _notifier.scrollOffset = _scrollController.offset;
    if (_scrollController.position.extentAfter <
        BrowseGridScreen.loadMoreThreshold) {
      _notifier.loadMore();
    }
  }

  /// The lists the filter sheet labels its sections from. Each is a
  /// convenience: one that fails to load hides its section and never blocks
  /// the others, and the sheet still offers years and rating.
  Future<void> _loadFilterLists() async {
    final api = ref.read(discoverServiceProvider);
    final tv = widget.query.type == MediaType.tv;
    final region = widget.query.filters.watchRegion ?? _deviceRegion;
    var genres = const <Genre>[];
    var languages = const <TmdbLanguage>[];
    var regions = const <WatchRegion>[];
    await Future.wait([
      _load(() => tv ? api.tvGenres() : api.movieGenres(), (v) => genres = v),
      _load(api.languages, (v) => languages = v),
      _load(api.watchRegions, (v) => regions = v),
      _load(() => _providersFor(region), (_) {}),
    ]);
    if (!mounted) return;
    setState(() {
      _genres = genres;
      _languages = languages;
      _regions = regions;
    });
  }

  static Future<void> _load<T>(
    Future<T> Function() fetch,
    void Function(T value) assign,
  ) async {
    try {
      assign(await fetch());
    } catch (_) {}
  }

  /// The services TMDB knows for a region, loaded once per region.
  Future<List<WatchProvider>> _providersFor(String region) async {
    final cached = _providersByRegion[region];
    if (cached != null) return cached;
    final api = ref.read(discoverServiceProvider);
    final providers = widget.query.type == MediaType.tv
        ? await api.tvWatchProviders(region: region)
        : await api.movieWatchProviders(region: region);
    _providersByRegion[region] = providers;
    return providers;
  }

  Future<void> _openFilters() async {
    final api = ref.read(discoverServiceProvider);
    final filters = _notifier.query.filters;
    final region = filters.watchRegion ?? _deviceRegion;
    final result = await FilterSheet.show(
      context,
      genres: _genres,
      initial: filters,
      languages: _languages,
      regions: _regions,
      providers: _providersByRegion[region] ?? const [],
      region: region,
      lookups: FilterLookups(
        providersFor: _providersFor,
        searchKeywords: api.searchKeywords,
        searchCompanies: api.searchCompanies,
      ),
    );
    if (result == null || !mounted) return;
    _selectQuery(_notifier.query.copyWith(filters: result));
  }

  void _setSort(BrowseSort sort) {
    if (sort == _notifier.query.sort) return;
    _selectQuery(_notifier.query.copyWith(sort: sort));
  }

  void _clearFilters() =>
      _selectQuery(_notifier.query.copyWith(filters: BrowseFilters.none));

  String get _title {
    final query = _notifier.query;
    if (query.title case final title?) return title;
    final tv = query.type == MediaType.tv;
    return switch (query.feed) {
      BrowseFeed.featured =>
        featuredRowTitle(_notifier.featuredSource ?? '', isTv: tv),
      BrowseFeed.popular => tv ? 'Popular TV Shows' : 'Popular Movies',
      BrowseFeed.topRated => 'Top Rated',
      BrowseFeed.upcoming => 'Coming Soon',
      BrowseFeed.nowPlaying => 'In Theaters',
      BrowseFeed.onTheAir => 'Airing This Week',
      BrowseFeed.anticipated => 'Most Anticipated',
      BrowseFeed.discover => tv ? 'Browse TV Shows' : 'Browse Movies',
      BrowseFeed.recommendations => 'Recommended',
      BrowseFeed.similar => 'Similar',
    };
  }

  /// What an empty grid says it looked for, so absence is never mistaken for
  /// a broken feed.
  String get _emptyMessage {
    final query = _notifier.query;
    if (!query.feed.isFilterable) {
      return 'This feed has no titles right now.';
    }
    if (query.filters.isEmpty) {
      return 'No titles came back for this browse.';
    }
    final genreNames = {for (final genre in _genres) genre.id: genre.name};
    final languageNames = {
      for (final language in _languages) language.code: language.englishName,
    };
    final providerNames = {
      for (final providers in _providersByRegion.values)
        for (final provider in providers)
          provider.providerId: provider.providerName,
    };
    final described = query.filters.describe(
      genreNames,
      languageNames: languageNames,
      providerNames: providerNames,
    );
    return 'No titles matched $described.';
  }

  @override
  Widget build(BuildContext context) {
    final snapshot = ref.watch(librarySnapshotProvider);
    final query = _notifier.query;
    final items = _notifier.items;
    _displayedItems = items;
    final libraryStatus = buildSearchLibraryStatus(
      searchResults: items,
      movies: snapshot.movies,
      series: snapshot.series,
      show4K: ref.watch(cover4KBadgesProvider),
    );
    final isTv = query.type == MediaType.tv;
    final error = _notifier.error;
    final settled = !_notifier.isLoading && items.isEmpty &&
        (_notifier.hasLoaded || error != null);

    return DiscoverRefresh(
      path: Uri.parse(query.toLocation()).path,
      onRefresh: _refresh,
      child: Scaffold(
        appBar: AppBar(title: Text(_title)),
        body: LayoutBuilder(
          builder: (context, constraints) {
            final horizontalPadding =
                AppBreakpoints.isDesktop(context) ? 24.0 : 16.0;
            final usable = constraints.maxWidth - 2 * horizontalPadding;
            final columns = (usable / BrowseGridScreen.minCardWidth)
                .floor()
                .clamp(BrowseGridScreen.minColumns, BrowseGridScreen.maxColumns);
            final cardWidth =
                (usable - BrowseGridScreen.columnSpacing * (columns - 1)) /
                    columns;
            final extent = cardWidth * 1.5 +
                MediaCard.rowExtraHeight(context, withSubtitle: isTv);

            _columns = columns;
            _rowExtent = extent + BrowseGridScreen.rowSpacing;
            return Stack(children: [
              CustomScrollView(
                controller: _scrollController,
                physics: const AlwaysScrollableScrollPhysics(),
                slivers: [
                  if (query.feed.isFilterable)
                    SliverToBoxAdapter(child: _controls(horizontalPadding)),
                  if (settled && error != null)
                    SliverFillRemaining(
                      hasScrollBody: false,
                      child: FullScreenError(
                        message: 'These titles could not be loaded.',
                        onRetry: _notifier.load,
                      ),
                    )
                  else if (settled)
                    SliverFillRemaining(
                      hasScrollBody: false,
                      child: _EmptyState(
                        message: _emptyMessage,
                        onClearFilters: query.feed.isFilterable &&
                                !query.filters.isEmpty
                            ? _clearFilters
                            : null,
                      ),
                    )
                  else
                    SliverPadding(
                      padding: EdgeInsets.fromLTRB(
                        horizontalPadding,
                        8,
                        horizontalPadding,
                        8,
                      ),
                      sliver: SliverGrid(
                        key: _gridKey,
                        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                          crossAxisCount: columns,
                          mainAxisExtent: extent,
                          crossAxisSpacing: BrowseGridScreen.columnSpacing,
                          mainAxisSpacing: BrowseGridScreen.rowSpacing,
                        ),
                        delegate: SliverChildBuilderDelegate(
                          (context, index) {
                            final item = items[index];
                            return CatalogStatusBuilder(
                              key: ValueKey((item.mediaType, item.id)),
                              item: item,
                              legacyStatus: libraryStatus[(item.mediaType, item.id)],
                              builder: (status) => MediaCard(
                              id: item.id,
                              title: item.title,
                              posterPath: item.posterPath,
                              rating: item.voteAverage,
                              statusLabel: status?.label,
                              statusColor: status?.color,
                              subtitle: status?.episodeSubtitle,
                              is4K: status?.is4K ?? false,
                              width: cardWidth,
                              onTap: () => context.push(
                                '/detail/${item.mediaType.name}/${item.id}',
                              ),
                              ),
                            );
                          },
                          childCount: items.length,
                        ),
                      ),
                    ),
                  if (_notifier.isLoading || (!_notifier.hasLoaded && error == null))
                    const SliverToBoxAdapter(
                      child: Padding(
                        padding: EdgeInsets.symmetric(vertical: 24),
                        child: Center(
                          child: SizedBox(
                            width: 24,
                            height: 24,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          ),
                        ),
                      ),
                    ),
                  const SliverToBoxAdapter(child: SizedBox(height: 24)),
                ],
              ),
              if (items.isNotEmpty && (error != null ||
                  (isTv ? snapshot.seriesFailed : snapshot.moviesFailed)))
                Align(
                  alignment: Alignment.bottomCenter,
                  child: SafeArea(child: ErrorBanner(
                    message: 'Could not refresh these titles. Showing the last loaded results.',
                    onRetry: _refresh,
                  )),
                ),
            ]);
          },
        ),
      ),
    );
  }

  Widget _controls(double horizontalPadding) {
    final query = _notifier.query;
    final count = query.filters.count;
    return Padding(
      padding: EdgeInsets.fromLTRB(horizontalPadding, 12, horizontalPadding, 4),
      child: Wrap(
        alignment: WrapAlignment.spaceBetween,
        crossAxisAlignment: WrapCrossAlignment.center,
        spacing: 8,
        runSpacing: 8,
        children: [
          SectionSortMenu<BrowseSort>(
            options: BrowseSort.values,
            selected: query.sort,
            labelOf: (sort) => sort.label,
            onSelected: _setSort,
            tooltip: 'Sort titles',
          ),
          OutlinedButton.icon(
            onPressed: _openFilters,
            icon: const Icon(Icons.tune_rounded, size: 18),
            label: Text(count == 0 ? 'Filters' : 'Filters ($count)'),
          ),
        ],
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.message, this.onClearFilters});

  final String message;
  final VoidCallback? onClearFilters;

  @override
  Widget build(BuildContext context) {
    final textTheme = Theme.of(context).textTheme;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.search_off_rounded,
                size: 48, color: AppTheme.textMuted),
            const SizedBox(height: 16),
            Text('Nothing matched',
                style: textTheme.titleLarge, textAlign: TextAlign.center),
            const SizedBox(height: 8),
            Text(message,
                style: textTheme.bodyMedium, textAlign: TextAlign.center),
            if (onClearFilters != null) ...[
              const SizedBox(height: 20),
              OutlinedButton(
                onPressed: onClearFilters,
                child: const Text('Clear filters'),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
