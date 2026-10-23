import 'package:cantinarr/l10n/app_localizations.dart';
import '../settings/settings_anchors.dart';

class PushCategory {
  final String key, title, subtitle, anchor;
  final bool admin;
  final String? service;
  final String? serverTitle;
  const PushCategory(this.key, this.title, this.subtitle, this.anchor,
      {this.admin = false, this.service, this.serverTitle});

  String localizedTitle(AppLocalizations l10n) => switch (key) {
        'request_decision' => l10n.notificationRequestDecision,
        'new_movie' => l10n.notificationNewMovie,
        'new_episode' => l10n.notificationNewEpisodes,
        'new_book' => l10n.notificationNewBook,
        'new_music' => l10n.notificationNewMusic,
        'media_server_access' => l10n.settingsMediaServerAccess,
        'issue_report_update' => l10n.notificationProblemResolved,
        'request_pending' => l10n.notificationRequestsToReview,
        'request_auto_approved' => l10n.notificationAutoApproved,
        'issue_created' => l10n.notificationProblemReports,
        'agent_action_pending' => l10n.notificationFixesToReview,
        'agent_digest' => l10n.notificationWeeklySummary,
        'plex_access_request' => l10n.notificationPlexAccessRequests,
        'content_upgraded' => l10n.notificationQualityUpgrades,
        _ => title,
      };

  String localizedSubtitle(AppLocalizations l10n) => switch (key) {
        'request_decision' => l10n.notificationRequestDecisionHelp,
        'new_movie' => l10n.notificationNewMovieHelp,
        'new_episode' => l10n.notificationNewEpisodesHelp,
        'new_book' => l10n.notificationNewBookHelp,
        'new_music' => l10n.notificationNewMusicHelp,
        'media_server_access' => l10n.notificationMediaAccessHelp,
        'issue_report_update' => l10n.notificationProblemResolvedHelp,
        'request_pending' => l10n.notificationRequestsToReviewHelp,
        'request_auto_approved' => l10n.notificationAutoApprovedHelp,
        'issue_created' => l10n.notificationProblemReportsHelp,
        'agent_action_pending' => l10n.notificationFixesToReviewHelp,
        'agent_digest' => l10n.notificationWeeklySummaryHelp,
        'plex_access_request' => l10n.notificationPlexAccessRequestsHelp,
        'content_upgraded' => l10n.notificationQualityUpgradesHelp,
        _ => subtitle,
      };

  String localizedServerTitle(AppLocalizations l10n) =>
      key == 'issue_report_update'
          ? l10n.notificationReportedProblemsResolved
          : localizedTitle(l10n);
}

const pushCategories = [
  PushCategory(
      'request_decision',
      'Request approved or denied',
      'When your request is approved or denied',
      SettingsAnchors.notificationsRequestDecision),
  PushCategory(
      'new_movie',
      'New movie available',
      'When a movie finishes downloading',
      SettingsAnchors.notificationsNewMovie),
  PushCategory(
      'new_episode',
      'New episodes available',
      'When new episodes are available',
      SettingsAnchors.notificationsNewEpisode),
  PushCategory('new_book', 'New book available',
      'When a book finishes downloading', SettingsAnchors.notificationsNewBook,
      service: 'chaptarr'),
  PushCategory(
      'new_music',
      'New music available',
      'When an album finishes downloading',
      SettingsAnchors.notificationsNewMusic,
      service: 'lidarr'),
  PushCategory(
      'media_server_access',
      'Media server access',
      'When you get access to a Plex, Jellyfin, Emby, or Audiobookshelf server',
      SettingsAnchors.notificationsMediaServerAccess),
  PushCategory(
      'issue_report_update',
      'Problem resolved',
      'When a problem you reported has been resolved',
      SettingsAnchors.notificationsReportUpdates,
      serverTitle: 'Reported problems resolved'),
  PushCategory(
      'request_pending',
      'New requests to review',
      'When someone submits a request needing approval',
      SettingsAnchors.notificationsRequestPending,
      admin: true),
  PushCategory(
      'request_auto_approved',
      'Automatically approved requests',
      'When a new request is accepted without needing review',
      SettingsAnchors.notificationsAutoApproved,
      admin: true),
  PushCategory(
      'issue_created',
      'Problem reports',
      'When a problem needs attention or the assistant needs more information',
      SettingsAnchors.notificationsProblemReports,
      admin: true),
  PushCategory(
      'agent_action_pending',
      'Fixes to review',
      'When the assistant needs approval or a repair needs checking',
      SettingsAnchors.notificationsAgentFixes,
      admin: true),
  PushCategory(
      'agent_digest',
      'Weekly agent summary',
      'What resolved itself, what your rules handled, and what needs you',
      SettingsAnchors.notificationsAgentDigest,
      admin: true),
  PushCategory(
      'plex_access_request',
      'Plex access requests',
      'When someone shares their Plex email and needs access',
      SettingsAnchors.notificationsPlexAccessRequests,
      admin: true),
  PushCategory(
      'content_upgraded',
      'Quality upgrades',
      'When an existing movie, episode, book, or album is replaced with a better version',
      SettingsAnchors.notificationsQualityUpgrades,
      admin: true),
];
