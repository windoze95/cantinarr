import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/instance_edit_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// Fake Dio adapter for the Jellyfin editor: serves the instance list and
/// grant rows, answers the connection test and the live library probe, and
/// records every request (method, path, decoded body) for assertions.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter({
    this.instances = const [],
    this.grants = const [],
    // Names differ from their kind labels so a finder for the tile title
    // never also matches the subtitle.
    this.libraries = const [
      {'id': 'lib-movies', 'name': 'Films', 'collection_type': 'movies'},
      {'id': 'lib-shows', 'name': 'Series', 'collection_type': 'tvshows'},
    ],
    this.librariesError,
    this.plexBeginStatus = 0,
  });

  /// When set, the Plex link begin answers this status with a JSON error
  /// body (404: a server from before Plex instances, whose arr proxy
  /// wildcard answers; 502: the server could not reach plex.tv).
  final int plexBeginStatus;

  final List<Map<String, dynamic>> instances;
  final List<Map<String, dynamic>> grants;
  final List<Map<String, dynamic>> libraries;

  /// The library probe answers 502 with this message when set.
  final String? librariesError;
  final List<({String method, String path, dynamic body})> requests = [];

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
    requests.add((method: options.method, path: path, body: body));

    dynamic response = <String, dynamic>{};
    if (options.method == 'GET' && path == '/api/instances/media-roots') {
      response = ['/media'];
    } else if (options.method == 'GET' && path == '/api/instances') {
      response = instances;
    } else if (path.endsWith('/grant-users')) {
      response = grants;
    } else if (options.method == 'GET' && path.endsWith('/users')) {
      response = const [];
    } else if (options.method == 'POST' && path == '/api/instances/test') {
      return ResponseBody.fromString('', 204, headers: {});
    } else if (options.method == 'POST' &&
        path == '/api/instances/plex/link/begin') {
      if (plexBeginStatus != 0) {
        return ResponseBody.fromString(
          '${jsonEncode({'error': 'not found'})}\n',
          plexBeginStatus,
          headers: {
            'content-type': ['text/plain; charset=utf-8'],
          },
        );
      }
      response = {'pin_id': 42, 'code': 'ABCD', 'url': 'https://app.plex.tv/auth#?code=ABCD'};
    } else if (options.method == 'POST' &&
        path == '/api/instances/plex/link/check') {
      response = {'linked': true, 'account': 'cantina-owner'};
    } else if (options.method == 'POST' && path == '/api/instances/plex/servers') {
      response = {
        'servers': [
          {'name': 'Cantina', 'machine_identifier': 'm1'},
          {'name': 'Den', 'machine_identifier': 'm2'},
        ],
      };
    } else if (options.method == 'POST' &&
        path == '/api/instances/media-server/libraries') {
      final error = librariesError;
      if (error != null) {
        // Mirrors Go's http.Error: JSON-shaped body, text/plain content type.
        return ResponseBody.fromString(
          '${jsonEncode({'error': error})}\n',
          502,
          headers: {
            'content-type': ['text/plain; charset=utf-8'],
          },
        );
      }
      response = {
        'server_name': 'Home Jellyfin',
        'version': '10.10.7',
        'libraries': libraries,
      };
    } else if (options.method == 'POST' && path == '/api/instances') {
      final map = body as Map<String, dynamic>;
      response = {...map, 'id': '${map['service_type']}-new'};
    } else if (options.method == 'GET' && path.endsWith('/webhook')) {
      return ResponseBody.fromString(
        '404 page not found\n',
        404,
        headers: {
          'content-type': ['text/plain; charset=utf-8'],
        },
      );
    } else if (options.method == 'PUT') {
      final id = path.split('/').last;
      response = {
        ...body as Map<String, dynamic>,
        'id': id,
        'service_type': 'jellyfin',
      };
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

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this.users);

  final List<UserSummary> users;

  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(id: 1, username: 'admin', role: 'admin'),
      );

  // A fresh, growable copy: the screen sorts what it is handed.
  @override
  Future<List<UserSummary>> listUsers() async => List.of(users);

  @override
  Future<void> refreshConfig() async {}
}

UserSummary _user(int id, String username) => UserSummary(
      id: id,
      username: username,
      role: 'user',
      permissions: const [],
      createdAt: '',
      deviceCount: 0,
      hasPassword: false,
      passwordEnabled: false,
      passkeyEnabled: false,
      hasPendingInvite: false,
    );

const _homeJellyfin = {
  'id': 'jf-a',
  'service_type': 'jellyfin',
  'name': 'Home Jellyfin',
  'url': 'http://jellyfin:8096',
  'is_default': false,
  'sort_order': 0,
  'media_server_config': {
    'public_address': 'https://jf.example.com',
    'library_ids': ['lib-movies', 'lib-gone'],
  },
};

Future<void> _pumpEdit(
  WidgetTester tester, {
  required _FakeAdapter adapter,
  List<UserSummary> users = const [],
  InstanceEditScreen screen = const InstanceEditScreen(),
}) async {
  // Tall viewport so the whole (lazily built) form list is materialized.
  tester.view.physicalSize = const Size(800, 2200);
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(path: '/', builder: (_, __) => const Scaffold(body: SizedBox())),
      GoRoute(path: '/edit', builder: (_, __) => screen),
    ],
  );
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(users)),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  router.push('/edit');
  await tester.pumpAndSettle();
}

/// Opens a new-instance form already on the Jellyfin type.
Future<void> _pumpNewJellyfin(
  WidgetTester tester, {
  required _FakeAdapter adapter,
  List<UserSummary> users = const [],
}) async {
  await _pumpEdit(
    tester,
    adapter: adapter,
    users: users,
    screen: const InstanceEditScreen(initialServiceType: 'jellyfin'),
  );
}

Future<void> _fillForm(WidgetTester tester) async {
  await tester.enterText(find.widgetWithText(TextField, 'Name'), 'Home');
  await tester.enterText(
      find.widgetWithText(TextField, 'URL'), 'http://jellyfin:8096');
  await tester.enterText(find.widgetWithText(TextField, 'API Key'), 'key');
}

Future<void> _testConnection(WidgetTester tester) async {
  final test = find.widgetWithText(OutlinedButton, 'Test Connection');
  await tester.ensureVisible(test);
  await tester.tap(test);
  await tester.pumpAndSettle();
}

Future<void> _tapSave(WidgetTester tester, String label) async {
  final save = find.widgetWithText(ElevatedButton, label);
  await tester.ensureVisible(save);
  await tester.tap(save);
  await tester.pumpAndSettle();
}

Iterable<({String method, String path, dynamic body})> _libraryProbes(
        _FakeAdapter adapter) =>
    adapter.requests.where((r) =>
        r.method == 'POST' && r.path == '/api/instances/media-server/libraries');

void main() {
  testWidgets(
      'Jellyfin hides the default toggle, shows User Access, and never reads '
      'pins', (tester) async {
    final adapter = _FakeAdapter(
      instances: [Map.of(_homeJellyfin)],
      grants: [
        {'user_id': 1, 'instance_id': 'jf-a'},
      ],
    );
    await _pumpEdit(tester, adapter: adapter, users: [_user(1, 'alice')]);

    // Pick Jellyfin from the type dropdown, the way an admin would.
    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Jellyfin').last);
    await tester.pumpAndSettle();

    expect(
        find.widgetWithText(SwitchListTile, 'Default Instance'), findsNothing);
    expect(find.text('User Access'), findsOneWidget);
    expect(
      find.textContaining('turns their account off without deleting it'),
      findsOneWidget,
    );
    expect(find.widgetWithText(CheckboxListTile, 'alice'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Sign-in address (optional)'),
        findsOneWidget);
    expect(find.text('Shared libraries'), findsOneWidget);
    expect(
      find.text('Test the connection to load the libraries this server '
          'reports.'),
      findsOneWidget,
    );

    // The grant rows were read through the existing sibling; the per-user
    // pin endpoint (a default-instance concept) was never touched.
    expect(
      adapter.requests.any((r) =>
          r.method == 'GET' && r.path == '/api/instances/jf-a/grant-users'),
      isTrue,
    );
    expect(
      adapter.requests.any((r) => r.path.endsWith('/users')),
      isFalse,
    );
  });

  testWidgets('Emby gets the media-server form with its own hints',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: [_user(1, 'alice')],
      screen: const InstanceEditScreen(initialServiceType: 'emby'),
    );

    expect(
        find.widgetWithText(SwitchListTile, 'Default Instance'), findsNothing);
    expect(find.text('User Access'), findsOneWidget);
    expect(find.text('Shared libraries'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Sign-in address (optional)'),
        findsOneWidget);
    expect(find.text('http://emby:8096'), findsOneWidget);
    expect(find.text('e.g. Home Emby'), findsOneWidget);
    expect(find.text('Your Emby API key (Settings > Advanced > API Keys)'),
        findsOneWidget);
    expect(find.text('https://emby.example.com'), findsOneWidget);

    // The probe carries the type, so the server reads it through the Emby
    // client.
    await _fillForm(tester);
    await _testConnection(tester);
    expect(_libraryProbes(adapter).single.body['service_type'], 'emby');
    expect(find.widgetWithText(CheckboxListTile, 'Films'), findsOneWidget);
  });

  testWidgets(
      'libraries load only after a passing test and save sends the chosen '
      'ids with is_default false', (tester) async {
    final adapter = _FakeAdapter();
    await _pumpNewJellyfin(tester, adapter: adapter, users: [_user(1, 'alice')]);
    await _fillForm(tester);

    // Typing alone never dials the server.
    expect(_libraryProbes(adapter), isEmpty);
    expect(find.widgetWithText(CheckboxListTile, 'Films'), findsNothing);

    await _testConnection(tester);
    expect(find.text('Connection successful!'), findsOneWidget);
    final probe = _libraryProbes(adapter).single;
    expect(probe.body['service_type'], 'jellyfin');
    expect(probe.body['url'], 'http://jellyfin:8096');
    expect(probe.body['api_key'], 'key');
    expect(probe.body.containsKey('id'), isFalse);

    expect(find.widgetWithText(CheckboxListTile, 'Films'), findsOneWidget);
    expect(find.widgetWithText(CheckboxListTile, 'Series'), findsOneWidget);
    // The server's kind, humanized, under each name.
    expect(find.text('Movies'), findsOneWidget);
    expect(find.text('Shows'), findsOneWidget);
    expect(find.text('All'), findsOneWidget);

    await tester.tap(find.widgetWithText(CheckboxListTile, 'Films'));
    await tester.pumpAndSettle();
    expect(find.text('1 selected'), findsOneWidget);
    await tester.enterText(
      find.widgetWithText(TextField, 'Sign-in address (optional)'),
      'https://jf.example.com',
    );
    await tester.tap(find.widgetWithText(CheckboxListTile, 'alice'));
    await tester.pumpAndSettle();

    await _tapSave(tester, 'Add Instance');

    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['service_type'], 'jellyfin');
    expect(post.body['is_default'], isFalse);
    expect(post.body['media_server_config'], {
      'public_address': 'https://jf.example.com',
      'library_ids': ['lib-movies'],
    });
    final putGrants = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/instances/jellyfin-new/grant-users');
    expect(putGrants.body, {
      'user_ids': [1]
    });
  });

  testWidgets('a failed library read offers Retry and never blocks save',
      (tester) async {
    final adapter = _FakeAdapter(librariesError: 'connection test failed');
    await _pumpNewJellyfin(tester, adapter: adapter);
    await _fillForm(tester);
    await _testConnection(tester);

    expect(
      find.textContaining("Couldn't load the libraries this server reports"),
      findsOneWidget,
    );
    final retry = find.widgetWithText(TextButton, 'Retry');
    expect(retry, findsOneWidget);
    await tester.tap(retry);
    await tester.pumpAndSettle();
    expect(_libraryProbes(adapter), hasLength(2));

    await _tapSave(tester, 'Add Instance');
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    // Nothing chosen (nothing could be) means every library is shared.
    expect(post.body['media_server_config'], {
      'public_address': '',
      'library_ids': <String>[],
    });
  });

  testWidgets('a server with no libraries reads as absence, not failure',
      (tester) async {
    final adapter = _FakeAdapter(libraries: const []);
    await _pumpNewJellyfin(tester, adapter: adapter);
    await _fillForm(tester);
    await _testConnection(tester);

    expect(
      find.text('This server reports no libraries yet. Every library you '
          'add later will be shared.'),
      findsOneWidget,
    );
    expect(find.widgetWithText(TextButton, 'Retry'), findsNothing);
    expect(find.text('All'), findsOneWidget);
  });

  testWidgets(
      'edit hydrates the config through the stored key and flags an unknown '
      'stored id', (tester) async {
    final adapter = _FakeAdapter(instances: [Map.of(_homeJellyfin)]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      screen: const InstanceEditScreen(
        instanceId: 'jf-a',
        initialServiceType: 'jellyfin',
        initialName: 'Home Jellyfin',
        initialUrl: 'http://jellyfin:8096',
      ),
    );

    // The library read rides on the stored credential: id set, key blank.
    final probe = _libraryProbes(adapter).single;
    expect(probe.body['id'], 'jf-a');
    expect(probe.body['api_key'], '');

    final address = tester.widget<TextField>(
        find.widgetWithText(TextField, 'Sign-in address (optional)'));
    expect(address.controller!.text, 'https://jf.example.com');
    expect(
      tester
          .widget<CheckboxListTile>(
              find.widgetWithText(CheckboxListTile, 'Films'))
          .value,
      isTrue,
    );
    expect(
      tester
          .widget<CheckboxListTile>(
              find.widgetWithText(CheckboxListTile, 'Series'))
          .value,
      isFalse,
    );
    final unknown = find.widgetWithText(CheckboxListTile, 'Unknown library');
    expect(unknown, findsOneWidget);
    expect(tester.widget<CheckboxListTile>(unknown).value, isTrue);
    expect(
      find.text('No longer reported by the server. Uncheck to drop it.'),
      findsOneWidget,
    );
    expect(find.text('2 selected'), findsOneWidget);
  });

  testWidgets('an unrelated edit omits the media server config',
      (tester) async {
    final adapter = _FakeAdapter(instances: [Map.of(_homeJellyfin)]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      screen: const InstanceEditScreen(
        instanceId: 'jf-a',
        initialServiceType: 'jellyfin',
        initialName: 'Home Jellyfin',
        initialUrl: 'http://jellyfin:8096',
      ),
    );

    await tester.enterText(
        find.widgetWithText(TextField, 'Name'), 'Renamed Jellyfin');
    await _tapSave(tester, 'Save Changes');

    final update = adapter.requests.singleWhere(
        (r) => r.method == 'PUT' && r.path == '/api/instances/jf-a');
    expect(update.body['name'], 'Renamed Jellyfin');
    expect(update.body.containsKey('media_server_config'), isFalse);
  });

  testWidgets('dropping an unknown stored id sends the remaining choice',
      (tester) async {
    final adapter = _FakeAdapter(instances: [Map.of(_homeJellyfin)]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      screen: const InstanceEditScreen(
        instanceId: 'jf-a',
        initialServiceType: 'jellyfin',
        initialName: 'Home Jellyfin',
        initialUrl: 'http://jellyfin:8096',
      ),
    );

    await tester.tap(find.widgetWithText(CheckboxListTile, 'Unknown library'));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(CheckboxListTile, 'Unknown library'),
        findsNothing);
    expect(find.text('1 selected'), findsOneWidget);

    await _tapSave(tester, 'Save Changes');
    final update = adapter.requests.singleWhere(
        (r) => r.method == 'PUT' && r.path == '/api/instances/jf-a');
    expect(update.body['media_server_config'], {
      'public_address': 'https://jf.example.com',
      'library_ids': ['lib-movies'],
    });
  });

  testWidgets('the sign-in address must be an http(s) URL', (tester) async {
    final adapter = _FakeAdapter();
    await _pumpNewJellyfin(tester, adapter: adapter);
    await _fillForm(tester);
    await tester.enterText(
      find.widgetWithText(TextField, 'Sign-in address (optional)'),
      'jf.example.com',
    );

    await _tapSave(tester, 'Add Instance');

    expect(find.text('Sign-in address must start with http:// or https://'),
        findsOneWidget);
    expect(
      adapter.requests
          .where((r) => r.method == 'POST' && r.path == '/api/instances'),
      isEmpty,
    );
  });

  testWidgets('a server without Plex linking says to update it, and one that '
      'cannot reach plex.tv says that', (tester) async {
    for (final (status, message) in [
      (
        404,
        "This server doesn't have Plex linking yet. Update the Cantinarr "
            'server, then try again.',
      ),
      (
        502,
        "Your server couldn't reach plex.tv. Check its internet access and "
            'try again.',
      ),
    ]) {
      final adapter = _FakeAdapter(plexBeginStatus: status);
      await _pumpEdit(
        tester,
        adapter: adapter,
        screen: const InstanceEditScreen(initialServiceType: 'plex'),
      );
      await tester
          .tap(find.widgetWithText(OutlinedButton, 'Link Plex account'));
      await tester.pumpAndSettle();
      expect(find.text(message), findsOneWidget, reason: '$status');
      expect(find.textContaining('Waiting for approval'), findsNothing);
      await tester.pumpWidget(const SizedBox());
      await tester.pumpAndSettle();
    }
  });

  testWidgets(
      'Plex links an account by PIN, picks a server, and saves with the pin '
      'and no URL or key', (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(
      tester,
      adapter: adapter,
      screen: const InstanceEditScreen(initialServiceType: 'plex'),
    );

    // No URL, no API key: the credential is the link.
    expect(find.widgetWithText(TextField, 'URL'), findsNothing);
    expect(find.widgetWithText(TextField, 'API Key'), findsNothing);
    expect(find.text('Plex account'), findsOneWidget);
    expect(find.text('Not linked'), findsOneWidget);
    expect(
        find.widgetWithText(SwitchListTile, 'Default Instance'), findsNothing);
    expect(find.text('User Access'), findsOneWidget);
    expect(find.textContaining('recognised as the owner, never invited'),
        findsOneWidget);
    // The sign-in address is prefilled with where everyone signs in to Plex.
    expect(find.text('https://app.plex.tv'), findsOneWidget);

    // Saving before linking is refused locally.
    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'Cantina');
    await _tapSave(tester, 'Add Instance');
    expect(find.text('Link a Plex account first'), findsOneWidget);
    expect(
      adapter.requests
          .where((r) => r.method == 'POST' && r.path == '/api/instances'),
      isEmpty,
    );

    final link = find.widgetWithText(OutlinedButton, 'Link Plex account');
    await tester.ensureVisible(link);
    await tester.tap(link);
    await tester.pumpAndSettle();
    expect(adapter.requests.any((r) => r.path == '/api/instances/plex/link/begin'),
        isTrue);
    // The form polls every few seconds; the fake approves on the first
    // check, so the poll may already have landed. Either way the "check
    // now" button gets there.
    final check = find.widgetWithText(OutlinedButton, "I've approved, check now");
    if (check.evaluate().isNotEmpty) {
      expect(find.textContaining('Waiting for approval'), findsOneWidget);
      await tester.ensureVisible(check);
      await tester.tap(check);
      await tester.pumpAndSettle();
    }
    expect(find.text('Linked'), findsOneWidget);
    expect(find.textContaining('Linked as cantina-owner'), findsOneWidget);
    expect(find.text('Server to share'), findsOneWidget);
    expect(find.widgetWithText(ListTile, 'Cantina'), findsOneWidget);
    expect(find.widgetWithText(ListTile, 'Den'), findsOneWidget);
    final serversCall = adapter.requests
        .singleWhere((r) => r.path == '/api/instances/plex/servers');
    expect(serversCall.body['plex_link_pin'], 42);
    expect(serversCall.body['api_key'], '');

    // Two servers: nothing is picked for the admin; picking one reads its
    // libraries through the pin and the machine identifier.
    expect(_libraryProbes(adapter), isEmpty);
    await tester.tap(find.text('Den'));
    await tester.pumpAndSettle();
    final probe = _libraryProbes(adapter).single;
    expect(probe.body['plex_link_pin'], 42);
    expect(probe.body['media_server_config'], {'machine_identifier': 'm2'});
    expect(find.text('Films'), findsOneWidget);

    final auto = find.widgetWithText(SwitchListTile, 'Auto-approve access requests');
    await tester.ensureVisible(auto);
    await tester.tap(auto);
    await tester.pumpAndSettle();

    await _tapSave(tester, 'Add Instance');
    final create = adapter.requests.singleWhere(
        (r) => r.method == 'POST' && r.path == '/api/instances');
    expect(create.body['service_type'], 'plex');
    expect(create.body['plex_link_pin'], 42);
    expect(create.body['api_key'], '');
    expect(create.body['url'], '');
    expect(create.body['is_default'], isFalse);
    expect(create.body['media_server_config'], {
      'public_address': 'https://app.plex.tv',
      'library_ids': <String>[],
      'machine_identifier': 'm2',
      'auto_approve': true,
    });
  });
}
