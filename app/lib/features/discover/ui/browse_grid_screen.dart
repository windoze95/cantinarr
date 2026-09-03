import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_sort_menu.dart';
import '../data/discover_api_service.dart';
import '../data/tmdb_models.dart';
import '../logic/browse_grid_notifier.dart';
import '../logic/browse_query.dart';
import '../logic/library_snapshot_provider.dart';
import '../logic/search_library_status.dart';
import 'filter_sheet.dart';

/// A feed as a full-page poster grid that keeps loading: the "See all" behind
/// every discovery row, and the Browse page when the feed is the filterable
/// one. Posters carry the same Available / Requested badges the rows do.
class BrowseGridScreen extends ConsumerStatefulWidget {
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
  ConsumerState<BrowseGridScreen> createState() => _BrowseGridScreenState();
}

class _BrowseGridScreenState extends ConsumerState<BrowseGridScreen> {
  late final BrowseGridNotifier _notifier;
  final ScrollController _scrollController = ScrollController();
  List<Genre> _genres = const [];

  @override
  void initState() {
    super.initState();
    _notifier =
        BrowseGridNotifier(ref.read(discoverServiceProvider), widget.query)
          ..addListener(_onFeedChanged);
    _scrollController.addListener(_maybeLoadMore);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      _notifier.load();
      // Badges come from the shared library snapshot: free when a tab just
      // filled it, one fetch on a deep link or once it has gone stale.
      ref.read(librarySnapshotProvider.notifier).refresh();
      if (widget.query.feed.isFilterable) _loadGenres();
    });
  }

  @override
  void dispose() {
    _notifier
      ..removeListener(_onFeedChanged)
      ..dispose();
    _scrollController
      ..removeListener(_maybeLoadMore)
      ..dispose();
    super.dispose();
  }

  void _onFeedChanged() {
    if (!mounted) return;
    setState(() {});
    // A wide grid whose first page does not reach the bottom never scrolls,
    // so ask again after each page lands whether the next is already due.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _maybeLoadMore();
    });
  }

  void _maybeLoadMore() {
    if (!_scrollController.hasClients) return;
    if (_scrollController.position.extentAfter <
        BrowseGridScreen.loadMoreThreshold) {
      _notifier.loadMore();
    }
  }

  Future<void> _loadGenres() async {
    final api = ref.read(discoverServiceProvider);
    try {
      final genres = widget.query.type == MediaType.tv
          ? await api.tvGenres()
          : await api.movieGenres();
      if (mounted) setState(() => _genres = genres);
    } catch (_) {
      // The sheet still offers years and rating; genres are a convenience.
    }
  }

  Future<void> _openFilters() async {
    final result = await FilterSheet.show(
      context,
      genres: _genres,
      initial: _notifier.query.filters,
    );
    if (result == null || !mounted) return;
    await _notifier.setQuery(_notifier.query.copyWith(filters: result));
  }

  void _setSort(BrowseSort sort) {
    if (sort == _notifier.query.sort) return;
    _notifier.setQuery(_notifier.query.copyWith(sort: sort));
  }

  void _clearFilters() =>
      _notifier.setQuery(_notifier.query.copyWith(filters: BrowseFilters.none));

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
    final names = {for (final genre in _genres) genre.id: genre.name};
    return 'No titles matched ${query.filters.describe(names)}.';
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(libraryRefreshTickProvider, (_, __) {
      ref.read(librarySnapshotProvider.notifier).refresh(force: true);
    });
    final snapshot = ref.watch(librarySnapshotProvider);
    final query = _notifier.query;
    final items = _notifier.items;
    final libraryStatus = buildSearchLibraryStatus(
      searchResults: items,
      movies: snapshot.movies,
      series: snapshot.series,
    );
    final isTv = query.type == MediaType.tv;
    final error = _notifier.error;
    final settled = !_notifier.isLoading && items.isEmpty;

    return Scaffold(
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
              (isTv
                  ? MediaCard.subtitleRowExtraHeight
                  : MediaCard.plainRowExtraHeight);

          return CustomScrollView(
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
                    gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                      crossAxisCount: columns,
                      mainAxisExtent: extent,
                      crossAxisSpacing: BrowseGridScreen.columnSpacing,
                      mainAxisSpacing: BrowseGridScreen.rowSpacing,
                    ),
                    delegate: SliverChildBuilderDelegate(
                      (context, index) {
                        final item = items[index];
                        final status =
                            libraryStatus[(item.mediaType, item.id)];
                        return MediaCard(
                          id: item.id,
                          title: item.title,
                          posterPath: item.posterPath,
                          rating: item.voteAverage,
                          statusLabel: status?.label,
                          statusColor: status?.color,
                          subtitle: status?.episodeSubtitle,
                          width: cardWidth,
                          onTap: () => context.push(
                            '/detail/${item.mediaType.name}/${item.id}',
                          ),
                        );
                      },
                      childCount: items.length,
                    ),
                  ),
                ),
              if (_notifier.isLoading)
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
          );
        },
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
