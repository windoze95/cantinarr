import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_service.dart';

/// A bottom sheet showing detailed request status in human-friendly language.
class RequestStatusSheet extends StatelessWidget {
  final String title;
  final RequestStatus status;
  final String? additionalInfo;

  /// Per-season availability (TV only). When present, the "partially available"
  /// copy names the gap (e.g. "Seasons 1-3 ready; Season 4 downloading").
  final List<RequestSeasonStatus> seasons;

  const RequestStatusSheet({
    super.key,
    required this.title,
    required this.status,
    this.additionalInfo,
    this.seasons = const [],
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(24),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Handle
          Container(
            width: 40,
            height: 4,
            decoration: BoxDecoration(
              color: AppTheme.textSecondary,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
          const SizedBox(height: 24),

          // Status icon
          _StatusIcon(status: status),
          const SizedBox(height: 16),

          // Title
          Text(
            title,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 8),

          // Status message
          Text(
            _statusMessage,
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 15),
            textAlign: TextAlign.center,
          ),

          if (additionalInfo != null) ...[
            const SizedBox(height: 12),
            Text(
              additionalInfo!,
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 13),
              textAlign: TextAlign.center,
            ),
          ],

          const SizedBox(height: 24),
        ],
      ),
    );
  }

  String get _statusMessage => switch (status) {
        RequestStatus.unavailable =>
          'This title is not yet on the server. Tap Request to add it!',
        RequestStatus.pending =>
          'Your request is awaiting approval from an administrator. You\'ll be notified once it\'s reviewed.',
        RequestStatus.denied =>
          'An administrator declined this request. You can request it again.',
        RequestStatus.requested =>
          'Your request has been received! The server is searching for this title and will start downloading it soon.',
        RequestStatus.downloading =>
          'Great news! This title is currently downloading and should be available shortly.',
        RequestStatus.available =>
          'This title is ready to watch! Open Plex or Infuse to start streaming.',
        RequestStatus.partial => _partialMessage,
      };

  /// For a partial title, name the gap using the per-season breakdown, e.g.
  /// "Seasons 1-3 ready; Season 4 downloading. You can request the rest."
  /// Falls back to a generic line when no season detail is available.
  String get _partialMessage {
    if (seasons.isEmpty) {
      return 'Some episodes are available. You can request the remaining ones.';
    }
    final ready = <int>[];
    final downloading = <int>[];
    final missing = <int>[];
    for (final s in seasons) {
      switch (s.status) {
        case RequestStatus.available:
          ready.add(s.seasonNumber);
        case RequestStatus.downloading:
        case RequestStatus.requested:
        case RequestStatus.pending:
          downloading.add(s.seasonNumber);
        default:
          missing.add(s.seasonNumber);
      }
    }
    final parts = <String>[];
    if (ready.isNotEmpty) parts.add('${_seasonsPhrase(ready)} ready');
    if (downloading.isNotEmpty) {
      parts.add('${_seasonsPhrase(downloading)} downloading');
    }
    if (parts.isEmpty) {
      return 'Some episodes are available. You can request the remaining ones.';
    }
    final base = '${parts.join('; ')}.';
    return missing.isNotEmpty
        ? '$base You can request the remaining seasons below.'
        : base;
  }

  /// Renders a sorted season-number list as "Season 4" or "Seasons 1-3" /
  /// "Seasons 1, 3, 5", collapsing contiguous runs into ranges.
  String _seasonsPhrase(List<int> numbers) {
    final sorted = [...numbers]..sort();
    final ranges = <String>[];
    var start = sorted.first;
    var prev = sorted.first;
    for (var i = 1; i <= sorted.length; i++) {
      final isBreak = i == sorted.length || sorted[i] != prev + 1;
      if (isBreak) {
        ranges.add(start == prev ? '$start' : '$start-$prev');
        if (i < sorted.length) {
          start = sorted[i];
          prev = sorted[i];
        }
      } else {
        prev = sorted[i];
      }
    }
    final noun = sorted.length == 1 ? 'Season' : 'Seasons';
    return '$noun ${ranges.join(', ')}';
  }
}

class _StatusIcon extends StatelessWidget {
  final RequestStatus status;
  const _StatusIcon({required this.status});

  @override
  Widget build(BuildContext context) {
    final (icon, color) = switch (status) {
      RequestStatus.unavailable => (Icons.add_circle_outline, AppTheme.accent),
      RequestStatus.pending => (Icons.hourglass_empty, AppTheme.requested),
      RequestStatus.denied => (Icons.block, AppTheme.error),
      RequestStatus.requested => (Icons.hourglass_top, AppTheme.requested),
      RequestStatus.downloading => (Icons.downloading, AppTheme.downloading),
      RequestStatus.available => (Icons.check_circle, AppTheme.available),
      RequestStatus.partial => (
          Icons.pie_chart,
          AppTheme.requested
        ),
    };

    return Container(
      width: 64,
      height: 64,
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        shape: BoxShape.circle,
      ),
      child: Icon(icon, color: color, size: 32),
    );
  }
}
