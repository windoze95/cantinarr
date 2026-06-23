import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/ui/request_status_sheet.dart';

void main() {
  group('RequestSeasonStatus', () {
    test('parses the backend season payload', () {
      final s = RequestSeasonStatus.fromJson({
        'season_number': 2,
        'episode_file_count': 7,
        'episode_count': 10,
        'status': 'partial',
        'progress': 0.7,
      });
      expect(s.seasonNumber, 2);
      expect(s.episodeFileCount, 7);
      expect(s.episodeCount, 10);
      expect(s.status, RequestStatus.partial);
      expect(s.progress, 0.7);
      expect(s.episodesLabel, '7/10');
      expect(s.isAvailable, isFalse);
    });

    test('an available season reports isAvailable', () {
      final s = RequestSeasonStatus.fromJson({
        'season_number': 1,
        'episode_file_count': 10,
        'episode_count': 10,
        'status': 'available',
      });
      expect(s.isAvailable, isTrue);
    });

    test('unknown status falls back to unavailable', () {
      final s = RequestSeasonStatus.fromJson({
        'season_number': 3,
        'status': 'something_new',
      });
      expect(s.status, RequestStatus.unavailable);
    });
  });

  group('RequestStatusDetail', () {
    test('parses status + seasons', () {
      final d = RequestStatusDetail.fromJson({
        'status': 'partial',
        'progress': 0.5,
        'seasons': [
          {'season_number': 1, 'status': 'available'},
          {'season_number': 2, 'status': 'downloading'},
        ],
      });
      expect(d.status, RequestStatus.partial);
      expect(d.seasons, hasLength(2));
      expect(d.seasons.first.seasonNumber, 1);
    });

    test('tolerates a movie response with no seasons', () {
      final d = RequestStatusDetail.fromJson({'status': 'available'});
      expect(d.status, RequestStatus.available);
      expect(d.seasons, isEmpty);
    });
  });

  group('RequestStatusSheet partial copy', () {
    Future<void> pumpSheet(
        WidgetTester tester, List<RequestSeasonStatus> seasons) async {
      await tester.pumpWidget(MaterialApp(
        home: Scaffold(
          body: RequestStatusSheet(
            title: 'Test Show',
            status: RequestStatus.partial,
            seasons: seasons,
          ),
        ),
      ));
    }

    RequestSeasonStatus season(int n, RequestStatus status) =>
        RequestSeasonStatus(seasonNumber: n, status: status);

    testWidgets('collapses a contiguous run of ready seasons into a range',
        (tester) async {
      await pumpSheet(tester, [
        season(1, RequestStatus.available),
        season(2, RequestStatus.available),
        season(3, RequestStatus.available),
        season(4, RequestStatus.downloading),
      ]);
      expect(
        find.textContaining('Seasons 1-3 ready'),
        findsOneWidget,
      );
      expect(
        find.textContaining('Season 4 downloading'),
        findsOneWidget,
      );
    });

    testWidgets('names a single ready season without a range', (tester) async {
      await pumpSheet(tester, [
        season(1, RequestStatus.available),
        season(2, RequestStatus.requested),
      ]);
      expect(find.textContaining('Season 1 ready'), findsOneWidget);
      expect(find.textContaining('Season 2 downloading'), findsOneWidget);
    });

    testWidgets('falls back to generic copy when no seasons given',
        (tester) async {
      await pumpSheet(tester, const []);
      expect(find.textContaining('Some episodes are available'), findsOneWidget);
    });
  });
}
