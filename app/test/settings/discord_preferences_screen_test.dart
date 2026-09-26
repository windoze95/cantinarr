import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/widgets/unsaved_changes_guard.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/data/discord_notifications_service.dart';
import 'package:cantinarr/features/settings/ui/discord_preferences_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _Auth extends AuthNotifier {
  _Auth(this.admin);
  final bool admin;
  @override
  Future<AuthState> build() async => AuthState(
      user: UserProfile(id: 1, username: 'viewer', role: admin ? 'admin' : 'user'));
}

class _Adapter implements HttpClientAdapter {
  Map<String, dynamic> prefs = {'enabled': false, 'discord_ids': <String>[], 'events': <String, bool>{}};
  bool blocked = false;
  bool fail = false;
  int tests = 0;
  int saves = 0;

  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async {
    if (fail) return ResponseBody.fromString('private error text', 503);
    Map<String, dynamic> data;
    if (options.method == 'POST') {
      tests++;
      data = {'status': 'sent', 'detail': 'Mention test delivered.'};
    } else {
      if (options.method == 'PUT') {
        saves++;
        prefs = Map<String, dynamic>.from(options.data as Map);
      }
      data = {...prefs,
        'allowed_events': {for (final key in discordEventLabels.keys) key: !blocked},
        if (blocked) 'blocked_reason': 'An administrator must enable Discord mentions.',
        if (!blocked && prefs['enabled'] != true) 'blocked_reason': 'Enable mentions for your account.',
      };
    }
    return ResponseBody.fromString(jsonEncode(data), 200,
        headers: {Headers.contentTypeHeader: [Headers.jsonContentType]});
  }
  @override
  void close({bool force = false}) {}
}

Future<void> _pump(WidgetTester tester, _Adapter adapter, {bool admin = false}) async {
  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.test'))..httpClientAdapter = adapter;
  await tester.pumpWidget(ProviderScope(overrides: [
    backendClientProvider.overrideWithValue(dio),
    authProvider.overrideWith(() => _Auth(admin)),
  ], child: const MaterialApp(home: DiscordPreferencesScreen())));
  await tester.pumpAndSettle();
}

Future<void> _tap(WidgetTester tester, String text) async {
  final finder = find.text(text);
  await tester.scrollUntilVisible(finder, 180, scrollable: find.byType(Scrollable).first);
  await tester.ensureVisible(finder);
  await tester.tap(finder);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('requester saves several IDs and can test only saved preferences', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    expect(find.text('Server Discord Notifications'), findsNothing);
    expect(find.textContaining('not direct messages'), findsOneWidget);
    await tester.enterText(find.byType(TextField), '123456789012345678, 234567890123456789');
    await _tap(tester, 'Mention me in Discord');
    await _tap(tester, 'Requested content available');
    await _tap(tester, 'Save');
    expect(adapter.prefs['discord_ids'], ['123456789012345678', '234567890123456789']);
    expect((adapter.prefs['events'] as Map)['request_available'], isTrue);
    expect(tester.widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard)).hasChanges(), isFalse);
    await _tap(tester, 'Test my mentions');
    expect(adapter.tests, 1);
    expect(find.text('Test: Delivered'), findsOneWidget);
  });

  testWidgets('server mute is explained and refresh keeps a draft on a phone', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final adapter = _Adapter()..blocked = true;
    await _pump(tester, adapter);
    expect(find.text('An administrator must enable Discord mentions.'), findsOneWidget);
    await tester.enterText(find.byType(TextField), '123456789012345678');
    await tester.tap(find.byTooltip('Refresh Discord status'));
    await tester.pumpAndSettle();
    expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, '123456789012345678');
    await tester.scrollUntilVisible(find.text('Test my mentions'), 180,
        scrollable: find.byType(Scrollable).first);
    expect(tester.widget<OutlinedButton>(find.byType(OutlinedButton)).onPressed, isNull);
    expect(tester.takeException(), isNull);
  });

  testWidgets('invalid IDs cannot save and admins can discover server controls', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter, admin: true);
    expect(find.text('Server Discord Notifications'), findsOneWidget);
    await tester.enterText(find.byType(TextField), '@everyone');
    await _tap(tester, 'Save');
    expect(adapter.saves, 0);
    expect(tester.widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard)).hasChanges(), isTrue);
  });
}
