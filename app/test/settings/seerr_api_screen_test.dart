import 'dart:convert';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/seerr_api_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _admin = UserProfile(id: 1, username: 'admin', role: 'admin');

class _Adapter implements HttpClientAdapter {
  String key = '';
  bool fail = false;
  int issues = 0;
  int revokes = 0;

  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    if (fail) return ResponseBody.fromString('{"error":"down"}', 503);
    if (options.method == 'POST') {
      issues++;
      key = 'cantinarr-issued-$issues';
    }
    if (options.method == 'DELETE') {
      revokes++;
      key = '';
    }
    final body = key.isEmpty
        ? {'configured': false}
        : {
            'configured': true,
            'api_key': key,
            'issued_by': 'admin',
            'created_at': '2026-09-21T10:00:00Z',
          };
    return ResponseBody.fromString(jsonEncode(body), 200, headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType]
    });
  }

  @override
  void close({bool force = false}) {}
}

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://cantinarr.lan:8585',
          accessToken: 'access',
          refreshToken: 'refresh',
          services: AvailableServices(),
        ),
        user: _admin,
      );

  @override
  Future<void> refreshUser() async {}

  @override
  Future<void> refreshConfig() async {}
}

Future<void> _pump(WidgetTester tester, _Adapter adapter) async {
  // Tall enough that the lazily built list renders every row, so the
  // assertions below see the whole screen without scrolling.
  tester.view.physicalSize = const Size(900, 2400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.test'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(ProviderScope(
    overrides: [
      backendClientProvider.overrideWithValue(dio),
      authProvider.overrideWith(_FakeAuthNotifier.new),
    ],
    child: const MaterialApp(home: SeerrApiScreen()),
  ));
  await tester.pumpAndSettle();
}

Future<void> _tap(WidgetTester tester, String label) async {
  final finder = find.text(label);
  await tester.ensureVisible(finder);
  await tester.tap(finder);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('issues a key and shows the address the app connects with',
      (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    expect(find.text('http://cantinarr.lan:8585'), findsOneWidget);
    expect(find.text('No key has been issued. Issue one to let apps connect.'),
        findsOneWidget);
    expect(find.text('Revoke key'), findsNothing);

    await _tap(tester, 'Issue key');
    expect(adapter.issues, 1);
    // Hidden until revealed; copy works without revealing.
    expect(find.text('cantinarr-issued-1'), findsNothing);
    expect(find.textContaining('Issued by admin on 2026-09-21'), findsOneWidget);
    await _tap(tester, 'Show key');
    expect(find.text('cantinarr-issued-1'), findsOneWidget);
    await _tap(tester, 'Hide key');
    expect(find.text('cantinarr-issued-1'), findsNothing);

    String? copied;
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') {
        copied = (call.arguments as Map)['text'] as String;
      }
      return null;
    });
    // The "Key issued" toast is still up; clear it so the copy toast shows.
    tester
        .state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger))
        .clearSnackBars();
    await tester.pumpAndSettle();
    await _tap(tester, 'Copy key');
    expect(copied, 'cantinarr-issued-1');
    expect(find.text('API key copied'), findsOneWidget);
  });

  testWidgets('replacing and revoking ask first', (tester) async {
    final adapter = _Adapter()..key = 'cantinarr-existing';
    await _pump(tester, adapter);
    expect(find.text('Issue key'), findsNothing);

    await _tap(tester, 'Replace key');
    expect(find.text('Replace the key?'), findsOneWidget);
    await _tap(tester, 'Cancel');
    expect(adapter.issues, 0);
    await _tap(tester, 'Replace key');
    await tester.tap(find.widgetWithText(FilledButton, 'Replace key'));
    await tester.pumpAndSettle();
    expect(adapter.issues, 1);
    expect(find.text('Key replaced'), findsOneWidget);

    await _tap(tester, 'Revoke key');
    expect(find.text('Revoke the key?'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Revoke key'));
    await tester.pumpAndSettle();
    expect(adapter.revokes, 1);
    expect(find.text('No key has been issued. Issue one to let apps connect.'),
        findsOneWidget);
  });

  testWidgets('a failed read is explained and retryable', (tester) async {
    final adapter = _Adapter()..fail = true;
    await _pump(tester, adapter);
    expect(find.textContaining('Could not read the Seerr-compatible API key'),
        findsOneWidget);
    // Nothing to issue against until the read succeeds.
    expect(
        tester
            .widget<FilledButton>(find.widgetWithText(FilledButton, 'Issue key'))
            .onPressed,
        isNull);
  });
}
