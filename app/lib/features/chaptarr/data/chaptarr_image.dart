import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';

/// A resolved image URL plus any headers needed to fetch it.
typedef ChaptarrImageSource = ({String url, Map<String, String>? headers});

/// Resolves a Chaptarr image `url` field into something loadable.
///
/// Author art comes back as an absolute metadata-provider URL (hardcover, etc.)
/// and loads directly. Book covers come back relative (`/MediaCover/...` or
/// `/MediaCoverProxy/...`), which the Chaptarr web layer gates behind a login
/// session — so we route them through the backend's cover endpoint, which logs
/// in with the instance's web credentials and streams the image. The user's
/// bearer token authorizes the backend call.
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

  final base = serverUrl.endsWith('/')
      ? serverUrl.substring(0, serverUrl.length - 1)
      : serverUrl;
  final path = rawUrl.startsWith('/') ? rawUrl : '/$rawUrl';
  final url = '$base/api/instances/$instanceId/cover'
      '?path=${Uri.encodeQueryComponent(path)}';

  final token = conn?.accessToken ?? '';
  final headers = token.isEmpty ? null : {'Authorization': 'Bearer $token'};
  return (url: url, headers: headers);
}
