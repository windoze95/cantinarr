/// Immutable episode identities captured when a missing-only request was made.
/// These are catalog season/episode numbers, never a live availability result.
class RequestEpisodeScope {
  final List<RequestScopeEpisode> missingEpisodes;
  final List<RequestScopeEpisode> initiallySatisfiedEpisodes;
  const RequestEpisodeScope(
      {required this.missingEpisodes,
      required this.initiallySatisfiedEpisodes});

  factory RequestEpisodeScope.fromJson(Map<String, dynamic> json) =>
      RequestEpisodeScope(
        missingEpisodes: _episodes(json['missing_episodes']),
        initiallySatisfiedEpisodes:
            _episodes(json['initially_satisfied_episodes']),
      );

  static List<RequestScopeEpisode> _episodes(dynamic value) => [
        for (final row in value is List ? value : const [])
          RequestScopeEpisode.fromJson(Map<String, dynamic>.from(row as Map)),
      ];
}

class RequestScopeEpisode {
  final int tmdbId, seasonNumber, episodeNumber;
  const RequestScopeEpisode(
      {required this.tmdbId,
      required this.seasonNumber,
      required this.episodeNumber});
  factory RequestScopeEpisode.fromJson(Map<String, dynamic> json) =>
      RequestScopeEpisode(
        tmdbId: (json['tmdb_id'] as num).toInt(),
        seasonNumber: (json['season_number'] as num).toInt(),
        episodeNumber: (json['episode_number'] as num).toInt(),
      );
}
