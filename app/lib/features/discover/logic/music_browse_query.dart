import 'package:flutter/foundation.dart';

const musicPeriods = {
  'this_week': 'This week',
  'this_month': 'This month',
  'this_year': 'This year',
};
const musicGenreNames = {
  'pop': 'Pop',
  'rock': 'Rock',
  'hip-hop': 'Hip-Hop',
  'r-and-b': 'R&B',
  'electronic': 'Electronic',
  'jazz': 'Jazz',
  'classical': 'Classical',
  'metal': 'Metal',
  'country': 'Country',
  'folk': 'Folk',
  'blues': 'Blues',
  'reggae': 'Reggae',
};

@immutable
class MusicBrowseQuery {
  final String feed;
  final String? instanceId;
  final String period;
  final String? genre;

  const MusicBrowseQuery({
    required this.feed,
    this.instanceId,
    this.period = 'this_week',
    this.genre,
  });

  static MusicBrowseQuery? tryParse(Uri uri) {
    final parts = uri.pathSegments;
    if (parts.length != 3 ||
        parts[0] != 'browse' ||
        parts[1] != 'music' ||
        !const ['popular', 'new-releases', 'genre'].contains(parts[2])) {
      return null;
    }
    final rawInstance = uri.queryParameters['instance_id']?.trim();
    if (rawInstance != null && rawInstance.isEmpty) return null;
    final instanceId = rawInstance;
    final period = uri.queryParameters['period'] ?? 'this_week';
    final genre = uri.queryParameters['genre'];
    if (!musicPeriods.containsKey(period) ||
        (parts[2] == 'genre' && !musicGenreNames.containsKey(genre)) ||
        (parts[2] != 'genre' && genre != null)) {
      return null;
    }
    // A hand-written cold link may omit the instance; the screen resolves the
    // user's active instance, or lets an admin browse without a library.
    return MusicBrowseQuery(
      feed: parts[2],
      instanceId: instanceId,
      period: period,
      genre: genre,
    );
  }

  Map<String, String> get parameters => {
        if (instanceId != null) 'instance_id': instanceId!,
        if (feed == 'popular') 'period': period,
        if (genre != null) 'genre': genre!,
      };
  String get location => Uri(
        path: '/browse/music/$feed',
        queryParameters: parameters,
      ).toString();
  String get title => switch (feed) {
        'popular' => 'Popular Albums',
        'new-releases' => 'New Releases',
        _ => musicGenreNames[genre] ?? 'Music',
      };
  String get description => switch (feed) {
        'popular' => '${musicPeriods[period]} · ListenBrainz',
        'new-releases' => 'Past 30 days · ListenBrainz',
        _ => 'Albums and EPs · MusicBrainz matching order',
      };
  MusicBrowseQuery withInstance(String? id) => MusicBrowseQuery(
        feed: feed,
        instanceId: id,
        period: period,
        genre: genre,
      );
  MusicBrowseQuery withPeriod(String value) => MusicBrowseQuery(
        feed: feed,
        instanceId: instanceId,
        period: value,
        genre: genre,
      );
  @override
  bool operator ==(Object other) =>
      other is MusicBrowseQuery &&
      feed == other.feed &&
      instanceId == other.instanceId &&
      period == other.period &&
      genre == other.genre;
  @override
  int get hashCode => Object.hash(feed, instanceId, period, genre);
}
