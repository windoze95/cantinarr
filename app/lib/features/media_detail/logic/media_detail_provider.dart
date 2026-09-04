import 'package:flutter/foundation.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';
import 'title_facts.dart';

/// State for the media detail screen.
class MediaDetailState {
  final bool isLoading;
  final String? error;

  // Movie fields
  final MovieDetail? movieDetail;

  // TV fields
  final TVDetail? tvDetail;

  // Recommendations
  final List<MediaItem> recommendations;
  final List<MediaItem> similar;

  const MediaDetailState({
    this.isLoading = false,
    this.error,
    this.movieDetail,
    this.tvDetail,
    this.recommendations = const [],
    this.similar = const [],
  });

  String get title =>
      movieDetail?.title ?? tvDetail?.name ?? '';

  String get tagline =>
      movieDetail?.tagline ?? tvDetail?.tagline ?? '';

  String get overview =>
      movieDetail?.overview ?? tvDetail?.overview ?? '';

  String? get posterPath =>
      movieDetail?.posterPath ?? tvDetail?.posterPath;

  String? get backdropPath =>
      movieDetail?.backdropPath ?? tvDetail?.backdropPath;

  double? get voteAverage =>
      movieDetail?.voteAverage ?? tvDetail?.voteAverage;

  List<Genre> get genres =>
      movieDetail?.genres ?? tvDetail?.genres ?? [];

  String? get trailerKey =>
      movieDetail?.trailerKey ?? tvDetail?.trailerKey;

  List<Season> get seasons => tvDetail?.seasons ?? [];

  TitleCredits get credits =>
      movieDetail?.credits ?? tvDetail?.credits ?? TitleCredits.empty;

  /// Everyone the cast and crew sheet lists besides the cast: a show's
  /// creators first, then the credited crew.
  List<CrewMember> get crew => [...?tvDetail?.createdBy, ...credits.crew];

  /// The studios shown as chips: the first five production companies, so a
  /// co-production with a dozen partners stays a line, not a wall.
  List<TaggedId> get studios =>
      (movieDetail?.companies ?? tvDetail?.companies ?? const [])
          .where((c) => c.name != null)
          .take(5)
          .toList();

  List<TitleFact> get facts => switch ((movieDetail, tvDetail)) {
        (final movie?, _) => movieFacts(movie),
        (_, final tv?) => tvFacts(tv),
        _ => const [],
      };

  MediaDetailState copyWith({
    bool? isLoading,
    String? error,
    MovieDetail? movieDetail,
    TVDetail? tvDetail,
    List<MediaItem>? recommendations,
    List<MediaItem>? similar,
  }) =>
      MediaDetailState(
        isLoading: isLoading ?? this.isLoading,
        error: error,
        movieDetail: movieDetail ?? this.movieDetail,
        tvDetail: tvDetail ?? this.tvDetail,
        recommendations: recommendations ?? this.recommendations,
        similar: similar ?? this.similar,
      );
}

/// Loads full detail + recommendations for a movie or TV show.
class MediaDetailNotifier extends ChangeNotifier {
  final DiscoverApiService _api;
  final int _id;
  final MediaType _mediaType;

  MediaDetailState _state = const MediaDetailState();
  MediaDetailState get state => _state;
  set state(MediaDetailState value) {
    _state = value;
    notifyListeners();
  }

  MediaDetailNotifier({
    required DiscoverApiService api,
    required int id,
    required MediaType mediaType,
  })  : _api = api,
        _id = id,
        _mediaType = mediaType;

  Future<void> load() async {
    state = state.copyWith(isLoading: true);
    try {
      if (_mediaType == MediaType.movie) {
        final detail = await _api.movieDetail(_id);
        final recs = await _api.movieRecommendations(_id);
        final sim = await _api.similarMovies(_id);
        state = state.copyWith(
          isLoading: false,
          movieDetail: detail,
          recommendations: recs.results,
          similar: sim.results,
        );
      } else {
        final detail = await _api.tvDetail(_id);
        final recs = await _api.tvRecommendations(_id);
        final sim = await _api.similarTV(_id);
        state = state.copyWith(
          isLoading: false,
          tvDetail: detail,
          recommendations: recs.results,
          similar: sim.results,
        );
      }
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        error: 'Failed to load details: $e',
      );
    }
  }
}
