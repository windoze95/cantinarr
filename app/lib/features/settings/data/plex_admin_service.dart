import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';

/// Admin view of the Plex integration: whether an account is linked and how
/// invites are configured. The token never leaves the server.
class PlexStatus {
  final bool linked;
  final String account;
  final String machineIdentifier;
  final String serverName;
  final List<int> librarySectionIds;
  final bool autoInvite;
  final bool configured;

  const PlexStatus({
    required this.linked,
    this.account = '',
    this.machineIdentifier = '',
    this.serverName = '',
    this.librarySectionIds = const [],
    this.autoInvite = false,
    this.configured = false,
  });

  factory PlexStatus.fromJson(Map<String, dynamic> json) => PlexStatus(
        linked: json['linked'] as bool? ?? false,
        account: json['account'] as String? ?? '',
        machineIdentifier: json['machine_identifier'] as String? ?? '',
        serverName: json['server_name'] as String? ?? '',
        librarySectionIds: (json['library_section_ids'] as List<dynamic>?)
                ?.map((e) => (e as num).toInt())
                .toList() ??
            const [],
        autoInvite: json['auto_invite'] as bool? ?? false,
        configured: json['configured'] as bool? ?? false,
      );
}

class PlexServer {
  final String name;
  final String machineIdentifier;

  const PlexServer({required this.name, required this.machineIdentifier});

  factory PlexServer.fromJson(Map<String, dynamic> json) => PlexServer(
        name: json['name'] as String? ?? '',
        machineIdentifier: json['machine_identifier'] as String? ?? '',
      );
}

class PlexLibrary {
  final int id;
  final String title;
  final String type;

  const PlexLibrary({required this.id, required this.title, required this.type});

  factory PlexLibrary.fromJson(Map<String, dynamic> json) => PlexLibrary(
        id: (json['id'] as num?)?.toInt() ?? 0,
        title: json['title'] as String? ?? '',
        type: json['type'] as String? ?? '',
      );
}

/// The start of the PIN link flow: open [url] for the admin, then poll
/// checkLink with [pinId] until they approve.
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

class PlexAdminService {
  final Dio _dio;

  PlexAdminService({required Dio backendDio}) : _dio = backendDio;

  Future<PlexStatus> status() async {
    final resp = await _dio.get('/api/admin/plex/status');
    return PlexStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<PlexLinkStart> beginLink() async {
    final resp = await _dio.post('/api/admin/plex/link/begin');
    return PlexLinkStart.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Polls the PIN; linked=false means the admin hasn't approved yet.
  Future<bool> checkLink(int pinId) async {
    final resp = await _dio.post(
      '/api/admin/plex/link/check',
      data: {'pin_id': pinId},
    );
    final data = resp.data as Map<String, dynamic>;
    return data['linked'] as bool? ?? false;
  }

  Future<void> unlink() async {
    await _dio.delete('/api/admin/plex/link');
  }

  Future<List<PlexServer>> servers() async {
    final resp = await _dio.get('/api/admin/plex/servers');
    final list = (resp.data as Map<String, dynamic>)['servers'] as List? ?? [];
    return list
        .map((e) => PlexServer.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<PlexLibrary>> libraries(String machineIdentifier) async {
    final resp =
        await _dio.get('/api/admin/plex/servers/$machineIdentifier/libraries');
    final list = (resp.data as Map<String, dynamic>)['libraries'] as List? ?? [];
    return list
        .map((e) => PlexLibrary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<PlexStatus> updateSettings({
    required String machineIdentifier,
    required String serverName,
    required List<int> librarySectionIds,
    required bool autoInvite,
  }) async {
    final resp = await _dio.put('/api/admin/plex/settings', data: {
      'machine_identifier': machineIdentifier,
      'server_name': serverName,
      'library_section_ids': librarySectionIds,
      'auto_invite': autoInvite,
    });
    return PlexStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Sends the Plex invite for a user's shared email. Returns the outcome:
  /// "invited" or "already_shared".
  Future<String> inviteUser(int userId) async {
    final resp = await _dio.post('/api/admin/users/$userId/plex-invite');
    return (resp.data as Map<String, dynamic>)['status'] as String? ?? 'invited';
  }
}

final plexAdminServiceProvider = Provider<PlexAdminService>(
  (ref) => PlexAdminService(backendDio: ref.watch(backendClientProvider)),
);

/// Whether one-tap invites are available (account linked + server selected).
/// The Users screen uses this to offer "Send Plex invite" instead of the
/// copy-email-and-open-Plex fallback. Admin-only endpoint — only watch this
/// from admin screens. Invalidate after link/unlink/settings changes.
final plexInviteConfiguredProvider = FutureProvider<bool>((ref) async {
  try {
    final status = await ref.watch(plexAdminServiceProvider).status();
    return status.configured;
  } catch (_) {
    return false;
  }
});
