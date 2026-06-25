import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';

/// A resolved image URL plus any headers needed to fetch it.
typedef ChaptarrImageSource = ({String url, Map<String, String>? headers});

/// Resolves a Chaptarr image `url` field into something loadable.
///
/// Author art comes back as an absolute metadata-provider URL (hardcover, etc.)
/// and loads directly. Book covers come back relative (`/MediaCover/...`), which
/// the Chaptarr web layer gates behind a login session — but prefixing the path
/// with `/api/v1` makes it API-key authed, so we route it through the backend's
/// authenticated instance proxy and attach the user's bearer token.
///
/// Returns null when there's no usable url or the connection isn't ready.
ChaptarrImageSource? chaptarrImageSource(
  WidgetRef ref,
  String? rawUrl,
  String instanceId,
) {
  if (rawUrl == null || rawUrl.isEmpty) return null;
  if (rawUrl.startsWith('http')) return (url: rawUrl, headers: null);

  final conn = ref.read(authProvider).valueOrNull?.connection;
  final serverUrl = conn?.serverUrl;
  if (serverUrl == null || serverUrl.isEmpty) return null;

  final base =
      serverUrl.endsWith('/') ? serverUrl.substring(0, serverUrl.length - 1) : serverUrl;
  final path = rawUrl.startsWith('/') ? rawUrl : '/$rawUrl';
  final url = '$base/api/instances/$instanceId/api/v1$path';

  final token = conn?.accessToken ?? '';
  final headers = token.isEmpty ? null : {'Authorization': 'Bearer $token'};
  return (url: url, headers: headers);
}
