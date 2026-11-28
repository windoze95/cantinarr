import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../l10n/l10n.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import '../logic/reviewed_import.dart';

/// Used for both the Doctor's single download and the season's grouped review.
/// Returns true if the caller's queue/library may have changed.
Future<bool?> showReviewedImport(
  BuildContext context, {
  required String instanceId,
  required SonarrApiService service,
  List<ImportTarget> targets = const [],
  List<ImportAttempt>? results,
  bool force = false,
}) =>
    showDialog<bool>(
      context: context,
      barrierDismissible: false,
      builder: (_) => _ReviewedImportDialog(
          instanceId: instanceId,
          service: service,
          targets: targets,
          results: results,
          force: force),
    );

class _ReviewedImportDialog extends ConsumerStatefulWidget {
  const _ReviewedImportDialog(
      {required this.instanceId,
      required this.service,
      required this.targets,
      required this.results,
      required this.force});
  final String instanceId;
  final SonarrApiService service;
  final List<ImportTarget> targets;
  final List<ImportAttempt>? results;
  final bool force;

  @override
  ConsumerState<_ReviewedImportDialog> createState() =>
      _ReviewedImportDialogState();
}

class _ReviewedImportDialogState extends ConsumerState<_ReviewedImportDialog> {
  late final ImportRecovery _recovery;
  final _candidates = <ImportTarget, List<SonarrManualImportCandidate>>{};
  final _errors = <ImportTarget, String>{};
  final _selected = <SonarrManualImportCandidate, ImportTarget>{};
  List<ImportAttempt>? _results;
  bool _busy = false;
  bool _confirming = false;
  Timer? _timer;
  bool get _authorized =>
      mounted &&
      ref.read(authProvider).valueOrNull?.user?.isAdmin == true &&
      identical(_recovery, ref.read(importRecoveryProvider(widget.instanceId)));

  @override
  void initState() {
    super.initState();
    _recovery = ref.read(importRecoveryProvider(widget.instanceId));
    _results = widget.results;
    if (_results == null) _load();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    _busy = true;
    for (final target in widget.targets) {
      if (!_authorized) return;
      if (target.downloadId.isEmpty ||
          target.mode == null ||
          !target.rows.every((r) => offersImport(r, force: widget.force))) {
        continue;
      }
      try {
        _candidates[target] =
            await widget.service.getManualImportCandidates(target.downloadId);
      } catch (e) {
        _errors[target] = e.toString();
      }
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _submit() async {
    if (_busy || _selected.isEmpty || _results != null || !_authorized) return;
    setState(() {
      _busy = true;
      _confirming = widget.force;
    });
    if (widget.force) {
      final confirmed = await showDialog<bool>(
          context: context,
          builder: (ctx) => AlertDialog(
                  title: Text(ctx.l10n.sonarrConfirmOverride),
                  scrollable: true,
                  content: Text(_selected.keys.single.path),
                  actions: [
                    TextButton(
                        onPressed: () => Navigator.pop(ctx, false),
                        child: Text(ctx.l10n.adminCancel)),
                    TextButton(
                        onPressed: () => Navigator.pop(ctx, true),
                        child: Text(ctx.l10n.adminForceImport)),
                  ]));
      if (!mounted) return;
      _confirming = false;
      if (confirmed != true || !_authorized) {
        setState(() => _busy = false);
        return;
      }
    }
    final selection = <ImportTarget, List<SonarrManualImportCandidate>>{};
    for (final entry in _selected.entries) {
      selection.putIfAbsent(entry.value, () => []).add(entry.key);
    }
    final future = _recovery.submit(widget.service, selection,
        force: widget.force, authorized: () => _authorized);
    setState(() => _results = _recovery.attempts
        .where((a) =>
            a.phase == ImportPhase.preparing && selection.containsKey(a.target))
        .toList());
    final results = await future;
    if (!mounted) return;
    setState(() {
      _results = results;
      _busy = false;
    });
    _schedule(5);
  }

  void _schedule(int remaining) {
    final pending = _results!
        .where((a) =>
            a.submitted &&
            a.phase != ImportPhase.verified &&
            !a.commandFailed &&
            !a.readFailed)
        .toList();
    if (pending.isEmpty) return;
    if (remaining == 0) {
      setState(() {
        for (final a in pending) {
          a.timedOut = true;
        }
      });
      return;
    }
    _timer = Timer(const Duration(seconds: 2), () async {
      for (final a in pending) {
        if (!_authorized) return;
        await _recovery.verify(widget.service, a);
      }
      if (mounted) _schedule(remaining - 1);
    });
  }

  Future<void> _refresh() async {
    if (_busy || !_authorized) return;
    _timer?.cancel();
    setState(() => _busy = true);
    for (final a in _results!) {
      if (!_authorized) return;
      a.timedOut = false;
      if (a.phase != ImportPhase.verified) {
        await _recovery.verify(widget.service, a);
      }
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final recovery = ref.watch(importRecoveryProvider(widget.instanceId));
    final strings = context.l10n;
    final results = _results;
    final authorized = identical(recovery, _recovery) &&
        ref.watch(authProvider).valueOrNull?.user?.isAdmin == true;
    final done = !_busy || !authorized;
    return PopScope(
        canPop: done,
        child: AlertDialog(
          title: Text(results != null
              ? strings.sonarrResults
              : widget.force
                  ? strings.adminForceImport
                  : strings.sonarrReviewFiles),
          content: SizedBox(
              width: double.maxFinite,
              child: ListView(
                shrinkWrap: true,
                children: [
                  if (authorized) ...[
                    if (_busy && !_confirming) const LinearProgressIndicator(),
                    if (results != null) ...[
                      if (results.isEmpty) Text(strings.sonarrNotSubmitted),
                      for (final a in results) _Result(attempt: a),
                    ] else ...[
                      Text(widget.force
                          ? strings.sonarrForceOne
                          : strings.sonarrChooseFiles),
                      for (final target in widget.targets) ...[
                        _DownloadTitle(target: target),
                        if (target.downloadId.isEmpty)
                          Text(strings.sonarrManualImportNeedsId)
                        else if (_errors.containsKey(target))
                          Text(
                              strings.adminCouldNotFetchFiles(_errors[target]!))
                        else if (!_busy && !_candidates.containsKey(target))
                          Text(strings.sonarrIneligible)
                        else if (_candidates[target]?.isEmpty == true)
                          Text(
                              strings.adminNoImportableFilesFoundInTheDownload),
                        for (final file in _candidates[target] ??
                            <SonarrManualImportCandidate>[])
                          _choice(target, file),
                      ],
                    ],
                  ],
                ],
              )),
          actions: [
            TextButton(
                onPressed: done
                    ? () => Navigator.pop(
                        context, results?.any((a) => a.submitted) ?? false)
                    : null,
                child: Text(
                    results == null ? strings.adminCancel : strings.adminDone)),
            if (authorized && results == null)
              TextButton(
                  onPressed: !_busy && _selected.isNotEmpty ? _submit : null,
                  child: Text(widget.force
                      ? strings.adminForceImport
                      : strings.adminImport)),
            if (authorized &&
                results != null &&
                results
                    .any((a) => a.submitted && a.phase != ImportPhase.verified))
              TextButton(
                  onPressed: !_busy && !results.any((a) => a.checking)
                      ? _refresh
                      : null,
                  child: Text(strings.sonarrCheckAgain)),
          ],
        ));
  }

  Widget _choice(ImportTarget target, SonarrManualImportCandidate file) {
    final checked = _selected.containsKey(file);
    final eligible = target.accepts(file, force: widget.force);
    final conflict = _recovery.conflicts(target, file) ||
        _selected.keys.any((other) =>
            other != file &&
            (widget.force ||
                other.path == file.path ||
                other.episodeIds.any(file.episodeIds.contains)));
    return CheckboxListTile(
      contentPadding: EdgeInsets.zero,
      title: Text(file.path),
      subtitle: Text([
        file.sizeFormatted,
        file.episodeLabels.isEmpty
            ? context.l10n.adminNoEpisodeMatch
            : file.episodeLabels.join(', '),
        ...file.rejections.map((r) => r.reason),
        if (!eligible)
          context.l10n.sonarrIneligible
        else if (conflict)
          context.l10n.sonarrConflict,
      ].join(' · ')),
      value: checked,
      onChanged: !_busy && eligible && !conflict
          ? (value) => setState(() {
                if (value == true) {
                  _selected[file] = target;
                } else {
                  _selected.remove(file);
                }
              })
          : null,
    );
  }
}

class _DownloadTitle extends StatelessWidget {
  const _DownloadTitle({required this.target});
  final ImportTarget target;
  @override
  Widget build(BuildContext context) => Padding(
      padding: const EdgeInsets.only(top: 12),
      child: Text(
          [
            target.item.title,
            '#${target.item.id}',
            if (target.mode == 'copy') context.l10n.sonarrCopy,
            if (target.mode == 'move') context.l10n.sonarrMove,
          ].join(' · '),
          style: Theme.of(context).textTheme.titleSmall));
}

class _Result extends StatelessWidget {
  const _Result({required this.attempt});
  final ImportAttempt attempt;
  @override
  Widget build(BuildContext context) {
    final t = context.l10n;
    final a = attempt;
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      _DownloadTitle(target: a.target),
      Text(switch (a.phase) {
        ImportPhase.preparing => t.sonarrPreparing,
        ImportPhase.notSubmitted => t.sonarrNotSubmitted,
        ImportPhase.processing => t.sonarrProcessing,
        ImportPhase.verified => t.sonarrVerified,
        ImportPhase.partial => t.sonarrPartial,
        ImportPhase.failed => t.sonarrCommandFailed,
        ImportPhase.unverified => t.sonarrUnverified,
      }),
      if (a.commandFailed && a.phase != ImportPhase.failed)
        Text(t.sonarrCommandFailed),
      if (a.readFailed) Text(t.sonarrVerificationUnavailable),
      if (a.submitted &&
          a.commandId == null &&
          a.phase == ImportPhase.unverified)
        Text(t.sonarrUnknownReceipt),
      if (a.timedOut) Text(t.sonarrVerificationTimeout),
      for (final file in a.files)
        ListTile(
            contentPadding: EdgeInsets.zero,
            title: Text(file.path),
            subtitle: Text([
              file.episodeLabels.join(', '),
              t.sonarrVerifiedEpisodes(
                  a.verifiedCount(file), file.episodeIds.length)
            ].join(' · '))),
    ]);
  }
}
