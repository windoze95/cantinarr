import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/status_pill.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/radarr/ui/widgets/radarr_queue_item_card.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Mirrors the Sonarr queue card matrix — the Radarr copy derives the same
/// status grammar and must not drift from it.
RadarrQueueItem _item({
  String status = '',
  String? state,
  String? trackedStatus,
  String protocol = 'torrent',
  String? errorMessage,
  List<String> statusMessages = const [],
}) =>
    RadarrQueueItem(
      id: 1,
      title: 'Movie.2024.1080p.WEB',
      status: status,
      trackedDownloadState: state,
      trackedDownloadStatus: trackedStatus,
      protocol: protocol,
      size: 8e9,
      sizeleft: 2e9,
      timeleft: '00:12:34',
      errorMessage: errorMessage,
      statusMessages: statusMessages,
      quality: 'WEBDL-1080p',
    );

Future<void> _pump(
  WidgetTester tester,
  RadarrQueueItem item, {
  VoidCallback? onShowIssues,
}) {
  return tester.pumpWidget(MaterialApp(
    home: Scaffold(
      body: RadarrQueueItemCard(
        item: item,
        primaryTitle: 'Movie',
        onShowIssues: onShowIssues,
      ),
    ),
  ));
}

Color _pillColor(WidgetTester tester, String text) =>
    tester.widget<StatusPill>(find.widgetWithText(StatusPill, text)).color;

Color? _barColor(WidgetTester tester) => tester
    .widget<LinearProgressIndicator>(find.byType(LinearProgressIndicator))
    .valueColor
    ?.value;

void main() {
  testWidgets('downloading item shows a Downloading pill in the info colour',
      (tester) async {
    await _pump(tester, _item(status: 'downloading'));
    expect(_pillColor(tester, 'Downloading'), AppTheme.downloading);
    expect(_barColor(tester), AppTheme.downloading);
    final bar = tester.widget<LinearProgressIndicator>(
        find.byType(LinearProgressIndicator));
    expect(bar.value, closeTo(0.75, 1e-9));
  });

  testWidgets('import pending shows an amber pill', (tester) async {
    await _pump(tester, _item(status: 'completed', state: 'importPending'));
    expect(_pillColor(tester, 'Import pending'), AppTheme.requested);
  });

  testWidgets('importing shows an amber pill', (tester) async {
    await _pump(tester, _item(status: 'completed', state: 'importing'));
    expect(_pillColor(tester, 'Importing'), AppTheme.requested);
  });

  testWidgets('imported shows a green pill', (tester) async {
    await _pump(tester, _item(status: 'completed', state: 'imported'));
    expect(_pillColor(tester, 'Imported'), AppTheme.available);
  });

  testWidgets('tracked warning outranks the downloading status',
      (tester) async {
    await _pump(
        tester, _item(status: 'downloading', trackedStatus: 'warning'));
    expect(find.text('Downloading'), findsNothing);
    expect(_pillColor(tester, 'Warning'), AppTheme.requested);
  });

  testWidgets('tracked error outranks the import phase', (tester) async {
    await _pump(tester,
        _item(status: 'completed', state: 'importing', trackedStatus: 'error'));
    expect(find.text('Importing'), findsNothing);
    expect(_pillColor(tester, 'Error'), AppTheme.error);
    expect(_barColor(tester), AppTheme.error);
  });

  testWidgets('failed import shows a red Failed pill', (tester) async {
    await _pump(tester, _item(status: 'completed', state: 'failedPending'));
    expect(_pillColor(tester, 'Failed'), AppTheme.error);
  });

  testWidgets('stalled client shows a muted Client unavailable pill',
      (tester) async {
    await _pump(tester, _item(status: 'downloadClientUnavailable'));
    expect(_pillColor(tester, 'Client unavailable'), AppTheme.unavailable);
  });

  testWidgets('queued item shows a muted pill', (tester) async {
    await _pump(tester, _item(status: 'queued'));
    expect(_pillColor(tester, 'Queued'), AppTheme.unavailable);
  });

  testWidgets('blank status falls back to Unknown', (tester) async {
    await _pump(tester, _item());
    expect(find.text('Unknown'), findsOneWidget);
  });

  testWidgets('protocol pill distinguishes torrent from usenet',
      (tester) async {
    await _pump(tester, _item(status: 'downloading'));
    expect(_pillColor(tester, 'TORRENT'), AppTheme.downloading);

    await _pump(
        tester, _item(status: 'downloading', protocol: 'usenet'));
    expect(_pillColor(tester, 'USENET'), AppTheme.available);
  });

  testWidgets('warning with messages shows the inline issues box',
      (tester) async {
    await _pump(
      tester,
      _item(
        status: 'completed',
        state: 'importPending',
        trackedStatus: 'warning',
        statusMessages: const ['Not an upgrade for existing movie file'],
      ),
    );
    expect(
      find.text('Not an upgrade for existing movie file'),
      findsOneWidget,
    );
    expect(find.byIcon(Icons.warning_amber_rounded), findsOneWidget);
  });

  testWidgets('issues defer to the Import Doctor when a handler is set',
      (tester) async {
    var opened = 0;
    await _pump(
      tester,
      _item(
        status: 'completed',
        state: 'importBlocked',
        trackedStatus: 'warning',
        statusMessages: const ['Sample file detected'],
      ),
      onShowIssues: () => opened++,
    );

    expect(find.text('Sample file detected'), findsNothing);
    await tester.tap(find.text('1 message — tap to resolve'));
    expect(opened, 1);
  });
}
