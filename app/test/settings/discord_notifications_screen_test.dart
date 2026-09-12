import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/widgets/unsaved_changes_guard.dart';
import 'package:cantinarr/features/settings/ui/discord_notifications_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _Adapter implements HttpClientAdapter {
  bool enabled = false;
  bool includeAutoApproved = false;
  bool hasWebhook = true;
  bool fail = false;
  String testStatus = 'sent';
  Map<String, dynamic>? saved;
  Map<String, dynamic>? tested;
  int removes = 0;
  final List<Map<String, dynamic>> recent = [];

  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    if (fail) return ResponseBody.fromString('secret_webhook_token', 503);
    Map<String, dynamic> body = {};
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    if (options.method == 'PUT') {
      saved = body;
      enabled = body['enabled'] == true;
      includeAutoApproved = body['include_auto_approved'] == true;
      hasWebhook = hasWebhook || body.containsKey('webhook_url');
    }
    if (options.method == 'DELETE') {
      removes++;
      enabled = false;
      hasWebhook = false;
    }
    if (options.method == 'POST') {
      tested = body;
      return _json(
          {'status': testStatus, 'detail': 'Test result from Discord.'});
    }
    return _json({
      'enabled': enabled,
      'has_webhook': hasWebhook,
      'include_auto_approved': includeAutoApproved,
      'recent': recent
    });
  }

  ResponseBody _json(Map<String, dynamic> data) =>
      ResponseBody.fromString(jsonEncode(data), 200, headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType]
      });

  @override
  void close({bool force = false}) {}
}

Future<void> _pump(WidgetTester tester, _Adapter adapter) async {
  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.test'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(ProviderScope(
    overrides: [backendClientProvider.overrideWithValue(dio)],
    child: const MaterialApp(home: DiscordNotificationsScreen()),
  ));
  await tester.pumpAndSettle();
}

Future<void> _tap(WidgetTester tester, String title) async {
  final finder = find.text(title);
  if (finder.evaluate().isEmpty) {
    tester
        .state<ScrollableState>(find.byType(Scrollable).first)
        .position
        .jumpTo(0);
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(finder, 180,
        scrollable: find.byType(Scrollable).first);
  }
  await tester.ensureVisible(finder);
  await tester.tap(finder);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('save preserves stored secret and explains sharing',
      (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    expect(find.textContaining('requester usernames'), findsOneWidget);
    final field = tester.widget<TextField>(find.byType(TextField));
    expect(field.obscureText, isTrue);
    expect(field.controller!.text, isEmpty);
    await _tap(tester, 'Send new requests to Discord');
    await _tap(tester, 'Save');
    expect(adapter.saved, {'enabled': true, 'include_auto_approved': false});
    expect(field.controller!.text, isEmpty);
    expect(
        tester
            .widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard))
            .hasChanges(),
        isFalse);
  });

  testWidgets('test uses draft without saving or clearing it', (tester) async {
    final adapter = _Adapter()..testStatus = 'unconfirmed';
    await _pump(tester, adapter);
    await tester.enterText(find.byType(TextField),
        'https://discord.com/api/webhooks/1/entered_token');
    await _tap(tester, 'Send test message');
    expect(adapter.tested,
        {'webhook_url': 'https://discord.com/api/webhooks/1/entered_token'});
    expect(adapter.saved, isNull);
    expect(adapter.enabled, isFalse);
    expect(find.text('Test: Delivery unconfirmed'), findsOneWidget);
    expect(
        tester
            .widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard))
            .hasChanges(),
        isTrue);
    await _tap(tester, 'Save');
    expect(tester.widget<TextField>(find.byType(TextField)).controller!.text,
        isEmpty);
  });

  testWidgets('automatic request alerts are optional and saved explicitly',
      (tester) async {
    final adapter = _Adapter()..enabled = true;
    await _pump(tester, adapter);
    await _tap(tester, 'Include automatically approved requests');
    expect(adapter.includeAutoApproved, isFalse);
    expect(
        tester
            .widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard))
            .hasChanges(),
        isTrue);
    await _tap(tester, 'Save');
    expect(adapter.saved, {'enabled': true, 'include_auto_approved': true});
    expect(adapter.includeAutoApproved, isTrue);
    await _tap(tester, 'Include automatically approved requests');
    await _tap(tester, 'Save');
    expect(adapter.includeAutoApproved, isFalse);
    expect(adapter.enabled, isTrue);
  });

  testWidgets('blank test uses stored URL and remove clears destination',
      (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    await _tap(tester, 'Send test message');
    expect(adapter.tested, isEmpty);
    await _tap(tester, 'Remove webhook');
    expect(adapter.removes, 1);
    expect(find.text('Webhook URL'), findsOneWidget);
    expect(adapter.enabled, isFalse);
  });

  testWidgets('missing webhook and failures are visible without secrets',
      (tester) async {
    final adapter = _Adapter()..hasWebhook = false;
    await _pump(tester, adapter);
    await _tap(tester, 'Send new requests to Discord');
    await _tap(tester, 'Save');
    expect(find.text('Add a webhook URL before enabling notifications.'),
        findsOneWidget);
    expect(adapter.saved, isNull);
    await tester.enterText(find.byType(TextField), 'a new value');
    adapter.fail = true;
    await _tap(tester, 'Save');
    expect(find.textContaining('Could not save Discord settings.'),
        findsOneWidget);
    expect(find.textContaining('secret_webhook_token'), findsNothing);
    expect(tester.widget<TextField>(find.byType(TextField)).controller!.text,
        'a new value');
  });

  testWidgets(
      'phone layout shows delivery uncertainty and preserves draft on refresh',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final adapter = _Adapter()
      ..recent.add({
        'request_id': 42,
        'status': 'unconfirmed',
        'detail': 'Discord may have received this alert.',
        'updated_at': 1800000000
      });
    await _pump(tester, adapter);
    await tester.enterText(find.byType(TextField), 'draft webhook');
    await tester.tap(find.byTooltip('Refresh delivery status'));
    await tester.pumpAndSettle();
    expect(tester.widget<TextField>(find.byType(TextField)).controller!.text,
        'draft webhook');
    await tester.scrollUntilVisible(
        find.text('Request #42 · Delivery unconfirmed'), 300,
        scrollable: find.byType(Scrollable).first);
    expect(find.textContaining('Discord may have received'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
