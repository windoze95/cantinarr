import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/theme/app_theme.dart';
import '../data/issue_models.dart';
import '../logic/issues_provider.dart';

/// The media scope a problem report is filed against. Mirrors the request
/// payload's identifier set: bound to one exact arr instance and media item,
/// optionally narrowed to a TV season/episode.
class ReportScope {
  final String instanceId;
  final String mediaType; // 'movie' | 'tv'
  final int tmdbId;
  final int? tvdbId;
  final int? seasonNumber;
  final int? episodeNumber;
  final String? title;

  const ReportScope({
    required this.instanceId,
    required this.mediaType,
    required this.tmdbId,
    this.tvdbId,
    this.seasonNumber,
    this.episodeNumber,
    this.title,
  });

  const ReportScope.movie({
    required String instanceId,
    required int tmdbId,
    String? title,
  }) : this(
          instanceId: instanceId,
          mediaType: 'movie',
          tmdbId: tmdbId,
          title: title,
        );

  const ReportScope.episode({
    required String instanceId,
    required int tmdbId,
    int? tvdbId,
    required int seasonNumber,
    required int episodeNumber,
    String? title,
  }) : this(
          instanceId: instanceId,
          mediaType: 'tv',
          tmdbId: tmdbId,
          tvdbId: tvdbId,
          seasonNumber: seasonNumber,
          episodeNumber: episodeNumber,
          title: title,
        );

  const ReportScope.series({
    required String instanceId,
    required int tmdbId,
    int? tvdbId,
    int? seasonNumber,
    String? title,
  }) : this(
          instanceId: instanceId,
          mediaType: 'tv',
          tmdbId: tmdbId,
          tvdbId: tvdbId,
          seasonNumber: seasonNumber,
          title: title,
        );
}

/// A shared "Report a problem" affordance: an outlined button that opens the
/// [ReportProblemSheet] for [scope]. Place on any media surface where a
/// reporter might flag a wrong/bad download; gate the caller on
/// `allow_reporting`.
///
/// On a successful submit it shows a confirmation SnackBar and, if
/// [onSubmitted] is provided (the arr screens), invokes it so the parent can
/// refresh.
class ReportProblemButton extends StatelessWidget {
  final ReportScope scope;
  final VoidCallback? onSubmitted;

  const ReportProblemButton({
    super.key,
    required this.scope,
    this.onSubmitted,
  });

  @override
  Widget build(BuildContext context) {
    return OutlinedButton.icon(
      onPressed: () => showReportProblemSheet(
        context,
        scope: scope,
        onSubmitted: onSubmitted,
      ),
      icon: const Icon(Icons.flag_outlined,
          size: 18, color: AppTheme.textSecondary),
      label: const Text('Report a problem',
          style: TextStyle(color: AppTheme.textPrimary)),
      style: OutlinedButton.styleFrom(
        side: const BorderSide(color: AppTheme.border),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
        padding: const EdgeInsets.symmetric(vertical: 12),
      ),
    );
  }
}

/// Opens the report sheet and submits the result. Shared by the button above
/// and by callers that present the affordance some other way (e.g. a quiet
/// inline link).
Future<void> showReportProblemSheet(
  BuildContext context, {
  required ReportScope scope,
  VoidCallback? onSubmitted,
}) async {
  final submitted = await showModalBottomSheet<bool>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    builder: (_) => ReportProblemSheet(scope: scope),
  );
  if (submitted == true) {
    onSubmitted?.call();
  }
}

/// Pre-submit sheet that lets a reporter classify a problem with a media item
/// (and add an optional reason). Modeled on `RequestOptionsSheet`: a [Wrap] of
/// category [ChoiceChip]s plus a reason [TextField] (required only for
/// "Something else"), with Cancel/Submit. Pops `true` on a successful submit.
class ReportProblemSheet extends ConsumerStatefulWidget {
  final ReportScope scope;

  const ReportProblemSheet({super.key, required this.scope});

  @override
  ConsumerState<ReportProblemSheet> createState() => _ReportProblemSheetState();
}

class _ReportProblemSheetState extends ConsumerState<ReportProblemSheet> {
  IssueCategory _category = IssueCategory.wrongContent;
  final _reasonController = TextEditingController();
  bool _submitting = false;
  String? _error;

  @override
  void dispose() {
    _reasonController.dispose();
    super.dispose();
  }

  bool get _reasonRequired => _category.requiresReason;

  Future<void> _submit() async {
    if (_submitting) return;
    final reason = _reasonController.text.trim();
    if (_reasonRequired && reason.isEmpty) {
      setState(() => _error = 'Please describe the problem.');
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    final scope = widget.scope;
    try {
      final result = await ref.read(issuesServiceProvider).reportProblem(
            instanceId: scope.instanceId,
            mediaType: scope.mediaType,
            tmdbId: scope.tmdbId,
            tvdbId: scope.tvdbId,
            seasonNumber: scope.seasonNumber,
            episodeNumber: scope.episodeNumber,
            category: _category,
            reason: reason.isEmpty ? null : reason,
            title: scope.title,
          );
      if (!mounted) return;
      Navigator.of(context).pop(true);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_successMessage(result.status))),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = _friendlyError(e);
      });
    }
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Could not send your report.';
  }

  String _successMessage(IssueStatus status) => switch (status) {
        IssueStatus.observing ||
        IssueStatus.recovering =>
          'Thanks — we’re tracking this quietly and will step in if it still '
              'needs help.',
        _ => "Thanks — we're looking into it",
      };

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: EdgeInsets.only(
        left: 24,
        right: 24,
        top: 16,
        bottom: 24 + MediaQuery.of(context).viewInsets.bottom,
      ),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Center(
            child: Container(
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: AppTheme.textSecondary,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          const SizedBox(height: 20),
          const Text(
            'Report a problem',
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
          ),
          const SizedBox(height: 16),
          const _SectionLabel("What's wrong?"),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: IssueCategory.values
                .map((c) => ChoiceChip(
                      label: Text(c.label),
                      selected: _category == c,
                      onSelected: (_) => setState(() {
                        _category = c;
                        _error = null;
                      }),
                      showCheckmark: false,
                      selectedColor: AppTheme.accent,
                      backgroundColor: AppTheme.surfaceVariant,
                      labelStyle: TextStyle(
                        color: _category == c
                            ? AppTheme.onAccent
                            : AppTheme.textPrimary,
                        fontSize: 13,
                      ),
                      side: const BorderSide(color: AppTheme.border),
                    ))
                .toList(),
          ),
          const SizedBox(height: 16),
          _SectionLabel(_reasonRequired ? 'Details' : 'Details (optional)'),
          const SizedBox(height: 8),
          TextField(
            controller: _reasonController,
            minLines: 2,
            maxLines: 4,
            style: const TextStyle(color: AppTheme.textPrimary),
            onChanged: (_) {
              if (_error != null) setState(() => _error = null);
            },
            decoration: InputDecoration(
              hintText: _reasonRequired
                  ? 'Tell us what happened'
                  : 'Add anything that helps (optional)',
              hintStyle: const TextStyle(color: AppTheme.textSecondary),
              contentPadding:
                  const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(12),
              ),
            ),
          ),
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text(
              _error!,
              style: const TextStyle(color: AppTheme.error, fontSize: 13),
            ),
          ],
          const SizedBox(height: 16),
          Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed:
                      _submitting ? null : () => Navigator.of(context).pop(),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppTheme.textPrimary,
                    side: const BorderSide(color: AppTheme.border),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                  child: const Text('Cancel'),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: ElevatedButton(
                  onPressed: _submitting ? null : _submit,
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppTheme.accent,
                    foregroundColor: AppTheme.onAccent,
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                  child: _submitting
                      ? const SizedBox(
                          width: 20,
                          height: 20,
                          child: CircularProgressIndicator(
                            color: AppTheme.onAccent,
                            strokeWidth: 2,
                          ),
                        )
                      : const Text('Submit'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _SectionLabel extends StatelessWidget {
  final String text;
  const _SectionLabel(this.text);

  @override
  Widget build(BuildContext context) => Text(
        text,
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 13,
          fontWeight: FontWeight.w600,
        ),
      );
}
