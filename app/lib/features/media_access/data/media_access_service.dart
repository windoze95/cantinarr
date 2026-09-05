import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../discover/data/tmdb_models.dart';
import '../../../core/network/backend_client.dart';

/// The live state of one user's account on a media server, as the server
/// answered just now. [verified] is false when the backend could not reach
/// the media server and fell back to what it last recorded: blindness, not
/// absence, and the guide says so. [pending] is an invite (Plex) the person
/// has not accepted yet. [administrator] is an account Cantinarr records and
/// never changes: a server administrator, or on Plex the server's owner.
class MediaServerAccountStatus {
  final String username;
  final bool disabled;
  final bool pending;
  final bool administrator;
  final bool verified;

  const MediaServerAccountStatus({
    required this.username,
    this.disabled = false,
    this.pending = false,
    this.administrator = false,
    this.verified = true,
  });

  factory MediaServerAccountStatus.fromJson(Map<String, dynamic> json) =>
      MediaServerAccountStatus(
        username: json['username'] as String? ?? '',
        disabled: json['disabled'] as bool? ?? false,
        pending: json['pending'] as bool? ?? false,
        administrator: json['administrator'] as bool? ?? false,
        verified: json['verified'] as bool? ?? true,
      );
}

/// One requested account's outcome from an import: [created] says a
/// Cantinarr user was made for it (an existing user of the same name is
/// reused and gets no new link), [linked] says the account is now that
/// user's, [link] is the connect link to hand out, and [error] is the
/// server's code when a step was refused (`not_found`, `already_linked`,
/// `user_failed`, `user_has_account`, `link_failed`).
class MediaServerImportResult {
  final String remoteUserId;
  final String remoteUsername;
  final int? userId;
  final String username;
  final bool created;
  final bool linked;
  final String link;
  final String originSource;
  final String error;

  const MediaServerImportResult({
    required this.remoteUserId,
    required this.remoteUsername,
    this.userId,
    this.username = '',
    this.created = false,
    this.linked = false,
    this.link = '',
    this.originSource = '',
    this.error = '',
  });

  factory MediaServerImportResult.fromJson(Map<String, dynamic> json) =>
      MediaServerImportResult(
        remoteUserId: json['remote_user_id']?.toString() ?? '',
        remoteUsername: json['remote_username'] as String? ?? '',
        userId: (json['user_id'] as num?)?.toInt(),
        username: json['username'] as String? ?? '',
        created: json['created'] as bool? ?? false,
        linked: json['linked'] as bool? ?? false,
        link: json['link'] as String? ?? '',
        originSource: json['origin_source'] as String? ?? '',
        error: json['error'] as String? ?? '',
      );
}

/// How a media server grants access: an account Cantinarr creates with a
/// password the user picks (Jellyfin, Emby), or an invite sent to the email
/// the user shares (Plex).
enum MediaServerKind { account, invite }

/// What a media server answered about one title: it holds it and the account
/// can see it ([found]), it confirmed it has no such title the account can
/// see ([missing]: not imported yet, or in a library not shared), or it
/// could not answer ([unreachable]), or could not verify an exact match
/// ([unverified]). Neither uncertain state is read as absence.
enum WatchLinkState { found, missing, unreachable, unverified }

/// Where one title can be watched on one media server, as the server
/// answered just now. [url] is the title's page (hosted Plex Web or the
/// admin-typed sign-in address), set only when [state] is [WatchLinkState.found].
/// [fallbackUrl] is a generic sign-in shortcut, never proof of availability.
class WatchLink {
  final String instanceId;
  final String name;
  final String serviceType;
  final WatchLinkState state;
  final String url;
  final String fallbackUrl;

  const WatchLink({
    required this.instanceId,
    required this.name,
    required this.serviceType,
    required this.state,
    this.url = '',
    this.fallbackUrl = '',
  });

  factory WatchLink.fromJson(Map<String, dynamic> json) => WatchLink(
        instanceId: json['instance_id'] as String? ?? '',
        serviceType: json['service_type'] as String? ?? '',
        name: json['name'] as String? ?? '',
        state: switch (json['state']) {
          'found' => WatchLinkState.found,
          'missing' => WatchLinkState.missing,
          'unverified' => WatchLinkState.unverified,
          _ => WatchLinkState.unreachable,
        },
        url: json['url'] as String? ?? '',
        fallbackUrl: json['fallback_url'] as String? ?? '',
      );
}

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

  /// The server confirmed an account named like this user that nobody is
  /// linked to, while the user has none here: creating one would collide,
  /// so the guide leads with signing in to link it. False only means no
  /// match was confirmed (the server may have been unreachable), so the
  /// card never claims there is no such account.
  final bool existingAccount;

  const MediaServerAccess({
    required this.instanceId,
    required this.serviceType,
    required this.name,
    this.kind = MediaServerKind.account,
    this.publicAddress = '',
    this.account,
    this.existingAccount = false,
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
      existingAccount: json['existing_account'] as bool? ?? false,
    );
  }
}

/// What the server answers once an account has been created, an invite
/// sent ([pending] true: the person still has to accept it), or an existing
/// account linked ([administrator] when it is one the server administers).
class MediaServerAccountCreated {
  final String username;
  final String publicAddress;
  final bool pending;
  final bool administrator;

  const MediaServerAccountCreated({
    required this.username,
    this.publicAddress = '',
    this.pending = false,
    this.administrator = false,
  });

  factory MediaServerAccountCreated.fromJson(Map<String, dynamic> json) =>
      MediaServerAccountCreated(
        username: json['username'] as String? ?? '',
        publicAddress: json['public_address'] as String? ?? '',
        pending: json['pending'] as bool? ?? false,
        administrator: json['administrator'] as bool? ?? false,
      );
}

/// A Plex sign-in that has begun: the PIN the app polls and the plex.tv page
/// the person opens to approve it with their own account.
class PlexSignInStart {
  final int pinId;
  final String code;
  final String url;

  const PlexSignInStart({
    required this.pinId,
    required this.code,
    required this.url,
  });

  factory PlexSignInStart.fromJson(Map<String, dynamic> json) =>
      PlexSignInStart(
        pinId: (json['pin_id'] as num?)?.toInt() ?? 0,
        code: json['code'] as String? ?? '',
        url: json['url'] as String? ?? '',
      );
}

/// One poll of a Plex sign-in. [linked] stays false until the person has
/// approved the PIN; then [email] is the verified address and [inviteState]
/// says what it led to: `sent` (an invite to accept), `adopted` (access was
/// already there), `failed` (an invite that could not go out yet; the server
/// retries it), or empty (nobody has granted this user Plex yet, and the
/// admins were told).
class PlexSignInState {
  final bool linked;
  final String username;
  final String email;
  final String inviteState;

  const PlexSignInState({
    required this.linked,
    this.username = '',
    this.email = '',
    this.inviteState = '',
  });

  factory PlexSignInState.fromJson(Map<String, dynamic> json) =>
      PlexSignInState(
        linked: json['linked'] as bool? ?? false,
        username: json['username'] as String? ?? '',
        email: json['email'] as String? ?? '',
        inviteState: json['invite_state'] as String? ?? '',
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
/// picker. Administrators are listed as such and link like any other
/// account, except that Cantinarr never changes them. On Plex the rows are
/// shares, keyed by email, and [pending] marks an invite nobody has accepted
/// yet.
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

/// The media-server account endpoints: a user's own servers, account
/// creation, linking an account that is already theirs (a password check or
/// a Plex sign-in), plus the admin link/unlink routes. Turning access off and
/// on is not here on purpose: access is the instance grant, edited through
/// the existing per-user grants endpoints.
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

  /// Asks where a title can be watched on the media servers the user holds a
  /// linked account on: one entry per server that was asked, each with the
  /// server's own answer; empty when no server was eligible. The year, the
  /// show's TVDB id, and the title narrow the lookup; the match itself is by
  /// provider id.
  Future<List<WatchLink>> watchLinks({
    required MediaType mediaType,
    required int tmdbId,
    int? tvdbId,
    int? year,
    String? title,
  }) async {
    final resp = await _dio.get(
      '/api/media-servers/watch',
      queryParameters: {
        'media_type': mediaType.name,
        'tmdb_id': tmdbId,
        if (tvdbId != null && tvdbId > 0) 'tvdb_id': tvdbId,
        if (year != null && year > 0) 'year': year,
        if (title != null && title.trim().isNotEmpty) 'title': title.trim(),
      },
    );
    return _list(resp.data)
        .map((raw) => WatchLink.fromJson(raw))
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

  /// Links the user's existing account on [instanceId] by proving it is
  /// theirs: the server checks [username] and [password] with the media
  /// server once and keeps neither. Throws [MediaAccessException] with the
  /// server's `code` (`bad_credentials`, `account_refused`, `account_exists`,
  /// `remote_already_linked`, `wrong_kind`) on refusal; a wrong password is
  /// a 400, never a 401.
  Future<MediaServerAccountCreated> linkOwnAccount(
    String instanceId, {
    required String username,
    required String password,
  }) async {
    try {
      final resp = await _dio.post(
        '/api/media-servers/$instanceId/account/link',
        data: {'username': username, 'password': password},
      );
      return MediaServerAccountCreated.fromJson(_map(resp.data));
    } on DioException catch (e) {
      throw MediaAccessException.fromDio(e);
    }
  }

  /// Begins a Plex sign-in: the server mints a plex.tv PIN for the person to
  /// approve with their own account.
  Future<PlexSignInStart> beginPlexSignIn() async {
    try {
      final resp = await _dio.post('/api/media-servers/plex/sign-in/begin');
      return PlexSignInStart.fromJson(_map(resp.data));
    } on DioException catch (e) {
      throw MediaAccessException.fromDio(e);
    }
  }

  /// Polls a Plex sign-in. Once approved, the server has already remembered
  /// the verified email and run the share pass; the same answer comes back
  /// on every later poll. Throws [MediaAccessException] with `pin_expired`
  /// when the sign-in is gone.
  Future<PlexSignInState> checkPlexSignIn(int pinId) async {
    try {
      final resp = await _dio.post(
        '/api/media-servers/plex/sign-in/check',
        data: {'pin_id': pinId},
      );
      return PlexSignInState.fromJson(_map(resp.data));
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

  /// Turns accounts the server lists into Cantinarr users (admin): each
  /// picked account gets a user named after it (found or created, a new one
  /// with a connect link), the instance grant, and the account linked. One
  /// result per picked account, each with its own outcome. [serverUrl] is
  /// the address the links are built on when no External Address is set.
  Future<List<MediaServerImportResult>> importAccounts({
    required String instanceId,
    required List<String> remoteUserIds,
    required String serverUrl,
  }) async {
    final resp = await _dio.post(
      '/api/admin/media-servers/$instanceId/import',
      data: {'remote_user_ids': remoteUserIds, 'server_url': serverUrl},
    );
    dynamic data = resp.data;
    if (data is Map && data['results'] is List) data = data['results'];
    return _list(data)
        .map((raw) => MediaServerImportResult.fromJson(raw))
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
