// Dev-only preview harness: boots the REAL app (router, shell, screens,
// theme) with a faked authenticated admin session and a stubbed backend, so
// the UI can be driven in a browser without standing up a server:
//
//   flutter run -d chrome -t test/preview/preview_main.dart
//
// Data-backed screens render their empty/error states (the stub returns empty
// payloads); navigation chrome, layout, and theming are the real code paths.
// Never ship or import this from lib/.
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/app_ambient_background.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

void main() {
  runApp(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_adminState)),
        backendClientProvider.overrideWithValue(_stubDio()),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
      child: const _PreviewApp(),
    ),
  );
}

class _PreviewApp extends ConsumerWidget {
  const _PreviewApp();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Same composition as CantinarrApp's authenticated branch (app.dart),
    // minus deep links and push which need native plumbing.
    return MaterialApp.router(
      title: 'Cantinarr Preview',
      theme: AppTheme.dark,
      debugShowCheckedModeBanner: false,
      routerConfig: ref.watch(appRouterProvider),
      builder: (context, child) =>
          AppAmbientBackground(child: child ?? const SizedBox.shrink()),
    );
  }
}

/// Admin with every module lit up: two Radarr instances (exercises the
/// sidebar instance selector), Sonarr, Chaptarr, three download clients
/// (exercises the aggregate "All" downloads view; torrent clients listed
/// before usenet here to prove the menu reorders them usenet-first),
/// Tautulli, a Jellyfin media server (exercises the "Watch on Jellyfin" menu
/// entry, the access guide, and the editor's media-server sections), plus
/// AI + Chaptarr services for the assistant module and Books tab.
const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost:8585',
    accessToken: 'preview-access',
    refreshToken: 'preview-refresh',
    services: AvailableServices(
      radarr: true,
      sonarr: true,
      chaptarr: true,
      ai: true,
      tmdb: true,
    ),
    instances: [
      ServiceInstance(
        id: 'radarr-main',
        serviceType: 'radarr',
        name: 'Main Radarr',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'radarr-4k',
        serviceType: 'radarr',
        name: '4K Radarr',
      ),
      ServiceInstance(
        id: 'sonarr-main',
        serviceType: 'sonarr',
        name: 'Sonarr',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'chaptarr-main',
        serviceType: 'chaptarr',
        name: 'Chaptarr',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'qbit-main',
        serviceType: 'qbittorrent',
        name: 'qBittorrent',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'qbit-yana',
        serviceType: 'qbittorrent',
        name: 'qBittorrent (Yana)',
      ),
      ServiceInstance(
        id: 'sab-main',
        serviceType: 'sabnzbd',
        name: 'SABnzbd',
      ),
      ServiceInstance(
        id: 'tautulli-main',
        serviceType: 'tautulli',
        name: 'Tautulli',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'jellyfin-main',
        serviceType: 'jellyfin',
        name: 'Home Jellyfin',
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'preview-admin', role: 'admin'),
);

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;

  // The real logout()'s teardown needs the storage/service fields the real
  // build() wires up; here only its end state matters — the router redirect
  // to the connect screen.
  @override
  Future<void> logout() async {
    state = const AsyncData(AuthState());
  }
}

Dio _stubDio() {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost:8585'));
  dio.httpClientAdapter = _StubAdapter();
  return dio;
}

/// Empty-but-well-shaped responses so providers settle into their empty
/// states instead of erroring where the shape matters. Download queues carry
/// fixture items so the aggregate "All" view has something to merge.
class _StubAdapter implements HttpClientAdapter {
  static Map<String, dynamic> _queueItem(
    String id,
    String name,
    double progress, {
    int speedBps = 0,
    String status = 'downloading',
  }) =>
      {
        'id': id,
        'name': name,
        'size_bytes': 4 * 1024 * 1024 * 1024,
        'size_left_bytes':
            (4 * 1024 * 1024 * 1024 * (100 - progress) ~/ 100),
        'progress': progress,
        'speed_bps': speedBps,
        'eta_seconds': speedBps > 0 ? 1800 : 0,
        'status': status,
        'category': 'movies',
      };

  static final Map<String, Map<String, dynamic>> _downloadQueues = {
    'sab-main': {
      'paused': false,
      'speed_bps': 6 * 1024 * 1024,
      'items': [
        _queueItem('sab-1', 'Movie.Night.2026.1080p.WEB-DL', 62),
        _queueItem('sab-2', 'Docu.Series.S01E03.720p', 12,
            status: 'queued'),
      ],
    },
    'qbit-main': {
      'paused': false,
      'speed_bps': 2 * 1024 * 1024,
      'items': [
        _queueItem('qb-1', 'Indie.Film.2025.2160p.REMUX', 38,
            speedBps: 2 * 1024 * 1024),
      ],
    },
    'qbit-yana': {
      'paused': false,
      'speed_bps': 512 * 1024,
      'items': [
        _queueItem('qb-2', 'Classic.Trilogy.1983.1080p', 91,
            speedBps: 512 * 1024),
        _queueItem('qb-3', 'Space.Opera.S02.Complete', 5,
            status: 'stalledDL'),
      ],
    },
  };

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    final Object body;
    final downloadsQueue =
        RegExp(r'/downloads/([^/]+)/queue$').firstMatch(path);
    if (downloadsQueue != null) {
      body = _downloadQueues[downloadsQueue.group(1)] ??
          {'paused': false, 'speed_bps': 0, 'items': <Object>[]};
    } else if (path.contains('/downloads/') && path.endsWith('/history')) {
      body = {'items': <Object>[]};
    } else if (path.endsWith('/api/admin/discovery-settings')) {
      // Must beat the generic '/discover' branch below.
      body = {
        'source': 'tmdb_trending',
        'english_only': true,
        'sources': ['tmdb_trending', 'trakt_trending', 'tmdb_popular'],
        'trakt_configured': false,
      };
    } else if (path.contains('/discover') || path.contains('/search')) {
      body = {'results': [], 'page': 1, 'total_pages': 1, 'total_results': 0};
    } else if (path.contains('/issues')) {
      body = {'issues': []};
    } else if (path.contains('/agent-actions')) {
      body = {'actions': []};
    } else if (RegExp(r'/api/media/\w+/\d+/(recommendations|similar)$')
        .hasMatch(path)) {
      // Detail loads its rows alongside the title; a bare list here fails the
      // whole load and drops the screen into its error state.
      body = {'results': [], 'page': 1, 'total_pages': 1, 'total_results': 0};
    } else if (RegExp(r'/api/media/movie/\d+$').hasMatch(path)) {
      // Enough of a TMDB movie detail for the detail screen to render its real
      // layout instead of the load-failure state.
      body = {
        'id': 550,
        'title': 'Preview: The Long Wait',
        'tagline': 'It gets here when it gets here.',
        'overview': 'A stand-in synopsis so the detail screen lays out the '
            'way it does with real TMDB copy underneath the request dock.',
        'release_date': _previewDate(21),
        'status': 'Post Production',
        'vote_average': 7.8,
        'runtime': 121,
        'genres': [
          {'id': 18, 'name': 'Drama'},
          {'id': 878, 'name': 'Science Fiction'},
        ],
      };
    } else if (RegExp(r'/api/requests/\d+/status$').hasMatch(path)) {
      // Must beat the generic '/requests' branch below. The tmdb id picks
      // which release state to preview, so the request dock's whole collapse
      // behaviour is reachable without a Radarr: 550 is still awaiting both
      // milestones, 551 has left cinemas and awaits only digital, 552 is fully
      // released and shows no line at all.
      final id = RegExp(r'/(\d+)/status$').firstMatch(path)!.group(1);
      body = {
        'status': 'requested',
        'progress': 0,
        'releases': switch (id) {
          '551' => {
              'in_cinemas': _previewDate(-45),
              'digital': _previewDate(30),
            },
          '552' => {
              'in_cinemas': _previewDate(-120),
              'digital': _previewDate(-60),
            },
          _ => {
              'in_cinemas': _previewDate(21),
              'digital': _previewDate(90),
            },
        },
      };
    } else if (path.endsWith('/api/requests/book-recent')) {
      // Must beat the generic '/requests' branch below. Four records covering
      // every state the Books tab's Recently Added row can render: fully
      // downloaded, a missing format on the way, a missing format nobody
      // requested, and a record with no digest row at all.
      body = {
        'items': [
          _recentBook(1, 'fb-1', 'The Long Way Home', 'ebook', 0),
          _recentBook(2, 'fb-2', 'Tide of Sand', 'ebook', 1),
          _recentBook(3, 'fb-3', 'Winter Signal', 'audiobook', 2),
          _recentBook(4, 'fb-4', 'The Unknown Ledger', 'ebook', 3),
        ],
      };
    } else if (path.endsWith('/api/requests/book-library')) {
      // Ownership digest for the records above. `fb-4` is deliberately absent
      // so its card exercises the no-pill path (an undetermined ownership
      // state renders no badge, never a fallback "Available").
      body = {
        'titles': [
          _ownedTitle('fb-1', 'The Long Way Home',
              ebook: (monitored: true, downloaded: true),
              audiobook: (monitored: true, downloaded: true)),
          _ownedTitle('fb-2', 'Tide of Sand',
              ebook: (monitored: true, downloaded: true),
              audiobook: (monitored: true, downloaded: false)),
          _ownedTitle('fb-3', 'Winter Signal',
              ebook: (monitored: false, downloaded: false),
              audiobook: (monitored: true, downloaded: true)),
        ],
      };
    } else if (path.endsWith('/api/requests/book-authors')) {
      // Four cards covering every state the Books tab's Authors row can
      // render: fully collected, partly collected, tracked but nothing on
      // disk, and an author the library has not keyed yet (shown, not
      // openable). Server order is preserved by the row, so these arrive
      // already sorted the way the server sorts them.
      // The server owns the order, so the stub answers each sort with the row
      // that sort would really produce — otherwise the preview's menu would
      // look broken for the two orders it never changes.
      const stubAuthors = [
        ('fa-1', 'Imogen Vale', 6, 6, 3),
        ('fa-2', 'Marcus Oyelaran', 4, 2, 1),
        ('fa-3', 'Sonya Petrov', 3, 0, 12),
        ('', 'Unkeyed Import', 1, 0, 40),
      ];
      final ordered = [...stubAuthors];
      switch (options.queryParameters['sort'] as String?) {
        case 'name':
          ordered.sort((a, b) => a.$2.compareTo(b.$2));
        case 'added':
          ordered.sort((a, b) => a.$5.compareTo(b.$5));
        default:
          break; // already most-collected first
      }
      body = {
        'authors': [
          for (final a in ordered)
            _libraryAuthor(a.$1, a.$2,
                titles: a.$3, available: a.$4, daysAgo: a.$5),
        ],
        // Deliberately larger than the list above, so the preview also shows
        // the header's "showing N of M" state a big library produces.
        'total': 137,
      };
    } else if (path.endsWith('/api/requests/book-series')) {
      // Four series covering the row's states: complete, a big gap, a single
      // book, and a name long enough to test the card's wrapping.
      const stubSeries = [
        ('The Ember Cycle', 12, 12),
        ('Riverwatch Chronicles', 41, 6),
        ('The Salt Road Standalones', 3, 1),
        ('Le Comte de Monte-Cristo / The Count of Monte Cristo', 6, 2),
      ];
      final ordered = [...stubSeries];
      if (options.queryParameters['sort'] == 'name') {
        ordered.sort((a, b) => a.$1.compareTo(b.$1));
      }
      body = {
        'series': [
          for (final s in ordered)
            {
              'name': s.$1,
              // Empty covers: the card draws its placeholder frames, so the
              // preview needs no image host and still shows the stack.
              'covers': const <String>[],
              'title_count': s.$2,
              'available_count': s.$3,
            },
        ],
        // Larger than the list, so the preview also shows the truncation note.
        'total': 143,
      };
    } else if (path.endsWith('/api/requests/book-series-detail')) {
      // Every per-title state a series page can render, in reading order,
      // including an unnumbered companion volume and an odd position label.
      body = {
        'series': {
          'name': 'Riverwatch Chronicles',
          'covers': <String>[],
          'title_count': 6,
          'available_count': 2,
        },
        'titles': [
          {..._ownedTitle('fb-s1', 'The Ninth Harbour', year: 2019,
              ebook: (monitored: true, downloaded: true),
              audiobook: (monitored: true, downloaded: true)), 'position': '1'},
          {..._ownedTitle('fb-s2', 'Salt and Lantern', year: 2020,
              ebook: (monitored: true, downloaded: true),
              audiobook: (monitored: true, downloaded: false)), 'position': '2'},
          {..._ownedTitle('fb-s3', 'A Quiet Inventory', year: 2021,
              ebook: (monitored: false, downloaded: false),
              audiobook: (monitored: false, downloaded: false)), 'position': '2A'},
          {..._ownedTitle('fb-s4', 'The Long Tide', year: 2022,
              ebook: (monitored: false, downloaded: false),
              audiobook: (monitored: false, downloaded: false)), 'position': '3'},
          {..._ownedTitle('fb-s5', 'Companion to Riverwatch', year: 0,
              ebook: (monitored: false, downloaded: false),
              audiobook: (monitored: false, downloaded: false)), 'position': ''},
        ],
      };
    } else if (path.endsWith('/api/requests/book-author')) {
      // One author page carrying every per-title verdict at once: both formats
      // on disk, a format still on the way, something on disk with nothing
      // pending, a title nobody has requested, and a record whose format truth
      // could not be resolved (which must render no pill at all).
      body = {
        'author': _libraryAuthor('fa-2', 'Marcus Oyelaran',
            titles: 5, available: 3),
        'titles': [
          _ownedTitle('fb-a1', 'The Salt Road', year: 2025,
              ebook: (monitored: true, downloaded: true),
              audiobook: (monitored: true, downloaded: true)),
          _ownedTitle('fb-a2', 'Ninth Harbour', year: 2024,
              ebook: (monitored: true, downloaded: true),
              audiobook: (monitored: true, downloaded: false)),
          _ownedTitle('fb-a3', 'Lantern Season', year: 2022,
              ebook: (monitored: false, downloaded: true),
              audiobook: (monitored: false, downloaded: false)),
          _ownedTitle('fb-a4', 'A Quiet Inventory', year: 2019,
              ebook: (monitored: false, downloaded: false),
              audiobook: (monitored: false, downloaded: false)),
          _ownedTitle('fb-a5', 'Uncatalogued Edition', year: 0,
              statusKnown: false,
              ebook: (monitored: true, downloaded: true),
              audiobook: (monitored: false, downloaded: false)),
        ],
      };
    } else if (path.contains('/requests')) {
      body = {'requests': []};
    } else if (path.endsWith('/api/media-servers')) {
      // The access guide: one granted server with no account yet, so the
      // create-account card and sheet are reachable.
      body = [
        {
          'instance_id': 'jellyfin-main',
          'service_type': 'jellyfin',
          'name': 'Home Jellyfin',
          'public_address': 'https://jellyfin.example.com',
          'account': null,
        },
      ];
    } else if (path.endsWith('/api/instances/media-server/libraries')) {
      // The Jellyfin editor's library probe after a passing test.
      body = {
        'server_name': 'Home Jellyfin',
        'version': '10.10.7',
        'libraries': [
          {'id': 'lib-movies', 'name': 'Movies', 'collection_type': 'movies'},
          {'id': 'lib-shows', 'name': 'Shows', 'collection_type': 'tvshows'},
          {'id': 'lib-music', 'name': 'Music', 'collection_type': 'music'},
        ],
      };
    } else if (path.endsWith('/api/admin/credentials')) {
      // Discover hosts the TMDB/Trakt credential sections; built-in TMDB is
      // the fresh-server truth.
      body = {
        'credentials': <String, bool>{},
        'tmdb_using_builtin': true,
        'ai': <String, dynamic>{},
      };
    } else if (path.endsWith('/api/admin/request-settings')) {
      // Defaults-shaped so the Request Defaults screen (a settings-search
      // deep-link target) renders its controls instead of the error state.
      body = {
        'settings': <String, dynamic>{},
        'radarr_profiles': <Object>[],
        'sonarr_profiles': <Object>[],
      };
    } else if (path.endsWith('/api/notifications/preferences')) {
      body = <String, dynamic>{};
    } else if (path.endsWith('/api/ai/settings')) {
      // Included AI active so the AI Access screen shows its covered state.
      body = {
        'providers': [
          {
            'id': 'anthropic',
            'label': 'Anthropic',
            'credential_key': 'anthropic_key',
            'models': [
              {'id': 'claude-sonnet-4-6', 'label': 'Claude Sonnet 4.6'},
            ],
          },
          {
            'id': 'openai',
            'label': 'OpenAI',
            'credential_key': 'openai_key',
            'models': [
              {'id': 'gpt-5.4-mini', 'label': 'GPT-5.4 mini'},
            ],
          },
          {
            'id': 'gemini',
            'label': 'Google Gemini',
            'credential_key': 'gemini_key',
            'models': [
              {'id': 'gemini-2.5-flash', 'label': 'Gemini 2.5 Flash'},
            ],
          },
          {
            'id': 'codex',
            'label': 'OpenAI (OAuth)',
            'credential_key': '',
            'auth_type': 'user_oauth',
            'models': [
              {'id': 'gpt-5.6-luna', 'label': 'GPT-5.6 Luna'},
            ],
          },
        ],
        'default_provider': 'codex',
        'default_model': 'gpt-5.6-luna',
        'personal': {
          'selected': false,
          'config': null,
          'credentials': {
            'anthropic': false,
            'openai': false,
            'gemini': false,
            'codex': false,
          },
        },
        'shared': {
          'granted': true,
          'configured': true,
          'config': {'provider': 'codex', 'model': 'gpt-5.6-luna'},
        },
        'effective': {
          'available': true,
          'source': 'shared',
          'provider': 'codex',
          'model': 'gpt-5.6-luna',
          'reason': '',
        },
      };
    } else {
      body = <Object>[];
    }
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

/// One `/api/requests/book-recent` item. [daysAgo] orders the row newest-first
/// without pinning the fixture to a calendar date that rots.
Map<String, dynamic> _recentBook(
  int bookId,
  String foreignBookId,
  String title,
  String format,
  int daysAgo,
) =>
    {
      'book_id': bookId,
      'foreign_book_id': foreignBookId,
      'title': title,
      'format': format,
      // Empty cover: the card falls back to its placeholder icon, so the
      // preview needs no image host.
      'cover': '',
      'imported_at': _previewDate(-daysAgo),
    };

/// One `/api/requests/book-library` ownership-digest entry, with each format's
/// `monitored`/`downloaded` flags spelled out at the call site.
Map<String, dynamic> _ownedTitle(
  String foreignBookId,
  String title, {
  required ({bool monitored, bool downloaded}) ebook,
  required ({bool monitored, bool downloaded}) audiobook,
  int year = 0,
  bool statusKnown = true,
}) =>
    {
      'title': title,
      'author': 'Preview Author',
      'year': year,
      'foreign_book_id': foreignBookId,
      'cover': '',
      'ebook': {'monitored': ebook.monitored, 'downloaded': ebook.downloaded},
      'audiobook': {
        'monitored': audiobook.monitored,
        'downloaded': audiobook.downloaded,
      },
      'status_known': statusKnown,
    };

/// One `/api/requests/book-authors` entry. An empty [foreignAuthorId] is the
/// not-yet-keyed author the row shows but refuses to open.
Map<String, dynamic> _libraryAuthor(
  String foreignAuthorId,
  String name, {
  required int titles,
  required int available,
  int daysAgo = 0,
}) =>
    {
      'foreign_author_id': foreignAuthorId,
      'name': name,
      // Empty image: the card falls back to its placeholder icon, so the
      // preview needs no image host.
      'image': '',
      'title_count': titles,
      'available_count': available,
      'added': '${_previewDate(-daysAgo)}T12:00:00Z',
    };

/// A `YYYY-MM-DD` calendar date [days] from today (negative for the past), so
/// preview fixtures that depend on being ahead of or behind now don't rot.
String _previewDate(int days) {
  final d = DateTime.now().add(Duration(days: days));
  final m = d.month.toString().padLeft(2, '0');
  final day = d.day.toString().padLeft(2, '0');
  return '${d.year}-$m-$day';
}
