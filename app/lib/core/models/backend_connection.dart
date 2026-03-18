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

  const BackendConnection({
    required this.serverUrl,
    required this.accessToken,
    required this.refreshToken,
    this.serverName,
    this.services = const AvailableServices(),
    this.instances = const [],
  });

  BackendConnection copyWith({
    String? serverUrl,
    String? accessToken,
    String? refreshToken,
    String? serverName,
    AvailableServices? services,
    List<ServiceInstance>? instances,
  }) =>
      BackendConnection(
        serverUrl: serverUrl ?? this.serverUrl,
        accessToken: accessToken ?? this.accessToken,
        refreshToken: refreshToken ?? this.refreshToken,
        serverName: serverName ?? this.serverName,
        services: services ?? this.services,
        instances: instances ?? this.instances,
      );

  /// Get all Radarr instances.
  List<ServiceInstance> get radarrInstances =>
      instances.where((i) => i.serviceType == 'radarr').toList();

  /// Get all Sonarr instances.
  List<ServiceInstance> get sonarrInstances =>
      instances.where((i) => i.serviceType == 'sonarr').toList();

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
