import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Handoff from a saved request or retired link to native book search.
final bookDiscoverySearchSeedProvider =
    StateProvider<({String query, String? instanceId})?>((_) => null);

@immutable
class BookBrowseQuery {
  final String feed;
  final String? instanceId;
  final String? genre;
  const BookBrowseQuery({this.feed = 'popular', this.instanceId, this.genre});
  String get location => Uri(path: '/browse/books/$feed', queryParameters: {
        if (instanceId != null) 'instance_id': instanceId,
        if (genre != null) 'genre': genre!,
      }).toString();
  static BookBrowseQuery? tryParse(Uri uri) {
    final p = uri.pathSegments;
    final id = uri.queryParameters['instance_id'];
    final genre = uri.queryParameters['genre'];
    if (p.length != 3 ||
        p[0] != 'browse' ||
        p[1] != 'books' ||
        !{'popular', 'genre'}.contains(p[2]) ||
        (id != null && id.trim().isEmpty) ||
        (p[2] == 'genre' ? genre == null || genre.isEmpty : genre != null)) {
      return null;
    }
    return BookBrowseQuery(feed: p[2], instanceId: id, genre: genre);
  }

  @override
  bool operator ==(Object other) =>
      other is BookBrowseQuery &&
      feed == other.feed &&
      instanceId == other.instanceId &&
      genre == other.genre;
  @override
  int get hashCode => Object.hash(feed, instanceId, genre);
}
