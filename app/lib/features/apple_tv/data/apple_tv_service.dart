import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../auth/logic/auth_provider.dart';

class AppleTV {
  const AppleTV({this.id = '', required this.name, this.address = '',
    this.identifier = ''});
  final String id;
  final String name;
  final String address;
  final String identifier;

  factory AppleTV.fromJson(Map data) => AppleTV(
    id: data['id'] as String? ?? '', name: data['name'] as String,
    address: data['address'] as String? ?? '',
    identifier: data['identifier'] as String? ?? '',
  );

  Map<String, String> get pairingDetails => {
    'name': name, 'address': address, 'identifier': identifier,
  };
}

class AppleTVList {
  const AppleTVList({required this.supported, required this.devices});
  final bool supported;
  final List<AppleTV> devices;
}

class AppleTVHandoff {
  const AppleTVHandoff({required this.id, required this.expiresAt});
  final String id;
  final DateTime expiresAt;
}

/// All TV traffic goes through Cantinarr. The phone never contacts the TV or
/// receives its Companion credentials. Mutations are never retried here.
class AppleTVService {
  AppleTVService(this._dio);
  final Dio _dio;
  static const _root = '/api/apple-tvs';
  Options get _options => Options(receiveTimeout: const Duration(seconds: 50));

  Future<AppleTVList> list() async {
    final data = (await _dio.get(_root)).data as Map;
    return AppleTVList(supported: data['supported'] == true,
      devices: (data['devices'] as List).map((d) => AppleTV.fromJson(d as Map)).toList());
  }

  Future<List<AppleTV>> discover(String address) async {
    final data = (await _dio.post('$_root/discover',
      data: {'address': address.trim()}, options: _options)).data as Map;
    return (data['devices'] as List).map((d) => AppleTV.fromJson(d as Map)).toList();
  }

  Future<String> begin(AppleTV tv) async {
    final data = (await _dio.post('$_root/pairings', data: tv.pairingDetails,
      options: _options)).data as Map;
    return data['id'] as String;
  }

  Future<void> complete(String id, String pin) async {
    await _dio.post('$_root/pairings/$id/complete', data: {'pin': pin}, options: _options);
  }

  Future<void> cancel(String id) async { await _dio.delete('$_root/pairings/$id'); }
  Future<void> check(String id) async { await _dio.post('$_root/$id/check', options: _options); }
  Future<void> forget(String id) async { await _dio.delete('$_root/$id'); }
  Future<void> update(String id, String name, String address) async {
    await _dio.patch('$_root/$id', data: {'name': name.trim(), 'address': address.trim()});
  }
  Future<Set<int>> grants(String id) async {
    final data = (await _dio.get('$_root/$id/grants')).data as Map;
    return (data['user_ids'] as List).cast<int>().toSet();
  }
  Future<void> saveGrants(String id, Set<int> users) async {
    await _dio.put('$_root/$id/grants', data: {'user_ids': users.toList()});
  }
  Future<AppleTVHandoff> open(String id, String mediaType, int tmdbId) async {
    final data = (await _dio.post('$_root/$id/open', options: _options,
      data: {'media_type': mediaType, 'tmdb_id': tmdbId})).data as Map;
    if (data['state'] != 'sent') throw const FormatException('Invalid TV response');
    return AppleTVHandoff(id: data['confirmation_id'] as String,
      expiresAt: DateTime.parse(data['confirmation_expires_at'] as String));
  }
  Future<void> confirm(String id, String confirmationId) async {
    await _dio.post('$_root/$id/confirm-open', options: _options,
      data: {'confirmation_id': confirmationId});
  }
}

final appleTVServiceProvider = Provider<AppleTVService>(
  (ref) => AppleTVService(ref.watch(backendClientProvider)),
);

final appleTVsProvider = FutureProvider.autoDispose<AppleTVList>((ref) {
  final auth = ref.watch(authProvider).valueOrNull;
  if (auth?.connection?.appleTvRemote != true || auth?.user == null ||
      auth!.user!.child) {
    return const AppleTVList(supported: false, devices: []);
  }
  return ref.watch(appleTVServiceProvider).list();
});

/// Fixed app copy: never render a raw network exception or upstream host.
String appleTVError(Object error) {
  final data = error is DioException ? error.response?.data : null;
  final code = data is Map ? data['code'] : null;
  return switch (code) {
    'unsupported' => 'Apple TV control is not installed on this server.',
    'invalid_request' => 'Check the TV details and access choices, then try again.',
    'invalid_pin' => 'Enter the four-digit PIN shown on the TV.',
    'pairing_failed' || 'pairing_expired' => 'Pairing ended. Start again with a new PIN.',
    'needs_pairing' => 'Pair this Apple TV again.',
    'identity_changed' => 'A different device answered. Check the TV address.',
    'infuse_missing' => 'Install Infuse on this Apple TV, then try again.',
    'unreachable' => 'The server could not reach the TV. Check its address and network.',
    'timeout' => 'The TV did not answer in time. Check the TV before trying again.',
    'busy' => 'Another action is in progress on this TV. Try again shortly.',
    'not_available' || 'unauthorized' => 'This Apple TV is no longer available to your account.',
    'title_unavailable' => 'This title is not available through your media servers.',
    'lookup_unavailable' => 'Could not verify your access to this title. Try again.',
    'launch_failed' => 'The TV rejected the request. Check the TV before trying again.',
    'confirmation_expired' => 'Confirmation expired. Open the title again if needed.',
    _ => 'Could not finish the request. Check the TV before trying again.',
  };
}
