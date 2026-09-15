import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';

/// One book on Hardcover's trending list, as the server hydrated it.
///
/// `foreignId` is the `hc:<id>` Chaptarr understands natively, so a tap opens
/// the same book page a Chaptarr search result would, and [identityKeys] are
/// the typed keys the owned-books digest also carries (`hc-book:<id>`,
/// `isbn:<13>`), so ownership is an exact key match rather than a title guess.
class TrendingBook {
  final int hardcoverId;
  final String foreignId;
  final String title;
  final List<String> authors;
  final int? year;
  final double? rating;
  final int ratingsCount;
  final int readersCount;
  final String description;
  final String? imageUrl;
  final String series;
  final double? seriesPosition;
  final List<String> isbn13s;

  const TrendingBook({
    required this.hardcoverId,
    required this.foreignId,
    required this.title,
    this.authors = const [],
    this.year,
    this.rating,
    this.ratingsCount = 0,
    this.readersCount = 0,
    this.description = '',
    this.imageUrl,
    this.series = '',
    this.seriesPosition,
    this.isbn13s = const [],
  });

  String get author => authors.join(', ');

  /// Typed identity keys in the digest's vocabulary.
  List<String> get identityKeys => [
        'hc-book:$hardcoverId',
        for (final isbn in isbn13s) 'isbn:$isbn',
      ];

  factory TrendingBook.fromJson(Map<String, dynamic> json) {
    final id = (json['hardcover_id'] as num?)?.toInt() ?? 0;
    final foreignId = json['foreign_id'] as String? ?? '';
    final title = (json['title'] as String? ?? '').trim();
    if (id <= 0 || foreignId.isEmpty || title.isEmpty) {
      throw const FormatException('Invalid trending book');
    }
    final image = json['image_url'] as String?;
    return TrendingBook(
      hardcoverId: id,
      foreignId: foreignId,
      title: title,
      authors: (json['authors'] as List?)?.whereType<String>().toList() ??
          const [],
      year: (json['year'] as num?)?.toInt(),
      rating: (json['rating'] as num?)?.toDouble(),
      ratingsCount: (json['ratings_count'] as num?)?.toInt() ?? 0,
      readersCount: (json['readers_count'] as num?)?.toInt() ?? 0,
      description: json['description'] as String? ?? '',
      imageUrl: image != null && image.startsWith('https://') ? image : null,
      series: json['series'] as String? ?? '',
      seriesPosition: (json['series_position'] as num?)?.toDouble(),
      isbn13s:
          (json['isbn13s'] as List?)?.whereType<String>().toList() ?? const [],
    );
  }
}

/// The trending feed for one Chaptarr instance.
///
/// [connected] is false when the instance holds no Hardcover token: the row
/// then offers an admin the way to Settings. It is null when the server did
/// not say (an older server), which hides the row rather than rendering a
/// wrong answer.
class TrendingBooks {
  final String instanceId;
  final bool? connected;
  final List<TrendingBook> books;

  const TrendingBooks({
    required this.instanceId,
    required this.connected,
    required this.books,
  });

  factory TrendingBooks.fromJson(dynamic json) {
    final map =
        json is Map<String, dynamic> ? json : const <String, dynamic>{};
    final books = <TrendingBook>[];
    for (final item in (map['books'] as List?) ?? const []) {
      if (item is! Map<String, dynamic>) continue;
      try {
        books.add(TrendingBook.fromJson(item));
      } on FormatException {
        // One malformed entry never hides the rest of the list.
      }
    }
    return TrendingBooks(
      instanceId: map['instance_id'] as String? ?? '',
      connected: map['connected'] is bool ? map['connected'] as bool : null,
      books: books,
    );
  }
}

class TrendingBooksService {
  final Dio _dio;

  TrendingBooksService({required Dio backendDio}) : _dio = backendDio;

  Future<TrendingBooks> fetch({String? instanceId}) async {
    final resp = await _dio.get(
      '/api/discover/books/trending',
      queryParameters: {
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
      },
    );
    return TrendingBooks.fromJson(resp.data);
  }
}

/// Trending books for one Chaptarr instance. Keyed on the instance id so
/// switching libraries can never show the previous library's connection.
final trendingBooksForInstanceProvider = FutureProvider.autoDispose
    .family<TrendingBooks, String?>((ref, instanceId) async {
  final dio = ref.read(backendClientProvider);
  return TrendingBooksService(backendDio: dio).fetch(instanceId: instanceId);
});

/// The row follows the drawer's active Chaptarr instance, like the other rows.
final trendingBooksProvider =
    FutureProvider.autoDispose<TrendingBooks>((ref) async {
  final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
  return ref.watch(trendingBooksForInstanceProvider(instanceId).future);
});
