import '../data/chaptarr_models.dart';
import '../../request/data/book_ownership.dart';

final _digits = RegExp(r'^[0-9]+$');
final _olWork = RegExp(r'^OL[0-9]+W$');
final _olEdition = RegExp(r'^OL[0-9]+M$');

String? _providerKey(String kind, String prefix, String? value, RegExp shape) {
  var id = value?.trim() ?? '';
  if (id.contains(':')) {
    if (id.split(':').first.toLowerCase() != prefix) return null;
    id = id.substring(id.indexOf(':') + 1);
  }
  id = id.toUpperCase();
  if (!shape.hasMatch(id) || id == '0') return null;
  if (shape == _digits) id = id.replaceFirst(RegExp(r'^0+'), '');
  return id.isEmpty ? null : '$kind:$id';
}

Set<String> nativeBookIdentityKeys(String? foreignId) {
  final id = foreignId?.trim() ?? '';
  if (id.isEmpty) return {};
  final prefix = id.split(':').first.toLowerCase();
  final typed = switch (prefix) {
    'gr' => _providerKey('gr-work', 'gr', id, _digits),
    'hc' => _providerKey('hc-book', 'hc', id, _digits),
    'ol' => _providerKey('ol-work', 'ol', id, _olWork),
    _ => null,
  };
  return {'native-book:$id', if (typed != null) typed};
}

/// Same typed keys as the server's chaptarr.Book.IdentityKeys. Goodreads works
/// and editions have separate namespaces even when their numeric IDs agree.
Set<String> bookIdentityKeys(ChaptarrBook book) {
  final keys = nativeBookIdentityKeys(book.foreignBookId);
  void add(String kind, String prefix, String? id, RegExp shape) {
    final key = _providerKey(kind, prefix, id, shape);
    if (key != null) keys.add(key);
  }

  add('gr-work', 'gr', book.goodreadsWorkId, _digits);
  add('gr-edition', 'gr', book.goodreadsBookId, _digits);
  add('hc-book', 'hc', book.hardcoverBookId, _digits);
  add('ol-work', 'ol', book.openLibraryWorkId, _olWork);
  for (final edition in [
    ChaptarrEdition(id: 0, foreignEditionId: book.foreignEditionId),
    ...book.editions
  ]) {
    add('gr-edition', 'gr', edition.goodreadsEditionId, _digits);
    add('ol-edition', 'ol', edition.openLibraryEditionId, _olEdition);
    add(
        'hc-edition',
        'hc',
        edition.hardcoverEditionId?.replaceFirst('hc:edition:', 'hc:'),
        _digits);
    final foreign = edition.foreignEditionId ?? '';
    if (foreign.toLowerCase().startsWith('gr:')) {
      add('gr-edition', 'gr', foreign, _digits);
    }
    if (foreign.toLowerCase().startsWith('ol:')) {
      add('ol-edition', 'ol', foreign, _olEdition);
    }
    if (foreign.toLowerCase().startsWith('hc:edition:')) {
      add('hc-edition', 'hc', foreign.substring('hc:edition:'.length), _digits);
    }
    for (final isbn in [edition.isbn13, edition.isbn10]) {
      final valid = normalizedBookISBN(isbn);
      if (valid != null) keys.add('isbn:$valid');
    }
  }
  return keys;
}

String? normalizedBookISBN(String? value) {
  var isbn = (value ?? '').toUpperCase().replaceAll(RegExp(r'[-\s]'), '');
  if (isbn.length == 10) {
    var sum = 0;
    for (var i = 0; i < 10; i++) {
      final n = i == 9 && isbn[i] == 'X' ? 10 : int.tryParse(isbn[i]);
      if (n == null) return null;
      sum += (10 - i) * n;
    }
    if (sum % 11 != 0) return null;
    isbn = '978${isbn.substring(0, 9)}';
    sum = 0;
    for (var i = 0; i < 12; i++) {
      sum += int.parse(isbn[i]) * (1 + 2 * (i % 2));
    }
    return '$isbn${(10 - sum % 10) % 10}';
  }
  if (isbn.length != 13 ||
      !_digits.hasMatch(isbn) ||
      !(isbn.startsWith('978') || isbn.startsWith('979'))) {
    return null;
  }
  var sum = 0;
  for (var i = 0; i < 13; i++) {
    sum += int.parse(isbn[i]) * (1 + 2 * (i % 2));
  }
  return sum % 10 == 0 ? isbn : null;
}

class BookLibraryMatch {
  final OwnedTitle? title;
  final bool ambiguous;
  const BookLibraryMatch({this.title, this.ambiguous = false});
}

/// Exact native identity wins. Otherwise all shared explicit identifiers must
/// name a single library title. Neither title nor author text is evidence.
BookLibraryMatch matchBookToLibrary(
    ChaptarrBook book, List<OwnedTitle> library) {
  final foreignId = book.foreignBookId?.trim() ?? '';
  for (final title in library) {
    if (foreignId.isNotEmpty && title.foreignBookId == foreignId) {
      return BookLibraryMatch(title: title);
    }
  }
  final keys = bookIdentityKeys(book);
  final matches = <String, OwnedTitle>{};
  for (final title in library) {
    if (title.foreignBookId.isEmpty) continue;
    final candidates = {
      ...title.identityKeys,
      ...nativeBookIdentityKeys(title.foreignBookId)
    };
    if (keys.any(candidates.contains)) matches[title.foreignBookId] = title;
  }
  if (matches.length > 1) return const BookLibraryMatch(ambiguous: true);
  return BookLibraryMatch(title: matches.values.firstOrNull);
}
