import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../auth/logic/auth_provider.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/data/radarr_models.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/data/sonarr_models.dart';

/// The user's default Radarr and Sonarr libraries as last read, so a grid can
/// badge its posters Available / Requested without a fetch of its own.
class LibrarySnapshot {
  const LibrarySnapshot({
    this.movies = const [],
    this.series = const [],
    this.fetchedAt,
    this.serverUrl,
  });

  final List<RadarrMovie> movies;
  final List<SonarrSeries> series;

  /// When the snapshot was last filled; null until something has read a
  /// library.
  final DateTime? fetchedAt;

  /// The server the libraries belong to. A different server is a different
  /// library, so a snapshot taken against another one is discarded.
  final String? serverUrl;

  LibrarySnapshot copyWith({
    List<RadarrMovie>? movies,
    List<SonarrSeries>? series,
    DateTime? fetchedAt,
    String? serverUrl,
  }) =>
      LibrarySnapshot(
        movies: movies ?? this.movies,
        series: series ?? this.series,
        fetchedAt: fetchedAt ?? this.fetchedAt,
        serverUrl: serverUrl ?? this.serverUrl,
      );
}

/// Holds the snapshot and knows how to refill it.
///
/// Arr state is never trusted for long: the discovery tabs seed this from the
/// fetch they already make, a grid opened from a tab costs no second call, and
/// a grid reached any other way (a deep link, a web reload) or after
/// [staleAfter] pulls the libraries again. A media request bumps
/// `libraryRefreshTickProvider`, and the grid asks for a forced refresh on it.
class LibrarySnapshotNotifier extends Notifier<LibrarySnapshot> {
  /// How long a snapshot is trusted before a reader refetches it, matching
  /// the shell's own throttle on its search-chip library reads.
  static const staleAfter = Duration(seconds: 30);

  Future<void>? _inFlight;

  @override
  LibrarySnapshot build() => const LibrarySnapshot();

  /// The server the user is signed into, or null while sign-in settles.
  String? get _currentServer =>
      ref.read(authProvider).valueOrNull?.connection?.serverUrl;

  /// Records libraries another surface just fetched.
  void seed({List<RadarrMovie>? movies, List<SonarrSeries>? series}) {
    final server = _currentServer;
    final base = state.serverUrl == server ? state : const LibrarySnapshot();
    state = base.copyWith(
      movies: movies,
      series: series,
      fetchedAt: DateTime.now(),
      serverUrl: server,
    );
  }

  /// Refetches the default libraries when the snapshot is empty, belongs to
  /// another server, or is older than [staleAfter]; always with [force].
  /// Concurrent callers share one fetch. A library that cannot be read keeps
  /// whatever it held.
  Future<void> refresh({bool force = false}) {
    final server = _currentServer;
    if (state.serverUrl != null && state.serverUrl != server) {
      state = const LibrarySnapshot();
    }
    final fetchedAt = state.fetchedAt;
    if (!force &&
        fetchedAt != null &&
        DateTime.now().difference(fetchedAt) < staleAfter) {
      return Future.value();
    }
    return _inFlight ??= _fetch().whenComplete(() => _inFlight = null);
  }

  Future<void> _fetch() async {
    // Sign-in may still be settling when the first grid mounts.
    final AuthState auth;
    try {
      auth = await ref.read(authProvider.future);
    } catch (_) {
      return;
    }
    final connection = auth.connection;
    if (connection == null) return;
    final dio = ref.read(backendClientProvider);

    List<RadarrMovie>? movies;
    List<SonarrSeries>? series;
    final radarr = connection.defaultRadarrInstance;
    if (radarr != null) {
      try {
        movies = await RadarrApiService(backendDio: dio, instanceId: radarr.id)
            .getMovies();
      } catch (_) {
        // Unreadable: keep the last good list rather than blanking badges.
      }
    }
    final sonarr = connection.defaultSonarrInstance;
    if (sonarr != null) {
      try {
        series = await SonarrApiService(backendDio: dio, instanceId: sonarr.id)
            .getSeries();
      } catch (_) {}
    }
    // The sign-in may have moved to another server while the libraries were
    // being read; those lists belong to the old one.
    if (_currentServer != connection.serverUrl) return;
    seed(movies: movies, series: series);
  }
}

final librarySnapshotProvider =
    NotifierProvider<LibrarySnapshotNotifier, LibrarySnapshot>(
  LibrarySnapshotNotifier.new,
);
