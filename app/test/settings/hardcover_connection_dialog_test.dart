import 'dart:async';
import 'dart:convert';

import 'package:cantinarr/features/settings/data/hardcover_connection.dart';
import 'package:cantinarr/features/settings/data/instance_api_service.dart';
import 'package:cantinarr/features/settings/ui/hardcover_connection_dialog.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _Adapter implements HttpClientAdapter {
  String status = 'pending';
  String message = '';
  int interval = 5;
  int checks = 0;
  int cancels = 0;
  bool missing = false;
  bool cancelFails = false;
  Completer<void>? beginWait;
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? stream,
      Future<void>? cancel) async {
    final begin = options.path.endsWith('/begin');
    if (begin) await beginWait?.future;
    if (options.method == 'GET') checks++;
    if (options.method == 'DELETE') cancels++;
    if (options.method == 'GET' && missing) {
      return ResponseBody.fromString('{"error":"flow not found"}', 404);
    }
    if (options.method == 'DELETE' && cancelFails) {
      return ResponseBody.fromString('{"error":"offline"}', 502);
    }
    return ResponseBody.fromString(
        jsonEncode({
          'flow_id': 'local-flow',
          'status': options.method == 'DELETE'
              ? 'cancelled'
              : begin
                  ? 'pending'
                  : status,
          'user_code': 'ABCD-EFGH',
          'verification_uri': 'https://hardcover.app/link',
          'expires_at': DateTime.now()
              .add(const Duration(minutes: 10))
              .toUtc()
              .toIso8601String(),
          'interval': interval,
          'connection_id': status == 'connected' ? 'connection-1' : '',
          'error': message,
        }),
        200,
        headers: {
          'content-type': ['application/json']
        });
  }

  @override
  void close({bool force = false}) {}
}

Future<void> _show(WidgetTester tester, _Adapter adapter,
    {Future<bool> Function(Uri)? launch,
    Size size = const Size(500, 900),
    double scale = 1}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final service = InstanceApiService(
      backendDio: Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter);
  await tester.pumpWidget(ProviderScope(
      overrides: [
        hardcoverUrlLauncherProvider
            .overrideWithValue(launch ?? (_) async => true),
      ],
      child: MaterialApp(
        builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: TextScaler.linear(scale)),
            child: child!),
        home: Scaffold(
            body: Builder(
                builder: (context) => TextButton(
                      onPressed: () async {
                        final result = await showDialog<HardcoverDeviceFlow>(
                            context: context,
                            barrierDismissible: false,
                            builder: (_) => HardcoverConnectionDialog(
                                service: service, instanceId: 'books'));
                        if (context.mounted) {
                          ScaffoldMessenger.of(context).showSnackBar(SnackBar(
                              content:
                                  Text(result?.connectionId ?? 'Cancelled')));
                        }
                      },
                      child: const Text('Connect'),
                    ))),
      )));
  await tester.tap(find.text('Connect'));
  await tester.pump();
  if (adapter.beginWait == null) await tester.pumpAndSettle();
}

void main() {
  test('rejects unsafe provider URLs and preserves a long polling interval',
      () {
    for (final url in [
      'https://hardcover.app.evil.test/link',
      'https://hardcover.app:444/link',
      'https://user@hardcover.app/link',
      'http://hardcover.app/link'
    ]) {
      expect(
          () => HardcoverDeviceFlow.fromJson(
              {'flow_id': 'f', 'status': 'pending', 'verification_uri': url}),
          throwsFormatException);
    }
    final flow = HardcoverDeviceFlow.fromJson({
      'flow_id': 'f',
      'status': 'pending',
      'user_code': 'CODE',
      'verification_uri': 'https://hardcover.app/link',
      'expires_at': '2030-01-01T00:00:00Z',
      'interval': 120
    });
    expect(flow.interval, const Duration(seconds: 120));
  });

  testWidgets('shows code, opens Hardcover, copies only code, and cancels',
      (tester) async {
    final adapter = _Adapter();
    Uri? opened;
    String? copied;
    tester.binding.defaultBinaryMessenger
        .setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') {
        copied = (call.arguments as Map)['text'] as String;
      }
      return null;
    });
    addTearDown(() => tester.binding.defaultBinaryMessenger
        .setMockMethodCallHandler(SystemChannels.platform, null));
    await _show(tester, adapter, launch: (uri) async {
      opened = uri;
      return true;
    });
    expect(find.text('ABCD-EFGH'), findsOneWidget);
    await tester.tap(find.text('Open Hardcover'));
    await tester.pump();
    expect(opened.toString(), 'https://hardcover.app/link');
    await tester.tap(find.text('Copy code'));
    await tester.pump();
    expect(copied, 'ABCD-EFGH');
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(adapter.cancels, 1);
    expect(find.byType(HardcoverConnectionDialog), findsNothing);
  });

  testWidgets('browser failure keeps code and manual instructions visible',
      (tester) async {
    final adapter = _Adapter();
    await _show(tester, adapter,
        launch: (_) async => throw Exception('unavailable'));
    await tester.tap(find.text('Open Hardcover'));
    await tester.pump();
    expect(find.textContaining('Visit hardcover.app/link'), findsOneWidget);
    expect(find.text('ABCD-EFGH'), findsOneWidget);
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
  });

  testWidgets('polls automatically and respects an increased interval',
      (tester) async {
    final adapter = _Adapter();
    await _show(tester, adapter);
    adapter.interval = 10;
    await tester.pump(const Duration(seconds: 5));
    await tester.pump();
    expect(adapter.checks, 1);
    await tester.pump(const Duration(seconds: 5));
    expect(adapter.checks, 1);
    adapter.status = 'connected';
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    expect(adapter.checks, 2);
    expect(find.text('connection-1'), findsOneWidget);
    expect(adapter.cancels, 0);
  });

  testWidgets('resume checks immediately without waiting for the next poll',
      (tester) async {
    final adapter = _Adapter();
    await _show(tester, adapter);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    adapter.status = 'connected';
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(adapter.checks, 1);
    expect(find.text('connection-1'), findsOneWidget);
  });

  for (final status in ['expired', 'denied', 'superseded', 'failed']) {
    testWidgets('$status offers an actionable retry', (tester) async {
      final adapter = _Adapter();
      await _show(tester, adapter);
      adapter.status = status;
      adapter.message = 'Start again after $status.';
      await tester.pump(const Duration(seconds: 5));
      await tester.pumpAndSettle();
      expect(find.text(adapter.message), findsOneWidget);
      expect(find.text('Start again'), findsOneWidget);
      expect(find.text('ABCD-EFGH'), findsNothing);
      await tester.tap(find.text('Start again'));
      await tester.pumpAndSettle();
      expect(find.text('ABCD-EFGH'), findsOneWidget);
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
    });
  }

  testWidgets('a server restart offers a new sign-in', (tester) async {
    final adapter = _Adapter();
    await _show(tester, adapter);
    adapter.missing = true;
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    expect(find.textContaining('server may have restarted'), findsOneWidget);
    expect(find.text('Start again'), findsOneWidget);
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
  });

  testWidgets('failed cancellation remains visible and can be retried',
      (tester) async {
    final adapter = _Adapter()..cancelFails = true;
    await _show(tester, adapter);
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(find.textContaining('Try Cancel again'), findsOneWidget);
    adapter.cancelFails = false;
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(adapter.cancels, 2);
    expect(find.byType(HardcoverConnectionDialog), findsNothing);
  });

  testWidgets('closing during begin cancels the late flow', (tester) async {
    final adapter = _Adapter()..beginWait = Completer<void>();
    await _show(tester, adapter);
    await tester.pump(const Duration(milliseconds: 300));
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    adapter.beginWait!.complete();
    await tester.pumpAndSettle();
    expect(adapter.cancels, 1);
  });

  for (final width in [320.0, 1280.0]) {
    testWidgets('dialog fits $width pixels at large text', (tester) async {
      final adapter = _Adapter();
      await _show(tester, adapter, size: Size(width, 900), scale: 2);
      expect(tester.takeException(), isNull);
      await tester.ensureVisible(find.text('Cancel'));
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
    });
  }
}
