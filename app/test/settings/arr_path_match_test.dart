import 'package:cantinarr/features/settings/logic/arr_path_match.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('exact match relates', () {
    expect(
      arrPathRelatesToReportedRoot('/media/ebooks', '/media/ebooks'),
      isTrue,
    );
  });

  test('mapping inside a reported root relates', () {
    expect(
      arrPathRelatesToReportedRoot(
        '/media-server/media/ebooks/series',
        '/media-server/media/ebooks',
      ),
      isTrue,
    );
  });

  test('mapping containing a reported root relates', () {
    expect(
      arrPathRelatesToReportedRoot(
        '/media-server',
        '/media-server/media/ebooks',
      ),
      isTrue,
    );
  });

  test('same basename under different parents does not relate', () {
    // The real-world trap: Cantinarr mounts the library at /media/ebooks but
    // the arr sees the share as /media-server/media/ebooks.
    expect(
      arrPathRelatesToReportedRoot(
        '/media/ebooks',
        '/media-server/media/ebooks',
      ),
      isFalse,
    );
  });

  test('sibling segment prefixes do not relate', () {
    expect(
      arrPathRelatesToReportedRoot('/media/ebooks', '/media/ebooks-yana'),
      isFalse,
    );
  });

  test('trailing slashes and duplicate separators are ignored', () {
    expect(
      arrPathRelatesToReportedRoot('/media//ebooks/', '/media/ebooks'),
      isTrue,
    );
  });

  test('windows separators and case differences still relate', () {
    expect(
      arrPathRelatesToReportedRoot(r'C:\Media\Books', 'c:/media/books/kept'),
      isTrue,
    );
  });

  test('different windows drives do not relate', () {
    expect(
      arrPathRelatesToReportedRoot(r'C:\media', r'D:\media'),
      isFalse,
    );
  });

  test('a bare root prefix relates to everything', () {
    expect(arrPathRelatesToReportedRoot('/', '/media/ebooks'), isTrue);
  });
}
