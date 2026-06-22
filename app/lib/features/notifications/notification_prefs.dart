/// A user's push-notification preferences. Each flag toggles one category of
/// push notification the server may send to this user's devices.
class NotificationPrefs {
  final bool requestDecision;
  final bool requestPending;
  final bool newMovie;
  final bool newEpisode;

  const NotificationPrefs({
    required this.requestDecision,
    required this.requestPending,
    required this.newMovie,
    required this.newEpisode,
  });

  factory NotificationPrefs.fromJson(Map<String, dynamic> json) =>
      NotificationPrefs(
        requestDecision: json['request_decision'] as bool? ?? false,
        requestPending: json['request_pending'] as bool? ?? false,
        newMovie: json['new_movie'] as bool? ?? false,
        newEpisode: json['new_episode'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'request_decision': requestDecision,
        'request_pending': requestPending,
        'new_movie': newMovie,
        'new_episode': newEpisode,
      };

  NotificationPrefs copyWith({
    bool? requestDecision,
    bool? requestPending,
    bool? newMovie,
    bool? newEpisode,
  }) =>
      NotificationPrefs(
        requestDecision: requestDecision ?? this.requestDecision,
        requestPending: requestPending ?? this.requestPending,
        newMovie: newMovie ?? this.newMovie,
        newEpisode: newEpisode ?? this.newEpisode,
      );
}
