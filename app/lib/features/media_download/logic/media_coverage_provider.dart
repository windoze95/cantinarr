import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/media_download_service.dart';

/// Per-(instance, arr-reported path) download coverage verdicts, resolved
/// lazily in per-instance batches.
///
/// `false` means the server confirmed no media path mapping covers the path,
/// so an affordance would always fail. Anything unknown — not yet resolved,
/// transport failure, or a server predating the coverage endpoint — stays
/// absent and callers fail open: ticket issuance remains the authority.
class MediaCoverageNotifier extends Notifier<Map<String, bool>> {
  /// Paths queued per instance for the next batch flush.
  final Map<String, Set<String>> _pending = {};

  /// Keys queued or currently being checked, so one path is asked once.
  final Set<String> _inFlight = {};

  /// Instances whose last batch failed. Left unresolved for this session
  /// instead of hammering a server that cannot answer.
  final Set<String> _failedInstances = {};

  @override
  Map<String, bool> build() => const {};

  static String keyFor(String instanceId, String path) =>
      '$instanceId\u0000$path';

  /// Queues a verdict fetch when none is cached. Safe to call during widget
  /// build: provider state never changes synchronously from here.
  void ensure(String instanceId, String? reportedPath) {
    final path = reportedPath?.trim() ?? '';
    if (instanceId.isEmpty ||
        path.isEmpty ||
        _failedInstances.contains(instanceId)) {
      return;
    }
    final key = keyFor(instanceId, path);
    if (state.containsKey(key) || _inFlight.contains(key)) return;
    _inFlight.add(key);
    final queue = _pending.putIfAbsent(instanceId, () => <String>{});
    final flushScheduled = queue.isNotEmpty;
    queue.add(path);
    // One microtask per instance batch: every ensure() from the same build
    // frame lands in a single request.
    if (!flushScheduled) {
      scheduleMicrotask(() => _flush(instanceId));
    }
  }

  Future<void> _flush(String instanceId) async {
    final paths =
        (_pending.remove(instanceId) ?? const <String>{}).toList(growable: false);
    if (paths.isEmpty) return;
    // Nudge listeners before awaiting so a frame stays scheduled while the
    // request is in flight; otherwise a check kicked off by the last frame of
    // a quiet screen resolves after every pump has stopped (widget-test
    // settles would strand the request mid-flight at teardown).
    state = Map.of(state);
    try {
      final covered = await ref
          .read(mediaDownloadServiceProvider)
          .checkCoverage(instanceId: instanceId, paths: paths);
      state = {
        ...state,
        for (var i = 0; i < paths.length; i++)
          keyFor(instanceId, paths[i]): covered[i],
      };
    } catch (_) {
      _failedInstances.add(instanceId);
      for (final path in paths) {
        _inFlight.remove(keyFor(instanceId, path));
      }
    }
  }
}

final mediaCoverageProvider =
    NotifierProvider<MediaCoverageNotifier, Map<String, bool>>(
  MediaCoverageNotifier.new,
);
