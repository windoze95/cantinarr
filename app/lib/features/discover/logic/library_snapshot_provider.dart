import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../auth/logic/auth_provider.dart';
import '../../dashboard/logic/library_rows.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/data/radarr_models.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/data/sonarr_models.dart';
import '../data/tmdb_models.dart';
import 'discover_session.dart';

/// One session read supplies both Discover's library rows and grid badges.
/// Failed refreshes retain successful data; authorization changes clear it.
class LibrarySnapshot {
  const LibrarySnapshot({
    this.movies = const [],
    this.series = const [],
    this.downloadingMovieIds = const {},
    this.recentSeries = const [],
    this.airingSeries = const [],
    this.sonarrInstanceId = '',
    this.moviesLoading = false,
    this.seriesLoading = false,
    this.moviesFailed = false,
    this.seriesFailed = false,
    this.moviesFetchedAt,
    this.seriesFetchedAt,
    this.serverUrl,
  });

  final List<RadarrMovie> movies;
  final List<SonarrSeries> series;
  final Set<int> downloadingMovieIds;
  final List<SonarrSeries> recentSeries;
  final List<SonarrSeries> airingSeries;
  final String sonarrInstanceId;
  final bool moviesLoading;
  final bool seriesLoading;
  final bool moviesFailed;
  final bool seriesFailed;
  final DateTime? moviesFetchedAt;
  final DateTime? seriesFetchedAt;
  final String? serverUrl;

  List<RadarrMovie> get recentMovies => recentlyDownloadedMovies(movies);
  List<RadarrMovie> get downloadingMovies {
    final waiting = movies.where((m) => m.monitored && !m.hasFile);
    return [
      ...waiting.where((m) => downloadingMovieIds.contains(m.id)),
      ...waiting.where((m) => !downloadingMovieIds.contains(m.id)),
    ].take(10).toList();
  }

  LibrarySnapshot copyWith({
    List<RadarrMovie>? movies,
    List<SonarrSeries>? series,
    Set<int>? downloadingMovieIds,
    List<SonarrSeries>? recentSeries,
    List<SonarrSeries>? airingSeries,
    String? sonarrInstanceId,
    bool? moviesLoading,
    bool? seriesLoading,
    bool? moviesFailed,
    bool? seriesFailed,
    DateTime? moviesFetchedAt,
    DateTime? seriesFetchedAt,
    String? serverUrl,
  }) => LibrarySnapshot(
    movies: movies ?? this.movies,
    series: series ?? this.series,
    downloadingMovieIds: downloadingMovieIds ?? this.downloadingMovieIds,
    recentSeries: recentSeries ?? this.recentSeries,
    airingSeries: airingSeries ?? this.airingSeries,
    sonarrInstanceId: sonarrInstanceId ?? this.sonarrInstanceId,
    moviesLoading: moviesLoading ?? this.moviesLoading,
    seriesLoading: seriesLoading ?? this.seriesLoading,
    moviesFailed: moviesFailed ?? this.moviesFailed,
    seriesFailed: seriesFailed ?? this.seriesFailed,
    moviesFetchedAt: moviesFetchedAt ?? this.moviesFetchedAt,
    seriesFetchedAt: seriesFetchedAt ?? this.seriesFetchedAt,
    serverUrl: serverUrl ?? this.serverUrl,
  );
}

class LibrarySnapshotNotifier extends Notifier<LibrarySnapshot> {
  static const staleAfter = Duration(seconds: 30);
  final _inFlight = <MediaType, Future<void>>{};
  int _generation = 0;
  String _scope = '';

  @override
  LibrarySnapshot build() {
    _scope = ref.watch(discoverSessionProvider);
    _generation++;
    _inFlight.clear();
    ref.onDispose(() => _generation++);
    return const LibrarySnapshot();
  }

  bool _current(int generation) => generation == _generation &&
      ref.read(discoverSessionProvider) == _scope;

  /// Also available to callers that already read an authoritative library.
  void seed({List<RadarrMovie>? movies, List<SonarrSeries>? series}) {
    state = state.copyWith(
      movies: movies,
      series: series,
      moviesFetchedAt: movies == null ? null : DateTime.now(),
      seriesFetchedAt: series == null ? null : DateTime.now(),
      serverUrl: ref.read(authProvider).valueOrNull?.connection?.serverUrl,
    );
  }

  /// Concurrent tab/grid callers share reads. Each media type has its own
  /// freshness clock; seeding Movies cannot mark an unread TV library fresh.
  Future<void> refresh({bool force = false, MediaType? type}) async {
    await Future.wait([
      if (type == null || type == MediaType.movie) _refresh(MediaType.movie, force),
      if (type == null || type == MediaType.tv) _refresh(MediaType.tv, force),
    ]);
  }

  Future<void> _refresh(MediaType type, bool force) {
    if (_inFlight[type] case final pending?) return pending;
    final fetched = type == MediaType.movie
        ? state.moviesFetchedAt : state.seriesFetchedAt;
    final failed = type == MediaType.movie ? state.moviesFailed : state.seriesFailed;
    if (!force && !failed && fetched != null &&
        DateTime.now().difference(fetched) < staleAfter) {
      return Future.value();
    }
    final generation = _generation;
    final future = type == MediaType.movie
        ? _fetchMovies(generation) : _fetchSeries(generation);
    _inFlight[type] = future;
    return future.whenComplete(() {
      if (identical(_inFlight[type], future)) _inFlight.remove(type);
    });
  }

  Future<void> _fetchMovies(int generation) async {
    final connection = ref.read(authProvider).valueOrNull?.connection;
    final instance = connection?.defaultRadarrInstance;
    if (instance == null) return;
    state = state.copyWith(moviesLoading: state.moviesFetchedAt == null);
    final api = RadarrApiService(
        backendDio: ref.read(backendClientProvider), instanceId: instance.id);
    try {
      final movies = await api.getMovies();
      if (!_current(generation)) return;
      state = state.copyWith(movies: movies, moviesFailed: false,
          moviesFetchedAt: DateTime.now(), serverUrl: connection!.serverUrl);
      try {
        final queue = await api.getQueue();
        if (!_current(generation)) return;
        state = state.copyWith(downloadingMovieIds: queue
            .map((r) => r['movieId'] as int?).whereType<int>().toSet());
      } catch (error) {
        if (!_current(generation)) return;
        state = state.copyWith(moviesFailed: true,
            downloadingMovieIds: discoverAccessDenied(error) ? {} : null);
      }
    } catch (error) {
      if (!_current(generation)) return;
      state = state.copyWith(moviesFailed: true,
          movies: discoverAccessDenied(error) ? [] : null,
          downloadingMovieIds: discoverAccessDenied(error) ? {} : null);
    }
    if (_current(generation)) state = state.copyWith(moviesLoading: false);
  }

  Future<void> _fetchSeries(int generation) async {
    final connection = ref.read(authProvider).valueOrNull?.connection;
    final instance = connection?.defaultSonarrInstance;
    if (instance == null) return;
    state = state.copyWith(seriesLoading: state.seriesFetchedAt == null);
    final api = SonarrApiService(
        backendDio: ref.read(backendClientProvider), instanceId: instance.id);
    try {
      final series = await api.getSeries();
      if (!_current(generation)) return;
      state = state.copyWith(series: series, seriesFailed: false,
          recentSeries: state.recentSeries.where((old) =>
              series.any((current) => current.id == old.id)).toList(),
          airingSeries: state.airingSeries.where((old) =>
              series.any((current) => current.id == old.id)).toList(),
          seriesFetchedAt: DateTime.now(), serverUrl: connection!.serverUrl,
          sonarrInstanceId: instance.id);
      // An imported season can consume dozens of history records. Keep the
      // existing 100-record window and import-event filter for this preview.
      await Future.wait([
        () async {
          try {
            final imports = await api.getHistory(pageSize: 100,
                eventType: SonarrHistoryRecord.importedEventTypeId);
            if (!_current(generation)) return;
            state = state.copyWith(
                recentSeries: recentlyDownloadedSeries(series, imports.records));
          } catch (error) {
            if (!_current(generation)) return;
            state = state.copyWith(seriesFailed: true,
                recentSeries: discoverAccessDenied(error) ? [] : null);
          }
        }(),
        () async {
          try {
            final now = DateTime.now();
            final calendar = await api.getCalendar(start: now.toIso8601String(),
                end: now.add(const Duration(days: 7)).toIso8601String());
            if (!_current(generation)) return;
            state = state.copyWith(airingSeries: airingNextSeries(series, calendar));
          } catch (error) {
            if (!_current(generation)) return;
            state = state.copyWith(seriesFailed: true,
                airingSeries: discoverAccessDenied(error) ? [] : null);
          }
        }(),
      ]);
    } catch (error) {
      if (!_current(generation)) return;
      state = state.copyWith(seriesFailed: true,
          series: discoverAccessDenied(error) ? [] : null,
          recentSeries: discoverAccessDenied(error) ? [] : null,
          airingSeries: discoverAccessDenied(error) ? [] : null);
    }
    if (_current(generation)) state = state.copyWith(seriesLoading: false);
  }
}

final librarySnapshotProvider =
    NotifierProvider<LibrarySnapshotNotifier, LibrarySnapshot>(
  LibrarySnapshotNotifier.new,
);
