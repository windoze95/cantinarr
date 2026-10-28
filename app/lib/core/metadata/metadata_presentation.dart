import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../features/discover/logic/discover_session.dart';
import '../../l10n/l10n.dart';
import '../config/app_config.dart';
import '../network/backend_client.dart';
import '../providers/library_refresh_provider.dart';
import '../providers/realtime_provider.dart';
import '../storage/metadata_preferences.dart';

/// Optional display text. Source records remain the authority for identity,
/// matching, edits, and request payloads.
class MetadataPresentation {
  const MetadataPresentation(
      {this.title,
      this.overview,
      this.posterPath,
      this.backdropPath,
      this.episodeTitle});

  final String? title, overview, posterPath, backdropPath, episodeTitle;

  factory MetadataPresentation.fromJson(Map<String, dynamic> json) {
    String? text(String key) {
      final value = json[key];
      return value is String && value.trim().isNotEmpty ? value : null;
    }

    return MetadataPresentation(
        title: text('title'),
        overview: text('overview'),
        posterPath: text('poster_path'),
        backdropPath: text('backdrop_path'),
        episodeTitle: text('episode_title'));
  }

  String? get posterUrl =>
      posterPath == null ? null : AppConfig.tmdbPoster(posterPath, width: 185);
}

/// Native references let the server verify corrected/combined TV identities.
/// A caller must never replace a native TV reference with a guessed TMDB id.
class MetadataReference {
  const MetadataReference.catalog(this.mediaType, this.tmdbId)
      : instanceId = null,
        recordId = null,
        episodeId = null;
  const MetadataReference.native(this.mediaType, this.instanceId, this.recordId,
      {this.episodeId})
      : tmdbId = null;

  final String mediaType;
  final int? tmdbId, recordId, episodeId;
  final String? instanceId;

  String get key => tmdbId != null
      ? '$mediaType:tmdb:$tmdbId'
      : '$mediaType:${Uri.encodeComponent(instanceId ?? '')}:$recordId:${episodeId ?? 0}';
  bool get isValid =>
      const {'movie', 'tv'}.contains(mediaType) &&
      (tmdbId != null
          ? tmdbId! > 0
          : instanceId?.isNotEmpty == true && (recordId ?? 0) > 0);

  Map<String, dynamic> toJson() => {
        'key': key,
        'media_type': mediaType,
        if (tmdbId != null) 'tmdb_id': tmdbId,
        if (instanceId != null) 'instance_id': instanceId,
        if (recordId != null) 'record_id': recordId,
        if (episodeId != null && episodeId! > 0) 'episode_id': episodeId,
      };

  @override
  bool operator ==(Object other) =>
      other is MetadataReference && key == other.key;
  @override
  int get hashCode => key.hashCode;
}

/// A fresh grant/policy failure supersedes previously visible source text.
class MetadataAccessUnavailable implements Exception {
  const MetadataAccessUnavailable({required this.denied});
  final bool denied;
}

MetadataAccessUnavailable? _accessUnavailable(Object error) {
  if (error is! DioException) return null;
  final data = error.response?.data;
  if (const {401, 403, 404}.contains(error.response?.statusCode)) {
    return const MetadataAccessUnavailable(denied: true);
  }
  if (data is Map && data['code'] == 'metadata_access_unavailable') {
    return const MetadataAccessUnavailable(denied: false);
  }
  return null;
}

/// Coalesces requests from lazy visible-row builders into bounded batches.
/// Each instance belongs to one authorization scope and immutable language.
class MetadataPresentationClient {
  MetadataPresentationClient(this.dio, this.language,
      {DateTime Function()? now})
      : _now = now ?? DateTime.now;
  final Dio dio;
  final String? language;
  final DateTime Function() _now;
  final _pending = <MetadataReference, Completer<MetadataPresentation?>>{};
  final _inFlight = <MetadataReference, Future<MetadataPresentation?>>{};
  final _cache =
      <MetadataReference, ({DateTime at, MetadataPresentation? value})>{};
  final _cancel = CancelToken();
  Timer? _timer;
  bool _disposed = false;
  bool _flushing = false;

  Future<MetadataPresentation?> load(MetadataReference reference) {
    if (_disposed || language == null || !reference.isValid) {
      return Future.value();
    }
    final cached = _cache[reference];
    final freshness = reference.tmdbId == null
        ? const Duration(seconds: 30)
        : const Duration(hours: 1);
    if (cached != null && _now().difference(cached.at) < freshness) {
      return Future.value(cached.value);
    }
    if (_inFlight[reference] case final future?) return future;
    final completer = Completer<MetadataPresentation?>();
    _pending[reference] = completer;
    _inFlight[reference] = completer.future;
    _timer ??= Timer(Duration.zero, _flush);
    return completer.future;
  }

  Future<void> _flush() async {
    _timer = null;
    if (_flushing || _disposed) return;
    _flushing = true;
    try {
      while (_pending.isNotEmpty && !_disposed) {
        final batch = _pending.entries.take(50).toList();
        for (final entry in batch) {
          _pending.remove(entry.key);
        }
        try {
          final response = await dio.post('/api/metadata/presentation',
              queryParameters: {'metadata_language': language},
              data: {
                'references': [for (final entry in batch) entry.key.toJson()]
              },
              cancelToken: _cancel);
          final values =
              (response.data as Map?)?['presentations'] as Map? ?? const {};
          for (final entry in batch) {
            final raw = values[entry.key.key];
            final value = !_disposed && raw is Map
                ? MetadataPresentation.fromJson(raw.cast<String, dynamic>())
                : null;
            if (!_disposed && value != null) {
              // Bound a long browsing session without caching whole libraries.
              if (_cache.length >= 512) _cache.remove(_cache.keys.first);
              _cache[entry.key] = (at: _now(), value: value);
            }
            if (!entry.value.isCompleted) entry.value.complete(value);
          }
        } catch (error, stack) {
          // An optional translation outage can retain source text. A fresh
          // denial or unreadable policy cannot reuse the older source read.
          final accessError = _accessUnavailable(error);
          for (final entry in batch) {
            _cache.remove(entry.key);
            if (!entry.value.isCompleted) {
              if (accessError != null) {
                entry.value.completeError(accessError, stack);
              } else {
                entry.value.complete(null);
              }
            }
          }
        } finally {
          for (final entry in batch) {
            _inFlight.remove(entry.key);
          }
        }
      }
    } finally {
      _flushing = false;
    }
  }

  void dispose() {
    _disposed = true;
    _timer?.cancel();
    _cancel.cancel();
    for (final pending in _pending.values) {
      if (!pending.isCompleted) pending.complete(null);
    }
    _pending.clear();
    _cache.clear();
  }
}

final metadataPresentationClientProvider =
    Provider<MetadataPresentationClient>((ref) {
  ref.watch(discoverSessionProvider);
  ref.watch(libraryRefreshTickProvider);
  final language = ref.watch(effectiveMetadataLanguageProvider);
  if (language != null) ref.watch(libraryChangedEventsProvider);
  final client =
      MetadataPresentationClient(ref.watch(backendClientProvider), language);
  ref.onDispose(client.dispose);
  return client;
});

final metadataPresentationProvider = FutureProvider.autoDispose
    .family<MetadataPresentation?, MetadataReference>((ref, reference) {
  final client = ref.watch(metadataPresentationClientProvider);
  if (reference.tmdbId == null &&
      reference.isValid &&
      client.language != null) {
    // Native identities can be corrected on another device without a local
    // tick. Only mounted native rows poll; catalog translation remains cached.
    final refresh = Timer(const Duration(seconds: 30), ref.invalidateSelf);
    ref.onDispose(refresh.cancel);
  }
  return client.load(reference);
});

/// Starts work only when the row builder is mounted, never for an entire
/// provider library. Language/account changes select a fresh provider future.
class MetadataPresentationBuilder extends ConsumerWidget {
  const MetadataPresentationBuilder(
      {super.key, required this.reference, required this.builder});
  final MetadataReference? reference;
  final Widget Function(MetadataPresentation?) builder;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final value = reference == null
        ? null
        : ref.watch(metadataPresentationProvider(reference!));
    final error = value?.error;
    if (error is MetadataAccessUnavailable) {
      if (error.denied) return const SizedBox.shrink();
      return TextButton(
        onPressed: value?.isLoading == true
            ? null
            : () => ref.invalidate(metadataPresentationProvider(reference!)),
        child: Text(context.l10n.browseCouldnTCheckRetry,
            textAlign: TextAlign.center,
            maxLines: 3,
            overflow: TextOverflow.ellipsis),
      );
    }
    // AsyncValue can retain the previous future's data during a dependency
    // reload. Do not display an earlier language while the new one is loading.
    return builder(value != null && (!value.isLoading || value.isRefreshing)
        ? value.valueOrNull
        : null);
  }
}
