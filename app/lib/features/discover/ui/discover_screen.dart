import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/search_bar.dart';
import '../../../core/theme/app_theme.dart';
import '../data/tmdb_models.dart';
import '../logic/discover_provider.dart';
import 'search_results_view.dart';
import 'category_row.dart';

/// The main discover screen – trending, categories, and search.
class DiscoverScreen extends ConsumerStatefulWidget {
  const DiscoverScreen({super.key});

  @override
  ConsumerState<DiscoverScreen> createState() => _DiscoverScreenState();
}

class _DiscoverScreenState extends ConsumerState<DiscoverScreen> {
  final _searchController = TextEditingController();
  final _searchFocusNode = FocusNode();
  final _scrollController = ScrollController();
  bool _hasScrolled = false;

  @override
  void initState() {
    super.initState();
    _scrollController.addListener(_onScroll);
    // Bootstrap data on first load.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(discoverProvider.notifier).bootstrap();
    });
  }

  void _onScroll() {
    final scrolled = _scrollController.offset > 10;
    if (scrolled != _hasScrolled) setState(() => _hasScrolled = scrolled);
  }

  @override
  void dispose() {
    _searchController.dispose();
    _searchFocusNode.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(discoverProvider);
    final notifier = ref.read(discoverProvider.notifier);

    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            // Search bar
            AnimatedContainer(
              duration: const Duration(milliseconds: 200),
              color: _hasScrolled
                  ? AppTheme.surface.withValues(alpha: 0.95)
                  : Colors.transparent,
              padding:
                  const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              child: CantinarrSearchBar(
                controller: _searchController,
                focusNode: _searchFocusNode,
                onChanged: (q) => notifier.updateSearch(q),
                onClear: () => notifier.updateSearch(''),
              ),
            ),

            // Error banner
            if (state.error != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: ErrorBanner(
                  message: state.error!,
                  onDismiss: notifier.clearError,
                ),
              ),

            // Content
            Expanded(
              child: state.isSearching
                  ? SearchResultsView(
                      results: state.searchResults,
                      isLoading: state.isLoadingSearch,
                      query: state.searchQuery,
                      onLoadMore: notifier.loadMoreSearch,
                    )
                  : _buildHomeContent(state, notifier),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildHomeContent(DiscoverState state, DiscoverNotifier notifier) {
    return RefreshIndicator(
      onRefresh: () async => notifier.bootstrap(),
      color: AppTheme.accent,
      child: ListView(
        controller: _scrollController,
        padding: const EdgeInsets.only(bottom: 24),
        children: [
          // Trending
          CategoryRow(
            title: 'Trending Now',
            items: state.trending,
            isLoading: state.isLoadingTrending,
            onLoadMore: notifier.loadMoreTrending,

          ),

          // Popular Movies
          CategoryRow(
            title: 'Popular Movies',
            items: state.popularMovies,
            isLoading: state.isLoadingPopularMovies,

          ),

          // Popular TV
          CategoryRow(
            title: 'Popular TV Shows',
            items: state.popularTV,
            isLoading: state.isLoadingPopularTV,

          ),

          // Top Rated
          if (state.topRated.isNotEmpty)
            CategoryRow(
              title: 'Top Rated',
              items: state.topRated,
              isLoading: false,
  
            ),

          // Upcoming
          if (state.upcoming.isNotEmpty)
            CategoryRow(
              title: 'Coming Soon',
              items: state.upcoming,
              isLoading: false,
  
            ),
        ],
      ),
    );
  }
}
