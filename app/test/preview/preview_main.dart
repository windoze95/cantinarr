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
/// sidebar instance selector), Sonarr, Chaptarr, a download client, Tautulli,
/// plus AI + Chaptarr services for the assistant module and Books tab.
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
        id: 'sab-main',
        serviceType: 'sabnzbd',
        name: 'SABnzbd',
        isDefault: true,
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
/// states instead of erroring where the shape matters.
class _StubAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    final Object body;
    if (path.contains('/discover') || path.contains('/search')) {
      body = {'results': [], 'page': 1, 'total_pages': 1, 'total_results': 0};
    } else if (path.contains('/issues')) {
      body = {'issues': []};
    } else if (path.contains('/agent-actions')) {
      body = {'actions': []};
    } else if (path.contains('/requests')) {
      body = {'requests': []};
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
