import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/unsaved_changes_guard.dart';
import 'package:cantinarr/features/settings/ui/discord_notifications_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _Adapter implements HttpClientAdapter {
  // A current server always reports its event choices; [legacy] mimics the
  // releases that only knew the original fields and rejected any others.
  _Adapter({bool legacy = false})
      : settings = legacy ? {} : {'events': {'request_pending': true}};
  bool enabled = false;
  bool includeAutoApproved = false;
  bool hasWebhook = true;
  bool fail = false;
  bool notFound = false;
  String testStatus = 'sent';
  Map<String, dynamic>? saved;
  Map<String, dynamic>? tested;
  int removes = 0;
  Map<String, dynamic> settings;
  final List<Map<String, dynamic>> recent = [];

  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    if (fail) return ResponseBody.fromString('secret_webhook_token', 503);
    if (notFound) return ResponseBody.fromString('', 404);
    Map<String, dynamic> body = {};
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    if (options.method == 'PUT') {
      saved = body;
      settings = body;
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
      ...settings,
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
    child: MaterialApp(theme: AppTheme.dark, home: const DiscordNotificationsScreen()),
  ));
  await tester.pumpAndSettle();
}

final _webhookField = find.byWidgetPredicate((widget) => widget is TextField && widget.obscureText);

Future<void> _show(WidgetTester tester, Finder finder) async {
  tester.state<ScrollableState>(find.byType(Scrollable).first).position.jumpTo(0);
  await tester.pumpAndSettle();
  await tester.scrollUntilVisible(finder, 160, scrollable: find.byType(Scrollable).first);
  await Scrollable.ensureVisible(tester.element(finder), alignment: 0.4);
  await tester.pumpAndSettle();
}

Future<void> _tap(WidgetTester tester, String title) async {
  await tester.pump(const Duration(seconds: 5));
  final finder = find.text(title);
  if (title == 'Include automatically approved requests') {
    await _show(tester, find.text('Events'));
    if (finder.evaluate().isEmpty) {
      await tester.tap(find.text('Events'));
      await tester.pumpAndSettle();
    }
  }
  await _show(tester, finder);
  await tester.tap(finder);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('phone expansion fields keep independent page storage and save drafts',
      (tester) async {
    tester.view.physicalSize = const Size(320, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final adapter = _Adapter()..enabled = true;
    await _pump(tester, adapter);
    await _tap(tester, 'Role mentions');
    final role = find.byKey(const PageStorageKey('discord-field-Discord role ID'));
    await _show(tester, role);
    await tester.enterText(role, '345678901234567890');
    await _tap(tester, 'Thread and appearance');
    final thread = find.byKey(const PageStorageKey('discord-field-Discord thread ID'));
    await _show(tester, thread);
    await tester.enterText(thread, '456789012345678901');
    await _tap(tester, 'Thread and appearance');
    await _tap(tester, 'Thread and appearance');
    await _show(tester, thread);
    expect(tester.widget<TextField>(thread).controller!.text, '456789012345678901');
    await _tap(tester, 'Save');
    expect(adapter.saved!['role_id'], '345678901234567890');
    expect(adapter.saved!['thread_id'], '456789012345678901');
    expect(tester.takeException(), isNull);
  });

  testWidgets('save preserves stored secret and explains sharing',
      (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    expect(find.textContaining('requester usernames'), findsOneWidget);
    await _show(tester, _webhookField);
    final field = tester.widget<TextField>(_webhookField);
    expect(field.obscureText, isTrue);
    expect(field.controller!.text, isEmpty);
    await _tap(tester, 'Send notifications to Discord');
    await _tap(tester, 'Save');
    expect(adapter.saved!['enabled'], isTrue);
    expect(adapter.saved!['events']['request_auto_approved'], isFalse);
    expect(adapter.saved!.containsKey('webhook_url'), isFalse);
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
    await _show(tester, _webhookField);
    await tester.enterText(_webhookField,
        'https://discord.com/api/webhooks/1/entered_token');
    await _tap(tester, 'Send test message');
    expect(adapter.tested!['webhook_url'], 'https://discord.com/api/webhooks/1/entered_token');
    expect(adapter.tested!['events'], isNotEmpty);
    expect(adapter.saved, isNull);
    expect(adapter.enabled, isFalse);
    expect(find.text('Test: Delivery unconfirmed'), findsOneWidget);
    expect(
        tester
            .widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard))
            .hasChanges(),
        isTrue);
    await _tap(tester, 'Save');
    await _show(tester, _webhookField);
    expect(tester.widget<TextField>(_webhookField).controller!.text,
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
    expect(adapter.saved!['enabled'], isTrue);
    expect(adapter.saved!['events']['request_auto_approved'], isTrue);
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
    expect(adapter.tested!.containsKey('webhook_url'), isFalse);
    await _tap(tester, 'Remove webhook');
    expect(adapter.removes, 1);
    await _show(tester, _webhookField);
    expect(find.text('Webhook URL'), findsOneWidget);
    expect(adapter.enabled, isFalse);
  });

  testWidgets('missing webhook and failures are visible without secrets',
      (tester) async {
    final adapter = _Adapter()..hasWebhook = false;
    await _pump(tester, adapter);
    await _tap(tester, 'Send notifications to Discord');
    await _tap(tester, 'Save');
    expect(find.text('Add a webhook URL before enabling notifications.'),
        findsOneWidget);
    expect(adapter.saved, isNull);
    await _show(tester, _webhookField);
    await tester.enterText(_webhookField, 'a new value');
    adapter.fail = true;
    await _tap(tester, 'Save');
    expect(find.textContaining('Could not save Discord settings.'),
        findsOneWidget);
    expect(find.textContaining('secret_webhook_token'), findsNothing);
    await _show(tester, _webhookField);
    expect(tester.widget<TextField>(_webhookField).controller!.text,
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
    await _show(tester, _webhookField);
    await tester.enterText(_webhookField, 'draft webhook');
    await tester.tap(find.byTooltip('Refresh delivery status'));
    await tester.pumpAndSettle();
    await _show(tester, _webhookField);
    expect(tester.widget<TextField>(_webhookField).controller!.text,
        'draft webhook');
    await tester.scrollUntilVisible(
        find.text('Request #42 · Delivery unconfirmed'), 300,
        scrollable: find.byType(Scrollable).first);
    expect(find.textContaining('Discord may have received'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('older servers get only the original fields and controls',
      (tester) async {
    final adapter = _Adapter(legacy: true)..enabled = true;
    await _pump(tester, adapter);
    expect(find.text('Events'), findsNothing);
    expect(find.text('Role mentions'), findsNothing);
    expect(find.textContaining('need a newer server'), findsOneWidget);
    final auto = find.text('Include automatically approved requests');
    await _show(tester, auto);
    await tester.tap(auto);
    await tester.pumpAndSettle();
    await _tap(tester, 'Save');
    expect(adapter.saved, {'enabled': true, 'include_auto_approved': true});
    await _tap(tester, 'Send test message');
    expect(adapter.tested, isEmpty);
  });

  testWidgets('a server without Discord settings is named', (tester) async {
    final adapter = _Adapter()..notFound = true;
    await _pump(tester, adapter);
    expect(find.textContaining('need a newer server'), findsOneWidget);
  });

  testWidgets('toggling an event on and back off leaves no draft',
      (tester) async {
    final adapter = _Adapter()..enabled = true;
    await _pump(tester, adapter);
    bool changed() => tester
        .widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard))
        .hasChanges();
    await _tap(tester, 'Events');
    await _tap(tester, 'Request denied');
    expect(changed(), isTrue);
    await _tap(tester, 'Request denied');
    expect(changed(), isFalse);
  });
}
