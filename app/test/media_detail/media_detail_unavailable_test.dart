import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// A title the server keeps from this account (a kids account outside its
/// limits) answers 404 on the detail route. The page says so plainly: an
/// answer, never the red failure text.
void main() {
  testWidgets('a not-available title lands on a plain line, not an error',
      (tester) async {
    await _pumpDetail(tester, status: 404, body: {'error': 'not available'});

    expect(find.text("This title isn't available on this account."),
        findsOneWidget);
    expect(find.textContaining('Failed to load'), findsNothing);
  });

  testWidgets('any other failure keeps the error text', (tester) async {
    await _pumpDetail(tester, status: 503, body: {'error': 'upstream'});

    expect(find.textContaining('Failed to load'), findsOneWidget);
    expect(find.text("This title isn't available on this account."),
        findsNothing);
  });
}

Future<void> _pumpDetail(
  WidgetTester tester, {
  required int status,
  required Map<String, dynamic> body,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final router = GoRouter(
    initialLocation: '/detail/movie/603',
    routes: [
      GoRoute(
        path: '/detail/:type/:id',
        builder: (_, state) => MediaDetailScreen(
          id: int.parse(state.pathParameters['id']!),
          mediaType: MediaType.movie,
        ),
      ),
    ],
  );

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _DetailAdapter(status: status, body: body);

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_state)),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
}

const _state = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
    instances: [
      ServiceInstance(
        id: 'radarr-main',
        serviceType: 'radarr',
        name: 'Movies',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'kid', role: 'user', child: true),
);

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

/// The detail route answers with the given status; every other read is an
/// empty success so nothing else on the page fails.
class _DetailAdapter implements HttpClientAdapter {
  final int status;
  final Map<String, dynamic> body;

  _DetailAdapter({required this.status, required this.body});

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    if (path.contains('/api/media/movie/') || path.contains('/api/media/tv/')) {
      return ResponseBody.fromString(
        jsonEncode(body),
        status,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    final Object response;
    if (path.endsWith('/status')) {
      response = {'status': 'unavailable'};
    } else {
      response = <String, dynamic>{'results': <dynamic>[]};
    }
    return ResponseBody.fromString(
      jsonEncode(response),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
