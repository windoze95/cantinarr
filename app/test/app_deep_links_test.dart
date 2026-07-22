import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/app.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/data/server_status.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Exercises the cantinarr:// deep-link surface through the
/// [deepLinkSourceProvider] seam: connect links must reach the auth flow,
/// passkey links must route only when the link's server matches the connected
/// one (after normalization), and anything else must be ignored quietly.
void main() {
  group('request decision copy', () {
    test('movie and TV decisions keep their existing whole-request copy', () {
      expect(
        requestDecisionSnackText({
          'decision': 'approved',
          'media_type': 'movie',
          'title': 'Arrival',
        }),
        'Approved: Arrival',
      );
      expect(
        requestDecisionSnackText({
          'decision': 'denied',
          'media_type': 'tv',
          'title': 'Severance',
          'reason': 'Not available',
        }),
        'Denied: Severance — Not available',
      );
    });

    test('partial book approval names only the successful format', () {
      expect(
        requestDecisionSnackText({
          'decision': 'approved',
          'media_type': 'book',
          'title': 'Flock',
          'book_format': 'both',
          'book_formats': {
            'ebook': 'requested',
            'audiobook': 'unavailable',
          },
        }),
        'eBook approved: Flock',
      );
    });

    test('book denial names only the denied format and keeps its reason', () {
      expect(
        requestDecisionSnackText({
          'decision': 'denied',
          'media_type': 'book',
          'title': 'Flock',
          'book_format': 'both',
          'book_formats': {
            'ebook': 'requested',
            'audiobook': 'denied',
          },
          'reason': 'No audiobook edition',
        }),
        'Audiobook denied: Flock — No audiobook edition',
      );
    });
  });

  group('normalizeServer / sameServer', () {
    test('ignores trailing slashes', () {
      expect(
        sameServer('https://media.example.com/', 'https://media.example.com'),
        isTrue,
      );
      expect(
        sameServer('https://media.example.com///', 'https://media.example.com'),
        isTrue,
      );
    });

    test('ignores a default port', () {
      expect(
        sameServer(
            'https://media.example.com:443', 'https://media.example.com'),
        isTrue,
      );
      expect(
        sameServer('http://media.example.com:80', 'http://media.example.com'),
        isTrue,
      );
      expect(
        sameServer(
            'https://media.example.com:8443', 'https://media.example.com'),
        isFalse,
        reason: 'a non-default port is a different server',
      );
    });

    test('ignores host casing', () {
      expect(
        sameServer('https://MEDIA.Example.COM', 'https://media.example.com'),
        isTrue,
      );
      expect(
        sameServer('HTTPS://media.example.com', 'https://media.example.com'),
        isFalse,
        reason: 'an uppercase scheme defeats the scheme probe and gets '
            'https:// prepended — pinned so a change here is a conscious one',
      );
    });

    test('assumes https for scheme-less input', () {
      expect(sameServer('media.example.com', 'https://media.example.com'),
          isTrue);
      expect(sameServer('media.example.com', 'http://media.example.com'),
          isFalse,
          reason: 'a plain host must not match an http:// server');
    });

    test('treats different hosts and base paths as different servers', () {
      expect(sameServer('https://a.example.com', 'https://b.example.com'),
          isFalse);
      expect(
        sameServer(
            'https://media.example.com/base', 'https://media.example.com'),
        isFalse,
      );
    });

    test('never throws on unparseable input', () {
      expect(() => normalizeServer(''), returnsNormally);
      expect(() => normalizeServer('http://'), returnsNormally);
      expect(() => normalizeServer('%%%'), returnsNormally);
      expect(sameServer('%%%', '%%%'), isTrue);
    });
  });

  group('deep link handling', () {
    testWidgets('an initial connect link is forwarded to the auth flow',
        (tester) async {
      final link =
          Uri.parse('cantinarr://connect?token=abc123&server=$_server');
      final app = await _pumpApp(tester,
          authState: const AuthState(), initialLink: link);

      expect(app.auth.connectLinks, [link.toString()]);
    });

    testWidgets('a connect link arriving while running is forwarded',
        (tester) async {
      final app = await _pumpApp(tester, authState: const AuthState());
      expect(app.auth.connectLinks, isEmpty);

      final link = Uri.parse('cantinarr://connect?token=xyz&server=$_server');
      app.links.controller.add(link);
      await _settleLink(tester);

      expect(app.auth.connectLinks, [link.toString()]);
    });

    testWidgets(
        'a passkeys link matching the connected server (any normalized '
        'variant) opens passkey creation', (tester) async {
      final app = await _pumpApp(tester, authState: _authedState);
      final router = app.container.read(appRouterProvider);

      const variants = [
        'https://media.example.com/', // trailing slash
        'https://MEDIA.EXAMPLE.COM', // case
        'https://media.example.com:443', // default port
        'media.example.com', // scheme-less
      ];
      for (final server in variants) {
        app.links.controller.add(
          Uri.parse('cantinarr://passkeys?server=${Uri.encodeComponent(server)}'),
        );
        await _settleLink(tester);
        expect(
          router.routeInformationProvider.value.uri.path,
          '/settings/passkeys/new',
          reason: '$server should match $_server',
        );

        router.go('/dashboard/movies');
        await tester.pumpAndSettle();
      }
    });

    testWidgets('a passkeys link without a server parameter routes when '
        'authenticated', (tester) async {
      final app = await _pumpApp(tester, authState: _authedState);
      app.links.controller.add(Uri.parse('cantinarr://passkeys'));
      await _settleLink(tester);

      expect(
        app.container
            .read(appRouterProvider)
            .routeInformationProvider
            .value
            .uri
            .path,
        '/settings/passkeys/new',
      );
    });

    testWidgets('a passkeys link for a different server does not open '
        'passkey creation', (tester) async {
      final app = await _pumpApp(tester, authState: _authedState);
      app.links.controller.add(
        Uri.parse('cantinarr://passkeys?server=https://other.example.com'),
      );
      await _settleLink(tester);

      expect(
        app.container
            .read(appRouterProvider)
            .routeInformationProvider
            .value
            .uri
            .path,
        '/dashboard/movies',
        reason: 'a mismatched server must never reach passkey creation',
      );
    });

    testWidgets('a passkeys link while logged out lands on login',
        (tester) async {
      final app = await _pumpApp(tester, authState: const AuthState());
      app.links.controller.add(Uri.parse('cantinarr://passkeys'));
      await _settleLink(tester);

      expect(
        app.container
            .read(appRouterProvider)
            .routeInformationProvider
            .value
            .uri
            .path,
        '/login',
      );
    });

    testWidgets('wrong-scheme, wrong-host, and malformed links are ignored',
        (tester) async {
      final app = await _pumpApp(tester, authState: _authedState);

      for (final uri in [
        Uri.parse('https://media.example.com/connect?token=abc'), // scheme
        Uri.parse('cantinarr://something-else?token=abc'), // host
        Uri.parse('cantinarr:'), // no host at all
        Uri.parse('mailto:user@example.com'),
      ]) {
        app.links.controller.add(uri);
        await _settleLink(tester);
      }

      expect(app.auth.connectLinks, isEmpty);
      expect(
        app.container
            .read(appRouterProvider)
            .routeInformationProvider
            .value
            .uri
            .path,
        '/dashboard/movies',
        reason: 'ignored links must not navigate',
      );
    });

    testWidgets('a failing initial-link lookup is swallowed and the stream '
        'still works', (tester) async {
      final app = await _pumpApp(
        tester,
        authState: const AuthState(),
        initialLinkError: Exception('platform channel unavailable'),
      );

      final link = Uri.parse('cantinarr://connect?token=abc&server=$_server');
      app.links.controller.add(link);
      await _settleLink(tester);

      expect(app.auth.connectLinks, [link.toString()]);
    });
  });
}

const _server = 'https://media.example.com';

const _authedState = AuthState(
  connection: BackendConnection(
    serverUrl: _server,
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

/// Pumps [CantinarrApp] with every platform boundary faked: deep links come
/// from [_FakeDeepLinks], auth is [_FakeAuthNotifier], the realtime socket is
/// an empty stream, and HTTP is a canned-JSON Dio adapter.
Future<
    ({
      ProviderContainer container,
      _FakeAuthNotifier auth,
      _FakeDeepLinks links,
    })> _pumpApp(
  WidgetTester tester, {
  required AuthState authState,
  Uri? initialLink,
  Object? initialLinkError,
}) async {
  SharedPreferences.setMockInitialValues({});
  final links = _FakeDeepLinks(
    initialLink: initialLink,
    initialLinkError: initialLinkError,
  );
  final auth = _FakeAuthNotifier(authState);
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => auth),
    deepLinkSourceProvider.overrideWithValue(links),
    realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
    backendClientProvider.overrideWithValue(_fakeDio()),
  ]);
  addTearDown(container.dispose);
  addTearDown(links.controller.close);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const CantinarrApp(),
    ),
  );
  await tester.pumpAndSettle();
  return (container: container, auth: auth, links: links);
}

/// Lets a just-delivered link work through its async chain: the stream event,
/// the auth read, the post-frame navigation, and the resulting rebuilds.
Future<void> _settleLink(WidgetTester tester) async {
  await tester.pump();
  await tester.pumpAndSettle();
}

/// [DeepLinkSource] whose initial link is canned (or throws) and whose stream
/// is test-driven.
class _FakeDeepLinks implements DeepLinkSource {
  _FakeDeepLinks({this.initialLink, this.initialLinkError});

  final Uri? initialLink;
  final Object? initialLinkError;
  final StreamController<Uri> controller = StreamController<Uri>.broadcast();

  @override
  Future<Uri?> getInitialLink() async {
    final error = initialLinkError;
    if (error != null) throw error;
    return initialLink;
  }

  @override
  Stream<Uri> get uriLinkStream => controller.stream;
}

/// [AuthNotifier] fake: fixed state, records connect links, and answers
/// checkServer without the network (PasskeyCreateScreen probes it on open).
class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;
  final List<String> connectLinks = [];

  @override
  Future<AuthState> build() async => _initial;

  @override
  Future<void> connectWithLink(String link) async {
    connectLinks.add(link);
  }

  @override
  Future<ServerStatus> checkServer(String serverUrl) async =>
      const ServerStatus(needsSetup: false);
}

Dio _fakeDio() {
  final dio = Dio(BaseOptions(baseUrl: _server));
  dio.httpClientAdapter = _JsonAdapter();
  return dio;
}

/// Minimal HTTP fake: every request gets an empty paged-results payload, which
/// is enough for the dashboard and settings screens the router lands on.
class _JsonAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Object body = switch (options.path) {
      '/api/trakt/anticipated' => <dynamic>[],
      _ => {
          'page': 1,
          'results': <dynamic>[],
          'total_pages': 0,
          'total_results': 0,
        },
    };
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
