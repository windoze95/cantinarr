import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../request/data/book_ownership.dart';

/// Fetches the backend's lean owned-books digest — what the user already has in
/// their Chaptarr library, reduced per title+format — so the Books search can
/// mark results as owned and stop re-requesting a format they already have.
class BookLibraryService {
  final Dio _dio;

  BookLibraryService({required Dio backendDio}) : _dio = backendDio;

  Future<List<OwnedTitle>> fetchOwnedTitles({String? instanceId}) async {
    final resp = await _dio.get(
      '/api/requests/book-library',
      queryParameters: {
        if (instanceId != null && instanceId.isNotEmpty)
          'instance_id': instanceId,
      },
    );
    final data = resp.data;
    final titles = data is Map ? data['titles'] : null;
    if (titles is! List) {
      throw const FormatException('Book library response is invalid');
    }
    return titles
        .whereType<Map<String, dynamic>>()
        .map(OwnedTitle.fromJson)
        .toList();
  }
}

/// The user's owned-book digest for the actively selected Chaptarr instance.
/// Failures remain AsyncError so callers never confuse an unreachable library
/// with a genuinely empty one.
final ownedBooksForInstanceProvider = FutureProvider.autoDispose
    .family<List<OwnedTitle>, String?>((ref, instanceId) async {
  final dio = ref.read(backendClientProvider);
  return BookLibraryService(backendDio: dio)
      .fetchOwnedTitles(instanceId: instanceId);
});

/// Convenience projection for search, which always follows the drawer's active
/// Chaptarr instance. Pinned detail routes use [ownedBooksForInstanceProvider]
/// directly so another library can never bleed ownership into their state.
final ownedBooksProvider =
    FutureProvider.autoDispose<List<OwnedTitle>>((ref) async {
  final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
  return ref.watch(ownedBooksForInstanceProvider(instanceId).future);
});
