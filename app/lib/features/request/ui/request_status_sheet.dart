import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
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
    return AppSheet(
      padding: const EdgeInsets.fromLTRB(
        AppTheme.spaceXl,
        AppTheme.spaceSm,
        AppTheme.spaceXl,
        AppTheme.spaceXl,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
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
            _statusMessage(context),
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 15),
            textAlign: TextAlign.center,
          ),

          if (additionalInfo != null) ...[
            const SizedBox(height: 12),
            Text(
              additionalInfo!,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              textAlign: TextAlign.center,
            ),
          ],
        ],
      ),
    );
  }

  String _statusMessage(BuildContext context) {
    final blocked = seasons.where((s) => s.hasRequestIssue).toList();
    if (blocked.isNotEmpty) {
      final scope =
          _seasonsPhrase(context, blocked.map((s) => s.seasonNumber).toList());
      final next = seasons.any((s) => s.isRequestable)
          ? context.l10n.browseYouCanRequestTheOtherSeasonsBelow
          : context.l10n.browseNoSeasonsCanCurrentlyBeRequested;
      return context.l10n
          .requestBlockedSeasonNotes(scope, blocked.length, next);
    }
    return switch (status) {
      RequestStatus.unavailable =>
        context.l10n.browseThisTitleIsNotYetOnTheServerTapRequestToAddIt,
      RequestStatus.pending => context.l10n
          .browseYourRequestIsAwaitingApprovalFromAnAdministratorYouLlBeNotifiedOnceItSReviewed,
      RequestStatus.denied => context
          .l10n.browseAnAdministratorDeclinedThisRequestYouCanRequestItAgain,
      RequestStatus.requested => context.l10n
          .browseYourRequestHasBeenReceivedTheServerIsSearchingForThisTitleAndWillStartDownloadingItSoon,
      RequestStatus.downloading => context.l10n
          .browseGreatNewsThisTitleIsCurrentlyDownloadingAndShouldBeAvailableShortly,
      RequestStatus.available => context
          .l10n.browseThisTitleIsReadyToWatchOpenPlexOrInfuseToStartStreaming,
      RequestStatus.partial => _partialMessage(context),
    };
  }

  /// For a partial title, name the gap using the per-season breakdown, e.g.
  /// "Seasons 1-3 ready; Season 4 downloading. You can request the rest."
  /// Falls back to a generic line when no season detail is available.
  String _partialMessage(BuildContext context) {
    if (seasons.isEmpty) {
      return context
          .l10n.browseSomeEpisodesAreAvailableYouCanRequestTheRemainingOnes;
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
    if (ready.isNotEmpty)
      parts.add(
          context.l10n.requestSeasonsReady(_seasonsPhrase(context, ready)));
    if (downloading.isNotEmpty) {
      parts.add(context.l10n
          .requestSeasonsDownloading(_seasonsPhrase(context, downloading)));
    }
    if (parts.isEmpty) {
      return context
          .l10n.browseSomeEpisodesAreAvailableYouCanRequestTheRemainingOnes;
    }
    final base = '${parts.join('; ')}.';
    return missing.isNotEmpty
        ? context.l10n.requestRemainingSeasons(base)
        : base;
  }

  /// Renders a sorted season-number list as "Season 4" or "Seasons 1-3" /
  /// "Seasons 1, 3, 5", collapsing contiguous runs into ranges.
  String _seasonsPhrase(BuildContext context, List<int> numbers) {
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
    final noun = sorted.length == 1
        ? context.l10n.browseSeason
        : context.l10n.browseSeasons;
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
      RequestStatus.partial => (Icons.pie_chart, AppTheme.requested),
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
