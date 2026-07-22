import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../discover/data/tmdb_models.dart';
import '../../media_download/data/media_download_models.dart';
import '../../media_download/ui/media_download_button.dart';
import '../../request/data/request_service.dart';
import '../../request/logic/request_provider.dart';

/// Interactive per-season request table (Overseerr-style): one row per season
/// with a checkbox, "Season N", an "x/y eps" availability count, and a status
/// badge. Already-available seasons are shown checked + disabled. Quick
/// All / First / Latest chips bulk-select. Submitting sends the chosen season
/// numbers to the request service.
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
  /// Season numbers the user has selected to request (excludes already-available
  /// seasons, which are implicitly "checked" but not actionable).
  final Set<int> _selected = {};

  List<Season> get _realSeasons =>
      widget.seasons.where((s) => s.seasonNumber > 0).toList()
        ..sort((a, b) => a.seasonNumber.compareTo(b.seasonNumber));

  /// Live per-season status keyed by season number, from the backend.
  Map<int, RequestSeasonStatus> get _statusBySeason => {
        for (final s in widget.notifier.state.seasons) s.seasonNumber: s,
      };

  bool _isAvailable(int seasonNumber) =>
      _statusBySeason[seasonNumber]?.isAvailable ?? false;

  void _toggle(int seasonNumber, bool? value) {
    if (_isAvailable(seasonNumber)) return; // can't re-request an available one
    setState(() {
      if (value ?? false) {
        _selected.add(seasonNumber);
      } else {
        _selected.remove(seasonNumber);
      }
    });
  }

  /// Selectable (not-yet-available) season numbers.
  List<int> get _selectableNumbers => _realSeasons
      .map((s) => s.seasonNumber)
      .where((n) => !_isAvailable(n))
      .toList();

  void _selectAll() => setState(() => _selected
    ..clear()
    ..addAll(_selectableNumbers));

  void _selectFirst() {
    final first =
        _selectableNumbers.isNotEmpty ? _selectableNumbers.first : null;
    setState(() {
      _selected.clear();
      if (first != null) _selected.add(first);
    });
  }

  void _selectLatest() {
    final latest =
        _selectableNumbers.isNotEmpty ? _selectableNumbers.last : null;
    setState(() {
      _selected.clear();
      if (latest != null) _selected.add(latest);
    });
  }

  Future<void> _submit() async {
    if (_selected.isEmpty) return;
    final seasons = _selected.toList()..sort();
    await widget.notifier.request(
      title: widget.title,
      tvdbId: widget.tvdbId,
      seasons: seasons,
    );
    if (!mounted) return;
    if (widget.notifier.state.error == null) widget.onRequested?.call();
    // Clear the local selection; the request button + badges reflect the new
    // state from the refreshed status.
    setState(() => _selected.clear());
    await widget.notifier.checkStatus();
  }

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: widget.notifier,
      builder: (context, _) {
        final seasons = _realSeasons;
        final hasSelectable =
            widget.canRequest && _selectableNumbers.isNotEmpty;
        return Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (hasSelectable) ...[
                Wrap(
                  spacing: 8,
                  children: [
                    _QuickChip(label: 'All', onTap: _selectAll),
                    _QuickChip(label: 'First', onTap: _selectFirst),
                    _QuickChip(label: 'Latest', onTap: _selectLatest),
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
                        status: _statusBySeason[seasons[i].seasonNumber],
                        selected: _selected.contains(seasons[i].seasonNumber),
                        available: _isAvailable(seasons[i].seasonNumber),
                        selectable: widget.canRequest,
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
                    onPressed: (_selected.isEmpty ||
                            widget.notifier.state.isRequesting)
                        ? null
                        : _submit,
                    icon: widget.notifier.state.isRequesting
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
  final RequestSeasonStatus? status;
  final bool selected;
  final bool available;
  final bool selectable;
  final ValueChanged<bool?> onChanged;
  final String? downloadInstanceId;
  final List<MediaDownloadChoice> downloadChoices;

  const _SeasonRow({
    required this.season,
    required this.status,
    required this.selected,
    required this.available,
    required this.selectable,
    required this.onChanged,
    required this.downloadInstanceId,
    required this.downloadChoices,
  });

  @override
  Widget build(BuildContext context) {
    // An available season is shown checked + disabled; otherwise it follows the
    // user's selection.
    final checked = available || selected;
    return InkWell(
      onTap: (!selectable || available) ? null : () => onChanged(!selected),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        child: Row(
          children: [
            if (selectable)
              Checkbox(
                value: checked,
                onChanged: available ? null : onChanged,
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
                    season.name ?? 'Season ${season.seasonNumber}',
                    style: const TextStyle(
                        color: AppTheme.textPrimary, fontSize: 14),
                  ),
                  if (status != null && status!.episodeCount > 0)
                    Text(
                      '${status!.episodesLabel} eps',
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12),
                    )
                  else if (season.episodeCount != null)
                    Text(
                      '${season.episodeCount} eps',
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12),
                    ),
                ],
              ),
            ),
            _SeasonStatusBadge(status: status?.status),
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
  final VoidCallback onTap;
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
