import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
      'book descriptions omit standalone cover links and keep story paragraphs',
      () {
    final book = ChaptarrBook.fromJson({
      'title': 'A new adventure',
      'overview': '<p>An alternative cover for this ASIN can be found '
          '<a href="https://www.goodreads.com/book/show/123">here</a></p>'
          '<p>A librarian finds a <b>secret</b> &amp; follows its trail.</p>'
          '<p>The search takes her far from home…</p>',
    });
    expect(
        book.displayOverview,
        'A librarian finds a secret & follows its trail.\n\n'
        'The search takes her far from home…');
    // Cleanup changes presentation only, not the provider's stored fragment.
    expect(book.overview, contains('<a href='));
  });

  test('plain-text alternate-cover notes are removed with common wording', () {
    for (final notice in [
      'An alternative cover for this ASIN can be found here',
      'An alternate cover edition for this ISBN can be found here.',
      'Alternate cover edition can be found here.',
    ]) {
      final book = ChaptarrBook(
          id: 0,
          title: 'A new adventure',
          overview: '$notice\n\nThe adventure begins…');
      expect(book.displayOverview, 'The adventure begins…', reason: notice);
    }
  });

  test('cover-only book metadata falls back to an actual edition synopsis', () {
    final book = ChaptarrBook.fromJson({
      'title': 'A new adventure',
      'overview': 'An alternative cover for this ASIN can be found here',
      'editions': [
        {
          'overview': 'An alternative cover for this ASIN can be found here'
              '<br/><br/>A librarian finds a secret.',
        }
      ],
    });
    expect(book.displayOverview, 'A librarian finds a secret.');
    expect(
        const ChaptarrBook(
                id: 0,
                title: 'A new adventure',
                overview:
                    'An alternative cover for this ASIN can be found here')
            .displayOverview,
        isNull);
  });

  test('ordinary prose and quoted cover references are preserved', () {
    const prose = 'Her alternative cover is a secret identity.\n\n'
        '“An alternative cover for this ASIN can be found here.”\n\n'
        'An alternative cover for this ASIN can be found here in the archive.';
    expect(
        const ChaptarrBook(id: 0, title: 'A new adventure', overview: prose)
            .displayOverview,
        prose);
  });
}
