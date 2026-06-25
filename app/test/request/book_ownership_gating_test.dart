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
      expect(d.coverageLabel(BookRequestFormat.ebook), 'Downloaded');
    });

    test('a monitored-only ebook is covered, labeled "In Library"', () {
      const d = BookRequestStatusDetail(
        ownership: BookOwnership(ebook: FormatOwnership(monitored: true)),
      );
      expect(d.isCovered(BookRequestFormat.ebook), isTrue);
      expect(d.coverageLabel(BookRequestFormat.ebook), 'In Library');
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
      expect(d.coverageLabel(BookRequestFormat.ebook), 'Downloaded');
      expect(d.coverageLabel(BookRequestFormat.audiobook),
          RequestStatus.requested.label);
    });

    test('withOwnership attaches ownership without mutating the original', () {
      const base = BookRequestStatusDetail();
      final withOwn = base.withOwnership(
          const BookOwnership(audiobook: FormatOwnership(monitored: true)));
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
  });
}
