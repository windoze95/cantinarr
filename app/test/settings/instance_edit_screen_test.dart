import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/unsaved_changes_guard.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/trending_books_service.dart';
import 'package:cantinarr/features/settings/ui/instance_edit_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// Fake Dio adapter: serves the instance list, per-type user pins, and
/// per-type access grants, and records every request (method, path, decoded
/// body) for assertions.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter({
    this.instances = const [],
    this.pins = const [],
    this.grants = const [],
    this.mediaRoots = const ['/media'],
    this.arrRootFolders = const [],
    this.instancesError,
    this.webhookError,
    this.webhookStatus,
    this.hardcoverStatus,
    this.hardcoverOAuth = false,
    this.hardcoverErrors = const {},
    this.beforeHardcoverSave,
    this.testError,
  });

  final List<Map<String, dynamic>> instances;
  final List<Map<String, dynamic>> pins;
  final List<Map<String, dynamic>> grants;
  final List<String> mediaRoots;
  final List<String> arrRootFolders;

  /// GET /api/instances answers 500 with this message when set — mimics the
  /// backend being down at screen mount.
  String? instancesError;
  final String? webhookError;

  /// GET /webhook status body; null mimics an older server (404), which the
  /// screen must treat as "unknown" and render nothing.
  final Map<String, dynamic>? webhookStatus;

  /// GET /hardcover status body; null mimics an older server (404), which
  /// hides the section entirely.
  final Map<String, dynamic>? hardcoverStatus;
  final Map<String, String> hardcoverErrors;
  final Future<void> Function(String)? beforeHardcoverSave;
  final String? testError;
  final bool hardcoverOAuth;
  String hardcoverDeviceStatus = 'connected';
  final Map<String, String> hardcoverLinks = {};
  final Map<String, int> hardcoverRevisions = {};
  final Map<String, String> hardcoverTokens = {};
  final Map<String, int> hardcoverFeedReads = {};
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
      response = mediaRoots;
    } else if (options.method == 'GET' && path.endsWith('/rootfolder')) {
      response = [
        for (var i = 0; i < arrRootFolders.length; i++)
          {'id': i + 1, 'path': arrRootFolders[i]},
      ];
    } else if (options.method == 'GET' && path == '/api/instances') {
      final error = instancesError;
      if (error != null) {
        // Mirrors Go's http.Error: JSON-shaped body, text/plain content type.
        return ResponseBody.fromString(
          '${jsonEncode({'error': error})}\n',
          500,
          headers: {
            'content-type': ['text/plain; charset=utf-8'],
          },
        );
      }
      response = instances;
    } else if (options.method == 'GET' && path.endsWith('/grant-users')) {
      response = grants;
    } else if (options.method == 'PUT' && path.endsWith('/grant-users')) {
      response = grants;
    } else if (options.method == 'GET' && path.endsWith('/users')) {
      response = pins;
    } else if (options.method == 'POST' && path == '/api/instances/test') {
      final error = testError;
      if (error != null) {
        // Mirrors Go's http.Error: JSON-shaped body, text/plain content type.
        return ResponseBody.fromString(
          '${jsonEncode({'error': error})}\n',
          400,
          headers: {
            'content-type': ['text/plain; charset=utf-8'],
          },
        );
      }
      return ResponseBody.fromString('', 204, headers: {});
    } else if (options.method == 'POST' && path == '/api/instances') {
      final map = body as Map<String, dynamic>;
      response = {...map, 'id': '${map['service_type']}-new'};
    } else if (options.method == 'PUT' && path.endsWith('/users')) {
      response = pins;
    } else if (options.method == 'GET' && path.endsWith('/webhook')) {
      final status = webhookStatus;
      if (status == null) {
        // Older server: the status route does not exist.
        return ResponseBody.fromString(
          '404 page not found\n',
          404,
          headers: {
            'content-type': ['text/plain; charset=utf-8'],
          },
        );
      }
      response = status;
    } else if (options.method == 'POST' && path.endsWith('/webhook')) {
      final error = webhookError;
      if (error != null) {
        // Mirrors Go's http.Error: the body is a JSON-shaped string but its
        // content type is text/plain, so Dio deliberately does not decode it.
        return ResponseBody.fromString(
          '${jsonEncode({'error': error})}\n',
          500,
          headers: {
            'content-type': ['text/plain; charset=utf-8'],
          },
        );
      }
      response = {'status': 'configured', 'action': 'created'};
    } else if (options.method == 'GET' && path.endsWith('/hardcover')) {
      final status = hardcoverStatus;
      if (status == null) {
        return ResponseBody.fromString(
          '404 page not found\n',
          404,
          headers: {
            'content-type': ['text/plain; charset=utf-8'],
          },
        );
      }
      final id = path.split('/')[3];
      response = hardcoverOAuth
          ? {
              ...status,
              'oauth_available': true,
              'configured': hardcoverLinks.containsKey(id) ||
                  hardcoverTokens.containsKey(id),
              'method': hardcoverLinks.containsKey(id)
                  ? 'oauth'
                  : hardcoverTokens.containsKey(id)
                      ? 'api_token'
                      : 'none',
              'connection_id': hardcoverLinks[id] ?? '',
              'revision': hardcoverRevisions[id] ?? 0,
            }
          : status;
    } else if (options.method == 'POST' && path.endsWith('/hardcover/apply')) {
      final targets =
          (body as Map<String, dynamic>)['instances'] as List<dynamic>;
      response = {
        'results': [
          for (final target in targets)
            {
              'instance_id': target['instance_id'],
              'applied': !hardcoverErrors.containsKey(target['instance_id']),
              'error': hardcoverErrors[target['instance_id']] ?? '',
            }
        ]
      };
      for (final target in targets) {
        final id = target['instance_id'] as String;
        if (!hardcoverErrors.containsKey(id)) {
          hardcoverLinks[id] = body['connection_id'] as String;
        }
      }
    } else if (path.contains('/hardcover/device/')) {
      final id = path.split('/')[3];
      final completed =
          options.method == 'GET' && hardcoverDeviceStatus == 'connected';
      if (completed) hardcoverLinks[id] = 'oauth-new';
      response = {
        'flow_id': 'local-flow',
        'status': completed
            ? 'connected'
            : options.method == 'DELETE'
                ? 'cancelled'
                : 'pending',
        'user_code': 'ABCD-EFGH',
        'verification_uri': 'https://hardcover.app/link',
        'expires_at': DateTime.now()
            .add(const Duration(minutes: 10))
            .toUtc()
            .toIso8601String(),
        'interval': 5,
        'connection_id': completed ? 'oauth-new' : '',
      };
    } else if (options.method == 'GET' &&
        path == '/api/discover/books/trending') {
      final id = options.queryParameters['instance_id'] as String;
      hardcoverFeedReads[id] = (hardcoverFeedReads[id] ?? 0) + 1;
      response = {
        'instance_id': id,
        'connected':
            hardcoverTokens.containsKey(id) || hardcoverLinks.containsKey(id),
        'books': <Map<String, dynamic>>[],
      };
    } else if (options.method == 'PUT' && path.endsWith('/hardcover')) {
      final id = path.split('/')[3];
      await beforeHardcoverSave?.call(id);
      final error = hardcoverErrors[id];
      if (error != null) {
        return ResponseBody.fromString(
          '${jsonEncode({'error': error})}\n',
          502,
          headers: {
            'content-type': ['text/plain; charset=utf-8'],
          },
        );
      }
      hardcoverTokens[id] = (body as Map<String, dynamic>)['token'] as String;
      response = {'supported': true, 'configured': true};
    } else if (options.method == 'DELETE' && path.endsWith('/hardcover')) {
      hardcoverTokens.remove(path.split('/')[3]);
      hardcoverLinks.remove(path.split('/')[3]);
      response = {'supported': true, 'configured': false};
    } else if (options.method == 'PUT') {
      // Instance update echo; the id encodes the service type (radarr-b).
      final id = path.split('/').last;
      response = {
        ...body as Map<String, dynamic>,
        'id': id,
        'service_type': id.split('-').first,
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
  _FakeAuthNotifier(this.users, {this.listUsersError});

  final List<UserSummary> users;

  /// listUsers throws this when set — mimics the backend being down at
  /// screen mount.
  final Object? listUsersError;

  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(id: 1, username: 'admin', role: 'admin'),
      );

  @override
  Future<List<UserSummary>> listUsers() async {
    final error = listUsersError;
    if (error != null) throw error;
    return users;
  }

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

const _mainRadarr = {
  'id': 'radarr-main',
  'service_type': 'radarr',
  'name': 'Main Radarr',
  'url': 'http://radarr-main',
  'is_default': true,
  'sort_order': 0,
};

const _radarrB = {
  'id': 'radarr-b',
  'service_type': 'radarr',
  'name': 'Radarr B',
  'url': 'http://radarr-b',
  'is_default': false,
  'sort_order': 1,
};

Future<void> _pumpEdit(
  WidgetTester tester, {
  required _FakeAdapter adapter,
  required List<UserSummary> users,
  Object? listUsersError,
  InstanceEditScreen screen = const InstanceEditScreen(),
  Size viewSize = const Size(800, 1800),
  double textScaleFactor = 1,
  bool directLink = false,
}) async {
  // Tall viewport so the whole (lazily built) form list is materialized.
  tester.view.physicalSize = viewSize;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  // Exercise both pushed editors and direct links with no previous page.
  final router = GoRouter(
    initialLocation: directLink ? '/edit' : '/',
    routes: [
      GoRoute(path: '/', builder: (_, __) => const Scaffold(body: SizedBox())),
      GoRoute(
        path: '/settings',
        builder: (_, __) => const Scaffold(body: Text('Settings home')),
      ),
      GoRoute(
        path: '/edit',
        builder: (_, __) => screen,
        onExit: (context, state) => ProviderScope.containerOf(context)
            .read(unsavedChangesProvider)
            .confirmExit(context, state),
      ),
    ],
  );
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(
            () => _FakeAuthNotifier(users, listUsersError: listUsersError)),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp.router(
        routerConfig: router,
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context).copyWith(
            textScaler: TextScaler.linear(textScaleFactor),
          ),
          child: child!,
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
  if (!directLink) router.push('/edit');
  await tester.pumpAndSettle();
}

Future<void> _fillForm(WidgetTester tester, String name) async {
  await tester.enterText(find.widgetWithText(TextField, 'Name'), name);
  await tester.enterText(
      find.widgetWithText(TextField, 'URL'), 'http://localhost:9999');
  await tester.enterText(find.widgetWithText(TextField, 'API Key'), 'key');
}

Map<String, dynamic> _chaptarr(String suffix) => {
      'id': 'chaptarr-$suffix',
      'service_type': 'chaptarr',
      'name': 'Books $suffix',
      'url': 'http://books-$suffix',
      'is_default': false,
      'sort_order': 0,
    };

const _chaptarrEditor = InstanceEditScreen(
  instanceId: 'chaptarr-a',
  initialServiceType: 'chaptarr',
  initialName: 'Books a',
  initialUrl: 'http://books-a',
);

Future<void> _saveHardcover(WidgetTester tester,
    {bool replacing = false}) async {
  await tester.enterText(
      find.widgetWithText(TextField, 'Hardcover API token'), 'hc-token');
  await tester.tap(
      find.text(replacing ? 'Replace Hardcover token' : 'Connect Hardcover'));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('saving a directly opened editor returns to Settings without a warning',
      (tester) async {
    await _pumpEdit(tester,
        adapter: _FakeAdapter(), users: const [], directLink: true);
    await _fillForm(tester, 'Movies');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();

    expect(find.text('Settings home'), findsOneWidget);
    expect(find.text('Discard unsaved changes?'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
      'loaded instances and automatic defaults are clean; reverted edits are clean again',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);
    bool dirty() => tester
        .widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard))
        .hasChanges();
    expect(dirty(), isFalse);
    final name = find.byWidgetPredicate(
        (w) => w is TextField && w.decoration?.labelText == 'Name');
    await tester.enterText(name, 'My library');
    expect(dirty(), isTrue);
    await tester.enterText(name, '');
    expect(dirty(), isFalse);
  });

  testWidgets(
      'backend down at mount shows a friendly error and no unhandled error '
      'leaks', (tester) async {
    // Both directory loads fail: the instance list 500s and the user list
    // throws. _loadDirectory used to await the two futures sequentially, so
    // when the first await threw, the second future's error had no listener
    // and escaped as an unhandled zone error — failing this test pre-fix.
    final adapter = _FakeAdapter(instancesError: 'connection refused');
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      listUsersError: StateError('backend down'),
    );

    expect(find.text('Could not load users'), findsOneWidget);
  });

  testWidgets('first instance of a type starts as the default', (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: [_user(1, 'alice')]);

    final toggle = tester.widget<SwitchListTile>(
        find.widgetWithText(SwitchListTile, 'Default Instance'));
    expect(toggle.value, isTrue);
  });

  testWidgets('creating a sibling starts non-default and shows the user-select',
      (tester) async {
    final adapter = _FakeAdapter(instances: [Map.of(_mainRadarr)]);
    await _pumpEdit(tester, adapter: adapter, users: [_user(1, 'alice')]);

    final toggle = tester.widget<SwitchListTile>(
        find.widgetWithText(SwitchListTile, 'Default Instance'));
    expect(toggle.value, isFalse);
    expect(find.text('User Access'), findsOneWidget);
    expect(find.widgetWithText(CheckboxListTile, 'alice'), findsOneWidget);
  });

  testWidgets(
      'taking over the default asks for confirmation naming both instances',
      (tester) async {
    final adapter = _FakeAdapter(instances: [Map.of(_mainRadarr)]);
    await _pumpEdit(tester, adapter: adapter, users: [_user(1, 'alice')]);

    await _fillForm(tester, 'Radarr B');
    await tester.tap(find.widgetWithText(SwitchListTile, 'Default Instance'));
    await tester.pumpAndSettle();

    // Cancelling the takeover aborts the save entirely.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();
    expect(find.text('Change default Radarr instance?'), findsOneWidget);
    expect(
      find.descendant(
          of: find.byType(AlertDialog),
          matching: find.textContaining('Main Radarr')),
      findsOneWidget,
    );
    expect(
      find.descendant(
          of: find.byType(AlertDialog),
          matching: find.textContaining('Radarr B')),
      findsOneWidget,
    );
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(adapter.requests.where((r) => r.method == 'POST'), isEmpty);

    // Confirming saves the instance as the new default.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Make Default'));
    await tester.pumpAndSettle();
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['is_default'], isTrue);
  });

  testWidgets('Tracearr is a plain name + URL + API key form with a default',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: [_user(1, 'alice')]);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Tracearr').last);
    await tester.pumpAndSettle();

    // Hints name the port and where the key comes from.
    expect(find.text('http://tracearr:3000'), findsOneWidget);
    expect(find.textContaining('trr_pub_'), findsOneWidget);
    // Admin-only monitoring: a global default, no per-user assignment, no
    // username/password, no media downloads or instant updates.
    final toggle = tester.widget<SwitchListTile>(
        find.widgetWithText(SwitchListTile, 'Default Instance'));
    expect(toggle.value, isTrue);
    expect(find.text('Use this as the default Tracearr instance'),
        findsOneWidget);
    expect(find.text('Assigned Users'), findsNothing);
    expect(find.text('Password'), findsNothing);
    expect(find.text('Media Downloads'), findsNothing);
  });

  testWidgets('Chaptarr hides the default toggle and assigns selected users',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester,
        adapter: adapter, users: [_user(1, 'alice'), _user(2, 'bob')]);

    // Switch the service type to Chaptarr.
    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Chaptarr').last);
    await tester.pumpAndSettle();

    expect(
        find.widgetWithText(SwitchListTile, 'Default Instance'), findsNothing);
    expect(find.text('Assigned Users'), findsOneWidget);

    await _fillForm(tester, 'Books');
    await tester.tap(find.widgetWithText(CheckboxListTile, 'alice'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();

    // No confirmation dialog for chaptarr, the flag is forced off, and the
    // selected users are granted the new instance.
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['service_type'], 'chaptarr');
    expect(post.body['is_default'], isFalse);
    final putGrants = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' &&
        r.path == '/api/instances/chaptarr-new/grant-users');
    expect(putGrants.body, {
      'user_ids': [1]
    });
  });

  testWidgets('Chaptarr create saves four independent media path mappings',
      (tester) async {
    final adapter = _FakeAdapter(mediaRoots: const ['/media']);
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Chaptarr').last);
    await tester.pumpAndSettle();
    await _fillForm(tester, 'Books');

    for (var i = 0; i < 4; i++) {
      final add = find.widgetWithText(OutlinedButton, 'Add path');
      await tester.ensureVisible(add);
      await tester.tap(add);
      await tester.pumpAndSettle();
    }
    final arrFields = find.widgetWithText(TextField, 'Chaptarr path');
    final cantinarrFields = find.widgetWithText(TextField, 'Cantinarr path');
    expect(arrFields, findsNWidgets(4));
    expect(cantinarrFields, findsNWidgets(4));
    const sources = [
      '/ebooks',
      '/audiobooks',
      '/yana-ebooks',
      '/yana-audiobooks',
    ];
    const targets = [
      '/media/ebooks',
      '/media/audiobooks',
      '/media/yana-ebooks',
      '/media/yana-audiobooks',
    ];
    for (var i = 0; i < 4; i++) {
      await tester.enterText(arrFields.at(i), sources[i]);
      await tester.enterText(cantinarrFields.at(i), targets[i]);
    }

    final save = find.widgetWithText(ElevatedButton, 'Add Instance');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    final post = adapter.requests.singleWhere(
        (request) => request.method == 'POST' && request.path == '/api/instances');
    expect(post.body['media_path_mappings'], [
      for (var i = 0; i < 4; i++)
        {'arr_path': sources[i], 'cantinarr_path': targets[i]},
    ]);
  });

  testWidgets('Lidarr create saves a media path mapping', (tester) async {
    // Per-track downloads ride the same mappings form as the other arrs, so
    // a Lidarr instance offers it whenever the server reports media roots.
    final adapter = _FakeAdapter(mediaRoots: const ['/media']);
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Lidarr').last);
    await tester.pumpAndSettle();
    await _fillForm(tester, 'Music');

    final add = find.widgetWithText(OutlinedButton, 'Add path');
    await tester.ensureVisible(add);
    await tester.tap(add);
    await tester.pumpAndSettle();
    await tester.enterText(
        find.widgetWithText(TextField, 'Lidarr path'), '/music');
    await tester.enterText(
        find.widgetWithText(TextField, 'Cantinarr path'), '/media/music');

    final save = find.widgetWithText(ElevatedButton, 'Add Instance');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    final post = adapter.requests.singleWhere(
        (request) => request.method == 'POST' && request.path == '/api/instances');
    expect(post.body['service_type'], 'lidarr');
    expect(post.body['media_path_mappings'], [
      {'arr_path': '/music', 'cantinarr_path': '/media/music'},
    ]);
  });

  testWidgets('an incomplete media mapping blocks instance creation',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);
    await _fillForm(tester, 'Movies');

    final add = find.widgetWithText(OutlinedButton, 'Add path');
    await tester.ensureVisible(add);
    await tester.tap(add);
    await tester.pumpAndSettle();
    await tester.enterText(
        find.widgetWithText(TextField, 'Radarr path'), '/movies');

    final save = find.widgetWithText(ElevatedButton, 'Add Instance');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(find.text('Both paths are required for every media mapping'),
        findsOneWidget);
    expect(
        adapter.requests.where((request) =>
            request.method == 'POST' && request.path == '/api/instances'),
        isEmpty);
  });

  testWidgets('media mapping editor fits a narrow screen at 200% text',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      viewSize: const Size(320, 1800),
      textScaleFactor: 2,
    );
    expect(tester.takeException(), isNull);

    final add = find.widgetWithText(OutlinedButton, 'Add path');
    await tester.drag(find.byType(ListView), const Offset(0, -600));
    await tester.pumpAndSettle();
    await tester.ensureVisible(add);
    await tester.pumpAndSettle();
    await tester.tap(add);
    await tester.pumpAndSettle();

    expect(find.widgetWithText(TextField, 'Radarr path'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Cantinarr path'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('edit hydrates, removes, and replaces this instance mappings',
      (tester) async {
    final mapped = {
      ..._radarrB,
      'media_downloads': true,
      'media_path_mappings': [
        {'arr_path': '/movies', 'cantinarr_path': '/media/movies'},
        {'arr_path': '/uhd', 'cantinarr_path': '/media/uhd'},
      ],
    };
    final adapter = _FakeAdapter(instances: [Map.of(_mainRadarr), mapped]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    final sourceFields = find.widgetWithText(TextField, 'Radarr path');
    final targetFields = find.widgetWithText(TextField, 'Cantinarr path');
    expect(sourceFields, findsNWidgets(2));
    expect(targetFields, findsNWidgets(2));
    expect(tester.widget<TextField>(sourceFields.first).controller!.text,
        '/movies');
    expect(tester.widget<TextField>(targetFields.last).controller!.text,
        '/media/uhd');

    await tester.tap(find.byTooltip('Remove path mapping').first);
    await tester.pumpAndSettle();
    expect(find.widgetWithText(TextField, 'Radarr path'), findsOneWidget);

    final save = find.widgetWithText(ElevatedButton, 'Save Changes');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    final update = adapter.requests.singleWhere((request) =>
        request.method == 'PUT' && request.path == '/api/instances/radarr-b');
    expect(update.body['media_path_mappings'], [
      {'arr_path': '/uhd', 'cantinarr_path': '/media/uhd'},
    ]);
  });

  testWidgets('edit offers reported arr folders and tap-to-map fills a row',
      (tester) async {
    final editable = {
      ..._radarrB,
      'media_downloads': false,
      'media_path_mappings': const [],
    };
    final adapter = _FakeAdapter(
      instances: [Map.of(_mainRadarr), editable],
      arrRootFolders: const ['/media-server/movies'],
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    // The reported folders come from this exact instance via the arr proxy.
    expect(
      adapter.requests.any((request) =>
          request.method == 'GET' &&
          request.path == '/api/instances/radarr-b/api/v3/rootfolder'),
      isTrue,
    );
    final chip =
        find.widgetWithText(ActionChip, '/media-server/movies');
    await tester.ensureVisible(chip);
    expect(find.text('Reported by Radarr — tap to map'), findsOneWidget);

    await tester.tap(chip);
    await tester.pumpAndSettle();
    final source = find.widgetWithText(TextField, 'Radarr path');
    expect(tester.widget<TextField>(source).controller!.text,
        '/media-server/movies');
    // A reported folder is by definition covered, so no mismatch warning.
    expect(find.textContaining('does not report any library folder'),
        findsNothing);

    await tester.enterText(
        find.widgetWithText(TextField, 'Cantinarr path'), '/media/movies');
    final save = find.widgetWithText(ElevatedButton, 'Save Changes');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    final update = adapter.requests.singleWhere((request) =>
        request.method == 'PUT' && request.path == '/api/instances/radarr-b');
    expect(update.body['media_path_mappings'], [
      {
        'arr_path': '/media-server/movies',
        'cantinarr_path': '/media/movies',
      },
    ]);
  });

  testWidgets('warns when a mapping matches no reported arr folder',
      (tester) async {
    final mapped = {
      ..._radarrB,
      'media_downloads': true,
      'media_path_mappings': [
        {'arr_path': '/movies', 'cantinarr_path': '/media/movies'},
      ],
    };
    final adapter = _FakeAdapter(
      instances: [Map.of(_mainRadarr), mapped],
      arrRootFolders: const ['/media-server/movies'],
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    // The saved source path exists nowhere in what Radarr reports, so the
    // row carries a warning until it is corrected.
    final warning =
        find.textContaining('does not report any library folder');
    await tester.ensureVisible(warning);
    expect(warning, findsOneWidget);

    await tester.enterText(find.widgetWithText(TextField, 'Radarr path'),
        '/media-server/movies/kids');
    await tester.pump();
    expect(find.textContaining('does not report any library folder'),
        findsNothing);
  });

  testWidgets('unrelated edit never resubmits untouched mappings',
      (tester) async {
    final mapped = {
      ..._radarrB,
      'media_downloads': true,
      'media_path_mappings': [
        {'arr_path': '/media', 'cantinarr_path': '/media'},
      ],
    };
    final adapter = _FakeAdapter(instances: [mapped]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    await tester.enterText(
        find.widgetWithText(TextField, 'Name'), 'Renamed Radarr');
    final save = find.widgetWithText(ElevatedButton, 'Save Changes');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    final update = adapter.requests.singleWhere((request) =>
        request.method == 'PUT' && request.path == '/api/instances/radarr-b');
    expect(update.body.containsKey('media_path_mappings'), isFalse);
  });

  testWidgets('unrelated edit preserves a temporarily unavailable mapping',
      (tester) async {
    final unavailable = {
      ..._radarrB,
      'media_downloads': false,
      'media_path_mappings': [
        {'arr_path': '/movies', 'cantinarr_path': '/media/offline'},
      ],
    };
    final adapter = _FakeAdapter(instances: [unavailable]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    await tester.enterText(
        find.widgetWithText(TextField, 'Name'), 'Renamed Radarr');
    final save = find.widgetWithText(ElevatedButton, 'Save Changes');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    final update = adapter.requests.singleWhere((request) =>
        request.method == 'PUT' && request.path == '/api/instances/radarr-b');
    expect(update.body.containsKey('media_path_mappings'), isFalse);
  });

  testWidgets('removing the final mapping sends an explicit empty array',
      (tester) async {
    final mapped = {
      ..._radarrB,
      'media_downloads': true,
      'media_path_mappings': [
        {'arr_path': '/movies', 'cantinarr_path': '/media/movies'},
      ],
    };
    final adapter = _FakeAdapter(instances: [mapped]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    await tester.tap(find.byTooltip('Remove path mapping'));
    await tester.pumpAndSettle();
    final save = find.widgetWithText(ElevatedButton, 'Save Changes');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    final update = adapter.requests.singleWhere((request) =>
        request.method == 'PUT' && request.path == '/api/instances/radarr-b');
    expect(update.body['media_path_mappings'], isEmpty);
  });

  testWidgets(
      'editing a non-default instance grants users beside their default',
      (tester) async {
    final adapter = _FakeAdapter(
      instances: [Map.of(_mainRadarr), Map.of(_radarrB)],
      pins: [
        {'user_id': 2, 'instance_id': 'radarr-main'},
      ],
      grants: [
        {'user_id': 1, 'instance_id': 'radarr-b'},
      ],
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: [_user(1, 'alice'), _user(2, 'bob')],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
        initialIsDefault: false,
      ),
    );

    // Alice already holds a grant here (checked); Bob's pin to the sibling is
    // informational — access here is additive, not a move.
    expect(find.text('User Access'), findsOneWidget);
    expect(find.text('Default library: "Main Radarr"'), findsOneWidget);
    final aliceTile = tester.widget<CheckboxListTile>(
        find.widgetWithText(CheckboxListTile, 'alice'));
    expect(aliceTile.value, isTrue);

    await tester.tap(find.widgetWithText(CheckboxListTile, 'bob'));
    await tester.pumpAndSettle();

    // Granting is additive: no reassignment dialog, the save applies both the
    // instance update and the grant list directly.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Save Changes'));
    await tester.pumpAndSettle();
    expect(find.byType(AlertDialog), findsNothing);

    expect(
      adapter.requests
          .any((r) => r.method == 'PUT' && r.path == '/api/instances/radarr-b'),
      isTrue,
    );
    final putGrants = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/instances/radarr-b/grant-users');
    expect(putGrants.body, {
      'user_ids': [1, 2]
    });
  });

  testWidgets(
      'granting a sibling Chaptarr instance never moves the existing one',
      (tester) async {
    final adapter = _FakeAdapter(
      instances: [
        {
          'id': 'chaptarr-a',
          'service_type': 'chaptarr',
          'name': 'Books A',
          'url': 'http://books-a',
          'is_default': false,
          'sort_order': 0,
        },
      ],
      pins: [
        {'user_id': 1, 'instance_id': 'chaptarr-a'},
      ],
    );
    await _pumpEdit(tester,
        adapter: adapter, users: [_user(1, 'alice'), _user(2, 'bob')]);

    // Switch the service type to Chaptarr.
    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Chaptarr').last);
    await tester.pumpAndSettle();

    await _fillForm(tester, 'Books B');
    // Alice's existing library shows as her default, not a pending move.
    expect(find.text('Default library: "Books A"'), findsOneWidget);
    await tester.tap(find.widgetWithText(CheckboxListTile, 'alice'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();

    // Additive grant: no reassignment dialog, the create goes straight
    // through and grants alice the new instance beside Books A.
    expect(find.byType(AlertDialog), findsNothing);
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['service_type'], 'chaptarr');
    final putGrants = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' &&
        r.path == '/api/instances/chaptarr-new/grant-users');
    expect(putGrants.body, {
      'user_ids': [1]
    });
  });

  testWidgets('Test Connection asks the server to dial the URL', (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);

    // Only the URL and key are filled in: the test must not require a name.
    await tester.enterText(
        find.widgetWithText(TextField, 'URL'), 'http://radarr:7878');
    await tester.enterText(find.widgetWithText(TextField, 'API Key'), 'key');
    await tester.tap(find.widgetWithText(OutlinedButton, 'Test Connection'));
    await tester.pumpAndSettle();

    // The check runs on the server — the host that can resolve
    // cluster-internal names — never as a device-direct arr call.
    final test = adapter.requests
        .singleWhere((r) => r.path == '/api/instances/test');
    expect(test.method, 'POST');
    expect(test.body['service_type'], 'radarr');
    expect(test.body['url'], 'http://radarr:7878');
    expect(test.body['api_key'], 'key');
    expect(test.body.containsKey('id'), isFalse);
    expect(find.text('Connection successful!'), findsOneWidget);
  });

  testWidgets(
      'Test Connection on edit sends the id so stored credentials are used',
      (tester) async {
    final adapter = _FakeAdapter(instances: [Map.of(_radarrB)]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    // The key field is blank (write-only credentials); the id lets the
    // server fall back to the stored key instead of failing with a 401.
    await tester.tap(find.widgetWithText(OutlinedButton, 'Test Connection'));
    await tester.pumpAndSettle();

    final test = adapter.requests
        .singleWhere((r) => r.path == '/api/instances/test');
    expect(test.body['id'], 'radarr-b');
    expect(test.body['api_key'], '');
    expect(find.text('Connection successful!'), findsOneWidget);
  });

  testWidgets('Test Connection failure surfaces the server reason',
      (tester) async {
    const reason =
        'connection test failed: could not reach server: dial tcp: connection refused';
    final adapter = _FakeAdapter(testError: reason);
    await _pumpEdit(tester, adapter: adapter, users: const []);

    // Download clients get the same server-side test as the arrs.
    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('SABnzbd').last);
    await tester.pumpAndSettle();

    await tester.enterText(
        find.widgetWithText(TextField, 'URL'), 'http://sabnzbd:8080');
    await tester.enterText(find.widgetWithText(TextField, 'API Key'), 'key');
    await tester.tap(find.widgetWithText(OutlinedButton, 'Test Connection'));
    await tester.pumpAndSettle();

    final test = adapter.requests
        .singleWhere((r) => r.path == '/api/instances/test');
    expect(test.body['service_type'], 'sabnzbd');
    expect(find.text(reason), findsOneWidget);
  });

  // The setup checklist's download-client row names a category, not a
  // service, so the form opens on the selector's disabled placeholder and
  // refuses to act until a real type is picked — a guess here is exactly the
  // guess the prompt exists to prevent, so neither Save nor Test Connection
  // may reach the server before the choice.
  testWidgets('a prompted form holds every action until a type is picked',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        serviceTypePrompt: 'Select a download client',
      ),
    );

    // The selector opens on the prompt itself…
    expect(find.text('Select a download client'), findsOneWidget);
    // …and nothing that depends on a type renders: credentials need to know
    // their shape (key vs username/password) first, and the default toggle
    // needs a service to be the default of.
    expect(find.widgetWithText(TextField, 'API Key'), findsNothing);
    expect(find.widgetWithText(TextField, 'Username'), findsNothing);
    expect(find.widgetWithText(TextField, 'Password'), findsNothing);
    expect(
        find.widgetWithText(SwitchListTile, 'Default Instance'), findsNothing);
    // The type-independent basics stay.
    expect(find.widgetWithText(TextField, 'Name'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'URL'), findsOneWidget);

    final requestsBefore = adapter.requests.length;

    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();
    expect(find.text('Choose a service type'), findsOneWidget);

    await tester.tap(find.widgetWithText(OutlinedButton, 'Test Connection'));
    await tester.pumpAndSettle();
    // A refusal, in red — not a connection result.
    final result =
        tester.widget<Text>(find.text('Choose a service type first.'));
    expect(result.style?.color, AppTheme.error);

    // Neither action may have reached the server.
    expect(adapter.requests.length, requestsBefore);
  });

  testWidgets('picking a type in a prompted form restores the normal fields',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        serviceTypePrompt: 'Select a download client',
      ),
    );

    // Open the selector from its placeholder and pick a real member; the
    // placeholder itself is disabled, so the menu must still offer a choice.
    await tester.tap(find.text('Select a download client'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('qBittorrent'));
    await tester.pumpAndSettle();

    // qBittorrent opens on its WebUI sign-in, the shape every version has,
    // with the 5.2+ API key offered as the other shape.
    expect(find.widgetWithText(TextField, 'Username'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Password'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'API Key'), findsNothing);
    expect(find.text('API key'), findsOneWidget);
    expect(find.widgetWithText(SwitchListTile, 'Default Instance'),
        findsOneWidget);

    // Save now fails on the ordinary empty-form check instead of the type
    // prompt: a choice has been made and the form believes it.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();
    expect(find.text('Name and URL are required'), findsOneWidget);
    expect(find.text('Choose a service type'), findsNothing);
  });

  testWidgets(
      'qBittorrent switched to an API key swaps the fields and saves only '
      'the key', (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('qBittorrent').last);
    await tester.pumpAndSettle();

    expect(find.text('Username & password'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Username'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'API Key'), findsNothing);
    await tester.enterText(
        find.widgetWithText(TextField, 'Username'), 'admin');

    await tester.tap(find.text('API key'));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(TextField, 'API Key'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Username'), findsNothing);
    expect(find.widgetWithText(TextField, 'Password'), findsNothing);
    expect(find.textContaining('5.2 or newer'), findsWidgets);

    await _fillForm(tester, 'Torrents');
    final save = find.widgetWithText(ElevatedButton, 'Add Instance');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    // The username typed before the switch is gone from the save, so the
    // server stores exactly one shape.
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['service_type'], 'qbittorrent');
    expect(post.body['api_key'], 'key');
    expect(post.body['username'], '');
    expect(post.body['password'], '');
  });

  testWidgets(
      'editing a qBittorrent instance stored with an API key opens on the '
      'key, and moving it to a password requires that password',
      (tester) async {
    final adapter = _FakeAdapter(instances: [
      {
        'id': 'qbittorrent-b',
        'service_type': 'qbittorrent',
        'name': 'Torrents',
        'url': 'http://qbittorrent:8081',
        'is_default': true,
        'has_api_key': true,
        'media_path_mappings': <dynamic>[],
      },
    ]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'qbittorrent-b',
        initialServiceType: 'qbittorrent',
        initialName: 'Torrents',
        initialUrl: 'http://qbittorrent:8081',
      ),
    );

    // The stored shape wins: the key field, blank to keep the stored key.
    expect(find.widgetWithText(TextField, 'API Key'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Username'), findsNothing);
    expect(find.text('Leave blank to keep existing'), findsOneWidget);

    await tester.tap(find.text('Username & password'));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(TextField, 'Username'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Password'), findsOneWidget);
    // Nothing is stored on this shape, so blank is no longer "keep".
    expect(find.text('Leave blank to keep existing'), findsNothing);
    final save = find.widgetWithText(ElevatedButton, 'Save Changes');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();
    expect(find.text('Username and password are required'), findsOneWidget);
    expect(
        adapter.requests.where((r) =>
            r.method == 'PUT' && r.path == '/api/instances/qbittorrent-b'),
        isEmpty);

    await tester.enterText(
        find.widgetWithText(TextField, 'Username'), 'admin');
    await tester.enterText(
        find.widgetWithText(TextField, 'Password'), 'secret');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();
    final update = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/instances/qbittorrent-b');
    expect(update.body['username'], 'admin');
    expect(update.body['password'], 'secret');
    expect(update.body['api_key'], '');
  });

  testWidgets('Deluge asks for its web UI password and nothing else',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Deluge').last);
    await tester.pumpAndSettle();

    // Deluge's web UI has no username, so one credential field and no
    // qBittorrent-style shape toggle.
    expect(find.widgetWithText(TextField, 'Web UI password'), findsOneWidget);
    expect(find.text("The password Deluge's web UI asks for"), findsOneWidget);
    expect(find.widgetWithText(TextField, 'Username'), findsNothing);
    expect(find.widgetWithText(TextField, 'API Key'), findsNothing);
    expect(find.text('Username & password'), findsNothing);
    expect(find.text('http://deluge:8112'), findsOneWidget);
  });

  testWidgets('a new Deluge instance saves its password with an empty username',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Deluge').last);
    await tester.pumpAndSettle();

    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'Torrents');
    await tester.enterText(
        find.widgetWithText(TextField, 'URL'), 'http://deluge:8112');
    await tester.enterText(
        find.widgetWithText(TextField, 'Web UI password'), 'secret');
    final save = find.widgetWithText(ElevatedButton, 'Add Instance');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    // The username/password payload shape, with the username the form never
    // asked for left empty.
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['service_type'], 'deluge');
    expect(post.body['username'], '');
    expect(post.body['password'], 'secret');
    expect(post.body['api_key'], '');
  });

  testWidgets('a Deluge instance without a password does not save',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Deluge').last);
    await tester.pumpAndSettle();

    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'Torrents');
    await tester.enterText(
        find.widgetWithText(TextField, 'URL'), 'http://deluge:8112');
    final requestsBefore = adapter.requests.length;
    final save = find.widgetWithText(ElevatedButton, 'Add Instance');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    // The password is the only credential, so it is the one that is required,
    // and nothing reaches the server without it.
    expect(find.text('Password is required'), findsOneWidget);
    expect(adapter.requests.length, requestsBefore);
  });

  testWidgets('ruTorrent asks for optional HTTP Basic credentials, no API key',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('ruTorrent').last);
    await tester.pumpAndSettle();

    // ruTorrent's web server may or may not sit behind HTTP Basic auth, so
    // both credential fields are offered as optional, like Transmission's.
    expect(
        find.widgetWithText(TextField, 'Username (optional)'), findsOneWidget);
    expect(
        find.widgetWithText(TextField, 'Password (optional)'), findsOneWidget);
    expect(find.text('Only if authentication is enabled'), findsNWidgets(2));
    expect(find.widgetWithText(TextField, 'API Key'), findsNothing);
    expect(find.text('Username & password'), findsNothing);
    expect(find.text('http://rutorrent:8080'), findsOneWidget);
  });

  testWidgets('a new ruTorrent instance saves with no credentials typed',
      (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('ruTorrent').last);
    await tester.pumpAndSettle();

    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'Torrents');
    await tester.enterText(
        find.widgetWithText(TextField, 'URL'), 'http://rutorrent:8080');
    final save = find.widgetWithText(ElevatedButton, 'Add Instance');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    // Optional credentials are not required: the save goes through with
    // both left empty, and no validation message stands in the way.
    expect(find.text('Username and password are required'), findsNothing);
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['service_type'], 'rutorrent');
    expect(post.body['username'], '');
    expect(post.body['password'], '');
    expect(post.body['api_key'], '');
  });

  testWidgets(
      'Lidarr hides the default toggle, assigns selected users, and installs '
      'its webhook on create', (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester,
        adapter: adapter, users: [_user(1, 'alice'), _user(2, 'bob')]);

    // Switch the service type to Lidarr.
    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Lidarr').last);
    await tester.pumpAndSettle();

    // Grant-only like Chaptarr: no global default, per-user assignment.
    expect(
        find.widgetWithText(SwitchListTile, 'Default Instance'), findsNothing);
    expect(find.text('Assigned Users'), findsOneWidget);

    await _fillForm(tester, 'Music');
    await tester.tap(find.widgetWithText(CheckboxListTile, 'alice'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();

    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['service_type'], 'lidarr');
    expect(post.body['is_default'], isFalse);
    final putGrants = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' &&
        r.path == '/api/instances/lidarr-new/grant-users');
    expect(putGrants.body, {
      'user_ids': [1]
    });
    // Instant updates install automatically at create, exactly like the
    // other webhook-capable arrs.
    expect(
      adapter.requests.any((r) =>
          r.method == 'POST' &&
          r.path == '/api/instances/lidarr-new/webhook'),
      isTrue,
    );
  });

  testWidgets('offers instant updates for a Chaptarr instance',
      (tester) async {
    // Books are the surface that needs this most: a small ebook can finish
    // downloading between two 30-second polls, so without the webhook its
    // alert is never sent rather than merely delayed.
    final adapter = _FakeAdapter(instances: [
      {
        'id': 'chaptarr-a',
        'service_type': 'chaptarr',
        'name': 'Books A',
        'url': 'http://books-a',
        'is_default': false,
        'sort_order': 0,
      },
    ]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'chaptarr-a',
        initialServiceType: 'chaptarr',
        initialName: 'Books A',
        initialUrl: 'http://books-a',
      ),
    );

    expect(find.text('Instant updates'), findsOneWidget);
    // Requester vocabulary, and it must not name the wrong service.
    expect(find.textContaining('Chaptarr'), findsWidgets);
    expect(find.textContaining('Radarr'), findsNothing);

    await tester
        .tap(find.widgetWithText(OutlinedButton, 'Configure instant updates'));
    await tester.pumpAndSettle();

    expect(
      adapter.requests.any((r) =>
          r.method == 'POST' && r.path == '/api/instances/chaptarr-a/webhook'),
      isTrue,
    );
    expect(find.text('Instant updates are configured.'), findsOneWidget);
  });

  testWidgets('configures instant updates without displaying a webhook token',
      (tester) async {
    const syntheticToken = 'synthetic-webhook-token-that-must-not-render';
    final instance = Map<String, dynamic>.of(_radarrB)
      ..['webhook_token'] = syntheticToken;
    final adapter = _FakeAdapter(instances: [instance]);
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    expect(find.textContaining(syntheticToken), findsNothing);
    await tester
        .tap(find.widgetWithText(OutlinedButton, 'Configure instant updates'));
    await tester.pumpAndSettle();

    expect(
      adapter.requests.any((r) =>
          r.method == 'POST' &&
          r.path == '/api/instances/radarr-b/webhook' &&
          r.body == null),
      isTrue,
    );
    expect(find.text('Instant updates are configured.'), findsOneWidget);
    expect(find.textContaining(syntheticToken), findsNothing);
  });

  testWidgets('shows retry guidance from a text/plain webhook error',
      (tester) async {
    const guidance =
        'webhook configured but credential promotion is pending; retry';
    final adapter = _FakeAdapter(
      instances: [Map.of(_radarrB)],
      webhookError: guidance,
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    await tester
        .tap(find.widgetWithText(OutlinedButton, 'Configure instant updates'));
    await tester.pumpAndSettle();

    expect(find.text(guidance), findsOneWidget);
  });

  testWidgets('creating a source instance turns on instant updates itself',
      (tester) async {
    // The webhook install must not depend on the admin later finding the
    // configure button at the bottom of the edit screen.
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await _fillForm(tester, 'Movies');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();

    final webhook = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path.endsWith('/webhook'));
    expect(webhook.path, '/api/instances/radarr-new/webhook');
    expect(find.text('Instance created — instant updates configured'),
        findsOneWidget);
  });

  testWidgets('a failed webhook install reports without undoing the create',
      (tester) async {
    const reason = 'failed to configure radarr webhook: callback unreachable';
    final adapter = _FakeAdapter(webhookError: reason);
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await _fillForm(tester, 'Movies');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();

    // The instance itself was created; the report carries the server's
    // reason and points at the retry path (the edit screen's button).
    expect(
      adapter.requests
          .any((r) => r.method == 'POST' && r.path == '/api/instances'),
      isTrue,
    );
    expect(
      find.textContaining("instant updates couldn't be configured: $reason"),
      findsOneWidget,
    );
    expect(find.textContaining('edit the instance to retry'), findsOneWidget);
  });

  testWidgets('download clients skip the webhook install', (tester) async {
    final adapter = _FakeAdapter();
    await _pumpEdit(tester, adapter: adapter, users: const []);

    await tester.tap(find.text('Radarr'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('SABnzbd').last);
    await tester.pumpAndSettle();
    await _fillForm(tester, 'Downloads');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();

    expect(adapter.requests.any((r) => r.path.endsWith('/webhook')), isFalse);
    expect(find.text('Instance created'), findsOneWidget);
  });

  testWidgets('the edit screen shows the live instant-updates state',
      (tester) async {
    final adapter = _FakeAdapter(
      instances: [Map.of(_radarrB)],
      webhookStatus: {'supported': true, 'configured': true, 'state': 'ok'},
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    expect(
      adapter.requests.any((r) =>
          r.method == 'GET' && r.path == '/api/instances/radarr-b/webhook'),
      isTrue,
    );
    expect(find.text('Instant updates are on.'), findsOneWidget);
  });

  testWidgets('a Chaptarr edit offers Hardcover above instant updates',
      (tester) async {
    final adapter = _FakeAdapter(
      instances: [
        {
          'id': 'chaptarr-a',
          'service_type': 'chaptarr',
          'name': 'Books',
          'url': 'http://books',
          'is_default': false,
          'sort_order': 0,
        },
      ],
      webhookStatus: {'supported': true, 'configured': true, 'state': 'ok'},
      hardcoverStatus: {'supported': true, 'configured': false},
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'chaptarr-a',
        initialServiceType: 'chaptarr',
        initialName: 'Books',
        initialUrl: 'http://books',
      ),
    );

    expect(
      adapter.requests.any((r) =>
          r.method == 'GET' && r.path == '/api/instances/chaptarr-a/hardcover'),
      isTrue,
    );
    // Sits directly above the instant-updates section, disconnected state.
    final hardcover = tester.getTopLeft(find.text('Hardcover'));
    final webhook = tester.getTopLeft(find.text('Instant updates'));
    expect(hardcover.dy, lessThan(webhook.dy));
    expect(find.text('Connect Hardcover'), findsOneWidget);
    expect(find.text('Disconnect'), findsNothing);

    // The Books tab stays mounted underneath the pushed instance editor.
    // Its already-loaded feed must follow connection changes immediately.
    final container = ProviderScope.containerOf(
        tester.element(find.byType(InstanceEditScreen)));
    final feedProvider = trendingBooksForInstanceProvider('chaptarr-a');
    final feedSubscription = container.listen(feedProvider, (_, __) {});
    addTearDown(feedSubscription.close);
    await tester.pumpAndSettle();
    expect(container.read(feedProvider).valueOrNull?.connected, isFalse);

    // An empty submit never dials the server.
    await tester.tap(find.text('Connect Hardcover'));
    await tester.pumpAndSettle();
    expect(find.text('Paste the API token from Hardcover first.'),
        findsOneWidget);
    expect(adapter.requests.any((r) => r.method == 'PUT'), isFalse);

    await tester.enterText(
        find.widgetWithText(TextField, 'Hardcover API token'), 'hc-token');
    await tester.tap(find.text('Connect Hardcover'));
    await tester.pumpAndSettle();

    final put = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/instances/chaptarr-a/hardcover');
    expect(put.body, {'token': 'hc-token'});
    expect(find.text('Hardcover is connected.'), findsOneWidget);
    expect(find.byType(AlertDialog), findsNothing);
    expect(container.read(feedProvider).valueOrNull?.connected, isTrue);
    // The token is write-only: the field empties, the state flips.
    expect(
      tester
          .widget<TextField>(
              find.widgetWithText(TextField, 'Hardcover API token'))
          .controller!
          .text,
      isEmpty,
    );
    expect(find.text('Replace Hardcover token'), findsOneWidget);

    await tester.tap(find.text('Disconnect'));
    await tester.pumpAndSettle();
    expect(
      adapter.requests.any((r) =>
          r.method == 'DELETE' &&
          r.path == '/api/instances/chaptarr-a/hardcover'),
      isTrue,
    );
    expect(find.text('Hardcover is disconnected.'), findsOneWidget);
    expect(container.read(feedProvider).valueOrNull?.connected, isFalse);
    expect(find.text('Connect Hardcover'), findsOneWidget);
  });

  for (final dismiss in [false, true]) {
    testWidgets('Hardcover ${dismiss ? 'dismissal' : 'decline'} keeps other '
        'instance tokens unchanged', (tester) async {
      final adapter = _FakeAdapter(
        instances: [_chaptarr('a'), _chaptarr('b'), Map.of(_radarrB)],
        hardcoverStatus: {'supported': true, 'configured': false},
      )..hardcoverTokens['chaptarr-b'] = 'previous-token';
      await _pumpEdit(
        tester,
        adapter: adapter,
        users: const [],
        screen: _chaptarrEditor,
      );
      expect(find.byType(AlertDialog), findsNothing);

      await _saveHardcover(tester);
      expect(
        find.text('Use Hardcover for all Chaptarr instances?'),
        findsOneWidget,
      );
      expect(adapter.hardcoverTokens, {
        'chaptarr-a': 'hc-token',
        'chaptarr-b': 'previous-token',
      });
      expect(
        tester
            .widget<TextField>(
              find.widgetWithText(TextField, 'Hardcover API token'),
            )
            .controller!
            .text,
        isEmpty,
      );
      if (dismiss) {
        await tester.tapAt(const Offset(5, 5));
      } else {
        await tester.tap(find.text('Only this instance'));
      }
      await tester.pumpAndSettle();

      expect(find.byType(AlertDialog), findsNothing);
      expect(find.text('Hardcover is connected.'), findsOneWidget);
      expect(adapter.hardcoverTokens, {
        'chaptarr-a': 'hc-token',
        'chaptarr-b': 'previous-token',
      });
      expect(adapter.requests.where((r) => r.method == 'PUT'), hasLength(1));
    });
  }

  testWidgets('applying Hardcover to all uses the current Chaptarr list and '
      'refreshes each saved feed', (tester) async {
    final adapter =
        _FakeAdapter(
            instances: [_chaptarr('a'), _chaptarr('b'), Map.of(_radarrB)],
            hardcoverStatus: {'supported': true, 'configured': true},
          )
          ..hardcoverTokens.addAll({
            'chaptarr-a': 'previous-token',
            'chaptarr-b': 'previous-token',
          });
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: _chaptarrEditor,
    );
    // An instance created while the editor is open must be offered too.
    adapter.instances.add(_chaptarr('c'));
    final container = ProviderScope.containerOf(
      tester.element(find.byType(InstanceEditScreen)),
    );
    final feeds = [
      for (final suffix in ['a', 'b', 'c'])
        trendingBooksForInstanceProvider('chaptarr-$suffix'),
    ];
    for (final feed in feeds) {
      final subscription = container.listen(feed, (_, __) {});
      addTearDown(subscription.close);
    }
    await tester.pumpAndSettle();
    expect(container.read(feeds[2]).valueOrNull?.connected, isFalse);

    await _saveHardcover(tester, replacing: true);
    final dialog = tester.widget<AlertDialog>(find.byType(AlertDialog));
    final content = (dialog.content! as Text).data!;
    expect(content, contains('Books b'));
    expect(content, contains('Books c'));
    expect(content, contains('will be replaced'));
    expect(content, isNot(contains('Radarr')));
    expect(content, isNot(contains('hc-token')));
    await tester.tap(find.text('Apply to all'));
    await tester.pumpAndSettle();

    expect(adapter.hardcoverTokens, {
      'chaptarr-a': 'hc-token',
      'chaptarr-b': 'hc-token',
      'chaptarr-c': 'hc-token',
    });
    expect(
      adapter.requests.where((r) => r.method == 'PUT').map((r) => r.path),
      [
        '/api/instances/chaptarr-a/hardcover',
        '/api/instances/chaptarr-b/hardcover',
        '/api/instances/chaptarr-c/hardcover',
      ],
    );
    for (var i = 0; i < feeds.length; i++) {
      expect(container.read(feeds[i]).valueOrNull?.connected, isTrue);
    }
    expect(adapter.hardcoverFeedReads, {
      'chaptarr-a': 2,
      'chaptarr-b': 2,
      'chaptarr-c': 2,
    });
    expect(
      find.text('Hardcover is connected to all 3 Chaptarr instances.'),
      findsOneWidget,
    );

    await tester.tap(find.text('Disconnect'));
    await tester.pumpAndSettle();
    expect(adapter.hardcoverTokens, {
      'chaptarr-b': 'hc-token',
      'chaptarr-c': 'hc-token',
    });
    expect(find.byType(AlertDialog), findsNothing);
  });

  testWidgets('a failed Hardcover update names the instance and continues '
      'saving the others', (tester) async {
    final delayed = Completer<void>();
    final adapter = _FakeAdapter(
      instances: [_chaptarr('a'), _chaptarr('b'), _chaptarr('c')],
      hardcoverStatus: {'supported': true, 'configured': false},
      hardcoverErrors: {'chaptarr-b': 'could not reach Hardcover'},
      beforeHardcoverSave: (id) async {
        if (id == 'chaptarr-b') await delayed.future;
      },
    )..hardcoverTokens['chaptarr-b'] = 'previous-token';
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: _chaptarrEditor,
    );
    await _saveHardcover(tester);
    await tester.tap(find.text('Apply to all'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 500));
    expect(
      find.text('Connecting Hardcover to the other Chaptarr instances…'),
      findsOneWidget,
    );
    expect(
      tester
          .widget<OutlinedButton>(
            find.widgetWithText(OutlinedButton, 'Replace Hardcover token'),
          )
          .onPressed,
      isNull,
    );
    expect(
      tester
          .widget<TextButton>(find.widgetWithText(TextButton, 'Disconnect'))
          .onPressed,
      isNull,
    );
    delayed.complete();
    await tester.pumpAndSettle();

    expect(adapter.hardcoverTokens, {
      'chaptarr-a': 'hc-token',
      'chaptarr-b': 'previous-token',
      'chaptarr-c': 'hc-token',
    });
    expect(
      find.textContaining('Token saved for 2 of 3 Chaptarr instances.'),
      findsOneWidget,
    );
    expect(
      find.textContaining('Books b: could not reach Hardcover'),
      findsOneWidget,
    );
    expect(
      find.textContaining('Open those instances to try again.'),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('a rejected Hardcover replacement never offers to apply it '
      'elsewhere', (tester) async {
    final adapter = _FakeAdapter(
      instances: [_chaptarr('a'), _chaptarr('b')],
      hardcoverStatus: {'supported': true, 'configured': true},
      hardcoverErrors: {'chaptarr-a': 'Hardcover rejected the API token'},
    )..hardcoverTokens['chaptarr-a'] = 'previous-token';
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: _chaptarrEditor,
    );
    await _saveHardcover(tester, replacing: true);
    expect(find.byType(AlertDialog), findsNothing);
    expect(find.text('Hardcover rejected the API token'), findsOneWidget);
    expect(adapter.hardcoverTokens, {'chaptarr-a': 'previous-token'});
    expect(adapter.requests.where((r) => r.method == 'PUT'), hasLength(1));
  });

  testWidgets('a failed instance list read preserves the saved Hardcover '
      'connection and reports the missing list', (tester) async {
    final adapter = _FakeAdapter(
      instances: [_chaptarr('a'), _chaptarr('b')],
      hardcoverStatus: {'supported': true, 'configured': false},
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: _chaptarrEditor,
    );
    adapter.instancesError = 'connection refused';
    await _saveHardcover(tester);
    expect(find.byType(AlertDialog), findsNothing);
    expect(
      find.textContaining(
        'Hardcover is connected to this instance, but '
        'the other instances could not be loaded.',
      ),
      findsOneWidget,
    );
    expect(adapter.hardcoverTokens, {'chaptarr-a': 'hc-token'});
    expect(find.text('Replace Hardcover token'), findsOneWidget);
  });

  testWidgets('other service types do not trigger the Hardcover prompt', (
    tester,
  ) async {
    final adapter = _FakeAdapter(
      instances: [_chaptarr('a'), Map.of(_mainRadarr), Map.of(_radarrB)],
      hardcoverStatus: {'supported': true, 'configured': false},
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: _chaptarrEditor,
    );
    await _saveHardcover(tester);
    expect(find.byType(AlertDialog), findsNothing);
    expect(adapter.hardcoverTokens, {'chaptarr-a': 'hc-token'});
  });

  for (final width in [320.0, 390.0, 1280.0]) {
    testWidgets('Hardcover prompt fits $width pixels with large text', (
      tester,
    ) async {
      final adapter = _FakeAdapter(
        instances: [
          _chaptarr('a'),
          for (var i = 0; i < 8; i++) _chaptarr('long library name $i'),
        ],
        hardcoverStatus: {'supported': true, 'configured': false},
      );
      await _pumpEdit(
        tester,
        adapter: adapter,
        users: const [],
        screen: _chaptarrEditor,
        viewSize: const Size(1280, 2400),
        textScaleFactor: 2,
      );
      await _saveHardcover(tester);
      tester.view.physicalSize = Size(width, 844);
      await tester.pumpAndSettle();
      expect(find.text('Apply to all').hitTestable(), findsOneWidget);
      expect(find.text('Only this instance').hitTestable(), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.tap(find.text('Only this instance'));
      await tester.pumpAndSettle();
      expect(adapter.requests.where((r) => r.method == 'PUT'), hasLength(1));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('Hardcover stays hidden on an older server and for other types',
      (tester) async {
    // Older server: 404 on the status route. Unknown must not render as
    // "not connected".
    final adapter = _FakeAdapter(
      instances: [
        {
          'id': 'chaptarr-a',
          'service_type': 'chaptarr',
          'name': 'Books',
          'url': 'http://books',
          'is_default': false,
          'sort_order': 0,
        },
      ],
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'chaptarr-a',
        initialServiceType: 'chaptarr',
        initialName: 'Books',
        initialUrl: 'http://books',
      ),
    );
    expect(find.text('Hardcover'), findsNothing);
  });

  testWidgets(
      'OAuth is the default and the API token fallback remains available',
      (tester) async {
    final adapter = _FakeAdapter(
        instances: [_chaptarr('a')],
        hardcoverOAuth: true,
        hardcoverStatus: {'supported': true, 'configured': false})
      ..hardcoverDeviceStatus = 'pending';
    await _pumpEdit(tester,
        adapter: adapter, users: const [], screen: _chaptarrEditor);
    expect(find.widgetWithText(TextField, 'Hardcover API token'), findsNothing);
    await tester.tap(find.text('Use an API token instead'));
    await tester.pumpAndSettle();
    expect(
        find.widgetWithText(TextField, 'Hardcover API token'), findsOneWidget);
    await tester.tap(find.text('Sign in with Hardcover instead'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Connect Hardcover'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text('ABCD-EFGH'), findsOneWidget);
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(adapter.hardcoverLinks, isEmpty);
    expect(adapter.requests.where((r) => r.method == 'PUT'), isEmpty);
  });

  testWidgets(
      'OAuth apply uses an expected connection and explicit revisions, refreshes feeds, and isolates disconnect',
      (tester) async {
    final adapter = _FakeAdapter(
        instances: [
          _chaptarr('a'),
          _chaptarr('b'),
          _chaptarr('c'),
          Map.of(_radarrB)
        ],
        hardcoverOAuth: true,
        hardcoverStatus: {'supported': true, 'configured': false})
      ..hardcoverRevisions['chaptarr-b'] = 7;
    await _pumpEdit(tester,
        adapter: adapter, users: const [], screen: _chaptarrEditor);
    final container = ProviderScope.containerOf(
        tester.element(find.byType(InstanceEditScreen)));
    for (final suffix in ['a', 'b', 'c']) {
      final subscription = container.listen(
          trendingBooksForInstanceProvider('chaptarr-$suffix'), (_, __) {});
      addTearDown(subscription.close);
    }
    await tester.pumpAndSettle();
    await tester.tap(find.text('Connect Hardcover'));
    await tester.pumpAndSettle();
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    expect(
        find.text('Use Hardcover for all Chaptarr instances?'), findsOneWidget);
    await tester.tap(find.text('Apply to all'));
    await tester.pumpAndSettle();
    final apply = adapter.requests
        .singleWhere((r) => r.path.endsWith('/hardcover/apply'));
    expect(apply.body, {
      'connection_id': 'oauth-new',
      'instances': [
        {'instance_id': 'chaptarr-b', 'revision': 7},
        {'instance_id': 'chaptarr-c', 'revision': 0},
      ]
    });
    expect(adapter.requests.where((r) => r.method == 'PUT'), isEmpty);
    expect(adapter.hardcoverFeedReads,
        {'chaptarr-a': 2, 'chaptarr-b': 2, 'chaptarr-c': 2});
    expect(adapter.hardcoverLinks.length, 3);
    expect(find.text('Replace Hardcover connection'), findsOneWidget);
    await tester.tap(find.text('Disconnect'));
    await tester.pumpAndSettle();
    expect(adapter.hardcoverLinks,
        {'chaptarr-b': 'oauth-new', 'chaptarr-c': 'oauth-new'});
  });

  testWidgets(
      'OAuth reconnect and partial apply failures identify the next action',
      (tester) async {
    final adapter = _FakeAdapter(
        instances: [_chaptarr('a'), _chaptarr('b'), _chaptarr('c')],
        hardcoverOAuth: true,
        hardcoverStatus: {
          'supported': true,
          'configured': true,
          'reconnect_required': true
        },
        hardcoverErrors: {
          'chaptarr-b': 'Connection changed. Reload and try again.'
        })
      ..hardcoverLinks['chaptarr-a'] = 'expired';
    await _pumpEdit(tester,
        adapter: adapter, users: const [], screen: _chaptarrEditor);
    expect(find.text('Reconnect Hardcover'), findsOneWidget);
    await tester.tap(find.text('Reconnect Hardcover'));
    await tester.pumpAndSettle();
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Apply to all'));
    await tester.pumpAndSettle();
    expect(find.textContaining('Books b: Connection changed.'), findsOneWidget);
    expect(adapter.hardcoverLinks['chaptarr-c'], 'oauth-new');
  });

  testWidgets('Radarr never asks about Hardcover', (tester) async {
    final adapter = _FakeAdapter(
      instances: [Map.of(_radarrB)],
      hardcoverStatus: {'supported': false, 'configured': false},
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );
    expect(
      adapter.requests.any((r) => r.path.endsWith('/hardcover')),
      isFalse,
    );
    expect(find.text('Hardcover'), findsNothing);
  });

  testWidgets('a webhook the arr no longer has reads as not configured',
      (tester) async {
    // The answer is derived from the arr's Connect list at read time; a
    // stored "configured once" flag would keep lying after an admin deleted
    // the record there.
    final adapter = _FakeAdapter(
      instances: [Map.of(_radarrB)],
      webhookStatus: {
        'supported': true,
        'configured': false,
        'state': 'missing',
      },
    );
    await _pumpEdit(
      tester,
      adapter: adapter,
      users: const [],
      screen: const InstanceEditScreen(
        instanceId: 'radarr-b',
        initialServiceType: 'radarr',
        initialName: 'Radarr B',
        initialUrl: 'http://radarr-b',
      ),
    );

    expect(
        find.text('Instant updates are not configured yet.'), findsOneWidget);

    // Configuring from here replaces the status line with the result.
    await tester
        .tap(find.widgetWithText(OutlinedButton, 'Configure instant updates'));
    await tester.pumpAndSettle();
    expect(find.text('Instant updates are configured.'), findsOneWidget);
    expect(find.text('Instant updates are not configured yet.'), findsNothing);
  });
}
