import 'package:cached_network_image/cached_network_image.dart';
import 'package:cached_network_image_platform_interface/cached_network_image_platform_interface.dart'
    show ImageRenderMethodForWeb;
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../features/auth/logic/auth_provider.dart';
import '../network/app_image_cache.dart';
import '../theme/app_theme.dart';

/// A resolved image request: the URL to fetch plus any headers it needs.
typedef ImageSource = ({String url, Map<String, String>? headers});

/// Prefetch uses exactly the same cache and web transport as visible artwork.
ImageProvider cachedImageProvider(ImageSource source) {
  if (usesHtmlImageElement(source)) {
    return NetworkImage(
      source.url,
      webHtmlElementStrategy: WebHtmlElementStrategy.prefer,
    );
  }
  return CachedNetworkImageProvider(
    source.url,
    headers: source.headers,
    cacheManager: appImageCache,
    imageRenderMethodForWeb: source.headers == null
        ? ImageRenderMethodForWeb.HtmlImage
        : ImageRenderMethodForWeb.HttpGet,
  );
}

/// Hardcover's public covers do not allow cross-origin byte reads. A browser
/// image element can display them without a relay. Headered images still use
/// HTTP so authentication is never silently dropped, and native keeps the
/// shared disk cache.
///
/// On web this is only the fallback for a client with no server URL to build a
/// relay path from: a DOM image element is a platform view, which Flutter web
/// composites above the canvas, so the availability badge and rating painted
/// over the artwork disappear behind it. [resolveImageSource] routes these
/// covers through the backend instead wherever it can.
bool usesHtmlImageElement(ImageSource source, {bool isWeb = kIsWeb}) {
  if (!isWeb || (source.headers?.isNotEmpty ?? false)) return false;
  final uri = Uri.tryParse(source.url);
  return uri?.scheme == 'https' && uri?.host == _hardcoverAssetHost;
}

/// The single host Hardcover serves cover art from.
const _hardcoverAssetHost = 'assets.hardcover.app';

/// True for Trakt's artwork CDNs (media.trakt.tv today, walter*.trakt.tv
/// before July 2026 — Trakt migrates these hosts, so match the domain rather
/// than pinning names). Unlike TMDB's CDN they send no CORS headers, so the
/// web renderer is forbidden from reading their bytes; the backend relays them
/// at `/api/trakt/images/{host}/…` so web can fetch same-origin.
bool _isTraktCdnHost(String host) => host.endsWith('.trakt.tv');

/// Resolves what [CachedImage] should actually fetch.
///
/// On native this is the identity function — native HTTP has no CORS, so every
/// host works directly. On web, Trakt CDN and Hardcover cover URLs are
/// rewritten to the backend's same-origin relay with the session bearer
/// attached; everything else (TMDB, author art, the backend's own proxy URLs)
/// passes through untouched.
///
/// Both relays exist because their CDNs send no CORS headers. Trakt's relay
/// makes the artwork loadable at all. Hardcover's covers would load without
/// one, as a DOM image element — but that makes every cover a platform view,
/// which Flutter web paints above the canvas, hiding the availability badge
/// and rating drawn over it. Routing through the backend puts covers back on
/// the canvas path, where the badges layer correctly.
ImageSource resolveImageSource({
  required String url,
  Map<String, String>? headers,
  String? serverUrl,
  String? accessToken,
  bool isWeb = kIsWeb,
}) {
  final passthrough = (url: url, headers: headers);
  if (!isWeb) return passthrough;

  final uri = Uri.tryParse(url);
  if (uri == null) return passthrough;

  final String relayPath;
  if (_isTraktCdnHost(uri.host) && uri.path.startsWith('/images/')) {
    relayPath = '/api/trakt/images/${uri.host}${uri.path}';
  } else if (uri.host == _hardcoverAssetHost) {
    relayPath = '/api/discover/books/images${uri.path}';
  } else {
    return passthrough;
  }
  if (serverUrl == null || serverUrl.isEmpty) return passthrough;

  final base = serverUrl.endsWith('/')
      ? serverUrl.substring(0, serverUrl.length - 1)
      : serverUrl;
  final merged = <String, String>{
    ...?headers,
    if (accessToken != null && accessToken.isNotEmpty)
      'Authorization': 'Bearer $accessToken',
  };
  return (url: '$base$relayPath', headers: merged.isEmpty ? null : merged);
}

/// The app's one network-image widget. Every poster/cover/photo goes through it
/// so they share [appImageCache] and render a consistent placeholder, error
/// fallback, and fade-in. Pass [headers] for images behind the authenticated
/// instance proxy (e.g. Chaptarr `/MediaCover`).
class CachedImage extends StatelessWidget {
  /// Absolute image URL. A null/empty url renders the [icon] fallback.
  final String? url;

  /// Optional request headers (e.g. a bearer token for the backend proxy).
  final Map<String, String>? headers;

  final BoxFit fit;

  /// Icon shown for an empty url or a load failure.
  final IconData icon;
  final double iconSize;

  final double? width;
  final double? height;

  const CachedImage({
    super.key,
    required this.url,
    this.headers,
    this.fit = BoxFit.cover,
    this.icon = Icons.image_outlined,
    this.iconSize = 20,
    this.width,
    this.height,
  });

  Widget _fallback() => Container(
        width: width,
        height: height,
        color: AppTheme.surfaceVariant,
        alignment: Alignment.center,
        child: Icon(icon, color: AppTheme.textSecondary, size: iconSize),
      );

  Widget _image(ImageSource source) {
    if (usesHtmlImageElement(source)) {
      return Image(
        image: cachedImageProvider(source),
        fit: fit,
        width: width,
        height: height,
        loadingBuilder: (_, child, progress) =>
            progress == null ? child : _fallback(),
        errorBuilder: (_, __, ___) => _fallback(),
      );
    }
    return CachedNetworkImage(
      imageUrl: source.url,
      httpHeaders: source.headers,
      cacheManager: appImageCache,
      // The default HtmlImage decode path on web drops httpHeaders entirely,
      // which silently unauthenticates covers behind the backend proxy. Any
      // headered request goes through the cache manager's real HTTP fetch
      // instead; header-free CDN images keep the browser-native path.
      imageRenderMethodForWeb: source.headers == null
          ? ImageRenderMethodForWeb.HtmlImage
          : ImageRenderMethodForWeb.HttpGet,
      fit: fit,
      width: width,
      height: height,
      fadeInDuration: const Duration(milliseconds: 200),
      // Keep the same fallback visible while the network image resolves. A
      // blank rectangle briefly reads as a missing cover on slower devices.
      placeholder: (_, __) => _fallback(),
      errorWidget: (_, __, ___) => _fallback(),
    );
  }

  @override
  Widget build(BuildContext context) {
    final src = url;
    if (src == null || src.isEmpty) return _fallback();
    if (!kIsWeb) return _image((url: src, headers: headers));
    // Web only: the Trakt relay rewrite needs the session's server URL and
    // bearer, so reach for them lazily here rather than making every native
    // poster read a provider.
    return Consumer(
      builder: (context, ref, _) {
        final conn =
            ref.watch(authProvider.select((s) => s.valueOrNull?.connection));
        return _image(resolveImageSource(
          url: src,
          headers: headers,
          serverUrl: conn?.serverUrl,
          accessToken: conn?.accessToken,
        ));
      },
    );
  }
}
