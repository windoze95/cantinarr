import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/logic/book_links.dart';
import 'package:cantinarr/features/media_detail/logic/title_links.dart';
import 'package:flutter_test/flutter_test.dart';

/// The Links line of a book page: which sites earn a chip, in what order,
/// and the exact page each one opens, built only from the ids Chaptarr's
/// BookResource carries.
void main() {
  test('a prefixed Goodreads book id opens that edition on Goodreads', () {
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        goodreadsBookId: 'gr:231198689',
      )),
      const [
        TitleLink('Goodreads', 'https://www.goodreads.com/book/show/231198689'),
      ],
    );
  });

  test('the leading edition id is preferred over the work id', () {
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        goodreadsWorkId: 'gr:51436013',
        editions: [ChaptarrEdition(id: 10, goodreadsEditionId: '5907')],
      )),
      const [
        TitleLink('Goodreads', 'https://www.goodreads.com/book/show/5907'),
      ],
    );
  });

  test('a work id alone opens the editions list, never /work/', () {
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        goodreadsWorkId: 'gr:51436013',
      )),
      const [
        TitleLink(
            'Goodreads', 'https://www.goodreads.com/work/editions/51436013'),
      ],
    );
  });

  test('Open Library links the work, else the leading edition, else an ISBN',
      () {
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        openLibraryWorkId: 'ol:OL262758W',
        editions: [
          ChaptarrEdition(
            id: 10,
            openLibraryEditionId: 'ol:OL7353617M',
            isbn13: '9781484705667',
          ),
        ],
      )),
      const [
        TitleLink('Open Library', 'https://openlibrary.org/works/OL262758W'),
      ],
    );
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        editions: [
          ChaptarrEdition(
            id: 10,
            openLibraryEditionId: 'ol:ol7353617m',
            isbn13: '9781484705667',
          ),
        ],
      )),
      const [
        TitleLink('Open Library', 'https://openlibrary.org/books/OL7353617M'),
      ],
    );
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        editions: [
          ChaptarrEdition(
              id: 10, isbn13: '978-1-4847-0566-7', isbn10: '1484705661'),
        ],
      )),
      const [
        TitleLink('Open Library', 'https://openlibrary.org/isbn/9781484705667'),
      ],
    );
  });

  test('an ISBN-10 with an X check digit is accepted', () {
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        editions: [ChaptarrEdition(id: 10, isbn10: '080442957x')],
      )),
      const [
        TitleLink('Open Library', 'https://openlibrary.org/isbn/080442957X'),
      ],
    );
  });

  test('an id that is not shaped like its provider is treated as unknown', () {
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        goodreadsBookId: 'gr:abc',
        goodreadsWorkId: 'gr:',
        openLibraryWorkId: 'ol:12345',
        editions: [
          ChaptarrEdition(
            id: 10,
            goodreadsEditionId: 'gr:12a',
            openLibraryEditionId: 'ol:OL7353617W',
            isbn13: '978148470566',
            isbn10: '14847056',
          ),
        ],
      )),
      isEmpty,
    );
  });

  test('Hardcover is linked only from a declared https hardcover.app page', () {
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        hardcoverBookId: 'hc:12345',
      )),
      isEmpty,
    );
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        links: [
          ChaptarrLink(
              name: 'Hardcover', url: 'http://hardcover.app/books/ahsoka'),
          ChaptarrLink(
              name: 'Hardcover',
              url: 'https://hardcover.example.com/books/ahsoka'),
          ChaptarrLink(
              name: 'Hardcover',
              url: 'https://hardcover.app/authors/e-k-johnston'),
          ChaptarrLink(name: 'Hardcover', url: 'https://hardcover.app/books/'),
        ],
      )),
      isEmpty,
    );
    // A declared Goodreads URL is not how a Goodreads chip is built either:
    // only the ids are trusted.
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        links: [
          ChaptarrLink(
              name: 'Goodreads',
              url: 'https://www.goodreads.com/book/show/5907'),
          ChaptarrLink(
              name: 'Hardcover', url: 'https://hardcover.app/books/ahsoka'),
        ],
      )),
      const [TitleLink('Hardcover', 'https://hardcover.app/books/ahsoka')],
    );
  });

  test('the chips keep a fixed order: Goodreads, Open Library, Hardcover', () {
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        openLibraryWorkId: 'ol:OL262758W',
        goodreadsBookId: 'gr:5907',
        links: [
          ChaptarrLink(
              name: 'Hardcover', url: 'https://www.hardcover.app/books/ahsoka'),
        ],
      )),
      const [
        TitleLink('Goodreads', 'https://www.goodreads.com/book/show/5907'),
        TitleLink('Open Library', 'https://openlibrary.org/works/OL262758W'),
        TitleLink('Hardcover', 'https://www.hardcover.app/books/ahsoka'),
      ],
    );
  });

  test('a book with no ids yields no links', () {
    expect(bookLinks(const ChaptarrBook(id: 1, title: 'Ahsoka')), isEmpty);
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        editions: [ChaptarrEdition(id: 10)],
      )),
      isEmpty,
    );
  });

  test('a monitored edition leads over an earlier unmonitored one', () {
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        editions: [
          ChaptarrEdition(
            id: 10,
            monitored: false,
            goodreadsEditionId: '111',
            isbn13: '9780000000001',
          ),
          ChaptarrEdition(
            id: 11,
            goodreadsEditionId: '222',
            isbn13: '9780000000002',
          ),
        ],
      )),
      const [
        TitleLink('Goodreads', 'https://www.goodreads.com/book/show/222'),
        TitleLink('Open Library', 'https://openlibrary.org/isbn/9780000000002'),
      ],
    );
    // With nothing monitored, the first edition leads.
    expect(
      bookLinks(const ChaptarrBook(
        id: 1,
        title: 'Ahsoka',
        editions: [
          ChaptarrEdition(id: 10, monitored: false, goodreadsEditionId: '111'),
          ChaptarrEdition(id: 11, monitored: false, goodreadsEditionId: '222'),
        ],
      )),
      const [TitleLink('Goodreads', 'https://www.goodreads.com/book/show/111')],
    );
  });

  test('the wire shape parses: prefixed book ids, a numeric edition id, links',
      () {
    final book = ChaptarrBook.fromJson({
      'id': 1,
      'title': 'Ahsoka',
      'goodreadsBookId': 'gr:231198689',
      'goodreadsWorkId': 'gr:51436013',
      'openLibraryWorkId': 'ol:OL262758W',
      'hardcoverBookId': 'hc:12345',
      'links': [
        {'name': 'Hardcover', 'url': 'https://hardcover.app/books/ahsoka'},
      ],
      'editions': [
        {
          'id': 10,
          'goodreadsEditionId': 231198689,
          'openLibraryEditionId': 'ol:OL7353617M',
          'isbn13': '9781484705667',
          'isbn10': '1484705661',
          'monitored': true,
        },
      ],
    });
    expect(book.editions.single.goodreadsEditionId, '231198689');
    expect(book.editions.single.isbn10, '1484705661');
    expect(book.hardcoverBookId, 'hc:12345');
    expect(book.links.single.url, 'https://hardcover.app/books/ahsoka');
    expect(bookLinks(book), const [
      TitleLink('Goodreads', 'https://www.goodreads.com/book/show/231198689'),
      TitleLink('Open Library', 'https://openlibrary.org/works/OL262758W'),
      TitleLink('Hardcover', 'https://hardcover.app/books/ahsoka'),
    ]);
    // Round trip: the edition id goes back out as the number it came in as.
    final json = book.toJson();
    final editions = json['editions'] as List<dynamic>;
    expect((editions.single as Map<String, dynamic>)['goodreadsEditionId'],
        231198689);
    expect(ChaptarrBook.fromJson(json).toJson(), json);
  });
}
