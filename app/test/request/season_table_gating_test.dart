import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/ui/season_table.dart';
import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/logic/request_provider.dart';

/// The season picker must be request-capable only for users the server allows
/// to choose seasons (can_choose_season). For everyone else the server ignores
/// an explicit season list, so showing checkboxes and a submit button would be
/// a silent no-op.
void main() {
  const seasons = [
    Season(id: 1, seasonNumber: 1, name: 'Season 1', episodeCount: 10),
    Season(id: 2, seasonNumber: 2, name: 'Season 2', episodeCount: 8),
  ];

  RequestNotifier notifier() => RequestNotifier(
        service: RequestService(backendDio: Dio()),
        tmdbId: 123,
        mediaType: MediaType.tv,
      );

  Widget host(SeasonTable table) => MaterialApp(
        home: Scaffold(body: SingleChildScrollView(child: table)),
      );

  testWidgets('season choice allowed: checkboxes, chips and submit render',
      (tester) async {
    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(),
    )));

    expect(find.byType(Checkbox), findsNWidgets(2));
    expect(find.text('All'), findsOneWidget);
    expect(find.text('Select seasons to request'), findsOneWidget);
  });

  testWidgets('season choice not allowed: table is status-only',
      (tester) async {
    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(),
      canRequest: false,
    )));

    // Season rows still render (status display)...
    expect(find.text('Season 1'), findsOneWidget);
    expect(find.text('Season 2'), findsOneWidget);
    // ...but every request affordance is gone.
    expect(find.byType(Checkbox), findsNothing);
    expect(find.text('All'), findsNothing);
    expect(find.text('Select seasons to request'), findsNothing);
    expect(find.byType(ElevatedButton), findsNothing);
  });
}
