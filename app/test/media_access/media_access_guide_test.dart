import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/safe_http_log_interceptor.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/media_access/data/media_access_service.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher/link.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/media_access/logic/media_app_launcher.dart';
import 'package:cantinarr/features/media_access/ui/media_access_guide.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// One canned answer: status, body (a String is sent verbatim), content type.
class _Reply {
  const _Reply(this.status, this.body, {this.contentType = 'application/json'});
  final int status;
  final Object body;
  final String contentType;
}

/// Serves replies by "METHOD path"; a handler may consult the decoded request
/// body and the adapter's own request log (so a later GET can reflect an
/// earlier POST). Unknown paths answer 404.
class _JsonAdapter implements HttpClientAdapter {
  _JsonAdapter(this.handlers);

  final Map<String, _Reply Function(dynamic body, int callsSoFar)> handlers;
  final List<({String method, String path, dynamic body})> requests = [];

  int calls(String method, String path) =>
      requests.where((r) => r.method == method && r.path == path).length;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    final path = options.uri.path;
    final before = calls(options.method, path);
    requests.add((method: options.method, path: path, body: body));
    final handler = handlers['${options.method} $path'];
    if (handler == null) {
      return ResponseBody.fromString(
        jsonEncode({'error': 'not found'}),
        404,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    final reply = handler(body, before);
    if (reply.status == 0) {
      throw DioException.connectionError(
        requestOptions: options,
        reason: 'connection refused',
      );
    }
    return ResponseBody.fromString(
      reply.body is String ? reply.body as String : jsonEncode(reply.body),
      reply.status,
      headers: {
        'content-type': [reply.contentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier({
    required this.user,
    this.instances = const [],
    this.plexAccessRequestable = false,
    this.supportsManagement = false,
  });

  final UserProfile user;
  final List<ServiceInstance> instances;
  final bool plexAccessRequestable;
  final bool supportsManagement;

  /// The emails shared through the ask-for-access card.
  final List<String> sharedEmails = [];

  @override
  Future<AuthState> build() async => AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
          instances: instances,
          plexAccessRequestable: plexAccessRequestable,
          mediaAccountManagement: supportsManagement,
        ),
        user: user,
      );

  @override
  Future<void> setPlexEmail(String email) async {
    sharedEmails.add(email);
    final current = state.valueOrNull;
    if (current?.user != null) {
      state = AsyncData(current!.copyWith(
        user: current.user!.copyWith(plexEmail: email.trim()),
      ));
    }
  }

  @override
  Future<void> refreshUser() async {}

  @override
  Future<void> refreshConfig() async {}
}

const _alice = UserProfile(id: 2, username: 'alice', role: 'user');
const _admin = UserProfile(id: 1, username: 'admin', role: 'admin');
const _jellyfin = ServiceInstance(
  id: 'jf-a',
  serviceType: 'jellyfin',
  name: 'Home Jellyfin',
);
const _emby = ServiceInstance(
  id: 'em-a',
  serviceType: 'emby',
  name: 'Den Emby',
);
const _audiobookshelf = ServiceInstance(
  id: 'abs-a',
  serviceType: 'audiobookshelf',
  name: 'Home Audiobookshelf',
);
const _plex = ServiceInstance(
  id: 'px-a',
  serviceType: 'plex',
  name: 'Cantina Plex',
);

Map<String, dynamic> _plexServer({Map<String, dynamic>? account}) => {
      'instance_id': 'px-a',
      'service_type': 'plex',
      'name': 'Cantina Plex',
      'kind': 'invite',
      'public_address': 'https://app.plex.tv',
      'account': account,
    };

Map<String, dynamic> _share({
  String username = 'alice@example.com',
  bool pending = true,
  bool verified = true,
}) =>
    {
      'username': username,
      'disabled': false,
      'pending': pending,
      'verified': verified,
    };

Map<String, dynamic> _embyServer({Map<String, dynamic>? account}) => {
      'instance_id': 'em-a',
      'service_type': 'emby',
      'name': 'Den Emby',
      'public_address': 'https://emby.example.com',
      'account': account,
    };

Map<String, dynamic> _server({
  Map<String, dynamic>? account,
  String publicAddress = 'https://jf.example.com',
  bool existingAccount = false,
}) =>
    {
      'instance_id': 'jf-a',
      'service_type': 'jellyfin',
      'name': 'Home Jellyfin',
      'public_address': publicAddress,
      'account': account,
      'existing_account': existingAccount,
    };

Map<String, dynamic> _account({
  String username = 'alice',
  bool disabled = false,
  bool verified = true,
}) =>
    {'username': username, 'disabled': disabled, 'verified': verified};

_FakeAuthNotifier? _lastAuth;

class _PendingMediaAccess extends MediaAccessService {
  _PendingMediaAccess() : super(backendDio: Dio());
  final result = Completer<List<MediaServerAccess>>();
  @override
  Future<List<MediaServerAccess>> listMine() => result.future;
}

Future<_JsonAdapter> _pumpGuide(
  WidgetTester tester, {
  required Map<String, _Reply Function(dynamic body, int callsSoFar)> handlers,
  UserProfile user = _alice,
  List<ServiceInstance> instances = const [_jellyfin],
  bool plexAccessRequestable = false,
  bool supportsManagement = false,
  List<String>? logs,
  MediaAppLauncher? launcher,
  MediaAccessService? service,
  Size size = const Size(800, 1600),
  double textScale = 1,
  bool settle = true,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final adapter = _JsonAdapter(handlers);
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  if (logs != null) {
    dio.interceptors.add(SafeHttpLogInterceptor(logPrint: logs.add));
  }
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        if (service != null)
          mediaAccessServiceProvider.overrideWithValue(service),
        if (launcher != null)
          mediaAppLauncherProvider.overrideWithValue(launcher),
        authProvider.overrideWith(() => _lastAuth = _FakeAuthNotifier(
            user: user,
            instances: instances,
            plexAccessRequestable: plexAccessRequestable,
            supportsManagement: supportsManagement)),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp(
        theme: AppTheme.dark,
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context)
              .copyWith(textScaler: TextScaler.linear(textScale)),
          child: child!,
        ),
        home: const MediaAccessGuide(),
      ),
    ),
  );
  if (settle) {
    await tester.pumpAndSettle();
  } else {
    await tester.pump();
  }
  return adapter;
}

/// Pumping a second guide into the same tree would update the existing
/// elements (same widget types, same positions) and keep the first pump's
/// state, overrides, and any open sheet; tear the tree down between pumps.
Future<void> _unmount(WidgetTester tester) async {
  await tester.pumpWidget(const SizedBox());
  await tester.pumpAndSettle();
}

/// Opens the password sheet from the account card.
Future<void> _openSheet(WidgetTester tester) async {
  await tester.tap(find.widgetWithText(ElevatedButton, 'Create my account'));
  await tester.pumpAndSettle();
  expect(find.text('Create your Jellyfin account'), findsOneWidget);
}

Future<void> _submitPassword(
  WidgetTester tester, {
  required String password,
  String? confirm,
}) async {
  await tester.enterText(find.widgetWithText(TextField, 'Password'), password);
  await tester.enterText(
      find.widgetWithText(TextField, 'Confirm password'), confirm ?? password);
  await tester.tap(find.widgetWithText(ElevatedButton, 'Create account'));
  await tester.pumpAndSettle();
}

/// A create handler whose GET flips to an active account once the POST
/// has been made, the way the real server would answer.
Map<String, _Reply Function(dynamic, int)> _createFlow(_Reply postReply) {
  var created = false;
  return {
    'GET /api/media-servers': (_, __) => _Reply(200, [
          _server(account: created ? _account() : null),
        ]),
    'POST /api/media-servers/jf-a/account': (_, __) {
      if (postReply.status == 201) created = true;
      return postReply;
    },
  };
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  for (final instances in <List<ServiceInstance>>[
    [_plex],
    [_jellyfin],
    [_emby],
    [_audiobookshelf],
    [_plex, _jellyfin, _audiobookshelf],
    [_plex, _jellyfin, _emby, _audiobookshelf],
    [
      _jellyfin,
      const ServiceInstance(
          id: 'jf-b', serviceType: 'jellyfin', name: 'Second server')
    ],
  ]) {
    testWidgets(
        'cards precede one instruction section per service: ${instances.map((i) => i.id).join(', ')}',
        (tester) async {
      await _pumpGuide(tester,
          instances: instances,
          size: const Size(1000, 5500),
          handlers: {
            'GET /api/media-servers': (_, __) => _Reply(200, [
                  for (final instance in instances)
                    {
                      'instance_id': instance.id,
                      'service_type': instance.serviceType,
                      'name': instance.name,
                      'account': instance.serviceType == 'plex'
                          ? _share()
                          : _account(),
                    },
                ]),
          });
      expect(
          find.textContaining('Each server has its own account or invitation'),
          findsOneWidget);
      final types = instances.map((i) => i.serviceType).toSet();
      expect(find.textContaining('For Plex, use'),
          types.contains('plex') ? findsOneWidget : findsNothing);
      for (final type in ['plex', 'jellyfin', 'emby', 'audiobookshelf']) {
        final section = find.byKey(ValueKey('media-guide-instructions-$type'));
        expect(section, types.contains(type) ? findsOneWidget : findsNothing);
        if (!types.contains(type)) continue;
        for (final instance in instances) {
          final card = find.text(
              '${mediaServerTypeLabel(instance.serviceType)} · ${instance.name}');
          expect(card, findsOneWidget);
          expect(tester.getTopLeft(card).dy,
              lessThan(tester.getTopLeft(section).dy));
        }
        expect(find.descendant(of: section, matching: find.byType(Link)),
            findsWidgets);
      }
      if (types.contains('plex')) {
        expect(find.textContaining('Remote Watch Pass'), findsOneWidget);
        expect(
            tester
                .widgetList<Link>(find.byType(Link))
                .map((link) => link.uri.toString()),
            contains(
                'https://support.plex.tv/articles/requirements-for-remote-playback-of-personal-media/'));
      }
      if (types.contains('emby')) {
        expect(
            tester
                .widgetList<Link>(find.byType(Link))
                .map((link) => link.uri.toString()),
            contains('https://emby.media/premiere.html'));
      }
      if (types.contains('audiobookshelf')) {
        expect(find.textContaining('separate Chaptarr access'), findsOneWidget);
      }
      expect(tester.takeException(), isNull);
    });
  }

  for (final layout in [
    (size: const Size(320, 800), scale: 1.0),
    (size: const Size(320, 800), scale: 2.0),
    (size: const Size(1200, 800), scale: 2.0),
  ]) {
    testWidgets(
        'hide banner stays below the title while scrolling at ${layout.size}, text ${layout.scale}',
        (tester) async {
      await _pumpGuide(tester,
          size: layout.size,
          textScale: layout.scale,
          instances: [
            _plex,
            _jellyfin,
            _emby,
            _audiobookshelf
          ],
          handlers: {
            'GET /api/media-servers': (_, __) => _Reply(200, [
                  _plexServer(account: _share()),
                  _server(account: _account(verified: false)),
                  _embyServer(),
                  {
                    'instance_id': 'abs-a',
                    'service_type': 'audiobookshelf',
                    'name': 'Home Audiobookshelf',
                  },
                ]),
          });
      final banner = find.byType(SwitchListTile);
      final original = tester.getRect(banner);
      expect(original.top,
          greaterThanOrEqualTo(tester.getRect(find.byType(AppBar)).bottom));
      expect(
          find.text(
              'You can always open this guide from Settings → Guides → Media server access.'),
          findsOneWidget);
      for (final type in ['plex', 'jellyfin', 'emby', 'audiobookshelf']) {
        await tester.scrollUntilVisible(
            find.byKey(ValueKey('media-guide-instructions-$type')), 250,
            scrollable: find.byType(Scrollable).first, maxScrolls: 100);
        expect(tester.getRect(banner), original);
        expect(tester.takeException(), isNull);
      }
      await tester.scrollUntilVisible(
          find.textContaining('Missing something after a scan?'), 250,
          scrollable: find.byType(Scrollable).first, maxScrolls: 100);
      await tester.tap(banner);
      await tester.pumpAndSettle();
      expect(tester.widget<SwitchListTile>(banner).value, isTrue);
      expect(tester.getRect(banner), original);
      expect(find.byType(MediaAccessGuide), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }

  for (final state in [
    'incomplete',
    'pending',
    'unconfirmed',
    'failed',
    'empty'
  ]) {
    testWidgets('can hide and restore the guide with $state setup',
        (tester) async {
      await _pumpGuide(tester, handlers: {
        'GET /api/media-servers': (_, __) => state == 'failed'
            ? const _Reply(0, {})
            : _Reply(
                200,
                switch (state) {
                  'pending' => [_plexServer(account: _share())],
                  'unconfirmed' => [
                      _server(account: _account(verified: false))
                    ],
                  'empty' => [],
                  _ => [_server()],
                }),
      });
      final banner = find.byType(SwitchListTile);
      await tester.tap(banner);
      await tester.pumpAndSettle();
      expect(tester.widget<SwitchListTile>(banner).value, isTrue);
      expect(find.byType(MediaAccessGuide), findsOneWidget);
      await tester.tap(banner);
      await tester.pumpAndSettle();
      expect(tester.widget<SwitchListTile>(banner).value, isFalse);
    });
  }

  testWidgets('can hide while the account request is still loading',
      (tester) async {
    final service = _PendingMediaAccess();
    await _pumpGuide(tester, service: service, settle: false, handlers: {});
    final banner = find.byType(SwitchListTile);
    await tester.tap(banner);
    await tester.pump();
    expect(tester.widget<SwitchListTile>(banner).value, isTrue);
    service.result.complete([]);
    await tester.pumpAndSettle();
    expect(tester.widget<SwitchListTile>(banner).value, isTrue);
  });

  testWidgets('hiding persists when the guide is reopened', (tester) async {
    final handlers = <String, _Reply Function(dynamic, int)>{
      'GET /api/media-servers': (_, __) => _Reply(200, [_server()]),
    };
    await _pumpGuide(tester, handlers: handlers);
    await tester.tap(find.byType(SwitchListTile));
    await tester.pumpAndSettle();
    await _unmount(tester);
    await _pumpGuide(tester, handlers: handlers);
    expect(tester.widget<SwitchListTile>(find.byType(SwitchListTile)).value,
        isTrue);
  });
  testWidgets('account management and pending changes are shown independently',
      (tester) async {
    await _pumpGuide(tester, supportsManagement: true, handlers: {
      'GET /api/media-servers': (_, __) => _Reply(200, [
            _server(account: {..._account(), 'manage_access': false})
          ]),
    });
    expect(
        find.text(
            'Linked only. Your account’s access is managed on Home Jellyfin.'),
        findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    await _pumpGuide(tester, supportsManagement: true, handlers: {
      'GET /api/media-servers': (_, __) => _Reply(200, [
            _server(account: {
              ..._account(disabled: true),
              'manage_access': true,
              'access_sync_pending': true
            })
          ]),
    });
    expect(
        find.text(
            'Your server access change is pending. Cantinarr will retry.'),
        findsOneWidget);
  });

  testWidgets('explicitly unlinked Plex server offers per-server relink',
      (tester) async {
    await _pumpGuide(tester, supportsManagement: true, instances: const [
      _plex
    ], handlers: {
      'GET /api/media-servers': (_, __) => _Reply(200, [
            {..._plexServer(), 'auto_link_suppressed': true}
          ]),
    });
    expect(find.text('Link my Plex account'), findsOneWidget);
    expect(find.text('Sign in with Plex'), findsNothing);
    expect(find.textContaining('was unlinked from'), findsOneWidget);
  });

  testWidgets(
      'create flow validates locally, posts the password once, and shows the '
      'account', (tester) async {
    final adapter = await _pumpGuide(
      tester,
      handlers: _createFlow(const _Reply(201, {
        'username': 'alice',
        'public_address': 'https://jf.example.com',
      })),
    );

    expect(find.text('Watch on Jellyfin'), findsOneWidget);
    expect(
      find.text('You have access to Home Jellyfin. Create your account to '
          'get started.'),
      findsOneWidget,
    );
    expect(find.text('Your account'), findsOneWidget);
    expect(find.textContaining('Install the Jellyfin app'), findsOneWidget);
    expect(find.text('Request here, watch there'), findsOneWidget);

    await _openSheet(tester);
    expect(
      find.textContaining("You'll sign in to Home Jellyfin as alice"),
      findsOneWidget,
    );

    await _submitPassword(tester, password: 'short');
    expect(
        find.text('Password must be at least 8 characters.'), findsOneWidget);
    await _submitPassword(tester,
        password: 'correct-horse', confirm: 'different-one');
    expect(find.text('Passwords do not match.'), findsOneWidget);
    expect(adapter.calls('POST', '/api/media-servers/jf-a/account'), 0);

    await _submitPassword(tester, password: 'correct-horse');
    final post = adapter.requests.singleWhere((r) => r.method == 'POST');
    expect(post.body, {'password': 'correct-horse'});
    expect(find.text('Create your Jellyfin account'), findsNothing);
    expect(find.text('Account created. Sign in with your new password.'),
        findsOneWidget);

    // The card re-read the server: the account is shown with where to sign
    // in, and the create button is gone.
    expect(find.text('Username'), findsOneWidget);
    expect(find.text('alice'), findsOneWidget);
    expect(find.text('Sign in at https://jf.example.com'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Copy address'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Open'), findsOneWidget);
    expect(
        find.widgetWithText(ElevatedButton, 'Create my account'), findsNothing);
  });

  for (final mixed in [false, true]) {
    testWidgets(
        'Audiobookshelf guide uses listening and browser guidance (mixed=$mixed)',
        (tester) async {
      final launched = <Uri>[];
      await _pumpGuide(tester,
          instances: [_audiobookshelf, if (mixed) _jellyfin],
          handlers: {
            'GET /api/media-servers': (_, __) => _Reply(200, [
                  {
                    'instance_id': 'abs-a',
                    'service_type': 'audiobookshelf',
                    'name': 'Home Audiobookshelf',
                    'public_address': 'https://books.example.com',
                    'account': _account(),
                  },
                  if (mixed) _server(account: _account()),
                ]),
          },
          launcher: MediaAppLauncher(
            platform: TargetPlatform.iOS,
            launchExternal: (uri) async {
              launched.add(uri);
              return true;
            },
          ));
      expect(find.text(mixed ? 'Media server access' : 'Audiobookshelf access'),
          findsOneWidget);
      await tester.scrollUntilVisible(
          find.byKey(const ValueKey('media-guide-instructions-audiobookshelf')),
          300,
          scrollable: find.byType(Scrollable).first);
      expect(find.textContaining('separate Chaptarr access'), findsOneWidget);
      expect(
          find.textContaining('Listen in Audiobookshelf opens a verified copy'),
          findsOneWidget);
      expect(find.textContaining('Open Audiobookshelf is a general shortcut'),
          findsOneWidget);
      if (mixed) {
        await tester.scrollUntilVisible(
            find.byKey(const ValueKey('media-guide-instructions-jellyfin')),
            -300,
            scrollable: find.byType(Scrollable).first);
        expect(find.textContaining('Install the Jellyfin app'), findsOneWidget);
      }
      tester
          .state<ScrollableState>(find.byType(Scrollable).first)
          .position
          .jumpTo(0);
      await tester.pumpAndSettle();
      final open = find.widgetWithText(TextButton, 'Open').first;
      await tester.ensureVisible(open);
      await tester.tap(open);
      await tester.pumpAndSettle();
      expect(launched.single.toString(), 'https://books.example.com');
    });
  }

  for (final platform in [TargetPlatform.iOS, TargetPlatform.android]) {
    for (final server in [_plex, _jellyfin, _emby]) {
      testWidgets('$platform guide opens the ${server.serviceType} app home',
          (tester) async {
        final external = <Uri>[];
        final android = <({String serviceType, Uri? uri})>[];
        final data = switch (server.serviceType) {
          'plex' => _plexServer(account: _share(pending: false)),
          'emby' => _embyServer(account: _account()),
          _ => _server(account: _account()),
        };
        await _pumpGuide(tester,
            instances: [server],
            handlers: {
              'GET /api/media-servers': (_, __) => _Reply(200, [data])
            },
            launcher: MediaAppLauncher(
              platform: platform,
              launchExternal: (uri) async {
                external.add(uri);
                return true;
              },
              launchAndroid: (serviceType, uri) async {
                android.add((serviceType: serviceType, uri: uri));
                return true;
              },
            ));
        final button = find.widgetWithText(TextButton, 'Open');
        await tester.ensureVisible(button);
        await tester.tap(button);
        await tester.pumpAndSettle();
        if (platform == TargetPlatform.iOS) {
          final scheme = server.serviceType == 'jellyfin'
              ? 'org.jellyfin.expo'
              : server.serviceType;
          expect(external.single.toString(), '$scheme://');
        } else {
          expect(android.single, (serviceType: server.serviceType, uri: null));
          expect(external, isEmpty);
        }
        expect(find.textContaining("Couldn't open"), findsNothing);
      });
    }
  }

  testWidgets('guide falls back to the public address before reporting failure',
      (tester) async {
    final launched = <Uri>[];
    await _pumpGuide(tester,
        handlers: {
          'GET /api/media-servers': (_, __) =>
              _Reply(200, [_server(account: _account())]),
        },
        launcher: MediaAppLauncher(
          platform: TargetPlatform.iOS,
          launchExternal: (uri) async {
            launched.add(uri);
            return false;
          },
        ));
    await tester.tap(find.widgetWithText(TextButton, 'Open'));
    await tester.pumpAndSettle();
    expect(launched.map((uri) => uri.toString()),
        ['org.jellyfin.expo://', 'https://jf.example.com']);
    expect(find.text("Couldn't open Home Jellyfin."), findsOneWidget);
  });

  testWidgets('an Emby server speaks Emby and never calls its app free',
      (tester) async {
    await _pumpGuide(
      tester,
      instances: const [_emby],
      handlers: {
        'GET /api/media-servers': (_, __) =>
            _Reply(200, [_embyServer(account: _account())]),
      },
    );

    expect(find.text('Watch on Emby'), findsOneWidget);
    expect(find.text('Emby · Den Emby'), findsOneWidget);
    expect(find.textContaining('Install the Emby app'), findsOneWidget);
    expect(find.text('Download Emby apps'), findsOneWidget);
    expect(find.textContaining('free'), findsNothing);
    expect(
      find.textContaining('one-time unlock or Emby Premiere'),
      findsOneWidget,
    );
    expect(
      find.textContaining('it verifies a matching movie or show'),
      findsOneWidget,
    );
    expect(find.text('Sign in at https://emby.example.com'), findsOneWidget);
  });

  testWidgets('a mixed set names both servers and keeps each card its own',
      (tester) async {
    await _pumpGuide(
      tester,
      instances: const [_jellyfin, _emby],
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: _account()),
              _embyServer(),
            ]),
      },
    );

    expect(find.text('Watch on Jellyfin or Emby'), findsOneWidget);
    expect(find.text('Jellyfin · Home Jellyfin'), findsOneWidget);
    expect(find.text('Emby · Den Emby'), findsOneWidget);
    expect(find.textContaining('Install the Jellyfin app'), findsOneWidget);

    // Only the Emby card has no account, and its sheet speaks Emby.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Create my account'));
    await tester.pumpAndSettle();
    expect(find.text('Create your Emby account'), findsOneWidget);
    expect(
      find.textContaining("You'll sign in to Den Emby as alice"),
      findsOneWidget,
    );
  });

  testWidgets('a taken name points at signing in with that account',
      (tester) async {
    await _pumpGuide(
      tester,
      handlers: _createFlow(const _Reply(409, {
        'error': 'that name is already taken on this server; ask your admin '
            'to link it to you',
        'code': 'name_taken',
      })),
    );

    await _openSheet(tester);
    await _submitPassword(tester, password: 'correct-horse');

    expect(
      find.text('The name alice is already taken on Home Jellyfin. If that '
          "account is yours, go back and tap 'I already have an account' to "
          'sign in with its password. Otherwise ask your admin.'),
      findsOneWidget,
    );
    // The sheet stays open for another try.
    expect(find.text('Create your Jellyfin account'), findsOneWidget);
  });

  testWidgets(
      'an account already named like the user leads with signing in and '
      'offers nothing to create', (tester) async {
    var linked = false;
    final adapter = await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(
                account: linked ? _account() : null,
                existingAccount: !linked,
              ),
            ]),
        'POST /api/media-servers/jf-a/account/link': (_, __) {
          linked = true;
          return const _Reply(201, {
            'username': 'alice',
            'public_address': 'https://jf.example.com',
          });
        },
      },
    );

    expect(
      find.text("There's already an account named alice on Home Jellyfin. "
          "If it's yours, sign in with its password to link it."),
      findsOneWidget,
    );
    expect(find.widgetWithText(ElevatedButton, 'Sign in to link it'),
        findsOneWidget);
    expect(find.text('Not yours? Ask your admin.'), findsOneWidget);
    expect(
        find.widgetWithText(ElevatedButton, 'Create my account'), findsNothing);
    expect(find.widgetWithText(TextButton, 'I already have an account'),
        findsNothing);
    // The flag is a hint about the layout, never a claim of an account.
    expect(find.text('Username'), findsNothing);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Sign in to link it'));
    await tester.pumpAndSettle();
    expect(find.text('Link your Jellyfin account'), findsOneWidget);
    await tester.enterText(
        find.widgetWithText(TextField, 'Password'), 'correct-horse-battery');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Link account'));
    await tester.pumpAndSettle();

    final post = adapter.requests.singleWhere((r) => r.method == 'POST');
    expect(
        post.body, {'username': 'alice', 'password': 'correct-horse-battery'});
    expect(find.text('Account linked. Sign in with your usual password.'),
        findsOneWidget);
    // The card re-read the server: the linked account is shown and the
    // hint is gone with it.
    expect(find.text('Username'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Sign in to link it'),
        findsNothing);
  });

  testWidgets('an existing account closes the sheet and refreshes',
      (tester) async {
    var polls = 0;
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              // Absent on the first read, present once re-read after the
              // refusal, as if another device had just created it.
              _server(account: polls++ == 0 ? null : _account()),
            ]),
        'POST /api/media-servers/jf-a/account': (_, __) => const _Reply(409, {
              'error': 'you already have an account on this server',
              'code': 'account_exists',
            }),
      },
    );

    await _openSheet(tester);
    await _submitPassword(tester, password: 'correct-horse');

    expect(find.text('Create your Jellyfin account'), findsNothing);
    expect(find.text('You already have an account here.'), findsOneWidget);
    expect(find.text('Username'), findsOneWidget);
    expect(find.text('alice'), findsOneWidget);
  });

  testWidgets('other refusals are said in requester words', (tester) async {
    // A text/plain JSON body (Go's http.Error) must decode like any other.
    final invalidName = _Reply(
      400,
      '${jsonEncode({
            'error': "your Cantinarr username can't be used as a name on "
                'this server; ask your admin to link an account for you',
            'code': 'invalid_name',
          })}\n',
      contentType: 'text/plain; charset=utf-8',
    );
    const notAvailable =
        _Reply(403, {'error': 'that server is not available to you'});
    const upstream = _Reply(502,
        {'error': "couldn't create the account right now; try again later"});
    const offline = _Reply(0, '');
    final expectations = <_Reply, String>{
      invalidName: "Home Jellyfin doesn't accept your username as an account "
          'name. Ask your admin to link an account for you.',
      notAvailable: 'That server is not available to you.',
      upstream: "Couldn't create the account. Try again in a moment, or ask "
          'your admin.',
      offline: "Couldn't reach the server. Check your connection and try "
          'again.',
    };

    for (final entry in expectations.entries) {
      await _pumpGuide(tester, handlers: _createFlow(entry.key));
      await _openSheet(tester);
      await _submitPassword(tester, password: 'correct-horse');
      expect(find.text(entry.value), findsOneWidget,
          reason: 'status ${entry.key.status}');
      expect(find.text('Create your Jellyfin account'), findsOneWidget);
      await _unmount(tester);
    }
  });

  testWidgets('a turned-off account says so and offers nothing to create',
      (tester) async {
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: _account(disabled: true)),
            ]),
      },
    );

    expect(
      find.text('Your access to Home Jellyfin is turned off. Ask your admin '
          "if you think that's a mistake."),
      findsOneWidget,
    );
    expect(
        find.widgetWithText(ElevatedButton, 'Create my account'), findsNothing);
    expect(find.text('Username'), findsNothing);
  });

  testWidgets('an unconfirmed account is said to be unconfirmed',
      (tester) async {
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: _account(verified: false)),
            ]),
      },
    );

    expect(find.text('alice'), findsOneWidget);
    expect(
      find.text(
          "We couldn't confirm your server access just now. Try again or check with your admin."),
      findsOneWidget,
    );
  });

  testWidgets('a missing sign-in address says to ask the admin',
      (tester) async {
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: _account(), publicAddress: ''),
            ]),
      },
    );

    expect(
      find.text("Your admin hasn't shared the sign-in address yet. Ask them "
          'where to sign in.'),
      findsOneWidget,
    );
    expect(find.widgetWithText(TextButton, 'Open'), findsNothing);
    expect(find.textContaining('Sign in at'), findsNothing);
  });

  testWidgets('nothing shared reads differently for requesters and admins',
      (tester) async {
    final empty = {
      'GET /api/media-servers': (_, __) => const _Reply(200, <Object>[]),
    };

    await _pumpGuide(tester, handlers: empty, instances: const []);
    expect(
      find.text('No media server is shared with you yet. Ask your admin for '
          'access.'),
      findsOneWidget,
    );
    expect(find.text('Watch on your media server'), findsOneWidget);

    await _unmount(tester);
    await _pumpGuide(tester,
        handlers: empty, user: _admin, instances: const []);
    expect(
      find.text('No media server is shared with your account yet. Open the '
          'instance under Settings and add yourself under User Access.'),
      findsOneWidget,
    );
  });

  testWidgets('a failed load offers Retry', (tester) async {
    final adapter = await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, calls) => calls == 0
            ? const _Reply(503, {'error': 'temporarily unavailable'})
            : _Reply(200, [_server()]),
      },
    );

    expect(find.text("Couldn't load your media servers."), findsOneWidget);
    // The granted set from the config still names the title meanwhile.
    expect(find.text('Watch on Jellyfin'), findsOneWidget);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Retry'));
    await tester.pumpAndSettle();

    expect(adapter.calls('GET', '/api/media-servers'), 2);
    expect(find.text("Couldn't load your media servers."), findsNothing);
    expect(find.widgetWithText(ElevatedButton, 'Create my account'),
        findsOneWidget);
  });

  testWidgets('the password never reaches the safe HTTP log', (tester) async {
    final logs = <String>[];
    await _pumpGuide(
      tester,
      logs: logs,
      handlers: _createFlow(const _Reply(201, {
        'username': 'alice',
        'public_address': 'https://jf.example.com',
      })),
    );

    await _openSheet(tester);
    await _submitPassword(tester, password: 'correct-horse-battery');

    final output = logs.join('\n');
    expect(output, contains('POST /api/media-servers/…'));
    expect(output, isNot(contains('correct-horse-battery')));
    expect(output, isNot(contains('jf-a')));
    expect(output, isNot(contains('password')));
  });

  testWidgets('the app theme renders the guide without overflow',
      (tester) async {
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final adapter = _JsonAdapter({
      'GET /api/media-servers': (_, __) => _Reply(200, [
            _server(account: _account(verified: false)),
          ]),
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(user: _alice)),
          backendClientProvider.overrideWithValue(dio),
        ],
        child:
            MaterialApp(theme: AppTheme.dark, home: const MediaAccessGuide()),
      ),
    );
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.text('Sign in at https://jf.example.com'), findsOneWidget);
  });

  testWidgets(
      'a Plex server asks for an email, posts it once, and shows the pending '
      'invite', (tester) async {
    var requested = false;
    final adapter = await _pumpGuide(
      tester,
      instances: const [_plex],
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _plexServer(account: requested ? _share() : null),
            ]),
        'POST /api/media-servers/px-a/account': (_, __) {
          requested = true;
          return const _Reply(201, {
            'username': 'alice@example.com',
            'public_address': 'https://app.plex.tv',
            'pending': true,
          });
        },
      },
    );

    expect(find.text('Watch on Plex'), findsOneWidget);
    expect(find.text('Your invite'), findsOneWidget);
    expect(
      find.textContaining('Sign in with Plex to link your account, or share '
          'the email of your Plex account.'),
      findsOneWidget,
    );
    expect(find.widgetWithText(ElevatedButton, 'Sign in with Plex'),
        findsOneWidget);
    expect(find.textContaining('Install the Plex app'), findsOneWidget);
    expect(find.text('Download Plex apps'), findsOneWidget);
    expect(find.textContaining('Remote Watch Pass'), findsOneWidget);
    expect(find.textContaining('open it and accept'), findsOneWidget);
    expect(find.textContaining('it verifies a matching movie or show'),
        findsOneWidget);
    // Nothing about passwords or a sign-in address to type on a Plex-only set.
    expect(find.textContaining('password you chose'), findsNothing);

    await tester.tap(find.widgetWithText(TextButton, 'Share my Plex email'));
    await tester.pumpAndSettle();
    expect(find.text('Your Plex email'), findsOneWidget);

    await tester.enterText(find.widgetWithText(TextField, 'Email'), 'nope');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Send my invite'));
    await tester.pumpAndSettle();
    expect(find.text('Enter a valid email address.'), findsOneWidget);
    expect(adapter.calls('POST', '/api/media-servers/px-a/account'), 0);

    await tester.enterText(
        find.widgetWithText(TextField, 'Email'), ' Alice@Example.com ');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Send my invite'));
    await tester.pumpAndSettle();
    final post = adapter.requests.singleWhere((r) => r.method == 'POST');
    expect(post.body, {'email': 'Alice@Example.com'});
    expect(find.text('Invite sent. Check your email, then accept it.'),
        findsOneWidget);
    expect(find.textContaining('Invite sent to alice@example.com'),
        findsOneWidget);
    expect(
        find.widgetWithText(TextButton, 'Share my Plex email'), findsNothing);
    expect(find.widgetWithText(TextButton, 'Wrong email?'), findsOneWidget);
  });

  testWidgets('an accepted Plex share shows where to sign in', (tester) async {
    await _pumpGuide(
      tester,
      instances: const [_plex],
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _plexServer(account: _share(pending: false, verified: false)),
            ]),
      },
    );
    expect(find.text('Cantina Plex is shared with alice@example.com'),
        findsOneWidget);
    expect(find.text('Sign in at https://app.plex.tv'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Open'), findsOneWidget);
    expect(find.textContaining("couldn't confirm your server access"),
        findsOneWidget);
  });

  testWidgets('a Plex server the user is not granted offers to ask for access',
      (tester) async {
    await _pumpGuide(
      tester,
      instances: const [],
      plexAccessRequestable: true,
      handlers: {
        'GET /api/media-servers': (_, __) => const _Reply(200, []),
      },
    );
    expect(find.text('Watch on Plex'), findsOneWidget);
    expect(find.textContaining('No media server is shared with you'),
        findsNothing);
    expect(
      find.textContaining('Sign in with Plex to ask for access, or share the '
          'email of your Plex account'),
      findsOneWidget,
    );
    expect(find.widgetWithText(ElevatedButton, 'Sign in with Plex'),
        findsOneWidget);

    await tester.tap(find.widgetWithText(TextButton, 'Share my Plex email'));
    await tester.pumpAndSettle();
    await tester.enterText(
        find.widgetWithText(TextField, 'Email'), 'alice@example.com');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Send'));
    await tester.pumpAndSettle();

    expect(_lastAuth!.sharedEmails, ['alice@example.com']);
    expect(find.text('Thanks! Your admin has been notified.'), findsOneWidget);
    expect(find.text('alice@example.com'), findsOneWidget);
    expect(find.textContaining('Your admin has been notified. Once they grant'),
        findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Change email'), findsOneWidget);
    expect(
        find.widgetWithText(TextButton, 'Sign in with Plex'), findsOneWidget);
    // The delayed re-read fires and finds nothing new; no pending timers.
    await tester.pump(const Duration(seconds: 4));
    await tester.pumpAndSettle();
  });

  testWidgets('a mixed Plex and Jellyfin set keeps both flows apart',
      (tester) async {
    await _pumpGuide(
      tester,
      instances: const [_jellyfin, _plex],
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: _account()),
              _plexServer(),
            ]),
      },
    );
    expect(find.text('Watch on Plex or Jellyfin'), findsOneWidget);
    expect(find.text('Your accounts'), findsOneWidget);
    expect(find.textContaining('Install the Plex app'), findsOneWidget);
    expect(
        find.widgetWithText(TextButton, 'Share my Plex email'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'I already have an account'),
        findsNothing);
    expect(find.textContaining('open it and accept'), findsOneWidget);
    expect(find.text('Sign in at https://jf.example.com'), findsOneWidget);
  });

  testWidgets('an existing account links from the guide with its password',
      (tester) async {
    var linked = false;
    final logs = <String>[];
    final adapter = await _pumpGuide(
      tester,
      logs: logs,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: linked ? _account(username: 'Alice') : null),
            ]),
        'POST /api/media-servers/jf-a/account/link': (body, __) {
          if (body['password'] == 'not-it') {
            return const _Reply(400, {
              'error': 'wrong username or password for this server',
              'code': 'bad_credentials',
            });
          }
          linked = true;
          return const _Reply(201, {
            'username': 'Alice',
            'public_address': 'https://jf.example.com',
            'administrator': false,
          });
        },
      },
    );

    await tester
        .tap(find.widgetWithText(TextButton, 'I already have an account'));
    await tester.pumpAndSettle();
    expect(find.text('Link your Jellyfin account'), findsOneWidget);
    expect(
      find.textContaining('Cantinarr checks them with Home Jellyfin once'),
      findsOneWidget,
    );
    // The username is prefilled with the Cantinarr one.
    expect(
      tester
          .widget<TextField>(find.widgetWithText(TextField, 'Username'))
          .controller!
          .text,
      'alice',
    );

    // Nothing is sent without a password.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Link account'));
    await tester.pumpAndSettle();
    expect(find.text('Enter your password.'), findsOneWidget);
    expect(adapter.calls('POST', '/api/media-servers/jf-a/account/link'), 0);

    // A refused password says so and keeps the sheet (and the session).
    await tester.enterText(
        find.widgetWithText(TextField, 'Password'), 'not-it');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Link account'));
    await tester.pumpAndSettle();
    expect(find.text('Wrong username or password for Home Jellyfin.'),
        findsOneWidget);
    expect(find.text('Link your Jellyfin account'), findsOneWidget);

    await tester.enterText(
        find.widgetWithText(TextField, 'Username'), ' Alice ');
    await tester.enterText(
        find.widgetWithText(TextField, 'Password'), 'correct-horse-battery');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Link account'));
    await tester.pumpAndSettle();
    final posts = adapter.requests.where((r) => r.method == 'POST').toList();
    expect(posts.length, 2);
    expect(posts.last.body,
        {'username': 'Alice', 'password': 'correct-horse-battery'});
    expect(find.text('Link your Jellyfin account'), findsNothing);
    expect(find.text('Account linked. Sign in with your usual password.'),
        findsOneWidget);

    // The card re-read the server: the linked account is shown.
    expect(find.text('Alice'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'I already have an account'),
        findsNothing);
    expect(find.text('Administrator account. Cantinarr never changes it.'),
        findsNothing);

    final output = logs.join('\n');
    expect(output, contains('POST /api/media-servers/…'));
    expect(output, isNot(contains('correct-horse-battery')));
    expect(output, isNot(contains('not-it')));
  });

  testWidgets('a claimed account and a refused one say what to do next',
      (tester) async {
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [_server()]),
        'POST /api/media-servers/jf-a/account/link': (_, callsSoFar) =>
            callsSoFar == 0
                ? const _Reply(409, {
                    'error': 'that account is already linked to another user',
                    'code': 'remote_already_linked',
                  })
                : const _Reply(400, {
                    'error': "that account can't sign in right now",
                    'code': 'account_refused',
                  }),
      },
    );
    await tester
        .tap(find.widgetWithText(TextButton, 'I already have an account'));
    await tester.pumpAndSettle();
    await tester.enterText(find.widgetWithText(TextField, 'Password'), 'pw');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Link account'));
    await tester.pumpAndSettle();
    expect(
      find.text('That account is already linked to another Cantinarr user. '
          'Ask your admin.'),
      findsOneWidget,
    );
    await tester.tap(find.widgetWithText(ElevatedButton, 'Link account'));
    await tester.pumpAndSettle();
    expect(
      find.text("Home Jellyfin won't let that account sign in right now. It "
          'may be turned off. Ask your admin.'),
      findsOneWidget,
    );
  });

  testWidgets('an administrator account says Cantinarr never changes it',
      (tester) async {
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: {
                ..._account(username: 'julian'),
                'administrator': true,
              }),
            ]),
      },
    );
    expect(find.text('julian'), findsOneWidget);
    expect(find.text('Administrator account. Cantinarr never changes it.'),
        findsOneWidget);
    expect(find.text('Sign in at https://jf.example.com'), findsOneWidget);
  });

  testWidgets('signing in with Plex polls the pin and says what it led to',
      (tester) async {
    var linked = false;
    final adapter = await _pumpGuide(
      tester,
      instances: const [_plex],
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _plexServer(account: linked ? _share() : null),
            ]),
        'POST /api/media-servers/plex/sign-in/begin': (_, __) =>
            const _Reply(200, {
              'pin_id': 42,
              'code': 'ABCD',
              'url': 'https://app.plex.tv/auth#?code=ABCD',
            }),
        'POST /api/media-servers/plex/sign-in/check': (_, callsSoFar) {
          if (callsSoFar == 0) return const _Reply(200, {'linked': false});
          linked = true;
          return const _Reply(200, {
            'linked': true,
            'username': 'alice',
            'email': 'alice@example.com',
            'invite_state': 'sent',
          });
        },
      },
    );

    // The sheet shows a spinner, so settle by hand: pumpAndSettle would run
    // the clock until the poll itself finished the flow.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Sign in with Plex'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.text('Waiting for approval.'), findsOneWidget);
    expect(adapter.calls('POST', '/api/media-servers/plex/sign-in/begin'), 1);
    expect(adapter.calls('POST', '/api/media-servers/plex/sign-in/check'), 0);

    // The first poll finds the pin unapproved; the sheet stays.
    await tester.pump(const Duration(seconds: 3));
    await tester.pump();
    expect(adapter.calls('POST', '/api/media-servers/plex/sign-in/check'), 1);
    expect(adapter.requests.last.body, {'pin_id': 42});
    expect(find.text('Waiting for approval.'), findsOneWidget);

    await tester
        .tap(find.widgetWithText(OutlinedButton, "I've approved, check now"));
    await tester.pumpAndSettle();
    expect(find.text('Waiting for approval.'), findsNothing);
    expect(find.text('Signed in as alice. Invite sent. Check your email.'),
        findsOneWidget);
    expect(find.textContaining('Invite sent to alice@example.com'),
        findsOneWidget);
    expect(adapter.calls('POST', '/api/media-servers/plex/sign-in/check'), 2);
  });

  testWidgets('a Plex sign-in with an account someone else holds says so',
      (tester) async {
    await _pumpGuide(
      tester,
      instances: const [_plex],
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [_plexServer()]),
        'POST /api/media-servers/plex/sign-in/begin': (_, __) => const _Reply(
                200, {
              'pin_id': 7,
              'code': 'WXYZ',
              'url': 'https://app.plex.tv/auth#?code=WXYZ'
            }),
        'POST /api/media-servers/plex/sign-in/check': (_, __) =>
            const _Reply(200, {
              'linked': true,
              'username': 'rey',
              'email': 'rey@example.com',
              'invite_state': 'claimed',
            }),
      },
    );
    await tester.tap(find.widgetWithText(ElevatedButton, 'Sign in with Plex'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 600));
    await tester
        .tap(find.widgetWithText(OutlinedButton, "I've approved, check now"));
    await tester.pumpAndSettle();
    expect(
      find.text('Signed in as rey, but that Plex account is already linked '
          'to another Cantinarr user here. Ask your admin.'),
      findsOneWidget,
    );
  });

  testWidgets('a server without the Plex sign-in route says to update it',
      (tester) async {
    await _pumpGuide(
      tester,
      instances: const [_plex],
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [_plexServer()]),
        'POST /api/media-servers/plex/sign-in/begin': (_, __) =>
            const _Reply(404, {'error': 'not found'}),
      },
    );
    await tester.tap(find.widgetWithText(ElevatedButton, 'Sign in with Plex'));
    await tester.pumpAndSettle();
    expect(
      find.text("This server doesn't support signing in with Plex yet. Ask "
          'your admin to update it.'),
      findsOneWidget,
    );
    expect(find.widgetWithText(OutlinedButton, 'Start again'), findsOneWidget);
  });

  testWidgets('the Plex owner reads as the owner', (tester) async {
    await _pumpGuide(
      tester,
      instances: const [_plex],
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _plexServer(account: {
                ..._share(username: 'cantina-owner', pending: false),
                'administrator': true,
              }),
            ]),
      },
    );
    expect(find.text('You own Cantina Plex.'), findsOneWidget);
    expect(find.text('Signed in as cantina-owner'), findsOneWidget);
    expect(find.text('Sign in at https://app.plex.tv'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Wrong email?'), findsNothing);
  });
}
