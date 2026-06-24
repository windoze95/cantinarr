import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../sonarr/logic/import_doctor.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';

/// Opens the Import Doctor for a stuck Chaptarr download. Returns `true` when a
/// fix was applied so the caller can refresh its queue.
Future<bool?> showChaptarrImportDoctorSheet(
  BuildContext context, {
  required ChaptarrApiService service,
  required ChaptarrQueueItem item,
}) {
  return showModalBottomSheet<bool>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    builder: (_) => _ChaptarrImportDoctorSheet(service: service, item: item),
  );
}

/// The "Import Doctor": explains why a download is stuck (full transparency,
/// including the raw Chaptarr messages) and offers one-click fixes. Destructive
/// actions (remove / blocklist / hand-off) ask for confirmation first.
class _ChaptarrImportDoctorSheet extends StatefulWidget {
  final ChaptarrApiService service;
  final ChaptarrQueueItem item;

  const _ChaptarrImportDoctorSheet({required this.service, required this.item});

  @override
  State<_ChaptarrImportDoctorSheet> createState() =>
      _ChaptarrImportDoctorSheetState();
}

class _ChaptarrImportDoctorSheetState
    extends State<_ChaptarrImportDoctorSheet> {
  ChaptarrApiService get _service => widget.service;
  bool _busy = false;

  void _toast(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  Future<bool> _confirm(
      String title, String message, String confirmLabel) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: Text(title),
        content: Text(message,
            style: const TextStyle(color: AppTheme.textSecondary)),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Cancel')),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: TextButton.styleFrom(foregroundColor: AppTheme.error),
            child: Text(confirmLabel),
          ),
        ],
      ),
    );
    return ok ?? false;
  }

  /// Runs a fix and, on success, closes the sheet returning `true` so the
  /// caller refreshes.
  Future<void> _run(
      Future<void> Function() action, String successMessage) async {
    setState(() => _busy = true);
    try {
      await action();
      if (!mounted) return;
      _toast(successMessage);
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      _toast('Failed: $e');
    }
  }

  Future<void> _onAction(DoctorAction action) async {
    final item = widget.item;
    switch (action) {
      case DoctorAction.process:
        await _run(
            _service.processMonitoredDownloads, 'Running the import pass…');
      case DoctorAction.rescan:
        if (item.authorId == null) {
          _toast('No author to rescan.');
          return;
        }
        await _run(() => _service.rescanAuthor(item.authorId!),
            'Rescanning the author…');
      case DoctorAction.manualImport:
        await _runManualImport(force: false);
      case DoctorAction.forceImport:
        await _runManualImport(force: true);
      case DoctorAction.remove:
        if (!await _confirm(
            'Remove download',
            'This deletes the download from the client. The release is not blocklisted, so it could be grabbed again.',
            'Remove')) {
          return;
        }
        await _run(
            () => _service.deleteQueueItem(item.id), 'Removed from the queue.');
      case DoctorAction.blocklistSearch:
        if (!await _confirm(
            'Remove, blocklist & search',
            'This deletes the download, blocklists the release so it is not grabbed again, and starts a fresh search for a different one.',
            'Do it')) {
          return;
        }
        await _run(() async {
          await _service.deleteQueueItem(item.id, blocklist: true);
          if (item.bookId != null) {
            await _service.searchBook([item.bookId!]);
          }
        }, 'Blocklisted and searching for a replacement…');
      case DoctorAction.changeCategory:
        if (!await _confirm(
            'Hand off to download client',
            'This stops Chaptarr managing the download and moves it to the post-import category (for tools like Unpackerr). It stays in your client.',
            'Hand off')) {
          return;
        }
        await _run(
            () => _service.deleteQueueItem(item.id,
                removeFromClient: false, changeCategory: true),
            'Handed off to the download client.');
    }
  }

  Future<void> _runManualImport({required bool force}) async {
    final downloadId = widget.item.downloadId;
    if (downloadId == null || downloadId.isEmpty) {
      _toast('This download has no client id yet — nothing to import.');
      return;
    }
    setState(() => _busy = true);
    List<ChaptarrManualImportCandidate> candidates;
    try {
      candidates = await _service.getManualImportCandidates(downloadId);
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      _toast('Could not fetch files: $e');
      return;
    }
    if (!mounted) return;
    setState(() => _busy = false);
    if (candidates.isEmpty) {
      _toast('No importable files found in the download folder.');
      return;
    }

    final importable = candidates
        .where((c) => c.isMapped && (force || !c.hasPermanentRejection))
        .toList();

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => _CandidatesDialog(
        candidates: candidates,
        importableCount: importable.length,
        force: force,
      ),
    );
    if (confirmed != true || !mounted) return;

    if (importable.isEmpty) {
      _toast(force
          ? 'None of the files are matched to a book, so they cannot be imported.'
          : 'No files qualify — use Force import to import despite the warnings.');
      return;
    }

    final files = importable.map((c) => c.toImportFile()).toList();
    final mode = widget.item.protocol == 'torrent' ? 'copy' : 'move';
    await _run(() => _service.executeManualImport(files, importMode: mode),
        'Importing ${files.length} file(s)…');
  }

  @override
  Widget build(BuildContext context) {
    final item = widget.item;
    final diagnosis = diagnoseChaptarrQueueItem(item);
    final (icon, color) = _severityVisual(diagnosis.severity);

    final rawMessages = <String>[
      if (item.errorMessage != null && item.errorMessage!.isNotEmpty)
        item.errorMessage!,
      for (final g in item.statusMessageGroups) ...[
        if (g.messages.isEmpty && g.title.isNotEmpty) g.title,
        ...g.messages,
      ],
    ];

    return Padding(
      padding:
          EdgeInsets.only(bottom: MediaQuery.of(context).viewInsets.bottom),
      child: Container(
        constraints: BoxConstraints(
            maxHeight: MediaQuery.of(context).size.height * 0.85),
        decoration: const BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
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
              const SizedBox(height: 16),
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Icon(icon, color: color, size: 26),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Text(
                      diagnosis.problem.isNotEmpty
                          ? diagnosis.problem
                          : 'Download status',
                      style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 18,
                          fontWeight: FontWeight.bold),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 4),
              Text(item.bookLabel ?? item.title,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 12),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis),
              if (diagnosis.transparency.isNotEmpty) ...[
                const SizedBox(height: 14),
                Text(diagnosis.transparency,
                    style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 14,
                        height: 1.4)),
              ],
              if (rawMessages.isNotEmpty) ...[
                const SizedBox(height: 16),
                const Text('Messages',
                    style: TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 13,
                        fontWeight: FontWeight.w600)),
                const SizedBox(height: 6),
                Container(
                  width: double.infinity,
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: AppTheme.requested.withValues(alpha: 0.08),
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      for (final m in rawMessages)
                        Padding(
                          padding: const EdgeInsets.symmetric(vertical: 2),
                          child: Text('• $m',
                              style: const TextStyle(
                                  color: AppTheme.textSecondary,
                                  fontSize: 12,
                                  height: 1.35)),
                        ),
                    ],
                  ),
                ),
              ],
              const SizedBox(height: 18),
              if (_busy)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 12),
                  child: Center(
                      child: CircularProgressIndicator(color: AppTheme.accent)),
                )
              else if (diagnosis.actions.isEmpty)
                const Text('No automatic fix is needed.',
                    style:
                        TextStyle(color: AppTheme.textSecondary, fontSize: 13))
              else
                ...diagnosis.actions.asMap().entries.map((e) => Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: _ActionButton(
                        meta: _actionMeta(e.value),
                        primary: e.key == 0,
                        onTap: () => _onAction(e.value),
                      ),
                    )),
            ],
          ),
        ),
      ),
    );
  }
}

(IconData, Color) _severityVisual(DoctorSeverity s) => switch (s) {
      DoctorSeverity.error => (Icons.error_outline, AppTheme.error),
      DoctorSeverity.warning => (
          Icons.warning_amber_rounded,
          AppTheme.requested
        ),
      DoctorSeverity.info => (Icons.info_outline, AppTheme.downloading),
      DoctorSeverity.ok => (Icons.check_circle_outline, AppTheme.available),
    };

class _ActionMeta {
  final String label;
  final IconData icon;
  final bool destructive;
  const _ActionMeta(this.label, this.icon, {this.destructive = false});
}

_ActionMeta _actionMeta(DoctorAction action) => switch (action) {
      DoctorAction.process =>
        const _ActionMeta('Process now', Icons.play_arrow_rounded),
      DoctorAction.manualImport =>
        const _ActionMeta('Manual import', Icons.download_done),
      DoctorAction.forceImport => const _ActionMeta('Force import', Icons.bolt),
      DoctorAction.rescan => const _ActionMeta('Rescan files', Icons.refresh),
      DoctorAction.remove =>
        const _ActionMeta('Remove', Icons.delete_outline, destructive: true),
      DoctorAction.blocklistSearch => const _ActionMeta(
          'Remove, block & search', Icons.block,
          destructive: true),
      DoctorAction.changeCategory => const _ActionMeta(
          'Hand off to client', Icons.outbox,
          destructive: true),
    };

class _ActionButton extends StatelessWidget {
  final _ActionMeta meta;
  final bool primary;
  final VoidCallback onTap;

  const _ActionButton(
      {required this.meta, required this.primary, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final color = meta.destructive ? AppTheme.error : AppTheme.accent;
    if (primary && !meta.destructive) {
      return SizedBox(
        width: double.infinity,
        child: ElevatedButton.icon(
          onPressed: onTap,
          icon: Icon(meta.icon, size: 18),
          label: Text(meta.label),
          style: ElevatedButton.styleFrom(
            backgroundColor: AppTheme.accent,
            foregroundColor: AppTheme.background,
            padding: const EdgeInsets.symmetric(vertical: 13),
            shape:
                RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
          ),
        ),
      );
    }
    return SizedBox(
      width: double.infinity,
      child: OutlinedButton.icon(
        onPressed: onTap,
        icon: Icon(meta.icon, size: 18, color: color),
        label: Text(meta.label, style: TextStyle(color: color)),
        style: OutlinedButton.styleFrom(
          side: BorderSide(color: color.withValues(alpha: 0.5)),
          padding: const EdgeInsets.symmetric(vertical: 13),
          shape:
              RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
        ),
      ),
    );
  }
}

/// Shows the importable files (with mappings + rejections) before importing —
/// full transparency about exactly what will land.
class _CandidatesDialog extends StatelessWidget {
  final List<ChaptarrManualImportCandidate> candidates;
  final int importableCount;
  final bool force;

  const _CandidatesDialog({
    required this.candidates,
    required this.importableCount,
    required this.force,
  });

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      backgroundColor: AppTheme.surface,
      title: Text(force ? 'Force import' : 'Manual import'),
      content: SizedBox(
        width: double.maxFinite,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              importableCount == 0
                  ? 'None of these files can be imported as-is:'
                  : 'Will import $importableCount of ${candidates.length} file(s):',
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
            const SizedBox(height: 10),
            Flexible(
              child: ListView(
                shrinkWrap: true,
                children: candidates.map((c) {
                  final willImport =
                      c.isMapped && (force || !c.hasPermanentRejection);
                  return Padding(
                    padding: const EdgeInsets.only(bottom: 10),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            Icon(
                              willImport
                                  ? Icons.check_circle
                                  : Icons.remove_circle_outline,
                              size: 15,
                              color: willImport
                                  ? AppTheme.available
                                  : AppTheme.textSecondary,
                            ),
                            const SizedBox(width: 6),
                            Expanded(
                              child: Text(c.name,
                                  style: const TextStyle(
                                      color: AppTheme.textPrimary,
                                      fontSize: 12),
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis),
                            ),
                          ],
                        ),
                        Padding(
                          padding: const EdgeInsets.only(left: 21, top: 2),
                          child: Text(
                            [
                              c.sizeFormatted,
                              if (c.isMapped) 'matched' else 'no book match',
                            ].join(' • '),
                            style: const TextStyle(
                                color: AppTheme.textSecondary, fontSize: 11),
                          ),
                        ),
                        if (c.rejections.isNotEmpty)
                          Padding(
                            padding: const EdgeInsets.only(left: 21, top: 2),
                            child: Text(
                              c.rejections.map((r) => r.reason).join('; '),
                              style: const TextStyle(
                                  color: AppTheme.requested, fontSize: 11),
                            ),
                          ),
                      ],
                    ),
                  );
                }).toList(),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel')),
        TextButton(
          onPressed:
              importableCount == 0 ? null : () => Navigator.pop(context, true),
          child: Text(force ? 'Force import' : 'Import'),
        ),
      ],
    );
  }
}
