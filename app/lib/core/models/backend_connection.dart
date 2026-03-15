/// Represents the active connection to a Cantinarr backend server.
class BackendConnection {
  final String serverUrl;
  final String accessToken;
  final String refreshToken;
  final String? serverName;
  final AvailableServices services;

  const BackendConnection({
    required this.serverUrl,
    required this.accessToken,
    required this.refreshToken,
    this.serverName,
    this.services = const AvailableServices(),
  });

  BackendConnection copyWith({
    String? serverUrl,
    String? accessToken,
    String? refreshToken,
    String? serverName,
    AvailableServices? services,
  }) =>
      BackendConnection(
        serverUrl: serverUrl ?? this.serverUrl,
        accessToken: accessToken ?? this.accessToken,
        refreshToken: refreshToken ?? this.refreshToken,
        serverName: serverName ?? this.serverName,
        services: services ?? this.services,
      );
}

/// Which services the backend has configured.
class AvailableServices {
  final bool radarr;
  final bool sonarr;
  final bool ai;
  final bool tmdb;
  final bool trakt;

  const AvailableServices({
    this.radarr = false,
    this.sonarr = false,
    this.ai = false,
    this.tmdb = false,
    this.trakt = false,
  });

  factory AvailableServices.fromJson(Map<String, dynamic> json) =>
      AvailableServices(
        radarr: json['radarr'] as bool? ?? false,
        sonarr: json['sonarr'] as bool? ?? false,
        ai: json['ai'] as bool? ?? false,
        tmdb: json['tmdb'] as bool? ?? false,
        trakt: json['trakt'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'radarr': radarr,
        'sonarr': sonarr,
        'ai': ai,
        'tmdb': tmdb,
        'trakt': trakt,
      };
}
