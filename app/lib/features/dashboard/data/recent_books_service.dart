import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';

/// One library record that recently gained a file.
///
/// Ebook and audiobook are separate Chaptarr records sharing a foreignBookId,
/// and they arrive at different times, so each is its own entry here.
class RecentBook {
  final int bookId;
  final String foreignBookId;
  final String title;
  final String format;
  final String cover;
  final DateTime? importedAt;

  const RecentBook({
    required this.bookId,
    required this.foreignBookId,
    required this.title,
    required this.format,
    required this.cover,
    this.importedAt,
  });

  factory RecentBook.fromJson(Map<String, dynamic> json) => RecentBook(
        bookId: (json['book_id'] as num?)?.toInt() ?? 0,
        foreignBookId: json['foreign_book_id'] as String? ?? '',
        title: json['title'] as String? ?? '',
        format: json['format'] as String? ?? '',
        cover: json['cover'] as String? ?? '',
        importedAt: DateTime.tryParse(json['imported_at'] as String? ?? ''),
      );

  /// Requester-facing format label, or null when the record's format is
  /// unknown and a subtitle would say nothing.
  String? get formatLabel {
    switch (format) {
      case 'ebook':
        return 'eBook';
      case 'audiobook':
        return 'Audiobook';
      default:
        return null;
    }
  }
}

/// Fetches the newest book-file imports for a Chaptarr instance, so the Books
/// tab can show what recently landed.
class RecentBooksService {
  final Dio _dio;

  RecentBooksService({required Dio backendDio}) : _dio = backendDio;

  Future<List<RecentBook>> fetchRecent({String? instanceId, int limit = 20}) async {
    final resp = await _dio.get(
      '/api/requests/book-recent',
      queryParameters: {
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
        'limit': limit,
      },
    );
    final data = resp.data;
    final items = data is Map ? data['items'] : null;
    if (items is! List) {
      throw const FormatException('Recent books response is invalid');
    }
    return items
        .whereType<Map<String, dynamic>>()
        .map(RecentBook.fromJson)
        .toList();
  }
}

/// Recently added books for one Chaptarr instance. Keyed on the instance id so
/// switching libraries can never show the previous library's books.
final recentBooksForInstanceProvider = FutureProvider.autoDispose
    .family<List<RecentBook>, String?>((ref, instanceId) async {
  final dio = ref.read(backendClientProvider);
  return RecentBooksService(backendDio: dio).fetchRecent(instanceId: instanceId);
});

/// The row follows the drawer's active Chaptarr instance, like search does.
final recentBooksProvider =
    FutureProvider.autoDispose<List<RecentBook>>((ref) async {
  final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
  return ref.watch(recentBooksForInstanceProvider(instanceId).future);
});
