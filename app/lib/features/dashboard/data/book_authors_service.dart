import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../request/data/book_ownership.dart';

/// The orders the Authors row can be read in.
///
/// The order is applied by the server, not here: the row is capped, so sorting
/// an already-capped list would mean "the most-collected authors, alphabetised"
/// — a row that looks complete while omitting everyone below the cut.
enum AuthorSort {
  mostBooks('books', 'Most books'),
  name('name', 'Name'),
  dateAdded('added', 'Date added');

  const AuthorSort(this.wire, this.label);

  /// The value the API expects.
  final String wire;

  /// What the menu shows.
  final String label;
}

/// One author the book library holds titles for.
///
/// Counts are per *title*, not per record: a title owned as both an ebook and an
/// audiobook is two Chaptarr records sharing a foreignBookId, and the server
/// reduces them the same way the ownership digest does before counting.
class LibraryAuthor {
  /// The metadata-provider id this author is addressed by. Empty for a record
  /// the library has not keyed yet, which leaves the author visible but not
  /// openable — the same treatment a book with no foreignBookId gets.
  final String foreignAuthorId;
  final String name;

  /// The author's image path — relative (`/MediaCover/...`) for library art, or
  /// an absolute metadata-CDN URL. Resolve it through [chaptarrImageSource].
  final String image;

  /// How many distinct titles by this author the library tracks.
  final int titleCount;

  /// How many of those have a file on disk in some format.
  final int availableCount;

  /// When the author entered the library. Null when the record carries no date
  /// — it makes no recency claim, and the server sorts it last accordingly.
  final DateTime? added;

  const LibraryAuthor({
    required this.foreignAuthorId,
    required this.name,
    this.image = '',
    this.titleCount = 0,
    this.availableCount = 0,
    this.added,
  });

  factory LibraryAuthor.fromJson(Map<String, dynamic> json) => LibraryAuthor(
        foreignAuthorId: json['foreign_author_id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        image: json['image'] as String? ?? '',
        titleCount: (json['title_count'] as num?)?.toInt() ?? 0,
        availableCount: (json['available_count'] as num?)?.toInt() ?? 0,
        added: DateTime.tryParse(json['added'] as String? ?? ''),
      );

  /// The card's one-line count, in requester vocabulary. It always says what the
  /// number counted, so "3 of 12 available" can never be misread as a library
  /// that only holds three books.
  String get countLabel {
    if (titleCount <= 0) return '';
    final books = titleCount == 1 ? 'book' : 'books';
    if (availableCount <= 0) return '$titleCount $books';
    if (availableCount >= titleCount) return '$titleCount $books · all available';
    return '$availableCount of $titleCount $books available';
  }
}

/// One page of the Authors row: the authors to show, and how many the library
/// actually holds.
///
/// The two differ once a library outgrows the row's cap, and the row has to be
/// able to say so — a shelf that simply stops reads as the whole library.
class BookAuthorsPage {
  final List<LibraryAuthor> authors;

  /// How many authors the library holds, before the row's cap.
  final int total;

  const BookAuthorsPage({required this.authors, this.total = 0});

  /// How many authors this page is not showing.
  int get hiddenCount => total > authors.length ? total - authors.length : 0;
}

/// One author plus every title of theirs the library tracks.
class BookAuthorDetail {
  final LibraryAuthor author;

  /// The author's titles, newest first, each carrying the same per-format
  /// ownership the search digest uses — so the page renders the same pills.
  final List<OwnedTitle> titles;

  const BookAuthorDetail({required this.author, required this.titles});

  factory BookAuthorDetail.fromJson(Map<String, dynamic> json) {
    final rawTitles = json['titles'];
    return BookAuthorDetail(
      author: LibraryAuthor.fromJson(
          json['author'] as Map<String, dynamic>? ?? const {}),
      titles: rawTitles is List
          ? rawTitles
              .whereType<Map<String, dynamic>>()
              .map(OwnedTitle.fromJson)
              .toList()
          : const [],
    );
  }
}

/// Fetches the book library's authors, so the Books tab can offer a browse row
/// and an author page.
class BookAuthorsService {
  final Dio _dio;

  BookAuthorsService({required Dio backendDio}) : _dio = backendDio;

  Future<BookAuthorsPage> fetchAuthors({
    String? instanceId,
    AuthorSort sort = AuthorSort.mostBooks,
  }) async {
    final resp = await _dio.get(
      '/api/requests/book-authors',
      queryParameters: {
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
        'sort': sort.wire,
      },
    );
    final data = resp.data;
    final authors = data is Map ? data['authors'] : null;
    if (authors is! List) {
      throw const FormatException('Book authors response is invalid');
    }
    final parsed = authors
        .whereType<Map<String, dynamic>>()
        .map(LibraryAuthor.fromJson)
        .toList();
    // A server too old to report a total says nothing about truncation, so the
    // row claims none rather than inventing one.
    final total = (data is Map ? (data['total'] as num?)?.toInt() : null) ?? 0;
    return BookAuthorsPage(
      authors: parsed,
      total: total > parsed.length ? total : parsed.length,
    );
  }

  Future<BookAuthorDetail> fetchAuthor(
    String foreignAuthorId, {
    String? instanceId,
  }) async {
    final resp = await _dio.get(
      '/api/requests/book-author',
      queryParameters: {
        'foreign_id': foreignAuthorId,
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
      },
    );
    final data = resp.data;
    if (data is! Map<String, dynamic>) {
      throw const FormatException('Book author response is invalid');
    }
    return BookAuthorDetail.fromJson(data);
  }
}

/// The order the Authors row is currently read in.
///
/// Deliberately session state, not a stored preference: it is a way to look
/// through the shelf right now, and a row that silently remembers last week's
/// ordering is harder to trust than one that starts where everyone else's does.
final bookAuthorsSortProvider =
    StateProvider<AuthorSort>((ref) => AuthorSort.mostBooks);

/// The authors of the drawer's active Chaptarr library, in the selected order.
///
/// Sort and instance are watched here rather than being family keys on purpose:
/// one provider instance spans every order, so changing the order keeps the
/// previous list on screen while the new one loads instead of collapsing the
/// row (and the menu that was just used) to nothing.
final bookAuthorsProvider =
    FutureProvider.autoDispose<BookAuthorsPage>((ref) async {
  final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
  final sort = ref.watch(bookAuthorsSortProvider);
  final dio = ref.read(backendClientProvider);
  return BookAuthorsService(backendDio: dio)
      .fetchAuthors(instanceId: instanceId, sort: sort);
});

/// The author a detail page is pinned to: an explicit instance id plus the
/// author's foreignAuthorId, so a pinned page can never read another library's
/// answer for the same author.
typedef BookAuthorRef = ({String? instanceId, String foreignAuthorId});

/// One author's page data. Deliberately uncached server-side — the page is
/// opened to decide what to request, so it must show a book requested seconds
/// ago as Requested.
final bookAuthorDetailProvider = FutureProvider.autoDispose
    .family<BookAuthorDetail, BookAuthorRef>((ref, target) async {
  final dio = ref.read(backendClientProvider);
  return BookAuthorsService(backendDio: dio).fetchAuthor(
    target.foreignAuthorId,
    instanceId: target.instanceId,
  );
});
