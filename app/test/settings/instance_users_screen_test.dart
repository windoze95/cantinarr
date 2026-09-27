import 'dart:convert';
import 'dart:typed_data';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/instance_users_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

UserSummary user(int id, String name, {bool admin = false}) => UserSummary(
      id: id,
      username: name,
      role: admin ? 'admin' : 'user',
      permissions: const [],
      createdAt: '',
      deviceCount: 0,
      hasPassword: false,
      passwordEnabled: false,
      passkeyEnabled: false,
      hasPendingInvite: false,
    );

class FakeAuth extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
            serverUrl: 'http://example',
            accessToken: 'a',
            refreshToken: 'r',
            instanceAssignments: true,
            instances: [
              ServiceInstance(
                  id: 'books',
                  serviceType: 'chaptarr',
                  name: 'Books',
                  isDefault: true),
              ServiceInstance(
                  id: 'other-books',
                  serviceType: 'chaptarr',
                  name: 'Other Books')
            ]),
        user: UserProfile(id: 4, username: 'Administrator', role: 'admin'),
      );
  @override
  Future<List<UserSummary>> listUsers() async => [
        user(1, 'Alice'),
        user(2, 'Alex'),
        user(3, 'Bob'),
        user(4, 'Administrator', admin: true)
      ];
  @override
  Future<void> refreshConfig() async {}
}

class Adapter implements HttpClientAdapter {
  final assigned = <int>{3};
  final changes = <Map<String, dynamic>>[];
  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    if (options.method == 'PATCH') {
      final bytes = await requestStream!.expand((e) => e).toList();
      final body = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
      changes.add(body);
      for (final id in body['user_ids'] as List) {
        if (body['action'] == 'add') {
          assigned.add(id as int);
        } else {
          assigned.remove(id);
        }
      }
    }
    return ResponseBody.fromString(
        jsonEncode([
          for (final id in [1, 2, 3, 4])
            {
              'user_id': id,
              'assigned':
                  options.path.contains('/books/') && assigned.contains(id),
              'preferred_instance_id': id == 3 ? 'books' : '',
              'effective_default_id': assigned.contains(id) ? 'books' : ''
            },
        ]),
        200,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType]
        });
  }

  @override
  void close({bool force = false}) {}
}

Future<Adapter> pump(WidgetTester tester,
    {Size size = const Size(900, 1200), bool otherInstance = false}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final adapter = Adapter();
  final dio = Dio(BaseOptions(baseUrl: 'http://example'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(ProviderScope(
      overrides: [
        authProvider.overrideWith(FakeAuth.new),
        backendClientProvider.overrideWithValue(dio)
      ],
      child: MaterialApp(
          theme: AppTheme.dark,
          home: InstanceUsersScreen(
              instanceId: otherInstance ? 'other-books' : 'books',
              instanceName: otherInstance ? 'Other Books' : 'Books'))));
  await tester.pumpAndSettle();
  return adapter;
}

void main() {
  testWidgets('filtered bulk adds affect only matching selected regular users',
      (tester) async {
    final adapter = await pump(tester);
    await tester.enterText(find.byType(TextField), 'Al');
    await tester.pump();
    await tester.tap(find.text('Select all matching'));
    await tester.pumpAndSettle();
    expect(find.text('2 matching · 2 selected'), findsOneWidget);
    await tester.tap(find.text('Add selected (2)'));
    await tester.pumpAndSettle();
    expect(adapter.changes.single, {
      'action': 'add',
      'user_ids': [1, 2]
    });
    expect(adapter.assigned, {1, 2, 3});
    await tester.enterText(find.byType(TextField), '');
    await tester.pumpAndSettle();
    await tester.tap(find.text('Select all matching'));
    await tester.pumpAndSettle();
    expect(find.text('4 matching · 3 selected'), findsOneWidget);
    await tester.enterText(find.byType(TextField), 'Bob');
    await tester.pumpAndSettle();
    expect(find.text('1 matching · 0 selected'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
  testWidgets(
      'removal previews cleared preferences and preserves unselected users',
      (tester) async {
    final adapter = await pump(tester);
    await tester.enterText(find.byType(TextField), 'Bob');
    await tester.tap(find.text('Select all matching'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Remove selected (1)'));
    await tester.pumpAndSettle();
    expect(find.textContaining('1 saved preferences will be cleared'),
        findsOneWidget);
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(adapter.changes, isEmpty);
    await tester.tap(find.text('Remove selected (1)'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Remove users'));
    await tester.pumpAndSettle();
    expect(adapter.changes.single, {
      'action': 'remove',
      'user_ids': [3]
    });
    expect(find.text('Unassigned'), findsOneWidget);
    expect(find.text('Request destination: None'), findsOneWidget);
  });
  testWidgets('access remains visible independently of bulk selection',
      (tester) async {
    final adapter = await pump(tester);
    final bob = find.byKey(const ValueKey('assignment-3'));
    final bobCheckbox =
        find.descendant(of: bob, matching: find.byType(Checkbox));
    expect(
        find.text(
            '1 assigned · 2 unassigned · 1 administrator in these results'),
        findsOneWidget);
    expect(find.descendant(of: bob, matching: find.text('Assigned')),
        findsOneWidget);
    expect(
        find.descendant(
            of: bob, matching: find.text('Request destination: Books')),
        findsOneWidget);
    expect(tester.widget<Checkbox>(bobCheckbox).value, isFalse);
    expect(tester.widget<Checkbox>(bobCheckbox).semanticLabel,
        'Select Bob for bulk changes');
    await tester.ensureVisible(bob);
    await tester.tap(bobCheckbox);
    await tester.pumpAndSettle();
    expect(tester.widget<Checkbox>(bobCheckbox).value, isTrue);
    expect(find.descendant(of: bob, matching: find.text('Assigned')),
        findsOneWidget);
    expect(adapter.changes, isEmpty);
    final admin = find.byKey(const ValueKey('assignment-4'));
    expect(find.descendant(of: admin, matching: find.byType(Checkbox)),
        findsNothing);
    expect(
        find.descendant(of: admin, matching: find.text('Administrator access')),
        findsOneWidget);
    expect(tester.widget<ListTile>(admin).enabled, isTrue);
  });
  testWidgets(
      'sibling request destination does not imply access to this instance',
      (tester) async {
    await pump(tester, otherInstance: true);
    final bob = find.byKey(const ValueKey('assignment-3'));
    expect(find.text('Current access to Other Books'), findsOneWidget);
    expect(find.descendant(of: bob, matching: find.text('Unassigned')),
        findsOneWidget);
    expect(
        find.descendant(
            of: bob, matching: find.text('Request destination: Books')),
        findsOneWidget);
  });
  testWidgets('filters fit a phone viewport', (tester) async {
    await pump(tester, size: const Size(390, 844));
    await tester.ensureVisible(find.text('Select all matching'));
    await tester.scrollUntilVisible(
        find.byKey(const ValueKey('assignment-3')), 250,
        scrollable: find.byType(Scrollable).first);
    expect(tester.takeException(), isNull);
  });
}
