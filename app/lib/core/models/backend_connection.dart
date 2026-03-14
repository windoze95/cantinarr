/// Represents the active connection to a Cantinarr backend server.
class BackendConnection {
  final String serverUrl;
  final String accessToken;
  final String refreshToken;
  final String? serverName;
  final String? tmdbApiKey;
  final String? traktClientId;
  final AvailableServices services;

  const BackendConnection({
    required this.serverUrl,
    required this.accessToken,
    required this.refreshToken,
    this.serverName,
    this.tmdbApiKey,
    this.traktClientId,
    this.services = const AvailableServices(),
  });

  BackendConnection copyWith({
    String? serverUrl,
    String? accessToken,
    String? refreshToken,
    String? serverName,
    String? tmdbApiKey,
    String? traktClientId,
    AvailableServices? services,
  }) =>
      BackendConnection(
        serverUrl: serverUrl ?? this.serverUrl,
        accessToken: accessToken ?? this.accessToken,
        refreshToken: refreshToken ?? this.refreshToken,
        serverName: serverName ?? this.serverName,
        tmdbApiKey: tmdbApiKey ?? this.tmdbApiKey,
        traktClientId: traktClientId ?? this.traktClientId,
        services: services ?? this.services,
      );
}

/// Which arr services the backend has configured.
class AvailableServices {
  final bool radarr;
  final bool sonarr;
  final bool ai;

  const AvailableServices({
    this.radarr = false,
    this.sonarr = false,
    this.ai = false,
  });

  factory AvailableServices.fromJson(Map<String, dynamic> json) =>
      AvailableServices(
        radarr: json['radarr'] as bool? ?? false,
        sonarr: json['sonarr'] as bool? ?? false,
        ai: json['ai'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'radarr': radarr,
        'sonarr': sonarr,
        'ai': ai,
      };
}
