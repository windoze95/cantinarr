/// Server-wide permission to deliver each push category.
class PushNotificationPolicy {
  final bool enabled;
  final Map<String, bool> categories;
  const PushNotificationPolicy(
      {required this.enabled, required this.categories});
  factory PushNotificationPolicy.fromJson(Map<String, dynamic> json) =>
      PushNotificationPolicy(
          enabled: json['enabled'] == true,
          categories: Map<String, bool>.from(json['categories'] as Map? ?? {}));
  Map<String, dynamic> toJson() =>
      {'enabled': enabled, 'categories': categories};
}

/// A user's push-notification preferences. Each flag toggles one category of
/// push notification the server may send to this user's devices.
///
/// PUT must include every existing category, including administrator choices:
/// omitting a legacy category turns it off. Server policy is read-only metadata
/// and is never included in a personal save.
class NotificationPrefs {
  final bool pushEnabled;
  final bool requestAutoApproved;
  final PushNotificationPolicy? serverPolicy;
  bool get supportsControls => serverPolicy != null;
  bool get serverEnabled => serverPolicy?.enabled ?? true;
  bool categoryAllowed(String key) =>
      serverPolicy == null || serverPolicy!.categories[key] == true;

  final bool requestDecision;
  final bool requestPending;
  final bool newMovie;
  final bool newEpisode;
  final bool newBook;
  final bool newMusic;
  final bool issueCreated;
  final bool agentActionPending;
  final bool plexAccessRequest;
  final bool mediaServerAccess;
  final bool issueReportUpdate;
  final bool agentDigest;
  final bool contentUpgraded;

  const NotificationPrefs({
    this.pushEnabled = true,
    this.requestAutoApproved = false,
    this.serverPolicy,
    required this.requestDecision,
    required this.requestPending,
    required this.newMovie,
    required this.newEpisode,
    this.newBook = true,
    this.newMusic = true,
    this.issueCreated = true,
    this.agentActionPending = true,
    this.plexAccessRequest = true,
    this.mediaServerAccess = true,
    this.issueReportUpdate = true,
    this.agentDigest = true,
    this.contentUpgraded = false,
  });

  factory NotificationPrefs.fromJson(Map<String, dynamic> json) =>
      NotificationPrefs(
        pushEnabled: json['push_enabled'] as bool? ?? true,
        requestAutoApproved: json['request_auto_approved'] as bool? ?? false,
        serverPolicy: json['server_policy'] is Map
            ? PushNotificationPolicy.fromJson(
                Map<String, dynamic>.from(json['server_policy'] as Map))
            : null,
        requestDecision: json['request_decision'] as bool? ?? false,
        requestPending: json['request_pending'] as bool? ?? false,
        newMovie: json['new_movie'] as bool? ?? false,
        newEpisode: json['new_episode'] as bool? ?? false,
        // Categories newer than the connected server (and the admin-only
        // ones) default on server-side; mirror that when a key is absent.
        newBook: json['new_book'] as bool? ?? true,
        newMusic: json['new_music'] as bool? ?? true,
        issueCreated: json['issue_created'] as bool? ?? true,
        agentActionPending: json['agent_action_pending'] as bool? ?? true,
        plexAccessRequest: json['plex_access_request'] as bool? ?? true,
        mediaServerAccess: json['media_server_access'] as bool? ??
            json['plex_invite_sent'] as bool? ??
            true,
        issueReportUpdate: json['issue_report_update'] as bool? ?? true,
        agentDigest: json['agent_digest'] as bool? ?? true,
        // Unlike the admin categories above, quality-upgrade alerts default
        // OFF server-side (upgrades are maintenance, not news) — an absent
        // key must mirror that or saving any toggle would silently opt the
        // admin in.
        contentUpgraded: json['content_upgraded'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'push_enabled': pushEnabled,
        'request_auto_approved': requestAutoApproved,
        'request_decision': requestDecision,
        'request_pending': requestPending,
        'new_movie': newMovie,
        'new_episode': newEpisode,
        'new_book': newBook,
        'new_music': newMusic,
        'issue_created': issueCreated,
        'agent_action_pending': agentActionPending,
        'plex_access_request': plexAccessRequest,
        'media_server_access': mediaServerAccess,
        // Older servers still call this preference Plex invite sent.
        'plex_invite_sent': mediaServerAccess,
        'issue_report_update': issueReportUpdate,
        'agent_digest': agentDigest,
        'content_upgraded': contentUpgraded,
      };

  NotificationPrefs withCategory(String key, bool value) =>
      NotificationPrefs.fromJson({
        ...toJson(),
        key: value,
        if (serverPolicy != null) 'server_policy': serverPolicy!.toJson(),
      });

  NotificationPrefs copyWith({
    bool? pushEnabled,
    bool? requestAutoApproved,
    bool? requestDecision,
    bool? requestPending,
    bool? newMovie,
    bool? newEpisode,
    bool? newBook,
    bool? newMusic,
    bool? issueCreated,
    bool? agentActionPending,
    bool? plexAccessRequest,
    bool? mediaServerAccess,
    bool? issueReportUpdate,
    bool? agentDigest,
    bool? contentUpgraded,
  }) =>
      NotificationPrefs(
        pushEnabled: pushEnabled ?? this.pushEnabled,
        requestAutoApproved: requestAutoApproved ?? this.requestAutoApproved,
        serverPolicy: serverPolicy,
        requestDecision: requestDecision ?? this.requestDecision,
        requestPending: requestPending ?? this.requestPending,
        newMovie: newMovie ?? this.newMovie,
        newEpisode: newEpisode ?? this.newEpisode,
        newBook: newBook ?? this.newBook,
        newMusic: newMusic ?? this.newMusic,
        issueCreated: issueCreated ?? this.issueCreated,
        agentActionPending: agentActionPending ?? this.agentActionPending,
        plexAccessRequest: plexAccessRequest ?? this.plexAccessRequest,
        mediaServerAccess: mediaServerAccess ?? this.mediaServerAccess,
        issueReportUpdate: issueReportUpdate ?? this.issueReportUpdate,
        agentDigest: agentDigest ?? this.agentDigest,
        contentUpgraded: contentUpgraded ?? this.contentUpgraded,
      );
}
