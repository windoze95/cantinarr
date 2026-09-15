import 'dart:convert';

import 'package:dio/dio.dart';
import 'hardcover_connection.dart';
import '../../media_access/data/listening_apps.dart';
import '../../media_access/data/video_apps.dart';
import '../../../core/models/backend_connection.dart';

/// Maps the path reported by one arr instance to the corresponding read-only
/// path mounted inside the Cantinarr server.
class MediaPathMapping {
  final String arrPath;
  final String cantinarrPath;

  const MediaPathMapping({
    required this.arrPath,
    required this.cantinarrPath,
  });

  factory MediaPathMapping.fromJson(Map<String, dynamic> json) =>
      MediaPathMapping(
        arrPath: json['arr_path'] as String? ?? '',
        cantinarrPath: json['cantinarr_path'] as String? ?? '',
      );

  Map<String, dynamic> toJson() => {
        'arr_path': arrPath,
        'cantinarr_path': cantinarrPath,
      };
}

/// Whether a Chaptarr instance is connected to Hardcover. `supported` is
/// false for every other service type (and for a response the client could
/// not read), so the editor renders nothing rather than a wrong answer.
class InstanceHardcoverStatus {
  final bool supported;
  final bool configured;
  final bool oauthAvailable;
  final String method;
  final bool reconnectRequired;
  final String connectionId;
  final int revision;

  const InstanceHardcoverStatus({
    required this.supported,
    required this.configured,
    this.oauthAvailable = false,
    this.method = 'none',
    this.reconnectRequired = false,
    this.connectionId = '',
    this.revision = 0,
  });

  factory InstanceHardcoverStatus.fromJson(dynamic json) {
    final map =
        json is Map<String, dynamic> ? json : const <String, dynamic>{};
    return InstanceHardcoverStatus(
      supported: map['supported'] == true,
      configured: map['configured'] == true,
      oauthAvailable: map['oauth_available'] == true,
      method: map['method'] as String? ??
          (map['configured'] == true ? 'api_token' : 'none'),
      reconnectRequired: map['reconnect_required'] == true,
      connectionId: map['connection_id'] as String? ?? '',
      revision: (map['revision'] as num?)?.toInt() ?? 0,
    );
  }
}

/// One instance's live instant-updates state. `state` explains a
/// not-configured answer ('missing', 'stale', 'credential_missing',
/// 'no_public_url'); unknown values from a newer server render generically.
class InstanceWebhookStatus {
  final bool supported;
  final bool configured;
  final String state;

  const InstanceWebhookStatus({
    required this.supported,
    required this.configured,
    required this.state,
  });
}

/// Media-server (Jellyfin) settings an instance carries: the address granted
/// users are told to sign in at, and which libraries a new account may see.
/// An empty [libraryIds] shares every library, including ones added later.
class MediaServerConfig {
  final String publicAddress;
  final List<String> libraryIds;
  final ListeningApps? listeningApps;
  final VideoApps? videoApps;

  /// Plex only: the server (plex.tv machine identifier) whose shares the
  /// instance manages, and whether anyone who shares a Plex email is granted
  /// it and invited at once.
  final String machineIdentifier;
  final bool autoApprove;

  const MediaServerConfig({
    this.publicAddress = '',
    this.libraryIds = const [],
    this.listeningApps,
    this.videoApps,
    this.machineIdentifier = '',
    this.autoApprove = false,
  });

  factory MediaServerConfig.fromJson(Map<String, dynamic> json) =>
      MediaServerConfig(
        publicAddress: json['public_address'] as String? ?? '',
        videoApps: json['video_apps'] is Map
            ? VideoApps.fromJson(json['video_apps'])
            : null,
        listeningApps: json['listening_apps'] is Map
            ? ListeningApps.fromJson(json['listening_apps'])
            : null,
        libraryIds: (json['library_ids'] as List<dynamic>?)
                ?.map((id) => id.toString())
                .toList(growable: false) ??
            const [],
        machineIdentifier: json['machine_identifier'] as String? ?? '',
        autoApprove: json['auto_approve'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'public_address': publicAddress,
        'library_ids': libraryIds,
        if (listeningApps != null) 'listening_apps': listeningApps!.toJson(),
        if (videoApps != null) 'video_apps': videoApps!.toJson(),
        if (machineIdentifier.isNotEmpty) 'machine_identifier': machineIdentifier,
        if (autoApprove) 'auto_approve': true,
      };
}

/// One owned Plex Media Server of a linked plex.tv account, for the editor's
/// server picker.
class PlexServerChoice {
  final String name;
  final String machineIdentifier;

  const PlexServerChoice({required this.name, required this.machineIdentifier});

  factory PlexServerChoice.fromJson(Map<String, dynamic> json) =>
      PlexServerChoice(
        name: json['name'] as String? ?? '',
        machineIdentifier: json['machine_identifier'] as String? ?? '',
      );
}

/// The start of a Plex PIN link: open [url] for the admin, then poll
/// [InstanceApiService.checkPlexLink] with [pinId] until they approve. The
/// token the approval yields stays on the server; the instance is saved with
/// the pin id.
class PlexLinkStart {
  final int pinId;
  final String code;
  final String url;

  const PlexLinkStart({required this.pinId, required this.code, required this.url});

  factory PlexLinkStart.fromJson(Map<String, dynamic> json) => PlexLinkStart(
        pinId: (json['pin_id'] as num?)?.toInt() ?? 0,
        code: json['code'] as String? ?? '',
        url: json['url'] as String? ?? '',
      );
}

/// A polled PIN link: [linked] once the admin approved it, with the plex.tv
/// account name.
class PlexLinkState {
  final bool linked;
  final String account;

  const PlexLinkState({required this.linked, this.account = ''});
}

/// One library a media server reports right now. [collectionType] is the
/// server's own kind label ('movies', 'tvshows', ...), '' when it has none.
class MediaServerLibrary {
  final String id;
  final String name;
  final String collectionType;

  const MediaServerLibrary({
    required this.id,
    required this.name,
    this.collectionType = '',
  });

  factory MediaServerLibrary.fromJson(Map<String, dynamic> json) =>
      MediaServerLibrary(
        id: json['id']?.toString() ?? '',
        name: json['name'] as String? ?? '',
        collectionType: json['collection_type'] as String? ?? '',
      );
}

/// What a media server answered when the backend dialed it: identity plus
/// the libraries it reports. Never stored; the editor re-reads it live.
class MediaServerProbe {
  final String serverName;
  final String version;
  final List<MediaServerLibrary> libraries;

  const MediaServerProbe({
    this.serverName = '',
    this.version = '',
    this.libraries = const [],
  });

  factory MediaServerProbe.fromJson(Map<String, dynamic> json) =>
      MediaServerProbe(
        serverName: json['server_name'] as String? ?? '',
        version: json['version'] as String? ?? '',
        libraries: (json['libraries'] as List<dynamic>?)
                ?.whereType<Map>()
                .map((raw) => MediaServerLibrary.fromJson(
                      Map<String, dynamic>.from(raw),
                    ))
                .toList(growable: false) ??
            const [],
      );
}

/// Calls the backend instance CRUD API endpoints.
class InstanceApiService {
  final Dio _dio;

  InstanceApiService({required Dio backendDio}) : _dio = backendDio;

  Future<List<ServiceInstance>> listInstances() async {
    final resp = await _dio.get('/api/instances');
    return (resp.data as List<dynamic>)
        .map((i) => ServiceInstance.fromJson(i as Map<String, dynamic>))
        .toList();
  }

  /// Fetch full details (url, username, ...) for one instance.
  /// The list endpoint is the only read endpoint; credentials are write-only.
  Future<Map<String, dynamic>?> getInstanceDetails(String id) async {
    final resp = await _dio.get('/api/instances');
    for (final inst in (resp.data as List<dynamic>)) {
      final map = inst as Map<String, dynamic>;
      if (map['id'] == id) return map;
    }
    return null;
  }

  /// Absolute filesystem roots the server operator has explicitly allowed for
  /// completed-media delivery. This endpoint is admin-only. Unsupported/404
  /// responses intentionally propagate so older servers remain write-safe.
  Future<List<String>> listMediaRoots() async {
    final resp = await _dio.get('/api/instances/media-roots');
    return (resp.data as List<dynamic>)
        .map((root) => root as String)
        .toList(growable: false);
  }

  /// Library root folders the saved arr instance reports right now, read
  /// through the admin arr proxy (Radarr/Sonarr speak v3, Chaptarr and
  /// Lidarr v1). These are the only path prefixes a media path mapping can
  /// ever match, so the
  /// editor offers them as mapping sources. Arr responses may arrive without
  /// a JSON content type, so a String body is decoded here; unexpected shapes
  /// yield an empty list while transport failures propagate for a retry.
  Future<List<String>> listArrRootFolders({
    required String instanceId,
    required String serviceType,
  }) async {
    // Chaptarr (Readarr lineage) and Lidarr speak the Servarr v1 API.
    const v1Services = {'chaptarr', 'lidarr'};
    final version = v1Services.contains(serviceType) ? 'v1' : 'v3';
    final resp =
        await _dio.get('/api/instances/$instanceId/api/$version/rootfolder');
    dynamic data = resp.data;
    if (data is String && data.trim().isNotEmpty) {
      data = jsonDecode(data);
    }
    if (data is! List) return const [];
    return [
      for (final folder in data)
        if (folder is Map && folder['path'] is String &&
            (folder['path'] as String).trim().isNotEmpty)
          (folder['path'] as String).trim(),
    ];
  }

  Future<ServiceInstance> createInstance({
    required String serviceType,
    required String name,
    required String url,
    String apiKey = '',
    String username = '',
    String password = '',
    bool isDefault = false,
    List<MediaPathMapping>? mediaPathMappings,
    MediaServerConfig? mediaServerConfig,
    int? plexLinkPin,
  }) async {
    final resp = await _dio.post('/api/instances', data: {
      'service_type': serviceType,
      'name': name,
      'url': url,
      'api_key': apiKey,
      'username': username,
      'password': password,
      'is_default': isDefault,
      if (mediaPathMappings != null)
        'media_path_mappings':
            mediaPathMappings.map((mapping) => mapping.toJson()).toList(),
      if (mediaServerConfig != null)
        'media_server_config': mediaServerConfig.toJson(),
      if (plexLinkPin != null) 'plex_link_pin': plexLinkPin,
    });
    return ServiceInstance.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<ServiceInstance> updateInstance({
    required String id,
    required String name,
    required String url,
    String apiKey = '',
    String username = '',
    String password = '',
    bool isDefault = false,
    List<MediaPathMapping>? mediaPathMappings,
    MediaServerConfig? mediaServerConfig,
    int? plexLinkPin,
  }) async {
    final resp = await _dio.put('/api/instances/$id', data: {
      'name': name,
      'url': url,
      'api_key': apiKey,
      'username': username,
      'password': password,
      'is_default': isDefault,
      if (mediaPathMappings != null)
        'media_path_mappings':
            mediaPathMappings.map((mapping) => mapping.toJson()).toList(),
      // Omitted (null) keeps the stored media-server settings untouched.
      if (mediaServerConfig != null)
        'media_server_config': mediaServerConfig.toJson(),
      // A relink: the stored token is replaced by the approved pin's.
      if (plexLinkPin != null) 'plex_link_pin': plexLinkPin,
    });
    return ServiceInstance.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> deleteInstance(String id) async {
    await _dio.delete('/api/instances/$id');
  }

  /// Create or refresh Cantinarr's server-managed Radarr/Sonarr/Chaptarr
  /// Connect webhook. Its callback credential remains entirely server-side.
  Future<void> configureWebhook(String id) async {
    await _dio.post('/api/instances/$id/webhook');
  }

  /// Live instant-updates state, derived by the server from the arr's own
  /// Connect list on every call — never a stored flag, which would drift the
  /// moment an admin edited the arr directly. Older servers without the route
  /// answer 404/405; that propagates for the caller to treat as unknown.
  Future<InstanceWebhookStatus> webhookStatus(String id) async {
    final resp = await _dio.get('/api/instances/$id/webhook');
    final map = resp.data is Map<String, dynamic>
        ? resp.data as Map<String, dynamic>
        : const <String, dynamic>{};
    return InstanceWebhookStatus(
      supported: map['supported'] == true,
      configured: map['configured'] == true,
      state: map['state'] as String? ?? '',
    );
  }

  /// Whether this Chaptarr instance holds a Hardcover API token. The token
  /// itself is write-only and never comes back. Older servers without the
  /// route answer 404/405; that propagates for the caller to treat as unknown.
  Future<InstanceHardcoverStatus> hardcoverStatus(String id) async {
    final resp = await _dio.get('/api/instances/$id/hardcover');
    return InstanceHardcoverStatus.fromJson(resp.data);
  }

  /// Connect a Hardcover account: the server verifies the token against
  /// Hardcover before storing it encrypted. A rejected token is a 400 that
  /// leaves any previous connection in place.
  Future<InstanceHardcoverStatus> saveHardcoverToken(
      String id, String token) async {
    final resp = await _dio.put(
      '/api/instances/$id/hardcover',
      data: {'token': token},
    );
    return InstanceHardcoverStatus.fromJson(resp.data);
  }

  /// Disconnect Hardcover from this instance.
  Future<InstanceHardcoverStatus> clearHardcoverToken(String id) async {
    final resp = await _dio.delete('/api/instances/$id/hardcover');
    return InstanceHardcoverStatus.fromJson(resp.data);
  }

  Future<HardcoverDeviceFlow> beginHardcoverDevice(String id) async {
    final response =
        await _dio.post('/api/instances/$id/hardcover/device/begin');
    return HardcoverDeviceFlow.fromJson(response.data as Map<String, dynamic>);
  }

  Future<HardcoverDeviceFlow> checkHardcoverDevice(
      String id, String flowId) async {
    final response =
        await _dio.get('/api/instances/$id/hardcover/device/$flowId');
    return HardcoverDeviceFlow.fromJson(response.data as Map<String, dynamic>);
  }

  Future<HardcoverDeviceFlow> cancelHardcoverDevice(
      String id, String flowId) async {
    final response =
        await _dio.delete('/api/instances/$id/hardcover/device/$flowId');
    return HardcoverDeviceFlow.fromJson(response.data as Map<String, dynamic>);
  }

  Future<List<HardcoverApplyResult>> applyHardcoverConnection(
    String id,
    String connectionId,
    Map<String, int> revisions,
  ) async {
    final response =
        await _dio.post('/api/instances/$id/hardcover/apply', data: {
      'connection_id': connectionId,
      'instances': [
        for (final entry in revisions.entries)
          {'instance_id': entry.key, 'revision': entry.value}
      ],
    });
    return ((response.data as Map<String, dynamic>)['results'] as List<dynamic>)
        .map((item) =>
            HardcoverApplyResult.fromJson(item as Map<String, dynamic>))
        .toList();
  }

  /// Per-user default pins for this instance's service type, keyed by user id.
  /// The pinned id may be a sibling instance of the same type, so the edit
  /// screen can show who is currently assigned where.
  Future<Map<int, String>> getInstanceUsers(String id) async {
    final resp = await _dio.get('/api/instances/$id/users');
    final pins = <int, String>{};
    for (final row in (resp.data as List<dynamic>? ?? const [])) {
      final map = row as Map<String, dynamic>;
      pins[(map['user_id'] as num).toInt()] = map['instance_id'] as String;
    }
    return pins;
  }

  /// Pin this instance as the per-user default for exactly [userIds]: listed
  /// users are pinned here (moving off a sibling instance if needed); users
  /// previously pinned here but not listed revert to the global default (for
  /// Chaptarr, they lose access).
  Future<void> updateInstanceUsers(String id, List<int> userIds) async {
    await _dio.put('/api/instances/$id/users', data: {'user_ids': userIds});
  }

  /// Every access-grant row for this instance's service type, as user id →
  /// granted instance ids. Like [getInstanceUsers] the answer covers the whole
  /// type so the UI can also show what a user holds on a sibling.
  Future<Map<int, List<String>>> getInstanceGrantUsers(String id) async {
    final resp = await _dio.get('/api/instances/$id/grant-users');
    final grants = <int, List<String>>{};
    for (final row in (resp.data as List<dynamic>? ?? const [])) {
      final map = row as Map<String, dynamic>;
      grants
          .putIfAbsent((map['user_id'] as num).toInt(), () => [])
          .add(map['instance_id'] as String);
    }
    return grants;
  }

  /// Grant this instance to exactly [userIds]. Users absent from the list
  /// lose their grant AND any per-user default pin naming this instance —
  /// unchecking really revokes — while listed users' pins and every sibling
  /// instance's rows stay untouched.
  Future<void> updateInstanceGrantUsers(String id, List<int> userIds) async {
    await _dio
        .put('/api/instances/$id/grant-users', data: {'user_ids': userIds});
  }

  /// Ask the server to test a candidate instance configuration. The server is
  /// what dials instance URLs in production, so cluster-internal names this
  /// device cannot resolve (e.g. http://radarr:7878) still test truthfully —
  /// and credentials never leave the backend boundary. When [id] is set,
  /// blank credentials fall back to the stored write-only ones, matching
  /// save's semantics. Throws on failure with the server's reason.
  Future<void> testConnection({
    String? id,
    required String serviceType,
    required String url,
    String apiKey = '',
    String username = '',
    String password = '',
    int? plexLinkPin,
    String machineIdentifier = '',
  }) async {
    await _dio.post('/api/instances/test', data: {
      if (id != null) 'id': id,
      'service_type': serviceType,
      'url': url,
      'api_key': apiKey,
      'username': username,
      'password': password,
      if (plexLinkPin != null) 'plex_link_pin': plexLinkPin,
      if (machineIdentifier.isNotEmpty)
        'media_server_config': {'machine_identifier': machineIdentifier},
    });
  }

  /// Starts a Plex PIN link for a new or relinked Plex instance.
  Future<PlexLinkStart> beginPlexLink() async {
    final resp = await _dio.post('/api/instances/plex/link/begin');
    return PlexLinkStart.fromJson(_asMap(resp.data));
  }

  /// Polls a Plex PIN link; linked=false means the admin has not approved
  /// it yet.
  Future<PlexLinkState> checkPlexLink(int pinId) async {
    final resp = await _dio.post(
      '/api/instances/plex/link/check',
      data: {'pin_id': pinId},
    );
    final data = _asMap(resp.data);
    return PlexLinkState(
      linked: data['linked'] as bool? ?? false,
      account: data['account'] as String? ?? '',
    );
  }

  /// The owned Plex Media Servers of a linked account, for the server
  /// picker: by approved pin when creating, by [id] (stored token) when
  /// editing. [url] is the plex.tv address the instance dials ('' = default).
  Future<List<PlexServerChoice>> listPlexServers({
    String? id,
    int? plexLinkPin,
    String url = '',
  }) async {
    final resp = await _dio.post('/api/instances/plex/servers', data: {
      if (id != null) 'id': id,
      'service_type': 'plex',
      'url': url,
      'api_key': '',
      'username': '',
      'password': '',
      if (plexLinkPin != null) 'plex_link_pin': plexLinkPin,
    });
    final list = _asMap(resp.data)['servers'];
    if (list is! List) return const [];
    return list
        .whereType<Map>()
        .map((raw) => PlexServerChoice.fromJson(Map<String, dynamic>.from(raw)))
        .toList(growable: false);
  }

  static Map<String, dynamic> _asMap(Object? data) {
    Object? decoded = data;
    if (data is String && data.trim().isNotEmpty) decoded = jsonDecode(data);
    return decoded is Map ? Map<String, dynamic>.from(decoded) : const {};
  }

  /// Ask the server to dial a media server (Jellyfin) and report the
  /// libraries it has right now. Same body and credential fallback as
  /// [testConnection]: with [id] set, a blank key uses the stored one, so an
  /// edit form can list libraries without retyping the key. Throws on failure
  /// with the server's host-free reason.
  Future<MediaServerProbe> listMediaServerLibraries({
    String? id,
    required String serviceType,
    required String url,
    String apiKey = '',
    int? plexLinkPin,
    String machineIdentifier = '',
  }) async {
    final resp =
        await _dio.post('/api/instances/media-server/libraries', data: {
      if (id != null) 'id': id,
      'service_type': serviceType,
      'url': url,
      'api_key': apiKey,
      'username': '',
      'password': '',
      if (plexLinkPin != null) 'plex_link_pin': plexLinkPin,
      if (machineIdentifier.isNotEmpty)
        'media_server_config': {'machine_identifier': machineIdentifier},
    });
    dynamic data = resp.data;
    if (data is String && data.trim().isNotEmpty) {
      data = jsonDecode(data);
    }
    return MediaServerProbe.fromJson(
      data is Map ? Map<String, dynamic>.from(data) : const {},
    );
  }
}
