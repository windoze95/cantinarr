import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';

/// Discovery state for the TV Shows tab.
class TvDiscoverState {
  /// The headline row, from whichever feed the admin configured.
  final List<MediaItem> featured;

  /// Which feed answered, so the row can title itself honestly.
  final String featuredSource;
  final List<MediaItem> anticipated;
  final bool isLoadingFeatured;
  final bool isLoadingAnticipated;

  const TvDiscoverState({
    this.featured = const [],
    this.featuredSource = '',
    this.anticipated = const [],
    this.isLoadingFeatured = false,
    this.isLoadingAnticipated = false,
  });

  /// The row's title for the feed that answered.
  String get featuredTitle => featuredRowTitle(featuredSource, isTv: true);

  TvDiscoverState copyWith({
    List<MediaItem>? featured,
    String? featuredSource,
    List<MediaItem>? anticipated,
    bool? isLoadingFeatured,
    bool? isLoadingAnticipated,
  }) =>
      TvDiscoverState(
        featured: featured ?? this.featured,
        featuredSource: featuredSource ?? this.featuredSource,
        anticipated: anticipated ?? this.anticipated,
        isLoadingFeatured: isLoadingFeatured ?? this.isLoadingFeatured,
        isLoadingAnticipated: isLoadingAnticipated ?? this.isLoadingAnticipated,
      );
}

/// Fetches TV discovery rows (the headline feed + Most Anticipated).
class TvDiscoverNotifier extends StateNotifier<TvDiscoverState> {
  final DiscoverApiService _api;

  TvDiscoverNotifier(this._api) : super(const TvDiscoverState());

  Future<void> bootstrap() async {
    await Future.wait([
      _fetchFeatured(),
      _fetchAnticipatedShows(),
    ]);
  }

  Future<void> _fetchFeatured() async {
    state = state.copyWith(isLoadingFeatured: true);
    try {
      final feed = await _api.fetchFeaturedTV();
      state = state.copyWith(
        featured: feed.items,
        featuredSource: feed.source,
        isLoadingFeatured: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingFeatured: false);
    }
  }

  Future<void> _fetchAnticipatedShows() async {
    state = state.copyWith(isLoadingAnticipated: true);
    try {
      final items = await _api.getTraktAnticipated('shows');
      state = state.copyWith(
        anticipated: items.map((t) => t.toMediaItem()).toList(),
        isLoadingAnticipated: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingAnticipated: false);
    }
  }
}

/// Provider for TV discovery data.
final tvDiscoverProvider =
    StateNotifierProvider<TvDiscoverNotifier, TvDiscoverState>(
  (ref) {
    final api = ref.watch(discoverServiceProvider);
    return TvDiscoverNotifier(api);
  },
);
