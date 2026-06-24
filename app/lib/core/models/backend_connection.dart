/// Represents a configured service instance (Radarr or Sonarr).
class ServiceInstance {
  final String id;
  final String serviceType;
  final String name;
  final bool isDefault;

  const ServiceInstance({
    required this.id,
    required this.serviceType,
    required this.name,
    this.isDefault = false,
  });

  factory ServiceInstance.fromJson(Map<String, dynamic> json) =>
      ServiceInstance(
        id: json['id'] as String,
        serviceType: json['service_type'] as String,
        name: json['name'] as String,
        isDefault: json['is_default'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'service_type': serviceType,
        'name': name,
        'is_default': isDefault,
      };
}

/// Represents the active connection to a Cantinarr backend server.
class BackendConnection {
  final String serverUrl;
  final String accessToken;
  final String refreshToken;
  final String? serverName;
  final AvailableServices services;
  final List<ServiceInstance> instances;

  /// Whether the AI-remediation feature is enabled server-side.
  final bool issuesEnabled;

  /// Whether the user-facing "Report a problem" affordance should be shown.
  final bool allowReporting;

  const BackendConnection({
    required this.serverUrl,
    required this.accessToken,
    required this.refreshToken,
    this.serverName,
    this.services = const AvailableServices(),
    this.instances = const [],
    this.issuesEnabled = false,
    this.allowReporting = false,
  });

  BackendConnection copyWith({
    String? serverUrl,
    String? accessToken,
    String? refreshToken,
    String? serverName,
    AvailableServices? services,
    List<ServiceInstance>? instances,
    bool? issuesEnabled,
    bool? allowReporting,
  }) =>
      BackendConnection(
        serverUrl: serverUrl ?? this.serverUrl,
        accessToken: accessToken ?? this.accessToken,
        refreshToken: refreshToken ?? this.refreshToken,
        serverName: serverName ?? this.serverName,
        services: services ?? this.services,
        instances: instances ?? this.instances,
        issuesEnabled: issuesEnabled ?? this.issuesEnabled,
        allowReporting: allowReporting ?? this.allowReporting,
      );

  /// Get all Radarr instances.
  List<ServiceInstance> get radarrInstances =>
      instances.where((i) => i.serviceType == 'radarr').toList();

  /// Get all Sonarr instances.
  List<ServiceInstance> get sonarrInstances =>
      instances.where((i) => i.serviceType == 'sonarr').toList();

  /// Get all Chaptarr (books) instances. The backend only includes a chaptarr
  /// instance in this list for users an admin has explicitly granted access, so
  /// its mere presence means the user may see the Books module.
  List<ServiceInstance> get chaptarrInstances =>
      instances.where((i) => i.serviceType == 'chaptarr').toList();

  /// Get all download client instances
  /// (SABnzbd, qBittorrent, NZBGet or Transmission).
  List<ServiceInstance> get downloadInstances => instances
      .where((i) =>
          i.serviceType == 'sabnzbd' ||
          i.serviceType == 'qbittorrent' ||
          i.serviceType == 'nzbget' ||
          i.serviceType == 'transmission')
      .toList();

  /// Get all Tautulli instances.
  List<ServiceInstance> get tautulliInstances =>
      instances.where((i) => i.serviceType == 'tautulli').toList();

  /// Get the default Radarr instance, if any.
  ServiceInstance? get defaultRadarrInstance {
    final radarr = radarrInstances;
    if (radarr.isEmpty) return null;
    return radarr.firstWhere((i) => i.isDefault, orElse: () => radarr.first);
  }

  /// Get the default Sonarr instance, if any.
  ServiceInstance? get defaultSonarrInstance {
    final sonarr = sonarrInstances;
    if (sonarr.isEmpty) return null;
    return sonarr.firstWhere((i) => i.isDefault, orElse: () => sonarr.first);
  }

  /// Get the default Chaptarr instance, if any (the user's granted instance).
  ServiceInstance? get defaultChaptarrInstance {
    final chaptarr = chaptarrInstances;
    if (chaptarr.isEmpty) return null;
    return chaptarr.firstWhere((i) => i.isDefault,
        orElse: () => chaptarr.first);
  }
}

/// Which services the backend has configured.
class AvailableServices {
  final bool radarr;
  final bool sonarr;
  final bool chaptarr;
  final bool ai;
  final bool tmdb;
  final bool trakt;

  const AvailableServices({
    this.radarr = false,
    this.sonarr = false,
    this.chaptarr = false,
    this.ai = false,
    this.tmdb = false,
    this.trakt = false,
  });

  factory AvailableServices.fromJson(Map<String, dynamic> json) =>
      AvailableServices(
        radarr: json['radarr'] as bool? ?? false,
        sonarr: json['sonarr'] as bool? ?? false,
        chaptarr: json['chaptarr'] as bool? ?? false,
        ai: json['ai'] as bool? ?? false,
        tmdb: json['tmdb'] as bool? ?? false,
        trakt: json['trakt'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'radarr': radarr,
        'sonarr': sonarr,
        'chaptarr': chaptarr,
        'ai': ai,
        'tmdb': tmdb,
        'trakt': trakt,
      };
}
