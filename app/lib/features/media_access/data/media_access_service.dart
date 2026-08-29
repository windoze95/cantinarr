import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';

/// The live state of one user's account on a media server, as the server
/// answered just now. [verified] is false when the backend could not reach
/// the media server and fell back to what it last recorded: blindness, not
/// absence, and the guide says so. [pending] is an invite (Plex) the person
/// has not accepted yet.
class MediaServerAccountStatus {
  final String username;
  final bool disabled;
  final bool pending;
  final bool verified;

  const MediaServerAccountStatus({
    required this.username,
    this.disabled = false,
    this.pending = false,
    this.verified = true,
  });

  factory MediaServerAccountStatus.fromJson(Map<String, dynamic> json) =>
      MediaServerAccountStatus(
        username: json['username'] as String? ?? '',
        disabled: json['disabled'] as bool? ?? false,
        pending: json['pending'] as bool? ?? false,
        verified: json['verified'] as bool? ?? true,
      );
}

/// How a media server grants access: an account Cantinarr creates with a
/// password the user picks (Jellyfin, Emby), or an invite sent to the email
/// the user shares (Plex).
enum MediaServerKind { account, invite }

/// One media server the signed-in user was granted, with their account on it
/// (null = no account yet). Carries the admin-typed sign-in address only,
/// never the instance URL the backend dials.
class MediaServerAccess {
  final String instanceId;
  final String serviceType;
  final String name;
  final MediaServerKind kind;
  final String publicAddress;
  final MediaServerAccountStatus? account;

  const MediaServerAccess({
    required this.instanceId,
    required this.serviceType,
    required this.name,
    this.kind = MediaServerKind.account,
    this.publicAddress = '',
    this.account,
  });

  bool get isInvite => kind == MediaServerKind.invite;

  factory MediaServerAccess.fromJson(Map<String, dynamic> json) {
    final rawAccount = json['account'];
    final serviceType = json['service_type'] as String? ?? '';
    final rawKind = json['kind'] as String?;
    return MediaServerAccess(
      instanceId: json['instance_id'] as String? ?? '',
      serviceType: serviceType,
      name: json['name'] as String? ?? '',
      // The server says which; an older server that does not is read from
      // the type, since Plex is the only invite server.
      kind: (rawKind ?? (serviceType == 'plex' ? 'invite' : 'account')) ==
              'invite'
          ? MediaServerKind.invite
          : MediaServerKind.account,
      publicAddress: json['public_address'] as String? ?? '',
      account: rawAccount is Map
          ? MediaServerAccountStatus.fromJson(
              Map<String, dynamic>.from(rawAccount),
            )
          : null,
    );
  }
}

/// What the server answers once an account has been created or an invite
/// sent ([pending] true: the person still has to accept it).
class MediaServerAccountCreated {
  final String username;
  final String publicAddress;
  final bool pending;

  const MediaServerAccountCreated({
    required this.username,
    this.publicAddress = '',
    this.pending = false,
  });

  factory MediaServerAccountCreated.fromJson(Map<String, dynamic> json) =>
      MediaServerAccountCreated(
        username: json['username'] as String? ?? '',
        publicAddress: json['public_address'] as String? ?? '',
        pending: json['pending'] as bool? ?? false,
      );
}

/// Admin view of one linked account: which Cantinarr user is which account
/// on which server. Rows are an action log; the media server stays the live
/// truth, so [disabled] is what the backend last reconciled.
class MediaServerAccountRow {
  final int userId;
  final String instanceId;
  final String instanceName;
  final String serviceType;
  final String remoteUserId;
  final String remoteUsername;
  final bool createdByCantinarr;
  final bool disabled;
  final String? createdAt;

  const MediaServerAccountRow({
    required this.userId,
    required this.instanceId,
    required this.instanceName,
    required this.serviceType,
    required this.remoteUserId,
    required this.remoteUsername,
    this.createdByCantinarr = true,
    this.disabled = false,
    this.createdAt,
  });

  factory MediaServerAccountRow.fromJson(Map<String, dynamic> json) =>
      MediaServerAccountRow(
        userId: (json['user_id'] as num?)?.toInt() ?? 0,
        instanceId: json['instance_id'] as String? ?? '',
        instanceName: json['instance_name'] as String? ?? '',
        serviceType: json['service_type'] as String? ?? '',
        remoteUserId: json['remote_user_id']?.toString() ?? '',
        remoteUsername: json['username'] as String? ?? '',
        createdByCantinarr: json['created_by_cantinarr'] as bool? ?? true,
        // The wire carries a bool; a stamp-shaped `disabled_at` is tolerated
        // as the same fact.
        disabled: json['disabled'] as bool? ?? json['disabled_at'] != null,
        createdAt: json['created_at'] as String?,
      );
}

/// One account as the media server itself lists it, for the admin link
/// picker. Administrators are listed by the server but never linkable. On
/// Plex the rows are shares, keyed by email, and [pending] marks an invite
/// nobody has accepted yet.
class RemoteMediaServerUser {
  final String id;
  final String name;
  final bool isAdministrator;
  final bool isDisabled;
  final bool pending;

  const RemoteMediaServerUser({
    required this.id,
    required this.name,
    this.isAdministrator = false,
    this.isDisabled = false,
    this.pending = false,
  });

  factory RemoteMediaServerUser.fromJson(Map<String, dynamic> json) =>
      RemoteMediaServerUser(
        id: json['id']?.toString() ?? '',
        name: json['name'] as String? ?? '',
        isAdministrator: json['is_administrator'] as bool? ?? false,
        isDisabled: json['is_disabled'] as bool? ?? false,
        pending: json['pending'] as bool? ?? false,
      );
}

/// A failed media-server account call, with the backend's `{error, code}`
/// envelope decoded. [status] is null for a transport failure (nothing was
/// answered at all); [code] is '' when the server sent none.
class MediaAccessException implements Exception {
  final int? status;
  final String code;
  final String message;

  const MediaAccessException({
    required this.status,
    this.code = '',
    this.message = '',
  });

  factory MediaAccessException.fromDio(DioException e) {
    final decoded = _decodeErrorBody(e.response?.data);
    return MediaAccessException(
      status: e.response?.statusCode,
      code: decoded.code,
      message: decoded.message,
    );
  }

  /// Nothing came back from the server: a connection, timeout, or cancel.
  bool get isTransport => status == null;

  @override
  String toString() =>
      'MediaAccessException(status: $status, code: $code, message: $message)';
}

/// The app-owned `{ "error": ..., "code": ... }` envelope. Some handlers use
/// Go's `http.Error`, which labels a JSON body text/plain; Dio then leaves it
/// as a string, so decode that here rather than reading `data['error']`.
({String code, String message}) _decodeErrorBody(Object? data) {
  Object? decoded = data;
  if (data is String) {
    final text = data.trim();
    if (text.isEmpty) return (code: '', message: '');
    try {
      decoded = jsonDecode(text);
    } catch (_) {
      final plain = text.length <= 500 && !text.toLowerCase().contains('<html')
          ? text
          : '';
      return (code: '', message: plain);
    }
  }
  if (decoded is Map) {
    return (
      code: (decoded['code'] as String? ?? '').trim(),
      message: (decoded['error'] as String? ?? '').trim(),
    );
  }
  return (code: '', message: '');
}

/// The media-server account endpoints: a user's own servers and account
/// creation, plus the admin link/unlink routes. Turning access off and on is
/// not here on purpose: access is the instance grant, edited through the
/// existing per-user grants endpoints.
class MediaAccessService {
  final Dio _dio;

  MediaAccessService({required Dio backendDio}) : _dio = backendDio;

  /// The media servers the signed-in user was granted, each with their live
  /// account state. Re-read on every open: the rows behind it are an action
  /// log and the media server is the truth.
  Future<List<MediaServerAccess>> listMine() async {
    final resp = await _dio.get('/api/media-servers');
    return _list(resp.data)
        .map((raw) => MediaServerAccess.fromJson(raw))
        .toList(growable: false);
  }

  /// Creates the user's account on [instanceId] with a password only they
  /// know. The password travels once, in this request body, and is never
  /// stored by Cantinarr. Throws [MediaAccessException] with the server's
  /// `code` (`account_exists`, `name_taken`, `invalid_name`) on refusal.
  Future<MediaServerAccountCreated> createAccount(
    String instanceId,
    String password,
  ) async {
    try {
      final resp = await _dio.post(
        '/api/media-servers/$instanceId/account',
        data: {'password': password},
      );
      return MediaServerAccountCreated.fromJson(_map(resp.data));
    } on DioException catch (e) {
      throw MediaAccessException.fromDio(e);
    }
  }

  /// Asks for the user's Plex invite on [instanceId]: the server records the
  /// email and sends the share invite (or adopts a share that already exists
  /// for that address). Throws [MediaAccessException] with the server's
  /// `code` (`account_exists`, `name_taken`, `invalid_email`, `wrong_kind`)
  /// on refusal.
  Future<MediaServerAccountCreated> requestInvite(
    String instanceId,
    String email,
  ) async {
    try {
      final resp = await _dio.post(
        '/api/media-servers/$instanceId/account',
        data: {'email': email},
      );
      return MediaServerAccountCreated.fromJson(_map(resp.data));
    } on DioException catch (e) {
      throw MediaAccessException.fromDio(e);
    }
  }

  /// Every linked account across all media servers (admin).
  Future<List<MediaServerAccountRow>> listAccounts() async {
    final resp = await _dio.get('/api/admin/media-servers/accounts');
    return _list(resp.data)
        .map((raw) => MediaServerAccountRow.fromJson(raw))
        .toList(growable: false);
  }

  /// The accounts the media server itself lists (admin), for linking an
  /// existing one to a Cantinarr user. Accepts a bare list or `{users: [...]}`.
  Future<List<RemoteMediaServerUser>> listRemoteUsers(
    String instanceId,
  ) async {
    final resp = await _dio.get('/api/admin/media-servers/$instanceId/users');
    dynamic data = resp.data;
    if (data is Map && data['users'] is List) data = data['users'];
    return _list(data)
        .map((raw) => RemoteMediaServerUser.fromJson(raw))
        .toList(growable: false);
  }

  /// Records that [userId] is [remoteUserId] on [instanceId] (admin). Only
  /// the connection is recorded; the remote account itself is not changed.
  Future<MediaServerAccountRow> link({
    required int userId,
    required String instanceId,
    required String remoteUserId,
  }) async {
    try {
      final resp = await _dio.put(
        '/api/admin/users/$userId/media-servers/$instanceId/account',
        data: {'remote_user_id': remoteUserId},
      );
      return MediaServerAccountRow.fromJson(_map(resp.data));
    } on DioException catch (e) {
      throw MediaAccessException.fromDio(e);
    }
  }

  /// Forgets the link (admin). The remote account and the grant stay as they
  /// are; Cantinarr just stops managing that account.
  Future<void> unlink({
    required int userId,
    required String instanceId,
  }) async {
    try {
      await _dio.delete(
        '/api/admin/users/$userId/media-servers/$instanceId/account',
      );
    } on DioException catch (e) {
      throw MediaAccessException.fromDio(e);
    }
  }

  static List<Map<String, dynamic>> _list(Object? data) {
    Object? decoded = data;
    if (data is String && data.trim().isNotEmpty) decoded = jsonDecode(data);
    if (decoded is! List) return const [];
    return decoded
        .whereType<Map>()
        .map((raw) => Map<String, dynamic>.from(raw))
        .toList(growable: false);
  }

  static Map<String, dynamic> _map(Object? data) {
    Object? decoded = data;
    if (data is String && data.trim().isNotEmpty) decoded = jsonDecode(data);
    return decoded is Map ? Map<String, dynamic>.from(decoded) : const {};
  }
}

final mediaAccessServiceProvider = Provider<MediaAccessService>(
  (ref) => MediaAccessService(backendDio: ref.watch(backendClientProvider)),
);

/// The product name for a media-server service type ('jellyfin' -> Jellyfin).
String mediaServerTypeLabel(String serviceType) {
  switch (serviceType) {
    case 'plex':
      return 'Plex';
    case 'jellyfin':
      return 'Jellyfin';
    case 'emby':
      return 'Emby';
    default:
      if (serviceType.isEmpty) return 'your media server';
      return serviceType[0].toUpperCase() + serviceType.substring(1);
  }
}

/// The product names of the granted media-server types, distinct and in a
/// fixed order: Plex, Jellyfin, then Emby, then anything unknown as typed.
List<String> mediaServerTypeLabels(Iterable<String> serviceTypes) {
  const order = ['plex', 'jellyfin', 'emby'];
  final distinct = serviceTypes.toSet();
  return [
    for (final type in order)
      if (distinct.remove(type)) mediaServerTypeLabel(type),
    for (final type in distinct) mediaServerTypeLabel(type),
  ];
}

/// The granted servers as one phrase: "Plex", "Plex or Jellyfin",
/// "Plex, Jellyfin, or Emby". Empty when nothing is granted.
String mediaServerNamesPhrase(Iterable<String> serviceTypes) {
  final labels = mediaServerTypeLabels(serviceTypes);
  switch (labels.length) {
    case 0:
      return '';
    case 1:
      return labels.single;
    case 2:
      return '${labels.first} or ${labels.last}';
    default:
      final head = labels.sublist(0, labels.length - 1).join(', ');
      return '$head, or ${labels.last}';
  }
}

/// The menu and app-bar title of the access guide, derived from the media
/// server types the user was granted: "Watch on Jellyfin", "Watch on Jellyfin
/// or Emby". Static surfaces (Settings row, breadcrumb, search index) say
/// "Media server access" instead, because they cannot know the set.
String mediaServerGuideTitle(Iterable<String> serviceTypes) {
  final names = mediaServerNamesPhrase(serviceTypes);
  return names.isEmpty ? 'Watch on your media server' : 'Watch on $names';
}
