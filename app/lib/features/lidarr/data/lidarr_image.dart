import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';

/// A resolved image URL plus any headers needed to fetch it.
typedef LidarrImageSource = ({String url, Map<String, String>? headers});

/// Resolves a Lidarr image `url` field into something loadable.
///
/// Lookup art comes back as an absolute metadata-provider URL and loads
/// directly. Library art comes back as a relative path (the backend digests
/// emit `/mediacover/artist/{id}/...` and `/mediacover/album/{id}/...` —
/// the exact routes the instance proxy allowlists for requesters), which is
/// routed through the backend's instance proxy with the user's bearer token.
///
/// Returns null when there's no usable url or the connection isn't ready.
LidarrImageSource? lidarrImageSource(
  WidgetRef ref,
  String? rawUrl,
  String instanceId,
) {
  if (rawUrl == null || rawUrl.isEmpty) return null;
  if (rawUrl.startsWith('http')) return (url: rawUrl, headers: null);

  final conn = ref.read(authProvider).valueOrNull?.connection;
  final serverUrl = conn?.serverUrl;
  if (serverUrl == null || serverUrl.isEmpty) return null;

  final base = serverUrl.endsWith('/')
      ? serverUrl.substring(0, serverUrl.length - 1)
      : serverUrl;
  final path = rawUrl.startsWith('/') ? rawUrl : '/$rawUrl';
  final url = '$base/api/instances/$instanceId/api/v1$path';

  final token = conn?.accessToken ?? '';
  final headers = token.isEmpty ? null : {'Authorization': 'Bearer $token'};
  return (url: url, headers: headers);
}
