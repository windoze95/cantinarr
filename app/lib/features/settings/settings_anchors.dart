/// Stable anchor ids for settings-search deep links.
///
/// A settings screen wraps each anchorable control in a `SettingsHighlight`
/// carrying one of these ids, and the settings-search index navigates with
/// `?highlight=<id>` to scroll to and flash that control on arrival. Ids are
/// kebab-case, dot-scoped by screen, and restricted to `[a-z0-9.-]` so they
/// never need URL encoding.
///
/// Never rename a value: ids travel in shareable URLs. Add, don't repurpose.
abstract final class SettingsAnchors {
  // /settings (root)
  static const rootRequestUpdates = 'root.request-updates';
  static const rootAttentionApprovals = 'root.attention-approvals';
  static const rootAttentionIssues = 'root.attention-issues';
  static const rootAttentionAgentFixes = 'root.attention-agent-fixes';
  static const rootAttentionProfileApprovals =
      'root.attention-profile-approvals';

  // /settings/request-settings
  static const requestsRequireApproval = 'requests.require-approval';
  static const requestsSeasonChoice = 'requests.season-choice';
  static const requestsSeasonScope = 'requests.season-scope';
  static const requestsQualityChoice = 'requests.quality-choice';
  static const requestsQualityRadarr = 'requests.quality-radarr';
  static const requestsQualitySonarr = 'requests.quality-sonarr';

  // /settings/notifications
  static const notificationsRequestDecision = 'notifications.request-decision';
  static const notificationsRequestPending = 'notifications.request-pending';
  static const notificationsProblemReports = 'notifications.problem-reports';
  static const notificationsAgentFixes = 'notifications.agent-fixes';
  static const notificationsAgentDigest = 'notifications.agent-digest';
  static const notificationsPlexAccessRequests =
      'notifications.plex-access-requests';
  static const notificationsQualityUpgrades = 'notifications.quality-upgrades';
  static const notificationsNewMovie = 'notifications.new-movie';
  static const notificationsNewEpisode = 'notifications.new-episode';
  static const notificationsNewBook = 'notifications.new-book';
  static const notificationsNewMusic = 'notifications.new-music';
  static const notificationsPlexInviteSent = 'notifications.plex-invite-sent';
  static const notificationsReportUpdates = 'notifications.report-updates';

  // /settings/ai-remediation
  static const remediationRules = 'remediation.rules';
  static const remediationFixes = 'remediation.fixes';
  static const remediationEnabled = 'remediation.enabled';
  static const remediationAutoDispatch = 'remediation.auto-dispatch';
  static const remediationAllowReporting = 'remediation.allow-reporting';
  static const remediationMarkResolvedRead = 'remediation.mark-resolved-read';
  static const remediationMode = 'remediation.mode';
  static const remediationWatchTime = 'remediation.watch-time';
  static const remediationQuietTime = 'remediation.quiet-time';
  static const remediationSettleTime = 'remediation.settle-time';
  static const remediationModel = 'remediation.model';
  static const remediationMaxSteps = 'remediation.max-steps';
  static const remediationMaxTurnTokens = 'remediation.max-turn-tokens';
  static const remediationMaxWallClock = 'remediation.max-wall-clock';
  static const remediationDailyCap = 'remediation.daily-cap';
  static const remediationUserWait = 'remediation.user-wait';
  static const remediationBreakerGiveups = 'remediation.breaker-giveups';

  // /settings/credentials
  static const credentialsAiModel = 'credentials.ai-model';
  static const credentialsOpenAiBaseUrl = 'credentials.openai-base-url';
  static const credentialsOpenAiReasoningEffort =
      'credentials.openai-reasoning-effort';
  static const credentialsHealthCheck = 'credentials.health-check';
  static const credentialsAnthropic = 'credentials.anthropic';
  static const credentialsOpenAi = 'credentials.openai';
  static const credentialsGemini = 'credentials.gemini';
  static const credentialsGrok = 'credentials.grok';

  // /settings/discovery — the TMDB/Trakt sections moved here from the
  // credentials screen; their ids keep the historic `credentials.` prefix
  // because anchor ids never rename.
  static const discoveryEnglishOnly = 'discovery.english-only';
  static const credentialsTmdb = 'credentials.tmdb';
  static const credentialsTrakt = 'credentials.trakt';

  // /settings/ai-tools
  static const aiToolsDebugLogging = 'ai-tools.debug-logging';

  // /settings/ai
  static const aiAccessIncluded = 'ai-access.included';
  static const aiAccessPersonal = 'ai-access.personal';
}
