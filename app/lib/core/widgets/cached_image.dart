import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';

import '../network/app_image_cache.dart';
import '../theme/app_theme.dart';

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

  @override
  Widget build(BuildContext context) {
    final src = url;
    if (src == null || src.isEmpty) return _fallback();
    return CachedNetworkImage(
      imageUrl: src,
      httpHeaders: headers,
      cacheManager: appImageCache,
      fit: fit,
      width: width,
      height: height,
      fadeInDuration: const Duration(milliseconds: 200),
      placeholder: (_, __) => Container(
        width: width,
        height: height,
        color: AppTheme.surfaceVariant,
      ),
      errorWidget: (_, __, ___) => _fallback(),
    );
  }
}
