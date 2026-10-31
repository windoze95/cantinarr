import 'package:flutter_cache_manager/flutter_cache_manager.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Shared native disk cache for public artwork. Authenticated artwork uses
/// fresh network reads and never enters this cache.
///
/// Disk cache: up to [_maxObjects] files, evicted after [_stalePeriod].
final BaseCacheManager appImageCache = CacheManager(
  Config(
    'cantinarrImageCache',
    stalePeriod: _stalePeriod,
    maxNrOfCacheObjects: _maxObjects,
  ),
);

const Duration _stalePeriod = Duration(days: 30);
const int _maxObjects = 1000;

/// Older releases stored authenticated art under URL-only keys in this cache.
/// Clear those files before the app displays anything on its first upgraded
/// launch. Keep the marker unset on failure so the next launch retries.
Future<void> purgeLegacyImageCache() async {
  const marker = 'cantinarr_private_image_cache_purged_v1';
  final prefs = await SharedPreferences.getInstance();
  if (prefs.getBool(marker) == true) return;
  await appImageCache.emptyCache();
  await prefs.setBool(marker, true);
}
