import 'dart:convert';
import 'dart:io';

import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/logic/book_identity.dart';
import 'package:cantinarr/features/chaptarr/logic/book_publication.dart';
import 'package:cantinarr/features/request/data/book_ownership.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final fixture =
      jsonDecode(File('../testdata/book_identity.json').readAsStringSync())
          as Map<String, dynamic>;
  for (final raw in fixture['cases'] as List) {
    final row = raw as Map<String, dynamic>;
    test(row['name'] as String, () {
      final selected =
          ChaptarrBook.fromJson(row['selected'] as Map<String, dynamic>);
      final library = (row['library'] as List).map((b) {
        final book = ChaptarrBook.fromJson(b as Map<String, dynamic>);
        return OwnedTitle(
            title: book.title,
            author: '',
            foreignBookId: book.foreignBookId ?? '',
            identityKeys: bookIdentityKeys(book).toList(),
            ownership: const BookOwnership());
      }).toList();
      final result = matchBookToLibrary(selected, library);
      expect(result.ambiguous, row['ambiguous'] ?? false);
      expect(result.title?.foreignBookId ?? '', row['expected'] ?? '');
    });
  }

  test('different libraries cannot contribute an identifier binding', () {
    const book = ChaptarrBook(id: 0, title: 'Ahsoka', foreignBookId: 'gr:101');
    expect(matchBookToLibrary(book, []).title, isNull);
  });

  test('publication fields come from the selected leading edition', () {
    final selected = ChaptarrBook.fromJson({
      'title': 'Ahsoka (Star Wars)',
      'foreignEditionId': 'gr:400',
      'editions': [
        {
          'id': 1,
          'foreignEditionId': 'gr:223',
          'pageCount': 223,
          'publisher': 'Another publisher',
          'format': 'eBook'
        },
        {
          'id': 2,
          'foreignEditionId': 'gr:400',
          'pageCount': 400,
          'publisher': 'Selected publisher',
          'format': 'Paperback',
          'releaseDate': '2016-10-11T00:00:00Z'
        },
      ],
    });
    final publication = BookPublication.fromBook(selected);
    expect(publication.summary, '2016 · 400 pages');
    expect(publication.editionLabel, 'Selected publisher · Paperback');
  });

  test('placeholder dates remain visible but explicitly unconfirmed', () {
    expect(bookPublicationYearLabel(2079, currentYear: 2026),
        'Date unconfirmed (2079)');
    expect(bookPublicationYearLabel(2031, currentYear: 2026), '2031');
    expect(bookPublicationYearLabel(2032, currentYear: 2026),
        'Date unconfirmed (2032)');
    expect(bookPublicationYearLabel(0, currentYear: 2026), '');
    expect(bookPublicationYearLabel(2016, currentYear: 2026), '2016');
  });
}
