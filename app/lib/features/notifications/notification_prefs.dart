/// A user's push-notification preferences. Each flag toggles one category of
/// push notification the server may send to this user's devices.
///
/// IMPORTANT: the server's PUT replaces the full preference row, treating
/// missing keys as false — so this model must carry EVERY category the server
/// knows (including admin-only ones), or saving any toggle silently disables
/// the omitted categories.
class NotificationPrefs {
  final bool requestDecision;
  final bool requestPending;
  final bool newMovie;
  final bool newEpisode;
  final bool newBook;
  final bool issueCreated;
  final bool agentActionPending;
  final bool plexAccessRequest;
  final bool plexInviteSent;

  const NotificationPrefs({
    required this.requestDecision,
    required this.requestPending,
    required this.newMovie,
    required this.newEpisode,
    this.newBook = true,
    this.issueCreated = true,
    this.agentActionPending = true,
    this.plexAccessRequest = true,
    this.plexInviteSent = true,
  });

  factory NotificationPrefs.fromJson(Map<String, dynamic> json) =>
      NotificationPrefs(
        requestDecision: json['request_decision'] as bool? ?? false,
        requestPending: json['request_pending'] as bool? ?? false,
        newMovie: json['new_movie'] as bool? ?? false,
        newEpisode: json['new_episode'] as bool? ?? false,
        // Categories newer than the connected server (and the admin-only
        // ones) default on server-side; mirror that when a key is absent.
        newBook: json['new_book'] as bool? ?? true,
        issueCreated: json['issue_created'] as bool? ?? true,
        agentActionPending: json['agent_action_pending'] as bool? ?? true,
        plexAccessRequest: json['plex_access_request'] as bool? ?? true,
        plexInviteSent: json['plex_invite_sent'] as bool? ?? true,
      );

  Map<String, dynamic> toJson() => {
        'request_decision': requestDecision,
        'request_pending': requestPending,
        'new_movie': newMovie,
        'new_episode': newEpisode,
        'new_book': newBook,
        'issue_created': issueCreated,
        'agent_action_pending': agentActionPending,
        'plex_access_request': plexAccessRequest,
        'plex_invite_sent': plexInviteSent,
      };

  NotificationPrefs copyWith({
    bool? requestDecision,
    bool? requestPending,
    bool? newMovie,
    bool? newEpisode,
    bool? newBook,
    bool? issueCreated,
    bool? agentActionPending,
    bool? plexAccessRequest,
    bool? plexInviteSent,
  }) =>
      NotificationPrefs(
        requestDecision: requestDecision ?? this.requestDecision,
        requestPending: requestPending ?? this.requestPending,
        newMovie: newMovie ?? this.newMovie,
        newEpisode: newEpisode ?? this.newEpisode,
        newBook: newBook ?? this.newBook,
        issueCreated: issueCreated ?? this.issueCreated,
        agentActionPending: agentActionPending ?? this.agentActionPending,
        plexAccessRequest: plexAccessRequest ?? this.plexAccessRequest,
        plexInviteSent: plexInviteSent ?? this.plexInviteSent,
      );
}
