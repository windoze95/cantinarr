import 'package:flutter/material.dart';

import '../../../core/models/user_profile.dart';
import '../settings_anchors.dart';

/// The hand-maintained index behind the settings search bar.
///
/// Every static setting across the settings screens has one entry here.
/// [SettingsSearchEntry.title] is a drift contract: it must equal, character
/// for character, a string the owning screen renders (tile title, switch
/// title, field label, or the root tile naming the screen) — the drift test
/// pumps each owning screen and fails when a title stops appearing. Renaming
/// copy on a screen therefore means updating its entry here in the same
/// change. Subtitles are deliberately not stored (many are dynamic); words
/// from them that add search value belong in [SettingsSearchEntry.keywords],
/// which are free to paraphrase.
///
/// Dynamic, server-supplied rows (instances, AI tools, users, devices,
/// passkeys, discovery sources) are not indexed: they have no stable strings
/// or routes. Each lives one tap behind an indexed screen-level entry whose
/// keywords name the concept. If runtime entries are ever wanted, add an
/// `extra` parameter to [searchSettingsIndex] rather than mutating the const
/// registry.

/// Inputs a visibility gate may consult. A plain value object (not Riverpod
/// refs) so the registry stays const and matching stays unit-testable.
class SettingsSearchGates {
  final UserProfile? user;

  /// `connection.services.chaptarr` — gates book-related entries.
  final bool chaptarrEnabled;

  /// Platform gate for the Donate tile (hidden in store binaries).
  final bool donateVisible;

  /// Platform gate for the phone-app tile (the store binaries are the apps it
  /// advertises, so they hide it).
  final bool phoneAppsVisible;

  /// `connection.mediaServerInstances.isNotEmpty` — a media server (Jellyfin, Emby)
  /// is shared with this account, so the access guide has something to show.
  final bool mediaServersVisible;

  const SettingsSearchGates({
    required this.user,
    this.chaptarrEnabled = false,
    this.donateVisible = false,
    this.phoneAppsVisible = false,
    this.mediaServersVisible = false,
  });
}

// Named top-level predicates keep the registry a const expression (tear-offs
// of top-level functions are constants). This is the complete vocabulary the
// settings screens' inline visibility `if`s use today — add a predicate only
// when a screen actually gates on something new.
bool gateEveryone(SettingsSearchGates g) => true;
bool gateAdmin(SettingsSearchGates g) => g.user?.isAdmin == true;
bool gateNonAdmin(SettingsSearchGates g) =>
    g.user != null && g.user!.isAdmin != true;
bool gateAiChat(SettingsSearchGates g) =>
    g.user?.hasPermission('ai:chat') == true;
bool gatePasskey(SettingsSearchGates g) => g.user?.canUsePasskey == true;
bool gatePassword(SettingsSearchGates g) => g.user?.canUsePassword == true;
bool gateChaptarr(SettingsSearchGates g) => g.chaptarrEnabled;
bool gateDonate(SettingsSearchGates g) => g.donateVisible;
bool gatePhoneApps(SettingsSearchGates g) => g.phoneAppsVisible;
bool gateMediaServers(SettingsSearchGates g) => g.mediaServersVisible;

/// One searchable setting.
class SettingsSearchEntry {
  /// Unique, dot-namespaced. Equals [anchorId] whenever one exists.
  final String id;

  /// Verbatim on-screen title — the drift contract (see library docs).
  final String title;

  final IconData icon;

  /// Literal GoRouter path of the owning screen.
  final String route;

  /// The owning screen's human name (its AppBar title; 'Settings' for root).
  final String screenTitle;

  /// Section label on the owning screen, '' when the screen has none.
  final String section;

  /// Extra match words: synonyms, subtitle phrasing, related jargon.
  final List<String> keywords;

  final bool Function(SettingsSearchGates) gate;

  /// Anchor passed as `?highlight=` so the destination scrolls and flashes.
  /// Null for entries whose tap just opens the screen (or runs a root
  /// action).
  final String? anchorId;

  const SettingsSearchEntry({
    required this.id,
    required this.title,
    required this.icon,
    required this.route,
    required this.screenTitle,
    this.section = '',
    this.keywords = const [],
    required this.gate,
    this.anchorId,
  });

  /// Where the setting lives, for result subtitles: 'Screen › Section'.
  String get breadcrumb =>
      section.isEmpty ? screenTitle : '$screenTitle › $section';
}

// ── Root — /settings — settings_screen.dart ─────────────────────────────────
// Screen-level entries (the root tiles that open other screens), then the
// root's own rows and action tiles, in the root ListView's visual order.
const List<SettingsSearchEntry> _rootEntries = [
  SettingsSearchEntry(
    id: 'screen.setup-checklist',
    title: 'Setup Checklist',
    icon: Icons.checklist_outlined,
    route: '/setup',
    screenTitle: 'Settings',
    keywords: ['configured', 'features', 'wizard', 'getting started', 'admin'],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.password',
    title: 'Password',
    icon: Icons.lock_outline,
    route: '/settings/password',
    screenTitle: 'Settings',
    section: 'Account',
    keywords: ['sign-in', 'security', 'change password', 'mcp'],
    gate: gatePassword,
  ),
  SettingsSearchEntry(
    id: 'screen.passkeys',
    title: 'Passkeys',
    icon: Icons.fingerprint,
    route: '/settings/passkeys',
    screenTitle: 'Settings',
    section: 'Account',
    keywords: ['sign-in', 'security', 'webauthn', 'biometric', 'face id'],
    gate: gatePasskey,
  ),
  SettingsSearchEntry(
    id: 'screen.ai-access',
    title: 'AI Access',
    icon: Icons.auto_awesome_outlined,
    route: '/settings/ai',
    screenTitle: 'Settings',
    section: 'Account',
    keywords: [
      'personal',
      'included',
      'provider',
      'byok',
      'api key',
      'model',
      'chatgpt',
      'codex',
      'oauth',
      'grok',
      'xai',
    ],
    gate: gateAiChat,
  ),
  SettingsSearchEntry(
    id: 'screen.discovery',
    title: 'Discover',
    icon: Icons.explore_outlined,
    route: '/settings/discovery',
    screenTitle: 'Settings',
    section: 'Modules',
    keywords: [
      'discovery',
      'headline rows',
      'row source',
      'trakt',
      'tmdb',
      'english',
      'language',
      'credentials',
      'api key',
      'token',
    ],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.my-reports',
    title: 'My reports',
    icon: Icons.flag_outlined,
    route: '/issues',
    screenTitle: 'Settings',
    section: 'Modules',
    keywords: ['problems', 'issues', 'reported'],
    gate: gateNonAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.add-instance',
    title: 'Add Instance',
    icon: Icons.add,
    route: '/settings/instance/new',
    screenTitle: 'Settings',
    section: 'Modules',
    keywords: [
      'radarr',
      'sonarr',
      'chaptarr',
      'download client',
      'tautulli',
      'jellyfin',
      'emby',
      'media server',
      'connect',
      'server',
    ],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'root.sign-out',
    title: 'Sign out',
    icon: Icons.logout,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'Server',
    keywords: [
      'log out',
      'logout',
      'disconnect',
      'switch server',
      'leave server',
    ],
    gate: gateEveryone,
  ),
  SettingsSearchEntry(
    id: 'screen.users',
    title: 'Users',
    icon: Icons.people_outline,
    route: '/settings/users',
    screenTitle: 'Settings',
    section: 'Admin',
    // Inviting a new user lives on this screen, so the old Generate
    // Connect Link vocabulary must land here.
    keywords: [
      'accounts',
      'roles',
      'invites',
      'connect link',
      'invite link',
      'new user',
      'add user',
      'permissions',
      'access',
    ],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'root.external-address',
    title: 'External Address',
    icon: Icons.public,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'Admin',
    keywords: [
      'public url',
      'domain',
      'reverse proxy',
      'invite link',
      'reachable',
    ],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.devices',
    title: 'Connected Devices',
    icon: Icons.devices,
    route: '/settings/devices',
    screenTitle: 'Settings',
    section: 'Admin',
    keywords: ['sessions', 'sign out', 'revoke', 'phones'],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.credentials',
    title: 'Providers & Credentials',
    icon: Icons.key_outlined,
    route: '/settings/credentials',
    screenTitle: 'Settings',
    section: 'Admin',
    keywords: [
      'api key',
      'anthropic',
      'openai',
      'gemini',
      'grok',
      'xai',
      'ai provider',
      'model',
      'chatgpt',
    ],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.ai-tools',
    title: 'AI Tools',
    icon: Icons.handyman_outlined,
    route: '/settings/ai-tools',
    screenTitle: 'Settings',
    section: 'Admin',
    keywords: ['assistant tools', 'enable', 'disable', 'mcp', 'debug'],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.ai-remediation',
    title: 'AI Remediation',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'Settings',
    section: 'Admin',
    keywords: ['auto-fix', 'problems', 'agent', 'issues', 'assistant'],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.agent-approval-rules',
    title: 'Agent Auto-Approvals',
    icon: Icons.rule_outlined,
    route: '/settings/agent-approval-rules',
    screenTitle: 'Settings',
    section: 'Admin',
    keywords: ['standing rules', 'repeat fixes', 'automatic approval'],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.change-history',
    title: 'Configuration History',
    icon: Icons.manage_history_outlined,
    route: '/settings/change-history',
    screenTitle: 'Settings',
    section: 'Admin',
    keywords: ['audit', 'changes', 'quality profile', 'custom format', 'log'],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'screen.request-settings',
    title: 'Request Settings',
    icon: Icons.tune,
    route: '/settings/request-settings',
    screenTitle: 'Settings',
    section: 'Admin',
    keywords: ['request defaults', 'approval', 'seasons', 'quality'],
    gate: gateAdmin,
  ),
  SettingsSearchEntry(
    id: 'root.update-portal',
    title: 'Update Portal',
    icon: Icons.open_in_new,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'Admin',
    keywords: ['container', 'update link', 'unraid', 'portainer'],
    gate: gateAdmin,
  ),
  // The attention rows double as each queue's stable doorway: the row opens
  // the queue, the switch governs its conditional menu entry.
  SettingsSearchEntry(
    id: 'root.attention-approvals',
    title: 'Approvals',
    icon: Icons.fact_check_outlined,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'Needs attention menu',
    keywords: ['menu', 'hide', 'badge', 'drawer', 'requests', 'queue', 'pending'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.rootAttentionApprovals,
  ),
  SettingsSearchEntry(
    id: 'root.attention-issues',
    title: 'Issues',
    icon: Icons.flag_outlined,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'Needs attention menu',
    keywords: ['menu', 'hide', 'badge', 'drawer', 'problems', 'reports'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.rootAttentionIssues,
  ),
  SettingsSearchEntry(
    id: 'root.attention-agent-fixes',
    title: 'Agent fixes',
    icon: Icons.build_circle_outlined,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'Needs attention menu',
    keywords: ['menu', 'hide', 'badge', 'drawer', 'proposals', 'review'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.rootAttentionAgentFixes,
  ),
  SettingsSearchEntry(
    id: 'root.attention-profile-approvals',
    title: 'Profile approvals',
    icon: Icons.tune,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'Needs attention menu',
    keywords: [
      'menu',
      'hide',
      'badge',
      'drawer',
      'profile change approvals',
      'quality profile',
      'external',
      'mcp',
    ],
    gate: gateAdmin,
    anchorId: SettingsAnchors.rootAttentionProfileApprovals,
  ),
  SettingsSearchEntry(
    id: 'screen.notifications',
    title: 'Notification Preferences',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Settings',
    section: 'Notifications',
    keywords: ['push', 'alerts', 'toggles'],
    gate: gateEveryone,
  ),
  SettingsSearchEntry(
    id: 'root.request-updates',
    title: 'Request updates',
    icon: Icons.notifications_active_outlined,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'Notifications',
    keywords: ['banner', 'in-app', 'approved', 'denied'],
    gate: gateEveryone,
    anchorId: SettingsAnchors.rootRequestUpdates,
  ),
  SettingsSearchEntry(
    id: 'screen.media-servers',
    title: 'Media server access',
    icon: Icons.live_tv_outlined,
    route: '/media-servers',
    screenTitle: 'Settings',
    section: 'Guides',
    keywords: [
      'plex',
      'jellyfin',
      'emby',
      'media server',
      'account',
      'invite',
      'email',
      'sign in',
      'password',
      'watch',
      'guide',
    ],
    gate: gateMediaServers,
  ),
  SettingsSearchEntry(
    id: 'root.about',
    title: 'Cantinarr',
    icon: Icons.info_outline,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'About',
    keywords: ['version', 'about', 'licenses'],
    gate: gateEveryone,
  ),
  SettingsSearchEntry(
    id: 'root.phone-apps',
    title: 'Get the phone app',
    icon: Icons.smartphone,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'About',
    keywords: [
      'iphone',
      'ios',
      'android',
      'mobile',
      'testflight',
      'beta',
      'push notifications',
      'download app',
    ],
    gate: gatePhoneApps,
  ),
  SettingsSearchEntry(
    id: 'root.github',
    title: 'GitHub',
    icon: Icons.code,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'About',
    keywords: ['source', 'releases', 'issues', 'code'],
    gate: gateEveryone,
  ),
  SettingsSearchEntry(
    id: 'root.discord',
    title: 'Discord',
    icon: Icons.forum_outlined,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'About',
    keywords: ['community', 'chat', 'help', 'questions'],
    gate: gateEveryone,
  ),
  SettingsSearchEntry(
    id: 'root.roadmap',
    title: 'Request a feature',
    icon: Icons.how_to_vote_outlined,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'About',
    keywords: ['roadmap', 'vote', 'feedback', 'suggest'],
    gate: gateEveryone,
  ),
  SettingsSearchEntry(
    id: 'root.donate',
    title: 'Donate',
    icon: Icons.favorite_outline,
    route: '/settings',
    screenTitle: 'Settings',
    section: 'About',
    keywords: ['sponsor', 'support', 'github sponsors'],
    gate: gateDonate,
  ),
];

// ── Request Defaults — /settings/request-settings ───────────────────────────
const List<SettingsSearchEntry> _requestDefaultsEntries = [
  SettingsSearchEntry(
    id: SettingsAnchors.requestsRequireApproval,
    title: 'Require approval for new requests',
    icon: Icons.tune,
    route: '/settings/request-settings',
    screenTitle: 'Request Defaults',
    section: 'Approval',
    keywords: ['queue', 'admin approval', 'pending', 'auto approve'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.requestsRequireApproval,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.requestsSeasonChoice,
    title: 'Let users choose seasons',
    icon: Icons.tune,
    route: '/settings/request-settings',
    screenTitle: 'Request Defaults',
    section: 'Seasons',
    keywords: ['pick seasons', 'tv', 'shows'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.requestsSeasonChoice,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.requestsSeasonScope,
    title: 'Default season scope',
    icon: Icons.tune,
    route: '/settings/request-settings',
    screenTitle: 'Request Defaults',
    section: 'Seasons',
    keywords: ['all seasons', 'first season', 'latest season', 'tv'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.requestsSeasonScope,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.requestsQualityChoice,
    title: 'Let users choose quality',
    icon: Icons.tune,
    route: '/settings/request-settings',
    screenTitle: 'Request Defaults',
    section: 'Quality',
    keywords: ['profile', 'resolution'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.requestsQualityChoice,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.requestsQualityRadarr,
    title: 'Default Radarr quality',
    icon: Icons.tune,
    route: '/settings/request-settings',
    screenTitle: 'Request Defaults',
    section: 'Quality',
    keywords: ['movies', 'profile'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.requestsQualityRadarr,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.requestsQualitySonarr,
    title: 'Default Sonarr quality',
    icon: Icons.tune,
    route: '/settings/request-settings',
    screenTitle: 'Request Defaults',
    section: 'Quality',
    keywords: ['tv', 'shows', 'profile'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.requestsQualitySonarr,
  ),
];

// ── AI Remediation — /settings/ai-remediation ───────────────────────────────
const List<SettingsSearchEntry> _aiRemediationEntries = [
  SettingsSearchEntry(
    id: SettingsAnchors.remediationRules,
    title: 'Standing auto-approvals',
    icon: Icons.rule_folder_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    keywords: ['rules', 'approved fixes', 'without paging'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationRules,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationFixes,
    title: 'Agent fixes',
    icon: Icons.build_circle_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    keywords: ['awaiting review', 'queue', 'proposals'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationFixes,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationEnabled,
    title: 'Enabled',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'General',
    keywords: ['master switch', 'remediation', 'assistant', 'on', 'off'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationEnabled,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationAutoDispatch,
    title: 'Auto-dispatch on detected problems',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'General',
    keywords: ['investigate', 'automatic', 'observation window'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationAutoDispatch,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationAllowReporting,
    title: 'Allow problem reporting',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'General',
    keywords: ['report a problem', 'button', 'users', 'media'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationAllowReporting,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationMarkResolvedRead,
    title: 'Mark resolved issues as read',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'General',
    keywords: ['unread dot', 'clear', 'resolved'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationMarkResolvedRead,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationMode,
    title: 'Mode',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'General',
    keywords: ['prepare a fix', 'investigate only', 'propose'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationMode,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationWatchTime,
    title: 'Minimum watch time (minutes)',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Automatic recovery',
    keywords: ['wait', 'timer', 'observation'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationWatchTime,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationQuietTime,
    title: 'Quiet time after arr activity (minutes)',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Automatic recovery',
    keywords: ['retrying', 'escalation', 'timer', 'radarr', 'sonarr'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationQuietTime,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationSettleTime,
    title: 'Recovery settle time (minutes)',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Automatic recovery',
    keywords: ['imports', 'library', 'timer', 'outcome'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationSettleTime,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationModel,
    title: 'Remediation model',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Shared AI',
    keywords: ['override', 'shared model', 'custom model'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationModel,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationMaxSteps,
    title: 'Max steps per run',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Limits',
    keywords: ['cap', 'bound'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationMaxSteps,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationMaxTurnTokens,
    title: 'Max output tokens per turn',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Limits',
    keywords: ['cap', 'usage', 'quota'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationMaxTurnTokens,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationMaxWallClock,
    title: 'Max wall-clock (seconds)',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Limits',
    keywords: ['timeout', 'cap'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationMaxWallClock,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationDailyCap,
    title: 'Daily run cap',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Limits',
    keywords: ['per day', 'limit', 'budget'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationDailyCap,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationUserWait,
    title: 'Wait for a user reply (hours)',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Limits',
    keywords: ['question', 'timeout', 'response'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationUserWait,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.remediationBreakerGiveups,
    title: 'Failed auto investigations before pausing',
    icon: Icons.auto_fix_high_outlined,
    route: '/settings/ai-remediation',
    screenTitle: 'AI Remediation',
    section: 'Limits',
    keywords: ['circuit breaker', 'pause', 'failures'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.remediationBreakerGiveups,
  ),
];

// ── Notification Preferences — /settings/notifications ──────────────────────
const List<SettingsSearchEntry> _notificationEntries = [
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsRequestDecision,
    title: 'Request approved or denied',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'my request'],
    gate: gateEveryone,
    anchorId: SettingsAnchors.notificationsRequestDecision,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsRequestPending,
    title: 'New requests to review',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'approval queue', 'submitted'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.notificationsRequestPending,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsProblemReports,
    title: 'Problem reports',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'issues', 'media'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.notificationsProblemReports,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsAgentFixes,
    title: 'Fixes awaiting approval',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'assistant', 'proposals'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.notificationsAgentFixes,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsAgentDigest,
    title: 'Weekly agent summary',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'digest', 'resolved'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.notificationsAgentDigest,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsPlexAccessRequests,
    title: 'Plex access requests',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'email', 'invite', 'grant'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.notificationsPlexAccessRequests,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsQualityUpgrades,
    title: 'Quality upgrades',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'replaced', 'better version'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.notificationsQualityUpgrades,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsNewMovie,
    title: 'New movie available',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'downloaded', 'finished'],
    gate: gateEveryone,
    anchorId: SettingsAnchors.notificationsNewMovie,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsNewEpisode,
    title: 'New episodes available',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'tv', 'downloaded'],
    gate: gateEveryone,
    anchorId: SettingsAnchors.notificationsNewEpisode,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsNewBook,
    title: 'New book available',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'chaptarr', 'downloaded'],
    gate: gateChaptarr,
    anchorId: SettingsAnchors.notificationsNewBook,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsPlexInviteSent,
    title: 'Plex invite sent',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'email'],
    gate: gateEveryone,
    anchorId: SettingsAnchors.notificationsPlexInviteSent,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.notificationsReportUpdates,
    title: 'My report updates',
    icon: Icons.notifications_outlined,
    route: '/settings/notifications',
    screenTitle: 'Notification Preferences',
    keywords: ['push', 'issue', 'question', 'fix ready'],
    gate: gateEveryone,
    anchorId: SettingsAnchors.notificationsReportUpdates,
  ),
];

// ── Providers & Credentials — /settings/credentials ─────────────────────────
const List<SettingsSearchEntry> _credentialsEntries = [
  SettingsSearchEntry(
    id: SettingsAnchors.credentialsAiModel,
    title: 'AI Model',
    icon: Icons.key_outlined,
    route: '/settings/credentials',
    screenTitle: 'Providers & Credentials',
    keywords: [
      'provider',
      'shared',
      'included',
      'chatgpt',
      'codex',
      'oauth',
      'grok',
      'xai',
      // The Local (OpenAI-compatible) provider lives in this dropdown.
      'local',
      'self-hosted',
      'base url',
      'endpoint',
      'ollama',
      'llama.cpp',
      'vllm',
      'lm studio',
      'openai compatible',
    ],
    gate: gateAdmin,
    anchorId: SettingsAnchors.credentialsAiModel,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.credentialsOpenAiReasoningEffort,
    title: 'OpenAI reasoning effort',
    icon: Icons.key_outlined,
    route: '/settings/credentials',
    screenTitle: 'Providers & Credentials',
    keywords: [
      'reasoning',
      'effort',
      'thinking',
      'speed',
      'latency',
      'local model',
    ],
    gate: gateAdmin,
    anchorId: SettingsAnchors.credentialsOpenAiReasoningEffort,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.credentialsHealthCheck,
    title: 'Daily shared-model test',
    icon: Icons.key_outlined,
    route: '/settings/credentials',
    screenTitle: 'Providers & Credentials',
    keywords: ['health check', 'background', 'usage'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.credentialsHealthCheck,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.credentialsAnthropic,
    title: 'Anthropic (AI)',
    icon: Icons.key_outlined,
    route: '/settings/credentials',
    screenTitle: 'Providers & Credentials',
    keywords: ['claude', 'api key'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.credentialsAnthropic,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.credentialsOpenAi,
    title: 'OpenAI (AI)',
    icon: Icons.key_outlined,
    route: '/settings/credentials',
    screenTitle: 'Providers & Credentials',
    keywords: ['gpt', 'api key'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.credentialsOpenAi,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.credentialsGemini,
    title: 'Google Gemini (AI)',
    icon: Icons.key_outlined,
    route: '/settings/credentials',
    screenTitle: 'Providers & Credentials',
    keywords: ['api key'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.credentialsGemini,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.credentialsGrok,
    title: 'xAI Grok (AI)',
    icon: Icons.key_outlined,
    route: '/settings/credentials',
    screenTitle: 'Providers & Credentials',
    keywords: ['grok', 'xai', 'api key'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.credentialsGrok,
  ),
];

// ── Discover — /settings/discovery ──────────────────────────────────────────
// TMDB/Trakt moved here with their credential sections; their anchor ids keep
// the historic `credentials.` names so shared links stay meaningful.
const List<SettingsSearchEntry> _discoveryEntries = [
  SettingsSearchEntry(
    id: SettingsAnchors.discoveryEnglishOnly,
    title: 'Only show English-language titles',
    icon: Icons.explore_outlined,
    route: '/settings/discovery',
    screenTitle: 'Discover',
    section: 'Language',
    keywords: ['filter', 'foreign', 'original language', 'rows'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.discoveryEnglishOnly,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.credentialsTrakt,
    title: 'Trakt',
    icon: Icons.explore_outlined,
    route: '/settings/discovery',
    screenTitle: 'Discover',
    section: 'Credentials',
    keywords: ['client id', 'trending', 'discovery', 'built-in key'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.credentialsTrakt,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.credentialsTmdb,
    title: 'TMDB',
    icon: Icons.explore_outlined,
    route: '/settings/discovery',
    screenTitle: 'Discover',
    section: 'Credentials',
    keywords: ['access token', 'discovery', 'built-in key', 'movie database'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.credentialsTmdb,
  ),
];

// ── AI Tools — /settings/ai-tools ───────────────────────────────────────────
const List<SettingsSearchEntry> _aiToolsEntries = [
  SettingsSearchEntry(
    id: SettingsAnchors.aiToolsDebugLogging,
    title: 'AI Debug Logging',
    icon: Icons.handyman_outlined,
    route: '/settings/ai-tools',
    screenTitle: 'AI Tools',
    keywords: ['prompts', 'temporary', 'server logs', 'troubleshoot'],
    gate: gateAdmin,
    anchorId: SettingsAnchors.aiToolsDebugLogging,
  ),
];

// ── AI Access — /settings/ai ────────────────────────────────────────────────
const List<SettingsSearchEntry> _aiAccessEntries = [
  SettingsSearchEntry(
    id: SettingsAnchors.aiAccessIncluded,
    title: 'Provided by this server',
    icon: Icons.auto_awesome_outlined,
    route: '/settings/ai',
    screenTitle: 'AI Access',
    section: 'Included',
    keywords: ['shared', 'server provider', 'use included access'],
    gate: gateAiChat,
    anchorId: SettingsAnchors.aiAccessIncluded,
  ),
  SettingsSearchEntry(
    id: SettingsAnchors.aiAccessPersonal,
    title: 'Your provider',
    icon: Icons.auto_awesome_outlined,
    route: '/settings/ai',
    screenTitle: 'AI Access',
    section: 'Personal',
    keywords: [
      'personal',
      'api key',
      'byok',
      'model',
      'chatgpt',
      'codex',
      'oauth',
    ],
    gate: gateAiChat,
    anchorId: SettingsAnchors.aiAccessPersonal,
  ),
];

/// The full registry, in the root screen's visual order (which is also the
/// ranking tiebreaker — see [searchSettingsIndex]).
const List<SettingsSearchEntry> settingsSearchIndex = [
  ..._rootEntries,
  ..._requestDefaultsEntries,
  ..._aiRemediationEntries,
  ..._notificationEntries,
  ..._credentialsEntries,
  ..._discoveryEntries,
  ..._aiToolsEntries,
  ..._aiAccessEntries,
];

/// Matches [query] against the gated registry.
///
/// House-style matching: lowercase `contains`, split on whitespace, every
/// term must hit the entry's haystack (title + keywords + screen + section).
/// Results come back in three tiers — whole query inside the title, any term
/// inside title/keywords, then screen/section-only hits — preserving registry
/// order within each tier. Entries whose gate rejects [gates] never match,
/// which is the client-side stand-in for the router having no role guard.
List<SettingsSearchEntry> searchSettingsIndex(
  String query,
  SettingsSearchGates gates, {
  List<SettingsSearchEntry> index = settingsSearchIndex,
}) {
  final q = query.trim().toLowerCase();
  if (q.isEmpty) return const [];
  final terms = q.split(RegExp(r'\s+'));

  final titleHits = <SettingsSearchEntry>[];
  final keywordHits = <SettingsSearchEntry>[];
  final contextHits = <SettingsSearchEntry>[];

  for (final entry in index) {
    if (!entry.gate(gates)) continue;
    final title = entry.title.toLowerCase();
    final primary = '$title ${entry.keywords.join(' ').toLowerCase()}';
    final haystack =
        '$primary ${entry.screenTitle.toLowerCase()} ${entry.section.toLowerCase()}';
    if (!terms.every(haystack.contains)) continue;
    if (title.contains(q)) {
      titleHits.add(entry);
    } else if (terms.any(primary.contains)) {
      keywordHits.add(entry);
    } else {
      contextHits.add(entry);
    }
  }
  return [...titleHits, ...keywordHits, ...contextHits];
}
