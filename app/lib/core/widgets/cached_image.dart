import 'dart:convert';

import 'package:cached_network_image/cached_network_image.dart';
import 'package:cached_network_image_platform_interface/cached_network_image_platform_interface.dart'
    show ImageRenderMethodForWeb;
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../features/auth/logic/auth_provider.dart';
import '../network/app_image_cache.dart';
import '../theme/app_theme.dart';
import 'artwork_resize_image.dart';

/// A resolved image request: the URL to fetch plus any headers it needs.
typedef ImageSource = ({String url, Map<String, String>? headers});

/// Prefetch uses exactly the same cache and web transport as visible artwork.
ImageProvider cachedImageProvider(ImageSource source, {bool isWeb = kIsWeb,
    String? cacheScope, Size? displaySize, double devicePixelRatio = 1,
    BoxFit fit = BoxFit.cover}) {
  if (isWeb) {
    final provider = NetworkImage(
      source.url,
      headers: source.headers,
      webHtmlElementStrategy: usesHtmlImageElement(source, isWeb: isWeb)
          ? WebHtmlElementStrategy.prefer
          : WebHtmlElementStrategy.never,
    );
    final ImageProvider scoped = cacheScope != null && (source.headers?.isNotEmpty ?? false)
        ? SessionNetworkImage(provider, cacheScope)
        : provider;
    return displaySize == null ? scoped : ArtworkResizeImage.forDisplay(
        scoped, displaySize, devicePixelRatio, fit);
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

/// Authenticated images share a decoded frame across token rotation, but
/// never across account, grant or content-policy changes. No credential is
/// part of this scope. Public CDN images can keep their ordinary URL key.
String imageCacheScope(AuthState? auth) {
  final permissions = [...?auth?.user?.permissions]..sort();
  final instances = [for (final i in auth?.connection?.instances ?? [])
    '${i.serviceType}:${i.id}:${i.isDefault}']..sort();
  return jsonEncode([auth?.connection?.serverUrl, auth?.user?.id,
    auth?.user?.role, permissions, auth?.user?.child,
    auth?.user?.contentLimits?.toJson(), instances]);
}

/// Only the cache key differs from NetworkImage. Fetching, decoding and
/// listener ownership stay entirely with Flutter's standard implementation.
/// A cache miss uses the current provider's headers, including a rotated JWT.
@visibleForTesting
class SessionNetworkImage extends ImageProvider<Object> {
  final NetworkImage networkImage;
  final String scope;
  const SessionNetworkImage(this.networkImage, this.scope);

  @override
  Future<Object> obtainKey(ImageConfiguration configuration) =>
      SynchronousFuture(_SessionImageKey(networkImage, scope));

  @override
  ImageStreamCompleter loadImage(Object key, ImageDecoderCallback decode) {
    final completer = networkImage.loadImage(networkImage, decode);
    // A failed stream must not occupy our stable key, or a rotated token
    // would reconnect to that failure instead of retrying with fresh headers.
    late ImageStreamListener listener;
    listener = ImageStreamListener((image, synchronous) {
      image.dispose();
      completer.removeListener(listener);
    }, onError: (Object error, StackTrace? stack) {
      PaintingBinding.instance.imageCache.evict(key);
      completer.removeListener(listener);
    });
    completer.addListener(listener);
    return completer;
  }

  // Image must resolve again when headers rotate: a failed old-token fetch
  // needs a retry. A successfully decoded frame still hits the stable key.
  @override
  bool operator ==(Object other) => other is SessionNetworkImage &&
      other.scope == scope && other.networkImage == networkImage &&
      mapEquals(other.networkImage.headers, networkImage.headers);

  @override
  int get hashCode => Object.hash(scope, networkImage);
}

class _SessionImageKey {
  final NetworkImage image;
  final String scope;
  const _SessionImageKey(this.image, this.scope);

  Map<String, String> get _nonAuthHeaders => {
    for (final header in image.headers?.entries ?? <MapEntry<String, String>>[])
      if (header.key.toLowerCase() != 'authorization') header.key: header.value,
  };

  @override
  bool operator ==(Object other) => other is _SessionImageKey &&
      other.scope == scope && other.image.url == image.url &&
      other.image.scale == image.scale &&
      other.image.webHtmlElementStrategy == image.webHtmlElementStrategy &&
      mapEquals(other._nonAuthHeaders, _nonAuthHeaders);

  @override
  int get hashCode => Object.hash(scope, image.url, image.scale,
      image.webHtmlElementStrategy,
      Object.hashAllUnordered(_nonAuthHeaders.entries.map((h) => Object.hash(h.key, h.value))));
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
/// so web shares Flutter's decoded cache and native shares [appImageCache].
/// Retained web frames paint immediately, with a fallback for a cold/failed
/// read. Pass [headers] for authenticated instance-proxy artwork.
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

  Widget _image(ImageSource source, {String? cacheScope}) {
    if (kIsWeb) {
      // Flutter owns the decoded image and listener lifecycle on web. The
      // plugin's multi-image completer decodes again when listeners reconnect,
      // which can dispose the browser image still used by a cached frame.
      return LayoutBuilder(builder: (context, constraints) => Image(
        image: cachedImageProvider(source, cacheScope: cacheScope,
          displaySize: constraints.constrain(Size(
              width ?? double.infinity, height ?? double.infinity)),
          devicePixelRatio: MediaQuery.devicePixelRatioOf(context), fit: fit),
        // CanvasKit can lose medium-quality mipmaps when a texture is recreated
        // under cache pressure. Unrelated hover repaints then change the artwork.
        // Reduce to display resolution first: bicubic alone aliases originals
        // that are much larger than their cards (issue #653).
        filterQuality: FilterQuality.high,
        fit: fit,
        width: width,
        height: height,
        frameBuilder: (_, child, frame, synchronouslyLoaded) =>
            synchronouslyLoaded || frame != null ? child : _fallback(),
        errorBuilder: (_, __, ___) => _fallback(),
      ));
    }
    return CachedNetworkImage(
      imageUrl: source.url,
      httpHeaders: source.headers,
      cacheManager: appImageCache,
      // Native keeps the shared disk cache and the same provider options
      // as prefetch. Web has already returned through Flutter's Image above.
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
        final auth = ref.watch(authProvider).valueOrNull;
        final conn = auth?.connection;
        return _image(resolveImageSource(
          url: src,
          headers: headers,
          serverUrl: conn?.serverUrl,
          accessToken: conn?.accessToken,
        ), cacheScope: imageCacheScope(auth));
      },
    );
  }
}
