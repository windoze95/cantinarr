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
/// Tautulli, plus AI + Chaptarr services for the assistant module and Books
/// tab.
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
    ],
  ),
  user: UserProfile(id: 1, username: 'preview-admin', role: 'admin'),
);

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
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
    } else if (path.contains('/requests')) {
      body = {'requests': []};
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
