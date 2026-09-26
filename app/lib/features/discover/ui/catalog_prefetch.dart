import 'dart:async';
import 'dart:collection';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/widgets/cached_image.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/music_discovery_service.dart';
import '../logic/discovery_access.dart';
import '../logic/music_browse_query.dart';
import '../logic/music_feed_provider.dart';

/// Holds only the opening feeds while Discovery is mounted. No library/status
/// calls, genre-feed fan-out, or next-page requests happen until a row opens.
final catalogOpeningArtworkProvider =
    Provider.autoDispose<List<ImageSource>>((ref) {
  final access = ref.watch(discoveryAccessProvider);
  final sources = <ImageSource>[];
  final music = access.activeId('lidarr');
  if (access.showMusic && access.canBrowse('lidarr', music)) {
    ref.watch(musicGenresProvider(music));
    for (final name in ['popular', 'new-releases']) {
      final feed = ref.watch(
          musicFeedProvider(MusicBrowseQuery(feed: name, instanceId: music)));
      sources.addAll(feed.items
          .take(6)
          .map((a) => musicArtworkSourceFor(access.connection, a, music))
          .whereType<ImageSource>());
    }
  }
  return sources;
});

class CatalogWarmup extends ConsumerWidget {
  final Widget child;
  const CatalogWarmup({super.key, required this.child});
  @override
  Widget build(BuildContext context, WidgetRef ref) => CatalogArtworkPrefetch(
      sources: ref.watch(catalogOpeningArtworkProvider), child: child);
}

/// A single account-scoped queue bounds speculative image work across rows.
/// Visible CachedImages share its cache keys and can join in-flight loads.
final catalogArtworkLoaderProvider = Provider<
    Future<void> Function(
        ImageSource source, BuildContext context, Size size)>((ref) => (source, context, size) =>
    precacheImage(cachedImageProvider(source,
        displaySize: size, devicePixelRatio: MediaQuery.devicePixelRatioOf(context),
        cacheScope: imageCacheScope(ref.read(authProvider).valueOrNull)),
        context, onError: (_, __) {}));

final _artworkQueueProvider = Provider.autoDispose((ref) {
  ref.watch(catalogDiscoveryScopeProvider);
  final queue = _ArtworkQueue(ref.watch(catalogArtworkLoaderProvider));
  ref.onDispose(queue.dispose);
  return queue;
});

class CatalogArtworkPrefetch extends ConsumerWidget {
  final List<ImageSource> sources;
  final Widget child;
  final double? artworkWidth;
  const CatalogArtworkPrefetch(
      {super.key, required this.sources, required this.child, this.artworkWidth});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final queue = ref.watch(_artworkQueueProvider);
    final viewport = MediaQuery.sizeOf(context).width;
    final width = artworkWidth ?? (viewport >= 900 ? 124.0 : viewport >= 600 ? 116.0 : 108.0);
    // MediaCard's one-pixel border surrounds the image on both sides.
    final imageSize = Size.square((width - 2).clamp(1, double.infinity));
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (context.mounted) queue.add(sources, context, imageSize);
    });
    return child;
  }
}

class _ArtworkQueue {
  final Future<void> Function(ImageSource source, BuildContext context, Size size) load;
  final _pending = Queue<({ImageSource source, BuildContext context, Size size})>();
  final _seen = <(String, Size, double)>{};
  int _active = 0;
  bool _disposed = false;

  _ArtworkQueue(this.load);

  void add(List<ImageSource> sources, BuildContext context, Size size) {
    if (_disposed) return;
    for (final source in sources) {
      if (_pending.length >= 24) break;
      if (!_seen.add((source.url, size, MediaQuery.devicePixelRatioOf(context)))) continue;
      if (_seen.length > 96) _seen.remove(_seen.first);
      _pending.add((source: source, context: context, size: size));
    }
    _drain();
  }

  void _drain() {
    while (!_disposed && _active < 2 && _pending.isNotEmpty) {
      final job = _pending.removeFirst();
      if (!job.context.mounted) continue;
      _active++;
      unawaited(load(job.source, job.context, job.size)
          .catchError((Object _) {})
          .whenComplete(() {
        _active--;
        _drain();
      }));
    }
  }

  void dispose() {
    _disposed = true;
    _pending.clear();
    _seen.clear();
  }
}
