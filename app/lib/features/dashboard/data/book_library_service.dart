import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../request/data/book_ownership.dart';

/// Fetches the backend's lean owned-books digest — what the user already has in
/// their Chaptarr library, reduced per title+format — so the Books search can
/// mark results as owned and stop re-requesting a format they already have.
class BookLibraryService {
  final Dio _dio;

  BookLibraryService({required Dio backendDio}) : _dio = backendDio;

  Future<List<OwnedTitle>> fetchOwnedTitles() async {
    try {
      final resp = await _dio.get('/api/requests/book-library');
      final data = resp.data;
      final titles = data is Map ? data['titles'] : null;
      if (titles is! List) return const [];
      return titles
          .whereType<Map<String, dynamic>>()
          .map(OwnedTitle.fromJson)
          .toList();
    } catch (_) {
      // Degrade silently: search still works, just without ownership marks.
      return const [];
    }
  }
}

/// The user's owned-book digest. Fetched while the Books tab is open; the
/// backend caches it (~2 min) so refetches are cheap. Degrades to an empty list
/// on any failure (no ownership info, search unaffected).
final ownedBooksProvider =
    FutureProvider.autoDispose<List<OwnedTitle>>((ref) async {
  final dio = ref.read(backendClientProvider);
  return BookLibraryService(backendDio: dio).fetchOwnedTitles();
});
