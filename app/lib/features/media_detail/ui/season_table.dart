import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../discover/data/tmdb_models.dart';
import '../../media_download/data/media_download_models.dart';
import '../../media_download/ui/media_download_button.dart';
import '../../request/data/request_service.dart';
import '../../request/logic/request_provider.dart';
import '../logic/season_label.dart';

/// Interactive per-season request table (Overseerr-style): one row per season
/// with a checkbox, "Season N" (with its first-air year when TMDB knows one),
/// an "x/y eps" availability count, and a status badge. Available, requested,
/// downloading and pending seasons are checked + disabled. All / First / Latest
/// bulk-select. Submitting sends the chosen season numbers to the request
/// service.
///
/// The table reads live per-season status from [notifier] and drives its
/// submit through it, so it stays in sync with the request button above it.
class SeasonTable extends StatefulWidget {
  /// TMDB seasons for the title (canonical season list + names). Specials
  /// (season 0) are filtered out, matching the rest of the app.
  final List<Season> seasons;
  final RequestNotifier notifier;
  final String? title;
  final int? tvdbId;

  /// Whether the current user may pick specific seasons (the server's
  /// can_choose_season option). When false the table is status-only — no
  /// checkboxes, chips, or submit button — because the server ignores an
  /// explicit season list from a user who isn't allowed to choose, which would
  /// make the picker a silent no-op.
  final bool canRequest;

  /// Called after a season request is accepted, so the caller can nudge
  /// stale-by-design surfaces (the shell's search-chip snapshot).
  final VoidCallback? onRequested;

  /// Exact episode files currently present in Sonarr, grouped per season.
  /// A season with multiple files opens a picker so one browser gesture starts
  /// one download instead of attempting a blocked batch launch.
  final String? downloadInstanceId;
  final Map<int, List<MediaDownloadChoice>> downloadChoicesBySeason;

  const SeasonTable({
    super.key,
    required this.seasons,
    required this.notifier,
    this.title,
    this.tvdbId,
    this.canRequest = true,
    this.onRequested,
    this.downloadInstanceId,
    this.downloadChoicesBySeason =
        const <int, List<MediaDownloadChoice>>{},
  });

  @override
  State<SeasonTable> createState() => _SeasonTableState();
}

class _SeasonTableState extends State<SeasonTable> {
  /// Only actionable seasons belong to the selection, never accepted work.
  final Set<int> _selected = {};
  bool _submitting = false;
  String? _selectionInstanceId;

  @override
  void initState() {
    super.initState();
    _selectionInstanceId = widget.notifier.instanceId;
    widget.notifier.addListener(_syncSelection);
  }

  @override
  void didUpdateWidget(covariant SeasonTable oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.notifier != widget.notifier) {
      oldWidget.notifier.removeListener(_syncSelection);
      widget.notifier.addListener(_syncSelection);
      _selected.clear();
    }
    _syncSelection();
  }

  @override
  void dispose() {
    widget.notifier.removeListener(_syncSelection);
    super.dispose();
  }

  // ListenableBuilder rebuilds after this listener reconciles the selection.
  void _syncSelection() {
    if (_selectionInstanceId != widget.notifier.instanceId) {
      _selectionInstanceId = widget.notifier.instanceId;
      _selected.clear();
    }
    _selected.retainAll(_selectableNumbers);
  }

  bool get _busy => _submitting ||
      widget.notifier.state.isRequesting ||
      widget.notifier.state.isCheckingStatus ||
      !widget.notifier.state.hasStatus;

  List<Season> get _realSeasons =>
      widget.seasons.where((s) => s.seasonNumber > 0).toList()
        ..sort((a, b) => a.seasonNumber.compareTo(b.seasonNumber));

  /// Live per-season status keyed by season number, from the backend.
  Map<int, RequestSeasonStatus> get _statusBySeason => {
        for (final s in widget.notifier.state.seasons) s.seasonNumber: s,
      };

  RequestSeasonStatus _statusFor(int seasonNumber) {
    final state = widget.notifier.state;
    final season = _statusBySeason[seasonNumber];
    // A title-wide pending badge must not erase an explicit mapping/status
    // problem on one native library season.
    if (season?.hasRequestIssue ?? false) return season!;
    // Pending approval covers the title; the API may omit its season rows.
    if (state.status == RequestStatus.pending) {
      return RequestSeasonStatus(
          seasonNumber: seasonNumber, status: RequestStatus.pending);
    }
    return season ?? RequestSeasonStatus(
      seasonNumber: seasonNumber,
      // A title without a breakdown must not turn accepted work into missing
      // seasons. With a breakdown, a newly announced season may still be new.
      status: state.seasons.isEmpty && state.status != RequestStatus.partial
          ? state.status
          : RequestStatus.unavailable,
    );
  }

  bool _canRequestSeason(int seasonNumber) =>
      widget.canRequest && _statusFor(seasonNumber).isRequestable;

  void _toggle(int seasonNumber, bool? value) {
    if (_busy || !_canRequestSeason(seasonNumber)) return;
    setState(() {
      if (value ?? false) {
        _selected.add(seasonNumber);
      } else {
        _selected.remove(seasonNumber);
      }
    });
  }

  /// Selectable season numbers, shared by every selection and submit path.
  List<int> get _selectableNumbers => _realSeasons
      .map((s) => s.seasonNumber)
      .where(_canRequestSeason)
      .toList();

  void _selectAll() {
    if (_busy) return;
    setState(() => _selected
      ..clear()
      ..addAll(_selectableNumbers));
  }

  void _selectFirst() {
    if (_busy) return;
    final first =
        _selectableNumbers.isNotEmpty ? _selectableNumbers.first : null;
    setState(() {
      _selected.clear();
      if (first != null) _selected.add(first);
    });
  }

  void _selectLatest() {
    if (_busy) return;
    final latest =
        _selectableNumbers.isNotEmpty ? _selectableNumbers.last : null;
    setState(() {
      _selected.clear();
      if (latest != null) _selected.add(latest);
    });
  }

  Future<void> _submit() async {
    if (_busy) return;
    _syncSelection();
    if (_selected.isEmpty) return;
    final seasons = _selected.toList()..sort();
    final notifier = widget.notifier;
    final instanceId = notifier.instanceId;
    setState(() => _submitting = true);
    try {
      final accepted = await notifier.request(
        title: widget.title,
        tvdbId: widget.tvdbId,
        seasons: seasons,
      );
      if (!mounted || notifier != widget.notifier ||
          instanceId != notifier.instanceId) {
        return;
      }
      if (!accepted) {
        final quotaMessage = notifier.state.quotaMessage;
        if (quotaMessage != null) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text(quotaMessage)),
          );
        }
        return;
      }
      widget.onRequested?.call();
      setState(() => _selected.clear());
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: widget.notifier,
      builder: (context, _) {
        final seasons = _realSeasons;
        final hasSelectable =
            widget.notifier.state.hasStatus && _selectableNumbers.isNotEmpty;
        return Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (hasSelectable) ...[
                Wrap(
                  spacing: 8,
                  children: [
                    _QuickChip(label: 'All', onTap: _busy ? null : _selectAll),
                    _QuickChip(label: 'First', onTap: _busy ? null : _selectFirst),
                    _QuickChip(label: 'Latest', onTap: _busy ? null : _selectLatest),
                  ],
                ),
                const SizedBox(height: 8),
              ],
              Container(
                decoration: BoxDecoration(
                  color: AppTheme.surface,
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(color: AppTheme.border),
                ),
                child: Column(
                  children: [
                    for (var i = 0; i < seasons.length; i++) ...[
                      if (i > 0)
                        const Divider(height: 1, color: AppTheme.border),
                      _SeasonRow(
                        season: seasons[i],
                        status: _statusFor(seasons[i].seasonNumber),
                        selected: _selected.contains(seasons[i].seasonNumber),
                        showCheckbox: widget.canRequest,
                        enabled: !_busy &&
                            _canRequestSeason(seasons[i].seasonNumber),
                        onChanged: (v) => _toggle(seasons[i].seasonNumber, v),
                        downloadInstanceId: widget.downloadInstanceId,
                        downloadChoices: widget.downloadChoicesBySeason[
                                seasons[i].seasonNumber] ??
                            const [],
                      ),
                    ],
                  ],
                ),
              ),
              if (hasSelectable) ...[
                const SizedBox(height: 12),
                SizedBox(
                  width: double.infinity,
                  child: ElevatedButton.icon(
                    onPressed: (_selected.isEmpty || _busy)
                        ? null
                        : _submit,
                    icon: _busy
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(
                                strokeWidth: 2, color: AppTheme.onAccent),
                          )
                        : const Icon(Icons.add, size: 18),
                    label: Text(
                      _selected.isEmpty
                          ? 'Select seasons to request'
                          : 'Request ${_selected.length} '
                              'season${_selected.length == 1 ? '' : 's'}',
                    ),
                    style: ElevatedButton.styleFrom(
                      backgroundColor: AppTheme.accent,
                      foregroundColor: AppTheme.onAccent,
                      disabledBackgroundColor:
                          AppTheme.accent.withValues(alpha: 0.3),
                      disabledForegroundColor: AppTheme.onAccent,
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(12),
                      ),
                    ),
                  ),
                ),
              ],
            ],
          ),
        );
      },
    );
  }
}

class _SeasonRow extends StatelessWidget {
  final Season season;
  final RequestSeasonStatus status;
  final bool selected;
  final bool showCheckbox;
  final bool enabled;
  final ValueChanged<bool?> onChanged;
  final String? downloadInstanceId;
  final List<MediaDownloadChoice> downloadChoices;

  const _SeasonRow({
    required this.season,
    required this.status,
    required this.selected,
    required this.showCheckbox,
    required this.enabled,
    required this.onChanged,
    required this.downloadInstanceId,
    required this.downloadChoices,
  });

  @override
  Widget build(BuildContext context) {
    final accepted = status.isKnown && switch (status.status) {
      RequestStatus.available || RequestStatus.requested ||
      RequestStatus.downloading || RequestStatus.pending => true,
      _ => false,
    };
    final checked = accepted || selected;
    return InkWell(
      onTap: enabled ? () => onChanged(!selected) : null,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        child: Row(
          children: [
            if (showCheckbox)
              Checkbox(
                value: checked,
                onChanged: enabled ? onChanged : null,
                activeColor: AppTheme.accent,
                checkColor: AppTheme.onAccent,
                side: const BorderSide(color: AppTheme.textSecondary),
              )
            else
              const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    seasonRowLabel(season),
                    style: const TextStyle(
                        color: AppTheme.textPrimary, fontSize: 14),
                  ),
                  if (status.episodeCount > 0)
                    Text(
                      '${status.episodesLabel} eps',
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12),
                    )
                  else if (season.episodeCount != null)
                    Text(
                      '${season.episodeCount} eps',
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12),
                    ),
                  if (status.hasRequestIssue)
                    Padding(
                      padding: const EdgeInsets.only(top: 4, right: 8),
                      child: Text(status.requestBlockedMessage ??
                          'Could not verify this season. Retry before requesting.',
                        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
                    ),
                ],
              ),
            ),
            _SeasonStatusBadge(status: status.isKnown ? status.status : null),
            if (downloadInstanceId != null && downloadChoices.isNotEmpty) ...[
              const SizedBox(width: 4),
              MediaDownloadChoiceButton(
                instanceId: downloadInstanceId!,
                choices: downloadChoices,
                label: 'Download Season ${season.seasonNumber} episodes',
                sheetTitle: 'Download Season ${season.seasonNumber}',
                iconOnly: true,
              ),
            ],
          ],
        ),
      ),
    );
  }
}

/// A compact status pill for a season, e.g. "Available" / "Downloading".
class _SeasonStatusBadge extends StatelessWidget {
  final RequestStatus? status;
  const _SeasonStatusBadge({required this.status});

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (status) {
      null => ('Unknown', AppTheme.textSecondary),
      RequestStatus.available => ('Available', AppTheme.available),
      RequestStatus.partial => ('Partial', AppTheme.requested),
      RequestStatus.downloading => ('Downloading', AppTheme.downloading),
      RequestStatus.requested => ('Requested', AppTheme.requested),
      RequestStatus.pending => ('Pending', AppTheme.requested),
      _ => ('Not added', AppTheme.unavailable),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text(
        label,
        style:
            TextStyle(color: color, fontSize: 11, fontWeight: FontWeight.w600),
      ),
    );
  }
}

class _QuickChip extends StatelessWidget {
  final String label;
  final VoidCallback? onTap;
  const _QuickChip({required this.label, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return ActionChip(
      label: Text(label),
      onPressed: onTap,
      backgroundColor: AppTheme.surfaceVariant,
      labelStyle: const TextStyle(color: AppTheme.textPrimary, fontSize: 13),
      side: const BorderSide(color: AppTheme.border),
    );
  }
}
