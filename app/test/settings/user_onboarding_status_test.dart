import 'dart:convert';
import 'dart:typed_data';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/user_onboarding_status.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class OnboardingAuth extends AuthNotifier {
  final bool pendingInvite;
  OnboardingAuth({this.pendingInvite = true});
  @override
  Future<AuthState> build() async => const AuthState(
        user: UserProfile(id: 1, username: 'admin', role: 'admin'),
        connection: BackendConnection(
            serverUrl: 'http://test',
            accessToken: 'a',
            refreshToken: 'r',
            instanceAssignments: true,
            instances: [
              ServiceInstance(
                  id: 'plex', serviceType: 'plex', name: 'Home Plex'),
              ServiceInstance(
                  id: 'movies', serviceType: 'radarr', name: 'Movies'),
              ServiceInstance(
                  id: 'books', serviceType: 'chaptarr', name: 'Books'),
            ]),
      );
  @override
  Future<List<UserSummary>> listUsers() async => [
        UserSummary(
          id: 2,
          username: 'reader',
          role: 'user',
          permissions: ['media:request'],
          createdAt: '',
          deviceCount: 0,
          hasPassword: false,
          passwordEnabled: false,
          passkeyEnabled: false,
          hasPendingInvite: pendingInvite,
        )
      ];
}

class OnboardingAdapter implements HttpClientAdapter {
  bool ssoOnly = false;
  bool failGrants = false;
  bool failPlex = false;
  bool failBooksOnce = false;
  bool plexEnabled = false;
  final changes = <Map<String, dynamic>>[];
  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    Object body;
    var status = 200;
    if (options.method == 'PATCH') {
      changes.add({
        'path': options.path,
        ...Map<String, dynamic>.from(options.data as Map)
      });
      if (options.path.contains('/books/') && failBooksOnce) {
        failBooksOnce = false;
        status = 503;
      }
      body = [];
    } else if (options.path.endsWith('/auth/status')) {
      body = {
        'needs_setup': false,
        'sso_only': ssoOnly,
        'sso_available': ssoOnly,
        'sso_provider': 'Household SSO'
      };
    } else if (options.path.endsWith('/instance-grants')) {
      body = {
        'radarr': ['movies']
      };
      if (failGrants) status = 503;
    } else if (options.path.endsWith('/accounts')) {
      body = [
        {
          'user_id': 2,
          'instance_id': 'plex',
          'instance_name': 'Home Plex',
          'service_type': 'plex',
          'remote_user_id': 'reader@example.test',
          'username': 'reader',
          'manage_access': false,
          'granted': true,
          'verified': false
        }
      ];
    } else if (options.path.endsWith('/plex-auth')) {
      body = {'enabled': plexEnabled, 'auto_create': false};
      if (failPlex) status = 503;
    } else if (options.path.endsWith('/plex')) {
      body = {
        'identities': [
          {'plex_account_id': 42}
        ]
      };
    } else {
      throw StateError('Unexpected ${options.method} ${options.path}');
    }
    return ResponseBody.fromString(jsonEncode(body), status, headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType]
    });
  }

  @override
  void close({bool force = false}) {}
}

Future<void> pump(WidgetTester tester, OnboardingAdapter adapter,
    {bool assignment = false,
    bool pendingInvite = true,
    String? invitationLink = 'http://example.test/connect?token=fixture',
    VoidCallback? onIssueInvitation}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://test'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(ProviderScope(
      overrides: [
        authProvider
            .overrideWith(() => OnboardingAuth(pendingInvite: pendingInvite)),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp(
          theme: AppTheme.dark,
          home: Scaffold(
              body: Consumer(
            builder: (context, ref, _) => ref.watch(authProvider).hasValue
                ? assignment
                    ? TextButton(
                        onPressed: () =>
                            assignRequestLibraries(context, ref, [2, 3]),
                        child: const Text('Assign'))
                    : SingleChildScrollView(
                        child: UserOnboardingStatus(
                            userId: 2,
                            invitationLink: invitationLink,
                            onIssueInvitation: onIssueInvitation))
                : const SizedBox(),
          )))));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('missing invitation exposes an explicit recovery action',
      (tester) async {
    var issued = false;
    await pump(tester, OnboardingAdapter(),
        pendingInvite: false,
        invitationLink: null,
        onIssueInvitation: () => issued = true);
    expect(find.textContaining('No current invitation or connected device'),
        findsOneWidget);
    await tester.tap(find.text('Issue invitation'));
    expect(issued, isTrue);
  });
  testWidgets(
      'identity, login switch, assignments and live access remain separate',
      (tester) async {
    await tester.binding.setSurfaceSize(const Size(390, 1800));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    await pump(tester, OnboardingAdapter());
    expect(find.text('Plex sign-in identity confirmed.'), findsOneWidget);
    expect(find.text('Plex sign-in is off.'), findsOneWidget);
    expect(find.text('Movies: Movies'), findsOneWidget);
    expect(find.text('Books: No libraries assigned'), findsOneWidget);
    expect(find.textContaining('Server access unconfirmed'), findsOneWidget);
    expect(
        find.textContaining('does not make its password a Cantinarr password'),
        findsOneWidget);
    expect(tester.takeException(), isNull);
  });
  testWidgets(
      'required SSO explains the invitation without advertising Plex as sufficient',
      (tester) async {
    await pump(
        tester,
        OnboardingAdapter()
          ..ssoOnly = true
          ..plexEnabled = true);
    expect(
        find.text('Single sign-on required: Household SSO.'), findsOneWidget);
    expect(find.textContaining('recipient continues with Household SSO'),
        findsOneWidget);
    expect(
        find.text('Plex does not satisfy the required single sign-on policy.'),
        findsOneWidget);
    expect(find.text('Copy invitation'), findsOneWidget);
  });
  testWidgets('failed reads do not claim an absent assignment or ready sign-in',
      (tester) async {
    await pump(
        tester,
        OnboardingAdapter()
          ..failGrants = true
          ..failPlex = true);
    expect(find.textContaining('Could not load request assignments'),
        findsOneWidget);
    expect(find.text('Movies: No libraries assigned'), findsNothing);
    expect(
        find.textContaining('Could not verify Plex sign-in'), findsOneWidget);
    expect(find.text('Plex sign-in is off.'), findsNothing);
  });
  testWidgets('bulk assignment adds only and preserves successes during retry',
      (tester) async {
    final adapter = OnboardingAdapter()..failBooksOnce = true;
    await pump(tester, adapter, assignment: true);
    await tester.tap(find.text('Assign'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Movies'));
    await tester.tap(find.text('Books'));
    await tester.pump();
    await tester.tap(find.text('Add selected libraries'));
    await tester.pumpAndSettle();
    expect(adapter.changes.length, 2);
    await tester.tap(find.text('Add selected libraries'));
    await tester.pumpAndSettle();
    expect(adapter.changes.length, 3);
    expect(
        adapter.changes
            .where((c) => (c['path'] as String).contains('/movies/'))
            .length,
        1);
    for (final change in adapter.changes) {
      expect(change['action'], 'add');
      expect(change['user_ids'], [2, 3]);
    }
  });
}
