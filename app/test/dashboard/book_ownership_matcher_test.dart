import 'package:flutter_test/flutter_test.dart';

import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/dashboard/logic/book_ownership_matcher.dart';
import 'package:cantinarr/features/request/data/book_ownership.dart';

/// Builds a lookup result. [author] becomes `author.authorName`; pass null for a
/// result with no author context.
ChaptarrBook _result(
  String title, {
  String? author,
  String? foreignBookId,
}) =>
    ChaptarrBook.fromJson({
      'id': 1,
      'title': title,
      if (foreignBookId != null) 'foreignBookId': foreignBookId,
      if (author != null) 'author': {'authorName': author},
    });

OwnedTitle _owned(
  String title,
  String author, {
  bool ebookMonitored = false,
  bool ebookDownloaded = false,
  bool audiobookMonitored = false,
  bool audiobookDownloaded = false,
  String foreignBookId = '',
}) =>
    OwnedTitle.fromJson({
      'title': title,
      'author': author,
      'foreign_book_id': foreignBookId,
      'ebook': {'monitored': ebookMonitored, 'downloaded': ebookDownloaded},
      'audiobook': {
        'monitored': audiobookMonitored,
        'downloaded': audiobookDownloaded,
      },
    });

void main() {
  group('normalizeTitleTokens', () {
    test('strips series prefix, trailing parenthetical, and stopwords', () {
      final tokens =
          normalizeTitleTokens('Star Wars: Heir to the Empire (Part 1)');
      expect(tokens.toSet(), {'heir', 'empire'});
      // Series prefix, parenthetical, and stopwords are all gone.
      for (final dropped in ['star', 'wars', 'part', '1', 'the', 'to']) {
        expect(tokens, isNot(contains(dropped)));
      }
    });

    test('keeps the series prefix when stripSeries is false', () {
      final tokens = normalizeTitleTokens(
        'Star Wars: Heir to the Empire',
        stripSeries: false,
      );
      expect(tokens, contains('star'));
      expect(tokens, contains('wars'));
      expect(tokens, contains('heir'));
    });

    test('does not strip the series prefix when nothing follows it', () {
      // Remainder after ": " would be empty, so the whole string is kept.
      final tokens = normalizeTitleTokens('Dune: ');
      expect(tokens, contains('dune'));
    });

    test('strips a trailing ", Book N" suffix', () {
      final tokens = normalizeTitleTokens('Mistborn, Book 2');
      expect(tokens, contains('mistborn'));
      expect(tokens, isNot(contains('book')));
      expect(tokens, isNot(contains('2')));
    });
  });

  group('jaccard', () {
    test('identical sets score 1.0', () {
      expect(jaccard({'art', 'war'}, {'art', 'war'}), 1.0);
    });

    test('two empty sets score 0', () {
      expect(jaccard(<String>{}, <String>{}), 0);
    });

    test('"The Art of War" vs "Art of War" is at least the threshold', () {
      final a = normalizeTitleTokens('The Art of War').toSet();
      final b = normalizeTitleTokens('Art of War').toSet();
      // Both reduce to {art, war} after stopword removal -> 1.0.
      expect(jaccard(a, b), greaterThanOrEqualTo(0.75));
    });
  });

  group('authorMatches', () {
    test('exact author matches', () {
      expect(authorMatches('Timothy Zahn', 'Timothy Zahn'), isTrue);
    });

    test('surname-only lookup matches a full digest author', () {
      expect(authorMatches('Zahn', 'Timothy Zahn'), isTrue);
    });

    test('different authors do not match', () {
      expect(authorMatches('Marcus Aurelius', 'Sun Tzu'), isFalse);
    });

    test('null author does not match', () {
      expect(authorMatches(null, 'Timothy Zahn'), isFalse);
    });
  });

  group('ownershipFor', () {
    test('matches a series-prefixed result to a bare owned title', () {
      final digest = [
        _owned('Heir to the Empire', 'Timothy Zahn', ebookDownloaded: true),
      ];
      final result =
          _result('Star Wars: Heir to the Empire', author: 'Timothy Zahn');

      final ownership = ownershipFor(result, digest);
      expect(ownership, isNotNull);
      expect(ownership!.ebook.downloaded, isTrue);
      expect(ownership.anyDownloaded, isTrue);
    });

    test('false-positive guard: same title, wrong author returns null', () {
      final digest = [
        _owned('Meditations and The Art of War', 'Sun Tzu',
            ebookDownloaded: true),
      ];
      final result = _result('Meditations and The Art of War',
          author: 'Marcus Aurelius');

      expect(ownershipFor(result, digest), isNull);
    });

    test('surname-only author remains a plausible but unsafe identity', () {
      final digest = [
        _owned('Heir to the Empire', 'Timothy Zahn',
            audiobookMonitored: true),
      ];
      final result = _result('Heir to the Empire', author: 'Zahn');

      expect(ownedMatchesFor(result, digest), hasLength(1));
      expect(ownershipFor(result, digest), isNull);
    });

    test('below-threshold title with same author returns null', () {
      final digest = [
        _owned('The Final Empire', 'Brandon Sanderson', ebookDownloaded: true),
      ];
      final result =
          _result('The Way of Kings', author: 'Brandon Sanderson');

      expect(ownershipFor(result, digest), isNull);
    });

    test('empty digest returns null', () {
      final result = _result('Heir to the Empire', author: 'Timothy Zahn');
      expect(ownershipFor(result, const []), isNull);
    });

    test('multiple plausible records fail closed instead of choosing one', () {
      final digest = [
        _owned('Ahsoka', 'E.K. Johnston', ebookDownloaded: true),
        _owned('Ahsoka (Star Wars)', 'E.K. Johnston',
            ebookMonitored: true, audiobookMonitored: true),
      ];
      final result =
          _result('Ahsoka (Star Wars)', author: 'E.K. Johnston');
      expect(ownedMatchesFor(result, digest), hasLength(2));
      expect(ownershipFor(result, digest), isNull);
    });

    test('ownedMatchFor exposes the matched record cover', () {
      final digest = [
        OwnedTitle.fromJson({
          'title': 'Ahsoka',
          'author': 'E.K. Johnston',
          'cover': '/MediaCover/Books/9/cover.jpg',
          'ebook': {'downloaded': true},
        }),
      ];
      final match =
          ownedMatchFor(_result('Ahsoka', author: 'E.K. Johnston'), digest);
      expect(match, isNotNull);
      expect(match!.cover, '/MediaCover/Books/9/cover.jpg');
      expect(match.ownership.ebook.downloaded, isTrue);
    });

    test('an exact foreign id outranks mismatched metadata', () {
      final digest = [
        _owned(
          'Library title',
          'Library author',
          ebookDownloaded: true,
          foreignBookId: 'same-id',
        ),
      ];
      final result = _result(
        'Provider title variant',
        author: 'Provider author variant',
        foreignBookId: 'same-id',
      );

      expect(ownedMatchFor(result, digest), same(digest.single));
    });
  });

  group('titleMatchesQuery', () {
    const subtitled = '10 Algorithms Every Forward Deployed Engineer Should '
        'Know: Shortest Paths and Minimum Spanning Trees';

    test('matches the headline of a title whose colon adds a subtitle', () {
      expect(titleMatchesQuery('10 algorithms every forward', subtitled),
          isTrue);
      // The subtitle is still the requester's to search by.
      expect(titleMatchesQuery('shortest paths', subtitled), isTrue);
    });

    test('matches the word still being typed as a prefix', () {
      expect(titleMatchesQuery('10 algorithms every fo', subtitled), isTrue);
      expect(titleMatchesQuery('10 algorithms every fx', subtitled), isFalse);
    });

    test('still requires the completed words in full', () {
      expect(titleMatchesQuery('algo forward', subtitled), isFalse);
    });

    test('drops a series prefix on either side', () {
      expect(
        titleMatchesQuery('Star Wars: Heir to the Empire', 'Heir to the Empire'),
        isTrue,
      );
      expect(
        titleMatchesQuery('heir', 'Star Wars: Heir to the Empire'),
        isTrue,
      );
    });

    test('a query naming nothing in the title does not match', () {
      expect(titleMatchesQuery('foundation', subtitled), isFalse);
      expect(titleMatchesQuery('', subtitled), isFalse);
    });
  });

  group('resolveBookSearchIdentity', () {
    OwnedTitle libraryFlock() => _owned(
          'Flock',
          'Kate Stewart',
          audiobookMonitored: true,
          foreignBookId: 'library-flock',
        );

    test('an exact library id outranks a same-title sibling claim', () {
      final digest = [libraryFlock()];
      final exact = _result('Flock',
          author: 'Kate Stewart', foreignBookId: 'library-flock');
      final sibling =
          _result('Flock', author: 'Kate Stewart', foreignBookId: 'other-id');

      final identity = resolveBookSearchIdentity(
        query: 'flock',
        lookupResults: [exact, sibling],
        digest: digest,
      );

      expect(identity.matches[exact], same(digest.single));
      // The record is spoken for, so the sibling is not left half-claiming it.
      expect(identity.contested, isEmpty);
      expect(identity.libraryRows, isEmpty);
    });

    test('two rows carrying the same library id stay unresolved', () {
      final digest = [libraryFlock()];
      final first = _result('Flock',
          author: 'Kate Stewart', foreignBookId: 'library-flock');
      final second = _result('Flock',
          author: 'Kate Stewart', foreignBookId: 'library-flock');

      final identity = resolveBookSearchIdentity(
        query: 'flock',
        lookupResults: [first, second],
        digest: digest,
      );

      expect(identity.matches, isEmpty);
      expect(identity.contested.keys, hasLength(2));
      expect(identity.libraryRows, [same(digest.single)]);
    });

    test('an author search still surfaces the record a row might be', () {
      final digest = [libraryFlock()];
      final identity = resolveBookSearchIdentity(
        // Names the author, never the title — so only the unresolved rows can
        // put the library record the guidance points at on screen.
        query: 'kate stewart',
        lookupResults: [
          _result('Flock', author: 'Kate Stewart', foreignBookId: 'lookup-a'),
          _result('Flock', author: 'Kate Stewart', foreignBookId: 'lookup-b'),
        ],
        digest: digest,
      );

      expect(identity.contested.keys, hasLength(2));
      expect(identity.libraryRows, [same(digest.single)]);
    });

    test('an unowned shell a row might be is still offered as a choice', () {
      // Shells stay out of query results, but a row that cannot be told apart
      // from one has to be able to point at it.
      final shell = _owned('Flock', 'Kate Stewart', foreignBookId: 'shell');
      final identity = resolveBookSearchIdentity(
        query: 'flock',
        lookupResults: [
          _result('Flock', author: 'Kate Stewart', foreignBookId: 'lookup-a'),
          _result('Flock', author: 'Kate Stewart', foreignBookId: 'lookup-b'),
        ],
        digest: [shell],
      );

      expect(identity.libraryRows, [same(shell)]);
    });
  });

  group('ownedTitlesForQuery surfaces owned books lookup missed', () {
    final digest = [
      _owned('Heir to the Empire', 'Timothy Zahn', ebookDownloaded: true),
      _owned('Dune', 'Frank Herbert', ebookMonitored: true),
    ];

    test('injects an owned title matching the query but absent from lookup', () {
      final lookup = [
        _result('Heir of Fire', author: 'Sarah J. Maas'),
        _result('The Heir', author: 'Kiera Cass'),
      ];
      final injected = ownedTitlesForQuery('heir', digest, lookup);
      expect(injected.map((t) => t.title), ['Heir to the Empire']);
      expect(injected.single.ownership.ebook.downloaded, isTrue);
    });

    test('skips an empty (all-missing, unmonitored) library shell', () {
      final two = [
        _owned('Heir to the Empire', 'Timothy Zahn', ebookDownloaded: true),
        _owned('Star Wars: Heir to the Empire', 'Timothy Zahn'), // all missing
      ];
      final injected = ownedTitlesForQuery('heir', two, const []);
      expect(injected.map((t) => t.title), ['Heir to the Empire']);
    });

    test('skips a record a lookup result lists under the exact same title', () {
      final lookup = [_result('Heir to the Empire', author: 'Timothy Zahn')];
      expect(ownedTitlesForQuery('heir', digest, lookup), isEmpty);
    });

    test('a safe normalized lookup mapping does not duplicate its library row',
        () {
      final lookup = [
        _result('Star Wars: Heir to the Empire', author: 'Timothy Zahn'),
      ];
      final injected = ownedTitlesForQuery('heir', digest, lookup);
      expect(injected, isEmpty);
    });

    test('injects distinct records as separate rows (no merge)', () {
      final two = [
        _owned('Ahsoka', 'E.K. Johnston', ebookDownloaded: true),
        _owned('Ahsoka (Star Wars)', 'E.K. Johnston', audiobookMonitored: true),
      ];
      final injected = ownedTitlesForQuery('ahsoka', two, const []);
      expect(injected.map((t) => t.title), ['Ahsoka', 'Ahsoka (Star Wars)']);
    });

    test('injects same-title records separately by stable library identity', () {
      final two = [
        _owned(
          'Flock',
          'Kate Stewart',
          ebookDownloaded: true,
          foreignBookId: 'library-a',
        ),
        _owned(
          'Flock',
          'Kate Stewart',
          audiobookMonitored: true,
          foreignBookId: 'library-b',
        ),
      ];

      final injected = ownedTitlesForQuery(
        'flock',
        two,
        [_result('Flock', author: 'Kate Stewart')],
      );
      expect(injected.map((row) => row.foreignBookId),
          ['library-a', 'library-b']);
    });

    test('an empty query injects nothing', () {
      expect(ownedTitlesForQuery('', digest, const []), isEmpty);
    });

    test('a query matching no owned title injects nothing', () {
      expect(ownedTitlesForQuery('foundation', digest, const []), isEmpty);
    });
  });
}
