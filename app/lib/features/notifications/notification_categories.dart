import '../settings/settings_anchors.dart';

class PushCategory {
  final String key, title, subtitle, anchor;
  final bool admin;
  final String? service;
  final String? serverTitle;
  const PushCategory(this.key, this.title, this.subtitle, this.anchor,
      {this.admin = false, this.service, this.serverTitle});
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
      'My report updates',
      'When the assistant has a question about your report, a fix is ready to confirm, or your report closes',
      SettingsAnchors.notificationsReportUpdates,
      serverTitle: 'Report updates'),
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
      'When someone reports a problem with their media',
      SettingsAnchors.notificationsProblemReports,
      admin: true),
  PushCategory(
      'agent_action_pending',
      'Fixes awaiting approval',
      'When the assistant needs a decision about a fix or configuration change',
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
