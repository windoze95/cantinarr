import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/logic/book_metadata_selection.dart';
import 'package:cantinarr/features/chaptarr/logic/book_publication.dart';
import 'package:flutter_test/flutter_test.dart';

ChaptarrBook metadata(
        {String id = 'gr:1',
        String? work,
        String? hardcover,
        String? edition,
        String? format,
        String overview = 'Full description.',
        int pages = 400}) =>
    ChaptarrBook(
      id: 0,
      title: 'The selected title',
      foreignBookId: id,
      goodreadsWorkId: work,
      hardcoverBookId: hardcover,
      foreignEditionId: edition,
      mediaType: format,
      pageCount: pages,
      overview: overview,
    );

void main() {
  test('format projections retain only an agreed publication year', () {
    ChaptarrBook projection(String format, DateTime date) => ChaptarrBook(
          id: 0,
          title: 'Ahsoka',
          foreignBookId: 'gr:1',
          mediaType: format,
          overview: 'The same synopsis.',
          releaseDate: date,
          pageCount: 400,
        );
    final ebook = projection('ebook', DateTime(2016, 10, 11));
    final sameYear = selectBookMetadata('gr:1', [
      ebook,
      projection('audiobook', DateTime(2016, 11, 1)),
    ])!;
    expect(BookPublication.fromBook(sameYear).year, 2016);
    expect(sameYear.pageCount, 0);
    expect(sameYear.editions, isEmpty);
    final differentYear = selectBookMetadata('gr:1', [
      ebook,
      projection('audiobook', DateTime(2017, 1, 1)),
    ])!;
    expect(differentYear.releaseDate, isNull);
  });

  test('a missing selected edition cannot borrow another publication', () {
    const seed = ChaptarrBook(
      id: 0,
      title: 'Ahsoka',
      foreignBookId: 'gr:1',
      foreignEditionId: 'gr:50',
    );
    const fresh = ChaptarrBook(
      id: 0,
      title: 'Ahsoka',
      foreignBookId: 'gr:1',
      foreignEditionId: 'gr:50',
      editions: [
        ChaptarrEdition(
          id: 89,
          foreignEditionId: 'gr:99',
          pageCount: 900,
          publisher: 'Another edition',
        ),
      ],
    );
    final publication =
        BookPublication.fromBook(enrichBookMetadata(seed, fresh));
    expect(publication.pages, 0);
    expect(publication.publisher, isEmpty);
  });

  test('sparse matching editions cannot erase an existing publication', () {
    final seed = ChaptarrBook(
      id: 0,
      title: 'Ahsoka',
      foreignBookId: 'gr:1',
      foreignEditionId: 'gr:50',
      editions: [
        ChaptarrEdition(
          id: 0,
          foreignEditionId: 'gr:50',
          releaseDate: DateTime(2016, 10, 11),
          pageCount: 400,
          publisher: 'The original publisher',
          format: 'Paperback',
        ),
      ],
    );
    const fresh = ChaptarrBook(
      id: 0,
      title: 'Ahsoka',
      foreignBookId: 'gr:1',
      foreignEditionId: 'gr:50',
      editions: [
        ChaptarrEdition(id: 88, foreignEditionId: 'gr:50'),
        ChaptarrEdition(id: 89, foreignEditionId: 'gr:99', pageCount: 900),
      ],
    );
    final enriched = enrichBookMetadata(seed, fresh);
    final publication = BookPublication.fromBook(enriched);
    expect(publication.year, 2016);
    expect(publication.pages, 400);
    expect(publication.publisher, 'The original publisher');
    expect(publication.format, 'Paperback');
    expect(enriched.foreignEditionId, 'gr:50');
  });

  test('verified work aliases enrich the original identity and same edition',
      () {
    final seed = metadata(edition: 'gr:50', overview: 'Short...', pages: 300);
    final fresh = metadata(id: 'hc:2', work: 'gr:1', edition: 'gr:50');
    final match = selectBookMetadata('gr:1', [fresh], initial: seed);
    expect(match, same(fresh));
    final enriched = enrichBookMetadata(seed, match!);
    expect(enriched.foreignBookId, 'gr:1');
    expect(enriched.title, seed.title);
    expect(enriched.pageCount, 400);
    expect(enriched.displayOverview, 'Full description.');
  });

  test(
      'same title, edition-number collisions and conflicting work aliases are rejected',
      () {
    expect(selectBookMetadata('gr:1', [metadata(id: 'gr:2')]), isNull);
    const editionCollision = ChaptarrBook(
        id: 0,
        title: 'The selected title',
        foreignBookId: 'hc:2',
        goodreadsBookId: 'gr:1');
    expect(selectBookMetadata('gr:1', [editionCollision]), isNull);
    expect(
        selectBookMetadata('gr:1', [metadata(hardcover: 'hc:3')],
            initial: metadata(hardcover: 'hc:2')),
        isNull);
  });

  test(
      'different editions can enrich prose without replacing selected publication',
      () {
    final seed = metadata(edition: 'gr:50', pages: 300);
    final fresh =
        metadata(edition: 'gr:60', pages: 900, overview: 'Richer prose.');
    final enriched = enrichBookMetadata(seed, fresh);
    expect(enriched.displayOverview, 'Richer prose.');
    expect(enriched.foreignEditionId, 'gr:50');
    expect(enriched.pageCount, 300);
  });

  test(
      'format projections retain the selected format instead of longest synopsis',
      () {
    final audio = metadata(
        format: 'audiobook', overview: 'A very long audio-specific synopsis.');
    final ebook = metadata(format: 'ebook', overview: 'Print description.');
    expect(
        selectBookMetadata('gr:1', [audio, ebook],
            initial: metadata(format: 'ebook')),
        same(ebook));
    expect(selectBookMetadata('gr:1', [audio, ebook]), isNull);
  });

  test(
      'multiple formats can share prose but never lend arbitrary publication fields',
      () {
    final results = [
      metadata(format: 'audiobook', pages: 0),
      metadata(format: 'ebook')
    ];
    final match = selectBookMetadata('gr:1', results)!;
    expect(match.displayOverview, 'Full description.');
    expect(match.pageCount, 0);
    expect(match.editions, isEmpty);
    expect(results, hasLength(2));
  });

  test('a sparse detailed response preserves existing descriptive fields', () {
    final seed = metadata(overview: 'Existing synopsis.', edition: 'gr:50');
    final fresh = metadata(overview: '', pages: 0, edition: 'gr:50');
    final enriched = enrichBookMetadata(seed, fresh);
    expect(enriched.displayOverview, seed.displayOverview);
    expect(enriched.pageCount, 400);
  });
}
