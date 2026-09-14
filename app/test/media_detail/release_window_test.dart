import 'package:flutter_test/flutter_test.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/logic/release_schedule.dart';
import 'package:cantinarr/features/media_detail/logic/release_window.dart';
import 'package:cantinarr/features/request/data/request_service.dart';

/// The reference "now" every case is written against.
final _today = DateTime(2026, 6, 20);

ReleaseSchedule? _dates({
  DateTime? limited,
  DateTime? cinemas,
  DateTime? digital,
  DateTime? disc,
}) => resolveReleaseSchedule([
      TmdbReleaseDateRegion(countryCode: 'US', entries: [
        if (limited != null) TmdbReleaseDateEntry(type: 2, date: limited),
        if (cinemas != null) TmdbReleaseDateEntry(type: 3, date: cinemas),
        if (digital != null) TmdbReleaseDateEntry(type: 4, date: digital),
        if (disc != null) TmdbReleaseDateEntry(type: 5, date: disc),
      ]),
    ], now: _today);

List<String> _labels(List<PendingRelease> pending) =>
    pending.map((r) => r.label).toList();

void main() {
  group('pendingReleases', () {
    test('before either date, both milestones show', () {
      final pending = pendingReleases(
        _dates(cinemas: DateTime(2026, 7, 3), digital: DateTime(2026, 9, 12)),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(_labels(pending), ['In cinemas', 'Digital']);
    });

    test('once the theatrical date passes, only digital is left', () {
      final pending = pendingReleases(
        _dates(cinemas: DateTime(2026, 5, 1), digital: DateTime(2026, 9, 12)),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(_labels(pending), ['Digital']);
    });

    test('once the digital date passes too, nothing is left', () {
      final pending = pendingReleases(
        _dates(cinemas: DateTime(2026, 1, 8), digital: DateTime(2026, 3, 4)),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(pending, isEmpty);
    });

    test('a date landing today still counts as pending', () {
      final pending = pendingReleases(
        _dates(digital: DateTime(2026, 6, 20)),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(_labels(pending), ['Digital']);
    });

    test('milestones are ordered soonest first, whatever their kind', () {
      // Day-and-date releases can put digital before the cinema run.
      final pending = pendingReleases(
        _dates(cinemas: DateTime(2026, 8, 1), digital: DateTime(2026, 7, 15)),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(_labels(pending), ['Digital', 'In cinemas']);
    });

    test('a single known date is enough', () {
      final pending = pendingReleases(
        _dates(cinemas: DateTime(2026, 7, 3)),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(_labels(pending), ['In cinemas']);
    });

    test('no dates at all yields nothing', () {
      final pending = pendingReleases(
        null,
        status: RequestStatus.requested,
        now: _today,
      );
      expect(pending, isEmpty);
    });

    test('an available title shows nothing even with a future date', () {
      // An early web release can land a file before the digital date; the
      // viewer can watch it, so the countdown is noise.
      final pending = pendingReleases(
        _dates(digital: DateTime(2026, 9, 12)),
        status: RequestStatus.available,
        now: _today,
      );
      expect(pending, isEmpty);
    });

    test('an unavailable title still explains itself', () {
      final pending = pendingReleases(
        _dates(cinemas: DateTime(2026, 7, 3)),
        status: RequestStatus.unavailable,
        now: _today,
      );
      expect(_labels(pending), ['In cinemas']);
    });

    test('limited and wide cinema dates keep the full schedule labels; disc stays in the full list', () {
      final pending = pendingReleases(
        _dates(
          limited: DateTime(2026, 7, 1),
          cinemas: DateTime(2026, 7, 3),
          digital: DateTime(2026, 9, 12),
          disc: DateTime(2026, 10, 1),
        ),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(_labels(pending), ['In cinemas (limited)', 'In cinemas', 'Digital']);
    });

    test('a region with only a disc date has no pending cinema or digital line', () {
      expect(pendingReleases(
        _dates(disc: DateTime(2026, 10, 1)),
        status: RequestStatus.requested,
        now: _today,
      ), isEmpty);
    });
  });

  group('formatPendingReleases', () {
    test('joins each milestone with its date', () {
      final pending = pendingReleases(
        _dates(cinemas: DateTime(2026, 7, 3), digital: DateTime(2026, 9, 12)),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(
        formatPendingReleases(pending, now: _today),
        'In cinemas Jul 3 • Digital Sep 12',
      );
    });

    test('a date landing today reads as "today", not as a date', () {
      final pending = pendingReleases(
        _dates(digital: DateTime(2026, 6, 20)),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(formatPendingReleases(pending, now: _today), 'Digital today');
    });

    test('a date in another year keeps its year', () {
      final pending = pendingReleases(
        _dates(cinemas: DateTime(2027, 2, 5)),
        status: RequestStatus.requested,
        now: _today,
      );
      expect(
        formatPendingReleases(pending, now: _today),
        'In cinemas Feb 5, 2027',
      );
    });
  });

  group('MovieReleaseDates.fromJson', () {
    test('parses plain calendar dates without shifting them', () {
      final d = MovieReleaseDates.fromJson({
        'in_cinemas': '2026-07-03',
        'digital': '2026-09-12',
      });
      // Component-wise equality: a time-zone conversion anywhere in the path
      // would land these on the 2nd and the 11th.
      expect(d.inCinemas, DateTime(2026, 7, 3));
      expect(d.digital, DateTime(2026, 9, 12));
    });

    test('a timestamp-shaped value keeps its date part', () {
      final d = MovieReleaseDates.fromJson({'digital': '2026-09-12T00:00:00Z'});
      expect(d.digital, DateTime(2026, 9, 12));
    });

    test('missing and malformed dates are simply absent', () {
      final d = MovieReleaseDates.fromJson({'in_cinemas': 'soon'});
      expect(d.inCinemas, isNull);
      expect(d.digital, isNull);
      expect(d.isEmpty, isTrue);
    });
  });

  group('RequestStatusDetail', () {
    test('picks up the releases block', () {
      final detail = RequestStatusDetail.fromJson({
        'status': 'requested',
        'releases': {'in_cinemas': '2026-07-03'},
      });
      expect(detail.status, RequestStatus.requested);
      expect(detail.releases.inCinemas, DateTime(2026, 7, 3));
    });

    test('a payload with no releases block reports none', () {
      final detail = RequestStatusDetail.fromJson({'status': 'available'});
      expect(detail.releases.isEmpty, isTrue);
    });
  });
}
