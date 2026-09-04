import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/ui/season_table.dart';
import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/logic/request_provider.dart';

/// Proves seasonRowLabel is actually wired into the season row: known-year
/// seasons render "Season N · YYYY", unknown-year seasons keep rendering
/// today's bare title, and the year survives the row configurations that
/// would have hidden it had it gone on the conditional eps subtitle instead.
void main() {
  RequestNotifier notifier() => RequestNotifier(
        service: RequestService(backendDio: Dio()),
        tmdbId: 123,
        mediaType: MediaType.tv,
      );

  Widget host(SeasonTable table) => MaterialApp(
        home: Scaffold(body: SingleChildScrollView(child: table)),
      );

  testWidgets(
      'a mixed series shows years for known seasons and bare titles for '
      'unknown ones, per-row', (tester) async {
    const seasons = [
      Season(
        id: 1,
        seasonNumber: 1,
        name: 'Season 1',
        episodeCount: 10,
        airDate: '2019-04-14',
      ),
      Season(
        id: 2,
        seasonNumber: 2,
        name: 'Season 2',
        episodeCount: 8,
        airDate: '2020-05-01',
      ),
      Season(
        id: 3,
        seasonNumber: 3,
        name: 'Season 3',
        episodeCount: 6,
        airDate: null,
      ),
    ];

    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(),
    )));

    expect(find.text('Season 1 · 2019'), findsOneWidget);
    expect(find.text('Season 2 · 2020'), findsOneWidget);
    expect(find.text('Season 3'), findsOneWidget);
  });

  testWidgets('an empty-string air date renders the bare title (D-03)',
      (tester) async {
    const seasons = [
      Season(
        id: 1,
        seasonNumber: 1,
        name: 'Season 1',
        episodeCount: 10,
        airDate: '',
      ),
    ];

    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(),
    )));

    expect(find.text('Season 1'), findsOneWidget);
  });

  testWidgets('a custom season name with an air date renders name plus year',
      (tester) async {
    const seasons = [
      Season(
        id: 1,
        seasonNumber: 1,
        name: 'The Final Season',
        episodeCount: 10,
        airDate: '2019-04-14',
      ),
    ];

    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(),
    )));

    expect(find.text('The Final Season · 2019'), findsOneWidget);
  });

  testWidgets('the year still renders with no episode count (D-02)',
      (tester) async {
    const seasons = [
      Season(
        id: 1,
        seasonNumber: 1,
        name: 'Season 1',
        episodeCount: null,
        airDate: '2019-04-14',
      ),
    ];

    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(),
    )));

    expect(find.text('Season 1 · 2019'), findsOneWidget);
  });

  testWidgets('the year still renders in the status-only table',
      (tester) async {
    const seasons = [
      Season(
        id: 1,
        seasonNumber: 1,
        name: 'Season 1',
        episodeCount: 10,
        airDate: '2019-04-14',
      ),
    ];

    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(),
      canRequest: false,
    )));

    expect(find.text('Season 1 · 2019'), findsOneWidget);
  });
}
