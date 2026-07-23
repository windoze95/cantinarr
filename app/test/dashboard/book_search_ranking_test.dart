import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/dashboard/logic/book_search_ranking.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('rankBookSearchResults', () {
    test('puts an exact title first and preserves every distinct record', () {
      final books = [
        _book('The Haunting of Hill House', 'Shirley Jackson', year: 1959),
        _book('Haunting', 'Chuck Palahniuk', year: 2005),
        _book('She Is a Haunting', 'Trang Thanh Tran', year: 2023),
      ];

      final ranked = rankBookSearchResults('haunting', books);

      expect(ranked.map((result) => result.book.title), [
        'Haunting',
        'The Haunting of Hill House',
        'She Is a Haunting',
      ]);
      expect(ranked.first.evidence, 'Exact title match');
      expect(ranked, hasLength(books.length));
    });

    test('uses Chaptarr order as the deterministic final tie-breaker', () {
      final books = [
        _book('Flock', 'Kate Stewart', foreignId: 'first'),
        _book('Flock', 'Kate Stewart', foreignId: 'second'),
      ];

      final ranked = rankBookSearchResults('flock', books);

      expect(
        ranked.map((result) => result.book.foreignBookId),
        ['first', 'second'],
      );
    });

    test('recognizes an exact author search', () {
      final ranked = rankBookSearchResults('kate stewart', [
        _book('A Book by Kate', 'Someone Else'),
        _book('Flock', 'Kate Stewart'),
      ]);

      expect(ranked.first.book.title, 'Flock');
      expect(ranked.first.evidence, 'Exact author match');
    });

    test('keeps obvious companion material visible but never recommends it',
        () {
      final ranked = rankBookSearchResults('haunting adeline', [
        _book(
          'Haunting Adeline: Summary and Analysis',
          'Study Guides Press',
        ),
        _book('Haunting Adeline', 'H. D. Carlton'),
      ]);

      expect(ranked, hasLength(2));
      expect(ranked.first.book.title, 'Haunting Adeline');
      expect(ranked.first.recommendationEligible, isTrue);
      expect(ranked.last.book.title, contains('Summary and Analysis'));
      expect(ranked.last.recommendationEligible, isFalse);
      expect(ranked.last.evidence, 'Companion or study material');
    });

    test('does not penalize a bare guide or summary word', () {
      final ranked = rankBookSearchResults('guide', [
        _book('The Hitchhiker’s Guide to the Galaxy', 'Douglas Adams'),
        _book('A Summary', 'Louise Bourgeois'),
      ]);

      expect(ranked, everyElement(
        isA<RankedBookSearchResult>().having(
          (result) => result.recommendationEligible,
          'recommendationEligible',
          isTrue,
        ),
      ));
    });

    test('keeps clear multiple-book sets visible but ineligible', () {
      final titles = [
        'Earthsea Omnibus',
        'Earthsea Box Set',
        'Earthsea Boxed Set',
        'Earthsea Bundle',
        'The Earthsea Trilogy',
        'The Complete Earthsea Series',
        'Earthsea Books 1-3',
        'Earthsea Books 1 & 2',
        'Earthsea Books 1 through 3',
      ];

      for (final title in titles) {
        final ranked = rankBookSearchResults('earthsea', [
          _book(title, 'Ursula K. Le Guin'),
        ]);
        expect(ranked, hasLength(1));
        expect(ranked.single.recommendationEligible, isFalse,
            reason: title);
        expect(ranked.single.evidence, 'Multiple-book set', reason: title);
      }
    });

    test('does not penalize bare collection or series wording', () {
      final ranked = rankBookSearchResults('earthsea', [
        _book('Earthsea Collection', 'Ursula K. Le Guin'),
        _book('Earthsea Series', 'Ursula K. Le Guin'),
      ]);

      expect(
        ranked.map((result) => result.recommendationEligible),
        everyElement(isTrue),
      );
    });
  });

  group('requester edition comparison', () {
    test('does not group a common title without matching author evidence', () {
      final identified = _book('Home', 'Toni Morrison');
      final missingAuthor = _book('Home', '');
      final differentAuthor = _book('Home', 'Harlan Coben');

      expect(sameBookWork(identified, missingAuthor), isFalse);
      expect(sameBookWork(identified, differentAuthor), isFalse);
    });

    test('does not let a shared provider id override title and author', () {
      final selected = _book(
        'Home',
        'Toni Morrison',
        foreignId: 'provider-collision',
      );
      final wrongWork = _book(
        'The Home',
        'Harlan Coben',
        foreignId: 'provider-collision',
      );

      expect(sameBookWork(selected, wrongWork), isFalse);
    });

    test('offers narrow subtitle variants from the same author together', () {
      final plain = _book('Haunting Adeline', 'H. D. Carlton');
      final seriesSubtitle = _book(
        'Haunting Adeline: Cat and Mouse, Book 1',
        'H. D. Carlton',
      );
      final differentPrefix = _book(
        'Haunting Adeline Returns',
        'H. D. Carlton',
      );

      expect(sameBookWork(plain, seriesSubtitle), isTrue);
      expect(sameBookWork(plain, differentPrefix), isFalse);
    });

    test('hides records that differ only by backend identity', () {
      final sparse = _book(
        'Flock',
        'Kate Stewart',
        foreignId: 'provider-a',
      );
      final described = _book(
        'Flock',
        'Kate Stewart',
        foreignId: 'provider-b',
        year: 2024,
      );

      expect(
        booksAreIndistinguishableForRequester(sparse, described),
        isTrue,
      );
    });

    test('keeps meaningfully different publications selectable', () {
      final original = _book(
        'The Hobbit',
        'J. R. R. Tolkien',
        year: 1937,
        publisher: 'George Allen & Unwin',
      );
      final anniversary = _book(
        'The Hobbit',
        'J. R. R. Tolkien',
        year: 2012,
        publisher: 'HarperCollins',
        editionTitle: '75th Anniversary Edition',
      );

      expect(
        booksAreIndistinguishableForRequester(original, anniversary),
        isFalse,
      );
      expect(bookEditionFacts(anniversary), containsAll([
        '2012',
        'HarperCollins',
        '75th Anniversary Edition',
      ]));
    });

    test('a shared provider id cannot hide publication differences', () {
      final first = _book(
        'The Hobbit',
        'J. R. R. Tolkien',
        foreignId: 'shared-id',
        year: 1937,
        publisher: 'George Allen & Unwin',
      );
      final second = _book(
        'The Hobbit',
        'J. R. R. Tolkien',
        foreignId: 'shared-id',
        year: 2012,
        publisher: 'HarperCollins',
      );

      expect(booksAreIndistinguishableForRequester(first, second), isFalse);
    });
  });
}

ChaptarrBook _book(
  String title,
  String author, {
  String? foreignId,
  int? year,
  String? publisher,
  String? editionTitle,
}) =>
    ChaptarrBook.fromJson({
      'title': title,
      'foreignBookId': foreignId,
      if (year != null) 'year': year,
      'author': {'authorName': author},
      if (publisher != null || editionTitle != null)
        'editions': [
          {
            'id': 1,
            if (publisher != null) 'publisher': publisher,
            if (editionTitle != null) 'title': editionTitle,
          },
        ],
    });
