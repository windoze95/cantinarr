import 'package:flutter/widgets.dart';

import 'l10n.dart';

/// Current server-owned setup vocabulary; newer unknown copy remains visible.
String localizeSetupLabel(BuildContext context, String label) =>
    switch (label) {
      'Movies (Radarr)' => context.l10n.setupMovies,
      'TV (Sonarr)' => context.l10n.setupTv,
      'Discovery (TMDB)' => context.l10n.setupDiscovery,
      'Push notifications' => context.l10n.setupPush,
      'Media server access' => context.l10n.setupMediaAccess,
      'Add people' => context.l10n.setupPeople,
      'Trakt discovery' => context.l10n.setupTrakt,
      'Discovery rows' => context.l10n.setupDiscoveryRows,
      'Download activity' => context.l10n.setupDownloadActivity,
      'Completed media downloads' => context.l10n.setupCompletedDownloads,
      'Monitoring (Tautulli or Tracearr)' => context.l10n.setupMonitoring,
      'Tdarr progress' => context.l10n.setupTdarr,
      'Books (Chaptarr)' => context.l10n.setupBooks,
      'Music (Lidarr)' => context.l10n.setupMusic,
      'AI assistant' => context.l10n.setupAi,
      'Automatic problem detection' => context.l10n.setupProblemDetection,
      'Connect Radarr so movie requests have somewhere to go.' =>
        context.l10n.setupMoviesDescription,
      'Connect Sonarr so TV requests have somewhere to go.' =>
        context.l10n.setupTvDescription,
      'Browsing, search, and artwork are powered by TMDB. The built-in key works out of the box; add your own in the Discover settings to use your account instead.' =>
        context.l10n.setupDiscoveryDescription,
      'For device alerts, set CANTINARR_PUSH_GATEWAY_URL on the server to https://push.cantinarr.com. It\'s free to use.' =>
        context.l10n.setupPushDescription,
      'Connect Plex, Jellyfin, Emby, or Audiobookshelf so users can get library access from the app.' =>
        context.l10n.setupMediaAccessDescription,
      'Import people from a media server or invite them to Cantinarr, then choose what they can request.' =>
        context.l10n.setupPeopleDescription,
      'Trending, popular lists, and the release calendar run on Cantinarr\'s built-in Trakt app out of the box; add your own client ID in the Discover settings to use yours instead.' =>
        context.l10n.setupTraktDescription,
      'Pick which feed backs the headline rows on Movies and TV, and whether to hide non-English titles.' =>
        context.l10n.setupDiscoveryRowsDescription,
      'Trakt is connected, so the headline rows already use it. Confirm the feed here — and whether to hide non-English titles.' =>
        context.l10n.setupDiscoveryRowsTraktDescription,
      'See and manage the live download queue (SABnzbd, qBittorrent, NZBGet, Transmission, Deluge, or ruTorrent).' =>
        context.l10n.setupDownloadActivityDescription,
      'Mount media read-only on the server, then map paths inside each Radarr, Sonarr, Chaptarr, or Lidarr instance.' =>
        context.l10n.setupCompletedDownloadsDescription,
      'See live streams, watch history, and stats in the Monitoring module. Tautulli covers Plex; Tracearr covers Plex, Jellyfin, and Emby.' =>
        context.l10n.setupMonitoringDescription,
      'See active processing jobs and library statistics in the Tdarr module.' =>
        context.l10n.setupTdarrDescription,
      'Let users request ebooks and audiobooks; access is granted per user.' =>
        context.l10n.setupBooksDescription,
      'Let users request albums; access is granted per user.' =>
        context.l10n.setupMusicDescription,
      'Conversational discovery, requests, and server management. Configure a shared provider; users may override it with their own credentials.' =>
        context.l10n.setupAiDescription,
      'Decide whether Cantinarr should detect and investigate stuck downloads on its own. On or off — deciding is the step.' =>
        context.l10n.setupProblemDetectionDescription,
      'Remediation is ON but no shared AI provider is configured — detected problems wait instead of being investigated. Add a provider under Providers & Credentials.' =>
        context.l10n.setupProblemDetectionWarning,
      _ => label,
    };
