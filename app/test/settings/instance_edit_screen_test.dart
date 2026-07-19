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
    this.webhookError,
    this.testError,
  });

  final List<Map<String, dynamic>> instances;
  final List<Map<String, dynamic>> pins;
  final String? webhookError;
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
    if (options.method == 'GET' && path == '/api/instances') {
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
}) async {
  // Tall viewport so the whole (lazily built) form list is materialized.
  tester.view.physicalSize = const Size(800, 1800);
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
      child: MaterialApp.router(routerConfig: router),
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
    final post = adapter.requests.singleWhere((r) => r.method == 'POST');
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
    final post = adapter.requests.singleWhere((r) => r.method == 'POST');
    expect(post.body['service_type'], 'chaptarr');
    expect(post.body['is_default'], isFalse);
    final putUsers = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/instances/chaptarr-new/users');
    expect(putUsers.body, {
      'user_ids': [1]
    });
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
    final post = adapter.requests.singleWhere((r) => r.method == 'POST');
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
}
