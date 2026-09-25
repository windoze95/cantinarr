// Fixture for widget tests and the browser screenshot harness. Never shipped.
const historyFixtureRows = <Map<String, dynamic>>[
  {
    'id': 9, 'tmdb_id': 438631, 'media_type': 'movie', 'title': 'Dune',
    'poster_path': '/d5NXSklXo0qyIYkgV94XAgMIckC.jpg',
    'instance_id': 'radarr-1', 'instance_name': 'Movies',
    'decision': 'approved', 'decided_by': 'Morgan',
    'requested_at': '2026-09-25T14:20:00Z', 'decided_at': '2026-09-25T14:32:00Z',
    'requesters': [{'user_id': 2, 'username': 'Alex'}],
  },
  {
    'id': 8, 'tmdb_id': 66732, 'media_type': 'tv', 'title': 'Stranger Things',
    'poster_path': '/uOOtwVbSr4QDjAGIifLDwpb2Pdl.jpg',
    'instance_id': 'sonarr-1', 'instance_name': 'TV', 'season_scope': '[1,2]',
    'decision': 'pending', 'requested_at': '2026-09-25T12:00:00Z',
    'requesters': [{'user_id': 3, 'username': 'Sam'}],
  },
  {
    'id': 7, 'media_type': 'book', 'title': 'Project Hail Mary',
    'foreign_id': 'book-1', 'instance_id': 'books-1', 'instance_name': 'Books',
    'book_format': 'both', 'decision': 'approved',
    'requested_at': '2026-09-24T16:00:00Z',
    'requesters': [
      {'user_id': 2, 'username': 'Alex', 'book_format': 'ebook'},
      {'user_id': 3, 'username': 'Sam', 'book_format': 'audiobook'},
    ],
  },
  {
    'id': 6, 'media_type': 'music', 'title': 'Kind of Blue',
    'foreign_id': 'album-1', 'instance_id': 'music-1', 'instance_name': 'Music',
    'decision': 'denied', 'decided_by': 'Morgan', 'deny_reason': 'We already have this edition.',
    'requested_at': '2026-09-23T18:05:00Z', 'decided_at': '2026-09-23T18:30:00Z',
    'requesters': [{'user_id': 3, 'username': 'Sam'}],
  },
  {
    'id': 5, 'media_type': 'book', 'title': 'The Hobbit',
    'book_format': 'ebook', 'decision': 'cancelled',
    'deny_reason': 'Cancelled', 'requested_at': '2026-09-22T20:00:00Z',
    'decided_at': '2026-09-22T20:10:00Z',
    'requesters': [{'user_id': 2, 'username': 'Alex'}],
  },
];

Map<String, dynamic> historyFixturePage(Map<String, dynamic> query) {
  final rows = historyFixtureRows.where((row) {
    final before = int.tryParse('${query['before']}');
    final q = '${query['q'] ?? ''}'.toLowerCase();
    final users = row['requesters'] as List;
    return (before == null || (row['id'] as int) < before) &&
        (row['title'] as String).toLowerCase().contains(q) &&
        (query['media_type'] == null || row['media_type'] == query['media_type']) &&
        (query['decision'] == null || row['decision'] == query['decision']) &&
        (query['user_id'] == null || users.any((u) => '${u['user_id']}' == '${query['user_id']}'));
  }).toList();
  // Small pages make paging directly testable with the same browser fixture.
  final hasMore = rows.length > 3;
  final page = rows.take(3).toList();
  return {
    'requests': page,
    'requesters': [
      {'user_id': 2, 'username': 'Alex'},
      {'user_id': 3, 'username': 'Sam'},
    ],
    if (hasMore) 'next_before': page.last['id'],
  };
}
