import 'dart:convert';

import 'package:cached_network_image/cached_network_image.dart';
import 'package:cached_network_image_platform_interface/cached_network_image_platform_interface.dart'
    show ImageRenderMethodForWeb;
import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../features/auth/logic/auth_provider.dart';
import '../network/app_image_cache.dart';
import '../theme/app_theme.dart';

/// A resolved image request: the URL to fetch plus any headers it needs.
typedef ImageSource = ({String url, Map<String, String>? headers});

/// Public native art uses the disk cache; headered native art reads live.
/// Web artwork uses Flutter's image providers and scoped decoded cache.
ImageProvider cachedImageProvider(
  ImageSource source, {
  bool isWeb = kIsWeb,
  String? cacheScope,
}) {
  if (isWeb) {
    final provider = NetworkImage(
      source.url,
      headers: source.headers,
      webHtmlElementStrategy: usesHtmlImageElement(source, isWeb: isWeb)
          ? WebHtmlElementStrategy.prefer
          : WebHtmlElementStrategy.never,
    );
    return cacheScope != null && (source.headers?.isNotEmpty ?? false)
        ? SessionNetworkImage(provider, cacheScope)
        : provider;
  }
  if (source.headers?.isNotEmpty == true) {
    if (cacheScope == null || cacheScope == _signedOutScope) {
      throw StateError('Authenticated artwork requires an active session');
    }
    return FreshNetworkImage(NetworkImage(source.url, headers: source.headers));
  }
  return CachedNetworkImageProvider(
    source.url,
    cacheManager: appImageCache,
    imageRenderMethodForWeb: ImageRenderMethodForWeb.HtmlImage,
  );
}

/// Each native authenticated read gets a fresh decoded-image key and uses
/// NetworkImage directly, so neither Flutter nor the disk cache can replay
/// protected bytes after a server-side grant or policy change.
@visibleForTesting
class FreshNetworkImage extends ImageProvider<Object> {
  final NetworkImage networkImage;
  final Object _key = Object();

  FreshNetworkImage(this.networkImage);

  @override
  Future<Object> obtainKey(ImageConfiguration configuration) =>
      SynchronousFuture<Object>(_key);

  @override
  ImageStreamCompleter loadImage(Object key, ImageDecoderCallback decode) =>
      networkImage.loadImage(networkImage, decode);
}

/// Bind a headered image to the live session. A retained widget may hold an
/// expired bearer; replace it only when the URL is under this same backend.
/// After a server switch, no header is sent to the old server.
@visibleForTesting
ImageSource? currentSessionImageSource(ImageSource source, AuthState? auth) {
  if (source.headers?.isNotEmpty != true) return source;
  final conn = auth?.connection;
  if (auth?.isAuthenticated != true || conn == null) return null;
  final base = conn.serverUrl.endsWith('/')
      ? conn.serverUrl.substring(0, conn.serverUrl.length - 1)
      : conn.serverUrl;
  if (!source.url.startsWith('$base/api/')) return null;
  final bearer = source.headers!.entries
      .where((entry) => entry.key.toLowerCase() == 'authorization')
      .firstOrNull
      ?.value;
  if (bearer == null) return source;
  if (bearer == 'Bearer ${conn.accessToken}') return source;
  return (
    url: source.url,
    headers: {
      for (final entry in source.headers!.entries)
        entry.key: entry.key.toLowerCase() == 'authorization'
            ? 'Bearer ${conn.accessToken}'
            : entry.value,
    },
  );
}

/// Web authenticated images share a decoded frame across token rotation and
/// separate known account, assignment and content-policy contexts. Native
/// authenticated images always fetch fresh. The digest keeps account details
/// out of cache keys, and public CDN images keep their ordinary URL key.
///
/// Every poster build asks for this, so it is computed once per [AuthState];
/// a changed session is always a new instance.
String imageCacheScope(AuthState? auth) {
  if (auth == null) return _signedOutScope;
  return _scopes[auth] ??= _computeScope(auth);
}

final _scopes = Expando<String>('imageCacheScope');
final _signedOutScope = _computeScope(null);

String _computeScope(AuthState? auth) {
  final permissions = [...?auth?.user?.permissions]..sort();
  final instances = [
    for (final i in auth?.connection?.instances ?? [])
      '${i.serviceType}:${i.id}:${i.isDefault}:${i.assigned}:'
          '${i.requestDefault}:${i.mediaDownloads}',
  ]..sort();
  final context = jsonEncode([
    auth?.connection?.serverUrl,
    auth?.user?.id,
    auth?.user?.role,
    permissions,
    auth?.user?.child,
    auth?.user?.contentLimits?.toJson(),
    instances,
  ]);
  return sha256.convert(utf8.encode(context)).toString();
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
    listener = ImageStreamListener(
      (image, synchronous) {
        image.dispose();
        completer.removeListener(listener);
      },
      onError: (Object error, StackTrace? stack) {
        PaintingBinding.instance.imageCache.evict(key);
        completer.removeListener(listener);
      },
    );
    completer.addListener(listener);
    return completer;
  }

  // Image must resolve again when headers rotate: a failed old-token fetch
  // needs a retry. A successfully decoded frame still hits the stable key.
  @override
  bool operator ==(Object other) =>
      other is SessionNetworkImage &&
      other.scope == scope &&
      other.networkImage == networkImage &&
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
  bool operator ==(Object other) =>
      other is _SessionImageKey &&
      other.scope == scope &&
      other.image.url == image.url &&
      other.image.scale == image.scale &&
      other.image.webHtmlElementStrategy == image.webHtmlElementStrategy &&
      mapEquals(other._nonAuthHeaders, _nonAuthHeaders);

  @override
  int get hashCode => Object.hash(
    scope,
    image.url,
    image.scale,
    image.webHtmlElementStrategy,
    Object.hashAllUnordered(
      _nonAuthHeaders.entries.map((h) => Object.hash(h.key, h.value)),
    ),
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
    if (kIsWeb || source.headers?.isNotEmpty == true) {
      // Flutter owns the decoded image and listener lifecycle on web. The
      // plugin's multi-image completer decodes again when listeners reconnect,
      // which can dispose the browser image still used by a cached frame.
      return Image(
        image: cachedImageProvider(source, cacheScope: cacheScope),
        // CanvasKit can lose medium-quality mipmaps when a texture is recreated
        // under cache pressure. Unrelated hover repaints then change the artwork.
        // Bicubic sampling stays consistent across those texture cache states.
        filterQuality: FilterQuality.high,
        fit: fit,
        width: width,
        height: height,
        frameBuilder: (_, child, frame, synchronouslyLoaded) =>
            synchronouslyLoaded || frame != null ? child : _fallback(),
        errorBuilder: (_, __, ___) => _fallback(),
      );
    }
    return CachedNetworkImage(
      imageUrl: source.url,
      cacheManager: appImageCache,
      // Public native artwork shares the disk cache with its prefetch.
      // Web and authenticated native art returned through Image above.
      imageRenderMethodForWeb: ImageRenderMethodForWeb.HtmlImage,
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
    if (!kIsWeb && (headers?.isNotEmpty != true)) {
      return _image((url: src, headers: headers));
    }
    // Authenticated images on both platforms need the live session. A widget
    // left over from the previous account must not fetch with its old bearer
    // and write the result under the new account's cache scope.
    return Consumer(
      builder: (context, ref, _) {
        final auth = ref.watch(authProvider).valueOrNull;
        final conn = auth?.connection;
        final current = currentSessionImageSource(
            (url: src, headers: headers), auth);
        if (current == null) return _fallback();
        return _image(
          resolveImageSource(
            url: current.url,
            headers: current.headers,
            serverUrl: conn?.serverUrl,
            accessToken: conn?.accessToken,
          ),
          cacheScope: imageCacheScope(auth),
        );
      },
    );
  }
}
