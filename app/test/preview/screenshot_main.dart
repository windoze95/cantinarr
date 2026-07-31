// Dev-only STORE-SCREENSHOT harness: boots the REAL app (router, shell,
// screens, theme) with a faked authenticated admin session and a stubbed
// backend that returns RICH demo payloads, so the UI renders fully populated
// in a browser without a server:
//
//   flutter run -d chrome -t test/preview/screenshot_main.dart
//
// It is the populated sibling of preview_main.dart (which returns empty
// payloads for empty-state previews). Navigation chrome, layout and theming
// are the real code paths; only the HTTP layer is stubbed. The demo bodies
// live in screenshot_data.dart. Never ship or import this from lib/.
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

import 'screenshot_data.dart';

void main() {
  runApp(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_adminState)),
        backendClientProvider.overrideWithValue(_stubDio()),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
      child: const _ScreenshotApp(),
    ),
  );
}

class _ScreenshotApp extends ConsumerWidget {
  const _ScreenshotApp();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return MaterialApp.router(
      title: 'Cantinarr Screenshots',
      theme: AppTheme.dark,
      debugShowCheckedModeBanner: false,
      routerConfig: ref.watch(appRouterProvider),
      builder: (context, child) =>
          AppAmbientBackground(child: child ?? const SizedBox.shrink()),
    );
  }
}

/// Admin with every module lit up. Mirrors preview_main's instance set, plus a
/// second (qBittorrent) download client so the download-queue screen can be
/// screenshotted for both SABnzbd (usenet) and qBittorrent (torrent) via the
/// drawer's instance selector.
const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost:8585',
    accessToken: 'screenshot-access',
    refreshToken: 'screenshot-refresh',
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
        id: 'sab-main',
        serviceType: 'sabnzbd',
        name: 'SABnzbd',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'qbit-main',
        serviceType: 'qbittorrent',
        name: 'qBittorrent',
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
  dio.httpClientAdapter = _ScreenshotAdapter();
  return dio;
}

/// Routes each request to a populated demo body (screenshot_data.dart), falling
/// back to empty-but-well-shaped responses (matching preview_main) so any
/// unmocked path settles into an empty state instead of crashing.
class _ScreenshotAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    final body = screenshotBodyFor(path, options.queryParameters) ??
        _fallback(path);
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  /// Shape-aware empty defaults for paths the demo data doesn't populate, so
  /// non-target screens (history, wanted, system status, etc.) don't throw on
  /// a type cast if navigated to during screenshotting.
  Object _fallback(String path) {
    if (path.endsWith('/system/status')) return {'version': '4.0.0'};
    if (path.contains('/api/downloads/') && path.endsWith('/history')) {
      return {'items': const []};
    }
    if (path.endsWith('/history')) {
      return {'records': const [], 'totalRecords': 0};
    }
    if (path.contains('/wanted/')) {
      return {'records': const [], 'totalRecords': 0};
    }
    if (path.endsWith('/queue')) return {'records': const []};
    if (path.contains('/discover') || path.contains('/search')) {
      return {'results': const [], 'page': 1, 'total_pages': 1, 'total_results': 0};
    }
    if (path.contains('/issues')) return {'issues': const []};
    if (path.contains('/agent-actions')) return {'actions': const []};
    if (path.contains('/requests')) return {'requests': const []};
    if (path.contains('/genres')) return {'genres': const []};
    if (path.contains('/providers')) return {'results': const []};
    if (path.contains('/plex/')) return const <String, dynamic>{};
    return const <Object>[];
  }

  @override
  void close({bool force = false}) {}
}
