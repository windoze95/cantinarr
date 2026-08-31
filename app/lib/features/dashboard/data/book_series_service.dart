import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../request/data/book_ownership.dart';

/// The orders the Series row can be read in.
///
/// There is deliberately no "date added" here, unlike the Authors row: a series
/// is not a library record — it exists only as a string on each book — and no
/// book carries an added date either, so the server has no honest date to order
/// by. See `library_series.go`.
enum SeriesSort {
  mostBooks('books', 'Most books'),
  name('name', 'Name');

  const SeriesSort(this.wire, this.label);

  final String wire;
  final String label;
}

/// One series the library holds at least one book of.
class LibrarySeries {
  /// The series' identity. Chaptarr exposes no library-wide series record, so
  /// the name parsed off each book is what addresses one — which is also what
  /// keeps a series that spans several authors in one piece.
  final String name;

  /// The earliest books' cover paths, in reading order, each resolved through
  /// [chaptarrImageSource]. The card stacks them so a series looks like a run
  /// of books rather than one of them; a series with one cover has one.
  final List<String> covers;

  /// How many distinct titles of the series the library tracks.
  final int titleCount;

  /// How many of those have a file on disk in some format.
  final int availableCount;

  const LibrarySeries({
    required this.name,
    this.covers = const [],
    this.titleCount = 0,
    this.availableCount = 0,
  });

  factory LibrarySeries.fromJson(Map<String, dynamic> json) => LibrarySeries(
        name: json['name'] as String? ?? '',
        covers: (json['covers'] as List?)
                ?.whereType<String>()
                .where((url) => url.isNotEmpty)
                .toList() ??
            const [],
        titleCount: (json['title_count'] as num?)?.toInt() ?? 0,
        availableCount: (json['available_count'] as num?)?.toInt() ?? 0,
      );

  /// The card's one-line count, in the same words the Authors row uses so the
  /// two shelves read alike.
  String get countLabel {
    if (titleCount <= 0) return '';
    final books = titleCount == 1 ? 'book' : 'books';
    if (availableCount <= 0) return '$titleCount $books';
    if (availableCount >= titleCount) return '$titleCount $books · all available';
    return '$availableCount of $titleCount $books available';
  }
}

/// One title of a series: the ownership shape every book surface uses, plus
/// where it falls in the series.
class SeriesTitle {
  final OwnedTitle title;

  /// The raw position as the library states it ("13", "2A", "1.5, 1.6, 1.7"),
  /// or empty when the series names none for this title.
  final String position;

  const SeriesTitle({required this.title, this.position = ''});

  factory SeriesTitle.fromJson(Map<String, dynamic> json) => SeriesTitle(
        title: OwnedTitle.fromJson(json),
        position: json['position'] as String? ?? '',
      );
}

/// One page of the Series row: what to show, and how many the library holds.
class BookSeriesPage {
  final List<LibrarySeries> series;
  final int total;

  const BookSeriesPage({required this.series, this.total = 0});

  int get hiddenCount => total > series.length ? total - series.length : 0;
}

/// One series plus every title of it the library tracks, in reading order.
class BookSeriesDetail {
  final LibrarySeries series;
  final List<SeriesTitle> titles;

  const BookSeriesDetail({required this.series, required this.titles});

  factory BookSeriesDetail.fromJson(Map<String, dynamic> json) {
    final raw = json['titles'];
    return BookSeriesDetail(
      series: LibrarySeries.fromJson(
          json['series'] as Map<String, dynamic>? ?? const {}),
      titles: raw is List
          ? raw
              .whereType<Map<String, dynamic>>()
              .map(SeriesTitle.fromJson)
              .toList()
          : const [],
    );
  }
}

/// Fetches the book library's series, for the browse row and the series page.
class BookSeriesService {
  final Dio _dio;

  BookSeriesService({required Dio backendDio}) : _dio = backendDio;

  Future<BookSeriesPage> fetchSeries({
    String? instanceId,
    SeriesSort sort = SeriesSort.mostBooks,
  }) async {
    final resp = await _dio.get(
      '/api/requests/book-series',
      queryParameters: {
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
        'sort': sort.wire,
      },
    );
    final data = resp.data;
    final series = data is Map ? data['series'] : null;
    if (series is! List) {
      throw const FormatException('Book series response is invalid');
    }
    final parsed = series
        .whereType<Map<String, dynamic>>()
        .map(LibrarySeries.fromJson)
        .toList();
    final total = (data is Map ? (data['total'] as num?)?.toInt() : null) ?? 0;
    return BookSeriesPage(
      series: parsed,
      total: total > parsed.length ? total : parsed.length,
    );
  }

  Future<BookSeriesDetail> fetchSeriesDetail(
    String name, {
    String? instanceId,
  }) async {
    final resp = await _dio.get(
      '/api/requests/book-series-detail',
      queryParameters: {
        'name': name,
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
      },
    );
    final data = resp.data;
    if (data is! Map<String, dynamic>) {
      throw const FormatException('Book series response is invalid');
    }
    return BookSeriesDetail.fromJson(data);
  }
}

/// The order the Series row is currently read in. Session state, like the
/// Authors row's.
final bookSeriesSortProvider =
    StateProvider<SeriesSort>((ref) => SeriesSort.mostBooks);

/// The series of the drawer's active Chaptarr library, in the selected order.
///
/// Sort and instance are watched here rather than being family keys so the
/// previous row stays on screen while a new order loads.
final bookSeriesProvider =
    FutureProvider.autoDispose<BookSeriesPage>((ref) async {
  final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
  final sort = ref.watch(bookSeriesSortProvider);
  final dio = ref.read(backendClientProvider);
  return BookSeriesService(backendDio: dio)
      .fetchSeries(instanceId: instanceId, sort: sort);
});

/// The series a detail page is pinned to.
typedef BookSeriesRef = ({String? instanceId, String name});

/// One series' page data. Uncached server-side: it is read to decide what to
/// request.
final bookSeriesDetailProvider = FutureProvider.autoDispose
    .family<BookSeriesDetail, BookSeriesRef>((ref, target) async {
  final dio = ref.read(backendClientProvider);
  return BookSeriesService(backendDio: dio)
      .fetchSeriesDetail(target.name, instanceId: target.instanceId);
});
