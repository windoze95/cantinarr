import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/users_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('a kids account carries a Child tag, others do not',
      (tester) async {
    await tester.binding.setSurfaceSize(const Size(1000, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final auth = _FakeAuthNotifier(users: const [
      UserSummary(
        id: 7,
        username: 'living-room',
        role: 'user',
        permissions: [],
        createdAt: '',
        deviceCount: 1,
        hasPassword: false,
        passwordEnabled: false,
        passkeyEnabled: false,
        hasPendingInvite: false,
        child: true,
      ),
      UserSummary(
        id: 8,
        username: 'parent',
        role: 'user',
        permissions: [],
        createdAt: '',
        deviceCount: 1,
        hasPassword: false,
        passwordEnabled: false,
        passkeyEnabled: false,
        hasPendingInvite: false,
      ),
    ]);
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _CredentialsAdapter(provider: 'codex');

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => auth),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const UsersScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Child'), findsOneWidget);
    expect(find.text('living-room'), findsOneWidget);
    expect(find.text('parent'), findsOneWidget);
  });

  testWidgets(
      'shared ChatGPT access warns about sharing and waits for confirmation',
      (tester) async {
    await tester.binding.setSurfaceSize(const Size(1000, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final auth = _FakeAuthNotifier();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _CredentialsAdapter(provider: 'codex');

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => auth),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const UsersScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Included AI access'));
    await tester.pumpAndSettle();

    expect(auth.aiAccessUpdates, isEmpty);
    expect(find.textContaining('Prompts and tool context'), findsOneWidget);
    expect(find.textContaining('same Codex allowance'), findsOneWidget);
    expect(find.textContaining('activity is attributable'), findsOneWidget);
    expect(find.textContaining('intended for one person'), findsOneWidget);
    expect(
        find.textContaining('people or devices you control'), findsOneWidget);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Include AI access'));
    await tester.pumpAndSettle();

    expect(auth.aiAccessUpdates, [(7, true)]);
    expect(find.text('AI included'), findsOneWidget);
  });

  testWidgets('unknown shared provider keeps the ChatGPT and cost warning',
      (tester) async {
    await tester.binding.setSurfaceSize(const Size(1000, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final auth = _FakeAuthNotifier();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _CredentialsAdapter(provider: 'codex', fail: true);

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => auth),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const UsersScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    expect(find.text('Provider status unavailable'), findsOneWidget);
    await tester.tap(find.text('Included AI access'));
    await tester.pumpAndSettle();

    expect(auth.aiAccessUpdates, isEmpty);
    expect(find.textContaining('could not confirm'), findsOneWidget);
    expect(find.textContaining('one shared account'), findsOneWidget);
    expect(find.textContaining('activity is attributable'), findsOneWidget);
    expect(find.textContaining('intended for one person'), findsOneWidget);
    expect(find.textContaining('paid quota'), findsOneWidget);

    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    expect(auth.aiAccessUpdates, isEmpty);
  });

  testWidgets('an unconfigured shared provider stages access without OAuth claims',
      (tester) async {
    await tester.binding.setSurfaceSize(const Size(1000, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final auth = _FakeAuthNotifier();
    // The server defaults the provider name to codex even when nothing is
    // configured — the flag, not the name, decides what the admin is told.
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter =
          _CredentialsAdapter(provider: 'codex', configured: false);

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => auth),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const UsersScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    expect(find.text('No shared provider configured yet'), findsOneWidget);
    await tester.tap(find.text('Included AI access'));
    await tester.pumpAndSettle();

    expect(auth.aiAccessUpdates, isEmpty);
    expect(
      find.textContaining('No shared AI provider is set up yet'),
      findsOneWidget,
    );
    expect(find.textContaining('Codex'), findsNothing);
    expect(find.textContaining('OAuth'), findsNothing);

    // The grant itself still saves — it simply waits for a provider.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Include AI access'));
    await tester.pumpAndSettle();
    expect(auth.aiAccessUpdates, [(7, true)]);
  });

  testWidgets('enable confirmation refreshes a provider changed elsewhere',
      (tester) async {
    await tester.binding.setSurfaceSize(const Size(1000, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final auth = _FakeAuthNotifier();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _CredentialsAdapter(
        provider: 'openai',
        nextProvider: 'codex',
      );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => auth),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const UsersScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    expect(find.text('Server provider quota'), findsOneWidget);
    await tester.tap(find.text('Included AI access'));
    await tester.pumpAndSettle();

    expect(auth.aiAccessUpdates, isEmpty);
    expect(find.textContaining('same Codex allowance'), findsOneWidget);
    expect(find.textContaining('activity is attributable'), findsOneWidget);
    expect(find.textContaining('intended for one person'), findsOneWidget);

    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    expect(auth.aiAccessUpdates, isEmpty);
  });

  testWidgets('changing the current admin grant refreshes effective AI state',
      (tester) async {
    await tester.binding.setSurfaceSize(const Size(1000, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final auth = _FakeAuthNotifier(currentUser: true);
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _CredentialsAdapter(provider: 'openai');

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => auth),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const UsersScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Included AI access'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Include AI access'));
    await tester.pumpAndSettle();

    expect(auth.aiAccessUpdates, [(7, true)]);
    expect(auth.configRefreshes, 1);
  });

  group('media server accounts', _mediaServerAccountTests);
}

const _homeJellyfin = ServiceInstance(
  id: 'jf-a',
  serviceType: 'jellyfin',
  name: 'Home Jellyfin',
);

Map<String, dynamic> _accountRow({
  String remoteUsername = 'living-room',
  bool disabled = false,
}) =>
    {
      'user_id': 7,
      'instance_id': 'jf-a',
      'instance_name': 'Home Jellyfin',
      'service_type': 'jellyfin',
      'remote_user_id': 'r1',
      'username': remoteUsername,
      'created_by_cantinarr': true,
      'disabled': disabled,
      'created_at': '2026-08-28T10:00:00Z',
    };

Future<_MediaAdapter> _pumpWithMediaServer(
  WidgetTester tester, {
  List<Map<String, dynamic>> accounts = const [],
  bool accountsFail = false,
  List<ServiceInstance> instances = const [_homeJellyfin],
  List<String> grants = const ['jf-a'],
  List<UserSummary>? users,
  List<Map<String, dynamic>> importResults = const [],
}) async {
  await tester.binding.setSurfaceSize(const Size(1000, 800));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final adapter = _MediaAdapter(
    accounts: accounts,
    accountsFail: accountsFail,
    grants: grants,
    importResults: importResults,
  );
  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(
            () => _FakeAuthNotifier(instances: instances, users: users)),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp(theme: AppTheme.dark, home: const UsersScreen()),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

Future<void> _openMenu(WidgetTester tester) async {
  await tester.tap(find.byIcon(Icons.more_vert));
  await tester.pumpAndSettle();
}

void _mediaServerAccountTests() {
  testWidgets('the import action shows only with a media server',
      (tester) async {
    await _pumpWithMediaServer(tester, instances: const []);
    expect(find.byTooltip('Import from a media server'), findsNothing);
  });

  testWidgets(
      'importing picks accounts, says what each becomes, and hands back '
      'the links', (tester) async {
    final adapter = await _pumpWithMediaServer(
      tester,
      // living-room (user 7) is linked to the server's r1; the picker's u2
      // is free. A Cantinarr user named old-tablet already exists.
      accounts: [
        {..._accountRow(remoteUsername: 'lr-tv'), 'remote_user_id': 'u2'},
      ],
      users: [
        const UserSummary(
          id: 7,
          username: 'living-room',
          role: 'admin',
          permissions: [],
          createdAt: '',
          deviceCount: 1,
          hasPassword: true,
          passwordEnabled: true,
          passkeyEnabled: false,
          hasPendingInvite: false,
        ),
        const UserSummary(
          id: 8,
          username: 'old-tablet',
          role: 'user',
          permissions: [],
          createdAt: '',
          deviceCount: 0,
          hasPassword: false,
          passwordEnabled: false,
          passkeyEnabled: false,
          hasPendingInvite: true,
        ),
      ],
      importResults: [
        {
          'remote_user_id': 'a1',
          'remote_username': 'jfadmin',
          'user_id': 9,
          'username': 'jfadmin',
          'created': true,
          'linked': true,
          'link': 'https://cantinarr.example/connect?token=one',
          'origin_source': 'app',
        },
        {
          'remote_user_id': 'u3',
          'remote_username': 'old-tablet',
          'user_id': 8,
          'username': 'old-tablet',
          'created': false,
          'linked': true,
        },
      ],
    );

    await tester.tap(find.byTooltip('Import from a media server'));
    await tester.pumpAndSettle();
    expect(find.text('Import from Home Jellyfin'), findsOneWidget);
    expect(find.textContaining('Nothing else on Home Jellyfin changes'),
        findsOneWidget);
    // What each pick would do, said before it happens.
    expect(find.text('Administrator · New Cantinarr user jfadmin'),
        findsOneWidget);
    expect(find.text('Already linked to living-room'), findsOneWidget);
    expect(
      find.text('Turned off on the server · Existing Cantinarr user '
          'old-tablet'),
      findsOneWidget,
    );
    // The linked row cannot be picked; the button waits for a pick.
    final linkedTile = tester.widget<CheckboxListTile>(
        find.widgetWithText(CheckboxListTile, 'lr-tv'));
    expect(linkedTile.onChanged, isNull);
    expect(find.widgetWithText(ElevatedButton, 'Import 0 accounts'),
        findsNothing);

    // Select all skips the administrator; it is ticked by hand.
    await tester.tap(find.text('Select all'));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(ElevatedButton, 'Import 1 account'),
        findsOneWidget);
    await tester.tap(find.widgetWithText(CheckboxListTile, 'jfadmin'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Import 2 accounts'));
    await tester.pumpAndSettle();

    final post = adapter.requests
        .singleWhere((r) => r.path == '/api/admin/media-servers/jf-a/import');
    expect(post.method, 'POST');
    expect(post.body, {
      'remote_user_ids': ['u3', 'a1'],
      'server_url': 'https://cantinarr.example',
    });

    // The outcome per row, the link only for a user that was created.
    expect(find.text('Imported 2 accounts from Home Jellyfin'), findsOneWidget);
    expect(find.text('New user jfadmin, linked'), findsOneWidget);
    expect(find.text('Existing user old-tablet, linked'), findsOneWidget);
    expect(find.byTooltip('Copy link for jfadmin'), findsOneWidget);
    expect(find.byTooltip('Copy link for old-tablet'), findsNothing);
    expect(find.textContaining('address your app connects with'),
        findsOneWidget);
    expect(find.text('Copy all links'), findsOneWidget);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Done'));
    await tester.pumpAndSettle();
    expect(find.text('Imported 2 users from Home Jellyfin'), findsOneWidget);
  });

  testWidgets('a linked account is tagged and its menu flips access',
      (tester) async {
    final adapter =
        await _pumpWithMediaServer(tester, accounts: [_accountRow()]);

    // The account name matches the Cantinarr username: the server's name
    // alone is the tag.
    expect(find.text('Home Jellyfin'), findsOneWidget);

    await _openMenu(tester);
    expect(find.text('Turn Home Jellyfin access off'), findsOneWidget);
    expect(find.text('Unlink Home Jellyfin account'), findsOneWidget);
    expect(find.text('Link Home Jellyfin account…'), findsNothing);

    await tester.tap(find.text('Turn Home Jellyfin access off'));
    await tester.pumpAndSettle();

    // Access is the grant: the jellyfin grant list is re-read and rewritten
    // without this instance. No media-server route is touched.
    final put = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/admin/users/7/instance-grants');
    expect(put.body, {
      'jellyfin': <String>[],
    });
    expect(
      adapter.requests.any((r) => r.path.contains('/media-servers/jf-a')),
      isFalse,
    );
    expect(find.text('Turned Home Jellyfin access off for living-room'),
        findsOneWidget);
  });

  testWidgets('a turned-off account is tagged off and its menu turns it on',
      (tester) async {
    final adapter = await _pumpWithMediaServer(
      tester,
      accounts: [_accountRow(remoteUsername: 'lr-tv', disabled: true)],
      grants: const [],
    );

    expect(find.text('Home Jellyfin: off'), findsOneWidget);

    await _openMenu(tester);
    expect(find.text('Turn Home Jellyfin access on'), findsOneWidget);
    await tester.tap(find.text('Turn Home Jellyfin access on'));
    await tester.pumpAndSettle();

    final put = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/admin/users/7/instance-grants');
    expect(put.body, {
      'jellyfin': ['jf-a'],
    });
    expect(find.text('Turned Home Jellyfin access on for living-room'),
        findsOneWidget);
  });

  testWidgets('a differently named account shows the remote name',
      (tester) async {
    await _pumpWithMediaServer(
      tester,
      accounts: [_accountRow(remoteUsername: 'lr-tv')],
    );

    expect(find.text('Home Jellyfin: lr-tv'), findsOneWidget);
  });

  testWidgets('an adopted Plex share or the owner is not called an invite',
      (tester) async {
    const plex = ServiceInstance(
      id: 'px-a',
      serviceType: 'plex',
      name: 'Cantina Plex',
    );
    await _pumpWithMediaServer(
      tester,
      instances: const [plex],
      grants: const ['px-a'],
      users: const [
        UserSummary(
          id: 7,
          username: 'living-room',
          role: 'admin',
          permissions: [],
          createdAt: '',
          deviceCount: 1,
          hasPassword: true,
          passwordEnabled: true,
          passkeyEnabled: false,
          hasPendingInvite: false,
          plexEmail: 'owner@example.com',
          plexInvitedAt: '2026-08-30T02:00:00Z',
        ),
      ],
      accounts: [
        {
          'user_id': 7,
          'instance_id': 'px-a',
          'instance_name': 'Cantina Plex',
          'service_type': 'plex',
          'remote_user_id': 'owner@example.com',
          'username': 'windo186',
          'created_by_cantinarr': false,
          'disabled': false,
          'created_at': '2026-08-30T02:00:00Z',
        },
      ],
    );
    expect(find.text('Cantina Plex: windo186'), findsOneWidget);
    expect(find.text('Plex invite sent'), findsNothing);
    expect(find.text('Asked for Plex access'), findsNothing);
  });

  testWidgets('the link picker marks administrators and PUTs the remote id',
      (tester) async {
    final adapter = await _pumpWithMediaServer(tester);

    // No linked account: no tag, and the menu offers to link one.
    expect(find.textContaining('Home Jellyfin'), findsNothing);
    await _openMenu(tester);
    expect(find.text('Turn Home Jellyfin access off'), findsNothing);
    await tester.tap(find.text('Link Home Jellyfin account…'));
    await tester.pumpAndSettle();

    expect(find.text('Link a Jellyfin account'), findsOneWidget);
    expect(
      find.text('Administrator accounts can be linked; Cantinarr never '
          'changes them.'),
      findsOneWidget,
    );
    expect(find.text('jfadmin'), findsOneWidget);
    expect(find.text('Administrator'), findsOneWidget);
    expect(find.text('lr-tv'), findsOneWidget);
    expect(find.text('old-tablet'), findsOneWidget);
    expect(find.text('Turned off on the server'), findsOneWidget);

    await tester.tap(find.text('lr-tv'));
    await tester.pumpAndSettle();

    final put = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' &&
        r.path == '/api/admin/users/7/media-servers/jf-a/account');
    expect(put.body, {'remote_user_id': 'u2'});
    expect(find.text('Linked lr-tv on Home Jellyfin to living-room'),
        findsOneWidget);
  });

  testWidgets('unlink asks first and only forgets the link', (tester) async {
    final adapter =
        await _pumpWithMediaServer(tester, accounts: [_accountRow()]);

    await _openMenu(tester);
    await tester.tap(find.text('Unlink Home Jellyfin account'));
    await tester.pumpAndSettle();

    expect(find.text('Unlink account?'), findsOneWidget);
    expect(
      find.textContaining('Cantinarr will forget that living-room is '
          'living-room on Home Jellyfin. The account on Home Jellyfin stays '
          'as it is'),
      findsOneWidget,
    );
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    expect(adapter.requests.where((r) => r.method == 'DELETE'), isEmpty);

    await _openMenu(tester);
    await tester.tap(find.text('Unlink Home Jellyfin account'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Unlink'));
    await tester.pumpAndSettle();

    final delete =
        adapter.requests.singleWhere((r) => r.method == 'DELETE');
    expect(delete.path, '/api/admin/users/7/media-servers/jf-a/account');
    // The grant is not touched by unlinking.
    expect(
      adapter.requests.any((r) => r.path.endsWith('/instance-grants')),
      isFalse,
    );
  });

  testWidgets('a failed accounts read is named, not shown as no accounts',
      (tester) async {
    await _pumpWithMediaServer(tester, accountsFail: true);

    expect(
      find.text("Couldn't load media server accounts. Pull to refresh."),
      findsOneWidget,
    );
    // Users still render and can still be managed.
    expect(find.text('living-room'), findsOneWidget);
    expect(find.byIcon(Icons.more_vert), findsOneWidget);
  });

  testWidgets('without a media server nothing is read or offered',
      (tester) async {
    final adapter = await _pumpWithMediaServer(tester, instances: const []);

    expect(
      adapter.requests.any((r) => r.path.contains('/media-servers')),
      isFalse,
    );
    await _openMenu(tester);
    expect(find.textContaining('Home Jellyfin'), findsNothing);
    expect(find.textContaining('access off'), findsNothing);
    expect(find.textContaining('Link '), findsNothing);
  });

  testWidgets('inviting a new user from the app bar mints their connect link',
      (tester) async {
    await tester.binding.setSurfaceSize(const Size(1000, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final auth = _FakeAuthNotifier();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _CredentialsAdapter(provider: 'codex');

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => auth),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const UsersScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byTooltip('Invite a new user'));
    await tester.pumpAndSettle();
    expect(find.text('Invite a new user'), findsOneWidget);

    // An empty name generates nothing and keeps the dialog open.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Create invite'));
    await tester.pumpAndSettle();
    expect(auth.connectTokenNames, isEmpty);
    expect(find.text('Invite a new user'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'Mom');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Create invite'));
    await tester.pumpAndSettle();

    expect(auth.connectTokenNames, ['Mom']);
    expect(find.text('Invite link for Mom'), findsOneWidget);
    expect(find.textContaining('token=tok123'), findsOneWidget);
  });
}

/// Serves the Users screen's media-server reads and records every request:
/// the credentials status, the account rows (or a 503), the remote account
/// list for the link picker, and the per-user grants.
class _MediaAdapter implements HttpClientAdapter {
  _MediaAdapter({
    required this.accounts,
    required this.accountsFail,
    required this.grants,
    this.importResults = const [],
  });

  final List<Map<String, dynamic>> accounts;
  final bool accountsFail;
  final List<String> grants;
  final List<Map<String, dynamic>> importResults;
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

    Object response;
    var status = 200;
    if (path == '/api/admin/credentials') {
      response = {
        'credentials': const <String, bool>{},
        'ai': {
          'config': {'provider': 'openai', 'model': 'default'},
          'providers': const [],
        },
      };
    } else if (path == '/api/admin/media-servers/accounts') {
      if (accountsFail) {
        status = 503;
        response = {'error': 'temporarily unavailable'};
      } else {
        response = accounts;
      }
    } else if (path == '/api/admin/media-servers/jf-a/users') {
      response = {
        'users': [
          {'id': 'a1', 'name': 'jfadmin', 'is_administrator': true},
          {'id': 'u2', 'name': 'lr-tv'},
          {'id': 'u3', 'name': 'old-tablet', 'is_disabled': true},
        ],
      };
    } else if (path == '/api/admin/media-servers/jf-a/import') {
      response = {'results': importResults};
    } else if (path == '/api/admin/users/7/instance-grants') {
      response = {'jellyfin': grants};
    } else if (path == '/api/admin/users/7/media-servers/jf-a/account') {
      if (options.method == 'DELETE') {
        return ResponseBody.fromString('', 204, headers: {});
      }
      response = {
        ..._accountRow(remoteUsername: 'lr-tv'),
        'remote_user_id': body['remote_user_id'],
        'created_by_cantinarr': false,
      };
    } else {
      response = const <Object>[];
    }
    return ResponseBody.fromString(
      jsonEncode(response),
      status,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier({
    this.currentUser = false,
    this.instances = const [],
    List<UserSummary>? users,
  }) {
    if (users != null) _users = users;
  }

  final bool currentUser;

  /// Instances the admin's config lists; a jellyfin one makes the screen
  /// read and offer media-server accounts.
  final List<ServiceInstance> instances;
  var _users = const [
    UserSummary(
      id: 7,
      username: 'living-room',
      role: 'user',
      permissions: [],
      createdAt: '',
      deviceCount: 1,
      hasPassword: true,
      passwordEnabled: true,
      passkeyEnabled: false,
      hasPendingInvite: false,
    ),
  ];

  final aiAccessUpdates = <(int, bool)>[];
  final connectTokenNames = <String>[];
  int configRefreshes = 0;

  @override
  Future<AuthState> build() async => AuthState(
        connection: BackendConnection(
          serverUrl: 'https://cantinarr.example',
          accessToken: 'access',
          refreshToken: 'refresh',
          instances: instances,
        ),
        user: currentUser
            ? const UserProfile(id: 7, username: 'admin', role: 'admin')
            : null,
      );

  @override
  Future<List<UserSummary>> listUsers() async => _users;

  @override
  Future<ConnectTokenResponse> generateConnectToken(String name) async {
    connectTokenNames.add(name);
    return const ConnectTokenResponse(
      link: 'https://cantinarr.example/connect?token=tok123',
      expiresAt: '2026-09-06T00:00:00Z',
      originSource: 'external_address',
    );
  }

  @override
  Future<UserSummary> updateUserAiAccess(
    int userId,
    bool sharedAiEnabled,
  ) async {
    aiAccessUpdates.add((userId, sharedAiEnabled));
    final current = _users.single;
    final updated = UserSummary(
      id: current.id,
      username: current.username,
      role: current.role,
      permissions: current.permissions,
      createdAt: current.createdAt,
      deviceCount: current.deviceCount,
      hasPassword: current.hasPassword,
      passwordEnabled: current.passwordEnabled,
      passkeyEnabled: current.passkeyEnabled,
      hasPendingInvite: current.hasPendingInvite,
      sharedAiEnabled: sharedAiEnabled,
      child: current.child,
    );
    _users = [updated];
    return updated;
  }

  @override
  Future<void> refreshConfig() async {
    configRefreshes++;
  }
}

class _CredentialsAdapter implements HttpClientAdapter {
  _CredentialsAdapter({
    required this.provider,
    this.nextProvider,
    this.fail = false,
    this.configured,
  });

  final String provider;
  final String? nextProvider;
  final bool fail;

  /// null omits the `shared` block, exercising the older-server default.
  final bool? configured;
  int requests = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final responseProvider =
        requests++ == 0 ? provider : nextProvider ?? provider;
    if (fail) {
      return ResponseBody.fromString(
        jsonEncode({'error': 'temporarily unavailable'}),
        503,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );
    }
    return ResponseBody.fromString(
      jsonEncode({
        'credentials': const <String, bool>{},
        'ai': {
          'config': {'provider': responseProvider, 'model': 'default'},
          'providers': const [],
          if (configured != null) 'shared': {'configured': configured},
        },
      }),
      200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
