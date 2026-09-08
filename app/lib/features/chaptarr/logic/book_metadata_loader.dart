import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../discover/logic/discovery_access.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';

typedef BookMetadataKey = ({String instanceId, String foreignId});
typedef BookMetadataFetch = Future<List<ChaptarrBook>> Function(
    BookMetadataKey key, CancelToken cancelToken);

class BookMetadataResult {
  final List<ChaptarrBook> books;
  final bool failed;
  final bool cancelled;
  const BookMetadataResult(this.books,
      {this.failed = false, this.cancelled = false});
}

/// Lives across search-overlay disposal and detail navigation, but never
/// across an account/access change. Tokens deliberately are not cache keys.
final bookMetadataLoaderProvider = Provider<BookMetadataLoader>((ref) {
  ref.watch(catalogDiscoveryScopeProvider);
  final dio = ref.watch(backendClientProvider);
  final access = ref.read(discoveryAccessProvider);
  final loader = BookMetadataLoader((key, token) {
    if (access.user == null ||
        !access.hasInstance('chaptarr', key.instanceId)) {
      throw StateError('Book instance unavailable');
    }
    return ChaptarrApiService(backendDio: dio, instanceId: key.instanceId)
        .lookupBook(key.foreignId,
            cancelToken: token,
            strictResponse: true,
            timeout: BookMetadataLoader.deadline);
  });
  ref.onDispose(loader.dispose);
  return loader;
});

/// A small metadata-only cache and queue. Cached records must never supply
/// availability, request status or file actions. One speculative call leaves
/// a second socket available for a book the reader actually opens.
class BookMetadataLoader {
  static const deadline = Duration(seconds: 30);
  static const freshness = Duration(minutes: 5);
  static const capacity = 32;
  final BookMetadataFetch fetch;
  final DateTime Function() now;
  final _cache = <BookMetadataKey, _CachedMetadata>{};
  final _jobs = <BookMetadataKey, _MetadataJob>{};
  final _pending = <_MetadataJob>[];
  final _active = <_MetadataJob>{};
  Object? _prefetchOwner;
  bool _disposed = false;

  BookMetadataLoader(this.fetch, {DateTime Function()? now})
      : now = now ?? DateTime.now;

  BookMetadataResult? peek(BookMetadataKey key) {
    final cached = _cache.remove(key);
    if (cached == null || _disposed) return null;
    if (!now().isBefore(cached.expires)) return null;
    _cache[key] = cached;
    return cached.result;
  }

  Future<BookMetadataResult> load(BookMetadataKey key,
      {bool foreground = true, bool retry = false}) {
    if (_disposed || key.foreignId.trim().isEmpty) {
      return Future.value(const BookMetadataResult([], cancelled: true));
    }
    final existing = _jobs[key];
    if (existing != null) {
      if (foreground) existing.foreground = true;
      if (_pending.contains(existing)) _drain();
      return existing.completion.future;
    }
    final cached = peek(key);
    if (!retry && cached != null) return Future.value(cached);
    final job = _MetadataJob(key, foreground);
    _jobs[key] = job;
    _pending.add(job);
    _drain();
    return job.completion.future;
  }

  /// Replace only queued speculation. Active work can still be useful on
  /// navigation; its completion updates its own cache entry, never search.
  void prefetch(Object owner, Iterable<BookMetadataKey> visible) {
    if (_disposed) return;
    _clearPending();
    _prefetchOwner = owner;
    for (final key in visible.take(3)) {
      unawaited(load(key, foreground: false));
    }
  }

  void clearPrefetch(Object owner) {
    if (!identical(owner, _prefetchOwner)) return;
    _prefetchOwner = null;
    _clearPending();
  }

  void _clearPending() {
    for (final job in _pending.where((job) => !job.foreground).toList()) {
      _pending.remove(job);
      _jobs.remove(job.key);
      job.completion.complete(const BookMetadataResult([], cancelled: true));
    }
  }

  void _drain() {
    while (!_disposed && _active.length < 2 && _pending.isNotEmpty) {
      final foreground = _pending.where((job) => job.foreground).firstOrNull;
      final job = foreground ??
          (_active.any((job) => !job.foreground) ? null : _pending.first);
      if (job == null) return;
      _pending.remove(job);
      _active.add(job);
      unawaited(_run(job));
    }
  }

  Future<void> _run(_MetadataJob job) async {
    BookMetadataResult result;
    try {
      // Include connect time in the deadline and release timers on disposal,
      // even when an upstream/test adapter ignores Dio cancellation.
      final books = await Future.any([
        Future.sync(() => fetch(job.key, job.token)),
        job.token.whenCancel.then<List<ChaptarrBook>>(
            (_) => throw StateError('Book metadata cancelled')),
      ]).timeout(deadline, onTimeout: () {
        job.token.cancel('Book details timed out');
        throw TimeoutException('Book details timed out');
      });
      result = BookMetadataResult(List.unmodifiable(books));
    } catch (_) {
      result = const BookMetadataResult([], failed: true);
    }
    if (!_disposed) {
      _cache.remove(job.key);
      _cache[job.key] = _CachedMetadata(result,
          now().add(result.failed ? const Duration(seconds: 30) : freshness));
      while (_cache.length > capacity) {
        _cache.remove(_cache.keys.first);
      }
    }
    _active.remove(job);
    _jobs.remove(job.key);
    if (!job.completion.isCompleted) job.completion.complete(result);
    _drain();
  }

  void dispose() {
    _disposed = true;
    _cache.clear();
    for (final job in _jobs.values) {
      job.token.cancel('Book metadata scope changed');
      if (!job.completion.isCompleted) {
        job.completion.complete(const BookMetadataResult([], cancelled: true));
      }
    }
    _pending.clear();
    _jobs.clear();
  }
}

class _CachedMetadata {
  final BookMetadataResult result;
  final DateTime expires;
  const _CachedMetadata(this.result, this.expires);
}

class _MetadataJob {
  final BookMetadataKey key;
  bool foreground;
  final token = CancelToken();
  final completion = Completer<BookMetadataResult>();
  _MetadataJob(this.key, this.foreground);
}
