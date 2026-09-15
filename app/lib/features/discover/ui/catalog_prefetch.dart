import 'dart:async';
import 'dart:collection';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/widgets/cached_image.dart';
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
        ImageSource source, BuildContext context)>((ref) => (source, context) =>
    precacheImage(cachedImageProvider(source), context, onError: (_, __) {}));

final _artworkQueueProvider = Provider.autoDispose((ref) {
  ref.watch(catalogDiscoveryScopeProvider);
  final queue = _ArtworkQueue(ref.watch(catalogArtworkLoaderProvider));
  ref.onDispose(queue.dispose);
  return queue;
});

class CatalogArtworkPrefetch extends ConsumerWidget {
  final List<ImageSource> sources;
  final Widget child;
  const CatalogArtworkPrefetch(
      {super.key, required this.sources, required this.child});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final queue = ref.watch(_artworkQueueProvider);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (context.mounted) queue.add(sources, context);
    });
    return child;
  }
}

class _ArtworkQueue {
  final Future<void> Function(ImageSource source, BuildContext context) load;
  final _pending = Queue<({ImageSource source, BuildContext context})>();
  final _seen = <String>{};
  int _active = 0;
  bool _disposed = false;

  _ArtworkQueue(this.load);

  void add(List<ImageSource> sources, BuildContext context) {
    if (_disposed) return;
    for (final source in sources) {
      if (_pending.length >= 24) break;
      if (!_seen.add(source.url)) continue;
      if (_seen.length > 96) _seen.remove(_seen.first);
      _pending.add((source: source, context: context));
    }
    _drain();
  }

  void _drain() {
    while (!_disposed && _active < 2 && _pending.isNotEmpty) {
      final job = _pending.removeFirst();
      if (!job.context.mounted) continue;
      _active++;
      unawaited(load(job.source, job.context)
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
