import 'package:cantinarr/features/request/data/book_ownership.dart';
import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('BookRequestStatusDetail unions library ownership into isCovered', () {
    test('a downloaded ebook is covered even with no request rows', () {
      const d = BookRequestStatusDetail(
        ownership: BookOwnership(ebook: FormatOwnership(downloaded: true)),
      );
      expect(d.isCovered(BookRequestFormat.ebook), isTrue);
      expect(d.isCovered(BookRequestFormat.audiobook), isFalse);
      expect(d.coverageLabel(BookRequestFormat.ebook), 'Available');
    });

    test('a monitored-only ebook is requested and cannot be requested again', () {
      const d = BookRequestStatusDetail(
        ownership: BookOwnership(ebook: FormatOwnership(monitored: true)),
      );
      expect(d.isCovered(BookRequestFormat.ebook), isTrue);
      expect(d.isRequestable(BookRequestFormat.ebook), isFalse);
      expect(d.coverageLabel(BookRequestFormat.ebook), 'Requested');
    });

    test('verified unavailable beats a leftover monitor flag', () {
      const d = BookRequestStatusDetail(
        formats: {BookRequestFormat.ebook: RequestStatus.unavailable},
        ownership: BookOwnership(ebook: FormatOwnership(monitored: true)),
      );

      expect(d.isCovered(BookRequestFormat.ebook), isFalse);
      expect(d.isRequestable(BookRequestFormat.ebook), isTrue);
      expect(d.coverageLabel(BookRequestFormat.ebook), isNull);
    });

    test('available ebook + monitored audiobook covers both formats', () {
      const d = BookRequestStatusDetail(
        ownership: BookOwnership(
          ebook: FormatOwnership(downloaded: true),
          audiobook: FormatOwnership(monitored: true),
        ),
      );
      expect(d.isCovered(BookRequestFormat.ebook), isTrue); // file present
      expect(d.isCovered(BookRequestFormat.audiobook), isTrue);
      expect(d.isCovered(BookRequestFormat.both), isTrue);
      expect(d.coverageLabel(BookRequestFormat.ebook), 'Available');
      expect(d.coverageLabel(BookRequestFormat.audiobook), 'Requested');
    });

    test('owned ebook + requested audiobook → both covered, labels distinct',
        () {
      const d = BookRequestStatusDetail(
        formats: {BookRequestFormat.audiobook: RequestStatus.requested},
        ownership: BookOwnership(ebook: FormatOwnership(downloaded: true)),
      );
      expect(d.isCovered(BookRequestFormat.ebook), isTrue); // owned
      expect(d.isCovered(BookRequestFormat.audiobook), isTrue); // requested
      expect(d.isCovered(BookRequestFormat.both), isTrue);
      expect(d.coverageLabel(BookRequestFormat.ebook), 'Available');
      expect(d.coverageLabel(BookRequestFormat.audiobook),
          RequestStatus.requested.label);
    });

    test('withOwnership attaches ownership without mutating the original', () {
      const base = BookRequestStatusDetail();
      final withOwn = base.withOwnership(
          const BookOwnership(audiobook: FormatOwnership(downloaded: true)));
      expect(withOwn.isCovered(BookRequestFormat.audiobook), isTrue);
      expect(base.isCovered(BookRequestFormat.audiobook), isFalse);
    });

    test('without ownership, a denied request stays requestable', () {
      const d = BookRequestStatusDetail(
        formats: {BookRequestFormat.ebook: RequestStatus.denied},
      );
      expect(d.isCovered(BookRequestFormat.ebook), isFalse);
      expect(d.coverageLabel(BookRequestFormat.ebook), isNull);
    });

    test('an unknown lookup blocks every request action', () {
      const d = BookRequestStatusDetail(isKnown: false);
      expect(d.statusFor(BookRequestFormat.ebook), isNull);
      expect(d.isRequestable(BookRequestFormat.ebook), isFalse);
      expect(d.isRequestable(BookRequestFormat.audiobook), isFalse);
    });

    test('an unresolved digest row keeps its id but fails format truth closed',
        () {
      final row = OwnedTitle.fromJson({
        'title': 'Flock',
        'foreign_book_id': 'library-flock',
        'status_known': false,
        'ebook': {'monitored': false, 'downloaded': false},
        'audiobook': {'monitored': false, 'downloaded': false},
      });
      final detail = const BookRequestStatusDetail().withOwnership(
        row.ownership,
        ownershipStatusKnown: row.statusKnown,
      );

      expect(row.foreignBookId, 'library-flock');
      expect(row.statusKnown, isFalse);
      expect(detail.isKnown, isFalse);
      expect(
        detail.effectiveUnknownReason,
        BookStatusUnknownReason.formatNeedsAttention,
      );
      expect(detail.isRequestable(BookRequestFormat.ebook), isFalse);
      expect(detail.isRequestable(BookRequestFormat.audiobook), isFalse);
    });

    test('legacy digest rows default to known status', () {
      final row = OwnedTitle.fromJson({
        'title': 'Flock',
        'foreign_book_id': 'library-flock',
      });

      expect(row.statusKnown, isTrue);
    });
  });
}
