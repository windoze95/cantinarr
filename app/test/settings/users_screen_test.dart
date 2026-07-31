import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/data/plex_admin_service.dart';
import 'package:cantinarr/features/settings/ui/users_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
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
          plexInviteConfiguredProvider.overrideWith((_) async => false),
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
          plexInviteConfiguredProvider.overrideWith((_) async => false),
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
          plexInviteConfiguredProvider.overrideWith((_) async => false),
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
          plexInviteConfiguredProvider.overrideWith((_) async => false),
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
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier({this.currentUser = false});

  final bool currentUser;
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
  int configRefreshes = 0;

  @override
  Future<AuthState> build() async => currentUser
      ? const AuthState(
          user: UserProfile(id: 7, username: 'admin', role: 'admin'),
        )
      : const AuthState();

  @override
  Future<List<UserSummary>> listUsers() async => _users;

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
  });

  final String provider;
  final String? nextProvider;
  final bool fail;
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
