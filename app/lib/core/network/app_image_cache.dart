import 'package:flutter_cache_manager/flutter_cache_manager.dart';

/// One shared image cache for the whole app. Posters, covers, author and person
/// photos all live here so a poster fetched on the dashboard is reused on a
/// detail screen, and the cache is bounded sensibly for an image-heavy media app
/// (the per-widget default cache manager only holds ~200 objects).
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
