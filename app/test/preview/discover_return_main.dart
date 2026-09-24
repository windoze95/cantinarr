// Release-mode browser regression fixture. Uses the real router, screens,
// image transport and providers with deterministic API payloads. Never ship.
// See tool/screenshots/discover_return.js for the server and assertions.
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
  WidgetsFlutterBinding.ensureInitialized();
  // Browser assertions use semantics only to click the real controls.
  WidgetsBinding.instance.ensureSemantics();
  runApp(ProviderScope(overrides: [
    authProvider.overrideWith(_FixtureAuth.new),
    backendClientProvider.overrideWithValue(
      Dio(BaseOptions(baseUrl: Uri.base.origin))..httpClientAdapter = _FixtureAdapter()),
    realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
  ], child: const _FixtureApp()));
}

class _FixtureAuth extends AuthNotifier {
  @override
  Future<AuthState> build() async => AuthState(
    connection: BackendConnection(
      serverUrl: Uri.base.origin,
      accessToken: 'browser-fixture', refreshToken: 'browser-fixture',
      services: const AvailableServices(radarr: true, sonarr: true, tmdb: true),
      instances: const [
        ServiceInstance(id: 'radarr-main', serviceType: 'radarr', name: 'Movies', isDefault: true),
        ServiceInstance(id: 'sonarr-main', serviceType: 'sonarr', name: 'TV', isDefault: true),
      ],
    ),
    user: const UserProfile(id: 1, username: 'browser-fixture', role: 'admin'),
  );
}

class _FixtureApp extends ConsumerWidget {
  const _FixtureApp();
  @override
  Widget build(BuildContext context, WidgetRef ref) => MaterialApp.router(
    theme: AppTheme.dark,
    debugShowCheckedModeBanner: false,
    routerConfig: ref.watch(appRouterProvider),
    builder: (context, child) => AppAmbientBackground(child: child!),
  );
}

class _FixtureAdapter implements HttpClientAdapter {
  final Dio _control = Dio(BaseOptions(baseUrl: Uri.base.origin));

  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    final control = await _control.get<Map<String, dynamic>>('/__fixture/read',
        queryParameters: {'path': options.path,
          'page': options.queryParameters['page'] ?? 1});
    final mode = control.data!;
    final path = options.path;
    var body = screenshotBodyFor(path, options.queryParameters) ?? _empty(path);
    if (path.startsWith('/api/discover/') && mode['empty'] == true) {
      body = {'results': [], 'page': 1, 'total_pages': 0, 'total_results': 0};
    }
    return ResponseBody.fromString(jsonEncode(_artwork(body)),
        mode['status'] as int? ?? 200,
        headers: {'content-type': ['application/json']});
  }

  Object _empty(String path) {
    if (path.endsWith('/history') || path.endsWith('/queue')) return {'records': []};
    if (path.contains('/discover') || path.contains('/search') || path.contains('/providers')) {
      return {'results': [], 'page': 1, 'total_pages': 0, 'total_results': 0};
    }
    if (path.contains('/genres')) return {'genres': []};
    if (path.contains('/issues')) return {'issues': []};
    if (path.contains('/agent-actions')) return {'actions': []};
    if (path.contains('/requests')) return {'requests': []};
    return [];
  }

  Object? _artwork(Object? value, [String key = '']) {
    if (value is Map) return value.map((k, v) => MapEntry(k, _artwork(v, k as String)));
    if (value is List) return value.map((v) => _artwork(v, key)).toList();
    if (value is String && value.isNotEmpty &&
        (const ['poster_path', 'backdrop_path', 'profile_path', 'remoteUrl', 'poster', 'fanart', 'thumb'].contains(key) ||
         value.startsWith('https://image.tmdb.org/'))) {
      var id = 0;
      for (final c in value.codeUnits) { id = (id * 31 + c) % 100000; }
      // Exercise normal byte reads AND authenticated same-origin relay reads.
      return id.isEven
          ? '${Uri.base.origin}/fixture-images/$id.png'
          : 'https://media.trakt.tv/images/fixture/$id.png';
    }
    return value;
  }

  @override
  void close({bool force = false}) { _control.close(force: force); }
}
