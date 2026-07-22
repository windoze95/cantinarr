import 'dart:async';

import 'package:cantinarr/features/media_download/data/media_download_models.dart';
import 'package:cantinarr/features/media_download/data/media_download_service.dart';
import 'package:cantinarr/features/media_download/ui/media_download_button.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('requests one ticket and launches the returned URL',
      (tester) async {
    final service = _FakeService();
    final launched = <Uri>[];
    await _pumpButton(
      tester,
      service: service,
      launcher: (uri) async {
        launched.add(uri);
        return true;
      },
    );

    await tester.tap(find.text('Download movie'));
    await tester.pumpAndSettle();

    expect(service.calls, 1);
    expect(service.instanceId, 'radarr-main');
    expect(service.fileId, 42);
    expect(launched, [Uri.parse('https://cantinarr.example/download/token')]);
  });

  testWidgets('suppresses a second tap while ticket creation is pending',
      (tester) async {
    final completer = Completer<MediaDownloadTicket>();
    final service = _FakeService(pending: completer.future);
    await _pumpButton(
      tester,
      service: service,
      launcher: (_) async => true,
    );

    await tester.tap(find.text('Download movie'));
    await tester.pump();
    await tester.tap(find.byType(TextButton));
    await tester.pump();

    expect(service.calls, 1);
    completer.complete(_ticket);
    await tester.pumpAndSettle();
  });

  testWidgets('multiple files require an individual choice', (tester) async {
    final service = _FakeService();
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          mediaDownloadServiceProvider.overrideWithValue(service),
          mediaDownloadLauncherProvider.overrideWithValue((_) async => true),
        ],
        child: const MaterialApp(
          home: Scaffold(
            body: MediaDownloadChoiceButton(
              instanceId: 'sonarr-main',
              label: 'Download Season 1 episodes',
              sheetTitle: 'Download Season 1',
              choices: [
                MediaDownloadChoice(fileId: 11, label: 'S01E01 · Pilot'),
                MediaDownloadChoice(fileId: 12, label: 'S01E02 · Next'),
              ],
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('Download Season 1 episodes'));
    await tester.pumpAndSettle();

    expect(service.calls, 0);
    expect(find.text('S01E01 · Pilot'), findsOneWidget);
    expect(find.text('S01E02 · Next'), findsOneWidget);
    expect(find.byTooltip('Download S01E01 · Pilot'), findsOneWidget);
    expect(find.byTooltip('Download S01E02 · Next'), findsOneWidget);
  });
}

final _ticket = MediaDownloadTicket(
  url: Uri.parse('https://cantinarr.example/download/token'),
  filename: 'Movie.mkv',
  sizeBytes: 100,
  expiresAt: DateTime.utc(2026, 7, 22, 18),
);

Future<void> _pumpButton(
  WidgetTester tester, {
  required _FakeService service,
  required MediaDownloadLauncher launcher,
}) =>
    tester.pumpWidget(
      ProviderScope(
        overrides: [
          mediaDownloadServiceProvider.overrideWithValue(service),
          mediaDownloadLauncherProvider.overrideWithValue(launcher),
        ],
        child: const MaterialApp(
          home: Scaffold(
            body: MediaDownloadButton(
              instanceId: 'radarr-main',
              fileId: 42,
              label: 'Download movie',
            ),
          ),
        ),
      ),
    );

class _FakeService extends MediaDownloadService {
  final Future<MediaDownloadTicket>? pending;
  int calls = 0;
  String? instanceId;
  int? fileId;

  _FakeService({this.pending}) : super(backendDio: Dio());

  @override
  Future<MediaDownloadTicket> createTicket({
    required String instanceId,
    required int fileId,
  }) {
    calls++;
    this.instanceId = instanceId;
    this.fileId = fileId;
    return pending ?? Future.value(_ticket);
  }
}
