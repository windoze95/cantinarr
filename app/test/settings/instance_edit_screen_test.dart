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

/// Fake Dio adapter: serves the instance list and per-type user pins, and
/// records every request (method, path, decoded body) for assertions.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter({
    this.instances = const [],
    this.pins = const [],
    this.mediaRoots = const ['/media'],
    this.arrRootFolders = const [],
    this.webhookError,
    this.webhookStatus,
    this.testError,
  });

  final List<Map<String, dynamic>> instances;
  final List<Map<String, dynamic>> pins;
  final List<String> mediaRoots;
  final List<String> arrRootFolders;
  final String? webhookError;

  /// GET /webhook status body; null mimics an older server (404), which the
  /// screen must treat as "unknown" and render nothing.
  final Map<String, dynamic>? webhookStatus;
  final String? testError;
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
      response = instances;
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

  @override
  Future<List<UserSummary>> listUsers() async => users;

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
  InstanceEditScreen screen = const InstanceEditScreen(),
  Size viewSize = const Size(800, 1800),
  double textScaleFactor = 1,
}) async {
  // Tall viewport so the whole (lazily built) form list is materialized.
  tester.view.physicalSize = viewSize;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  // A dummy root route so the screen's context.pop(true) has somewhere to go.
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
  router.push('/edit');
  await tester.pumpAndSettle();
}

Future<void> _fillForm(WidgetTester tester, String name) async {
  await tester.enterText(find.widgetWithText(TextField, 'Name'), name);
  await tester.enterText(
      find.widgetWithText(TextField, 'URL'), 'http://localhost:9999');
  await tester.enterText(find.widgetWithText(TextField, 'API Key'), 'key');
}

void main() {
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
    expect(find.text('Per-User Default'), findsOneWidget);
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
    // selected users are assigned to the new instance.
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['service_type'], 'chaptarr');
    expect(post.body['is_default'], isFalse);
    final putUsers = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/instances/chaptarr-new/users');
    expect(putUsers.body, {
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
      'editing a non-default instance pins users and shows current pins',
      (tester) async {
    final adapter = _FakeAdapter(
      instances: [Map.of(_mainRadarr), Map.of(_radarrB)],
      pins: [
        {'user_id': 2, 'instance_id': 'radarr-main'},
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

    // Bob is pinned to the sibling instance; selecting him here is a move.
    expect(find.text('Per-User Default'), findsOneWidget);
    expect(find.text('Currently assigned to "Main Radarr"'), findsOneWidget);

    await tester.tap(find.widgetWithText(CheckboxListTile, 'bob'));
    await tester.pumpAndSettle();

    // Moving bob off the sibling asks for confirmation naming who moves from
    // where; cancelling aborts the save entirely.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Save Changes'));
    await tester.pumpAndSettle();
    expect(find.text('Reassign 1 user?'), findsOneWidget);
    expect(
      find.descendant(
          of: find.byType(AlertDialog),
          matching: find.textContaining(
              'removes bob from "Main Radarr" and assigns them to "Radarr B"')),
      findsOneWidget,
    );
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(adapter.requests.where((r) => r.method == 'PUT'), isEmpty);

    // Confirming applies both the instance update and the reassignment.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Save Changes'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Reassign'));
    await tester.pumpAndSettle();

    expect(
      adapter.requests
          .any((r) => r.method == 'PUT' && r.path == '/api/instances/radarr-b'),
      isTrue,
    );
    final putUsers = adapter.requests.singleWhere(
        (r) => r.method == 'PUT' && r.path == '/api/instances/radarr-b/users');
    expect(putUsers.body, {
      'user_ids': [2]
    });
  });

  testWidgets(
      'assigning a user pinned to a sibling Chaptarr instance confirms the move',
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
    await tester.tap(find.widgetWithText(CheckboxListTile, 'alice'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();

    // Alice is pinned to Books A, so creating must confirm the removal and
    // spell out where her Books access lands; cancelling creates nothing.
    expect(find.text('Reassign 1 user?'), findsOneWidget);
    expect(
      find.descendant(
          of: find.byType(AlertDialog),
          matching: find.textContaining(
              'removes alice from "Books A" and assigns them to "Books B"')),
      findsOneWidget,
    );
    expect(
      find.descendant(
          of: find.byType(AlertDialog),
          matching: find
              .textContaining('Books access will come from "Books B" instead')),
      findsOneWidget,
    );
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(adapter.requests.where((r) => r.method == 'POST'), isEmpty);

    // Confirming creates the instance and moves alice to it.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Add Instance'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Reassign'));
    await tester.pumpAndSettle();
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST' && r.path == '/api/instances');
    expect(post.body['service_type'], 'chaptarr');
    final putUsers = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/instances/chaptarr-new/users');
    expect(putUsers.body, {
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
