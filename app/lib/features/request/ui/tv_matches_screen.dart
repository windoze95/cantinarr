import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/tv_match_service.dart';

class TVMatchesScreen extends ConsumerStatefulWidget {
  const TVMatchesScreen({super.key});
  @override
  ConsumerState<TVMatchesScreen> createState() => _TVMatchesScreenState();
}

class _TVMatchesScreenState extends ConsumerState<TVMatchesScreen> {
  final _id = TextEditingController();
  List<TVMatch>? _matches;
  String? _error;
  @override
  void initState() { super.initState(); WidgetsBinding.instance.addPostFrameCallback((_) => _load()); }
  @override
  void dispose() { _id.dispose(); super.dispose(); }
  Future<void> _load() async {
    if (!mounted || !ref.read(tvMatchesAllowedProvider)) return;
    try {
      final matches = await ref.read(tvMatchServiceProvider).list();
      if (mounted) setState(() { _matches = matches; _error = null; });
    } catch (e) { if (mounted) setState(() => _error = tvMatchError(e)); }
  }
  Future<void> _open(int id) async {
    await context.push('/settings/tv-matches/$id');
    if (mounted) await _load();
  }
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('TV matches')),
    body: CenteredContent(child: !ref.watch(tvMatchesAllowedProvider)
      ? const Center(child: Text('TV corrections are not available for this account and server.'))
      : ListView(padding: const EdgeInsets.all(16), children: [
        const Text('Match a TMDB title to a Sonarr series and its seasons. Bundled corrections apply automatically; local corrections take precedence.'),
        const SizedBox(height: 16),
        TextField(controller: _id, keyboardType: TextInputType.number,
          decoration: const InputDecoration(labelText: 'TMDB TV ID'), onSubmitted: (_) {
            final id = int.tryParse(_id.text.trim()); if (id != null && id > 0) _open(id);
          }),
        Align(alignment: Alignment.centerLeft, child: TextButton.icon(
          onPressed: () { final id = int.tryParse(_id.text.trim());
            if (id != null && id > 0) { _open(id); } else { setState(() => _error = 'Enter a valid TMDB TV ID.'); } },
          icon: const Icon(Icons.add), label: const Text('Correct a TV match'))),
        if (_error != null) _ErrorRetry(message: _error!, retry: _load),
        if (_matches == null && _error == null) const Center(child: CircularProgressIndicator()),
        for (final match in _matches ?? <TVMatch>[]) Card(child: ListTile(
          title: Text(match.title.isEmpty ? 'TMDB ${match.tmdbId}' : match.title),
          subtitle: Text('${match.originLabel}${match.state == 'paused' ? ' · Paused' : ''}\n'
            '${tvSeasonMappingLabel(match.seasonMap)}'),
          trailing: const Icon(Icons.chevron_right), onTap: () => _open(match.tmdbId))),
      ])),
  );
}

class TVMatchEditorScreen extends ConsumerStatefulWidget {
  final int tmdbId;
  final String? instanceId;
  const TVMatchEditorScreen({super.key, required this.tmdbId, this.instanceId});
  @override
  ConsumerState<TVMatchEditorScreen> createState() => _TVMatchEditorScreenState();
}

class _TVMatchEditorScreenState extends ConsumerState<TVMatchEditorScreen> {
  final _query = TextEditingController();
  TVMatchView? _view;
  List<TVMatchCandidate>? _candidates;
  TVMatchCandidate? _selected;
  List<TVRepairPreview>? _repairs;
  final _seasons = <int, int>{};
  String? _instanceId;
  String? _error;
  String? _repairError;
  bool _busy = false;
  bool _dirty = false;
  int _generation = 0;
  int _selectionRevision = 0;
  @override
  void initState() { super.initState(); _instanceId = widget.instanceId;
    WidgetsBinding.instance.addPostFrameCallback((_) => _load()); }
  @override
  void dispose() { _query.dispose(); super.dispose(); }
  TVMatchService get _service => ref.read(tvMatchServiceProvider);
  Future<void> _load() async {
    if (!mounted || !ref.read(tvMatchesAllowedProvider)) return;
    final generation = ++_generation;
    setState(() { _busy = true; _error = null; });
    try {
      final view = await _service.read(widget.tmdbId, _instanceId);
      if (!mounted || generation != _generation) return;
      setState(() { _view = view; _instanceId = view.instanceId; _selected = null;
        _candidates = null; _seasons.clear(); _dirty = false; });
      await _loadRepairs();
    } catch (e) { if (mounted && generation == _generation) setState(() => _error = tvMatchError(e)); }
    finally { if (mounted && generation == _generation) setState(() => _busy = false); }
  }
  Future<void> _loadRepairs() async {
    try {
      final repairs = await _service.repairs(widget.tmdbId);
      if (mounted) setState(() { _repairs = repairs; _repairError = null; });
    } catch (e) { if (mounted) setState(() => _repairError = tvMatchError(e)); }
  }
  Future<void> _search() async {
    if (_busy || _query.text.trim().isEmpty) return;
    final generation = _generation;
    setState(() { _busy = true; _error = null; _candidates = null; });
    try {
      final matches = await _service.search(_query.text.trim(), _instanceId);
      if (mounted && generation == _generation) setState(() => _candidates = matches);
    } catch (e) { if (mounted && generation == _generation) setState(() => _error = tvMatchError(e)); }
    finally { if (mounted && generation == _generation) setState(() => _busy = false); }
  }
  void _select(TVMatchCandidate candidate) => setState(() {
    _selected = candidate; _seasons.clear(); _dirty = true; _selectionRevision++;
  });
  Future<void> _save(String mode) async {
    final view = _view;
    if (_busy || view == null) return;
    setState(() { _busy = true; _error = null; });
    try {
      final updated = await _service.save(widget.tmdbId, view.match.revision, mode, _instanceId,
        tvdbId: _selected?.tvdbId, seasons: _seasons);
      if (!mounted) return;
      setState(() { _view = updated; _selected = null; _seasons.clear(); _dirty = false; });
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('TV match saved. Existing requests can be reviewed below.')));
      await _loadRepairs();
    } catch (e) { if (mounted) setState(() => _error = tvMatchError(e)); }
    finally { if (mounted) setState(() => _busy = false); }
  }
  Future<void> _repair(TVRepairPreview preview) async {
    final confirmed = await showDialog<bool>(context: context, builder: (context) => AlertDialog(
      title: const Text('Repair TV request?'),
      content: SingleChildScrollView(child: Column(mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text(preview.title), const SizedBox(height: 12),
          Text('Library: ${preview.instanceName}'),
          const SizedBox(height: 12), Text('Recorded target\n${TVRepairPreview.describeTarget(preview.recordedTarget)}'),
          const SizedBox(height: 12), Text('Corrective request\n${TVRepairPreview.describeTarget(preview.intendedTarget)}'),
          const SizedBox(height: 12), Text(preview.message),
          if (preview.requiresApproval) const Padding(padding: EdgeInsets.only(top: 12),
            child: Text('The corrective request will wait for approval.')),
        ])),
      actions: [TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
        FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('Create corrective request'))],
    ));
    if (confirmed != true || !mounted) return;
    setState(() { _busy = true; _error = null; });
    try { await _service.repair(preview); await _loadRepairs(); }
    catch (e) { if (mounted) setState(() => _error = tvMatchError(e)); }
    finally { if (mounted) setState(() => _busy = false); }
  }
  @override
  Widget build(BuildContext context) {
    final allowed = ref.watch(tvMatchesAllowedProvider);
    final view = _view;
    final libraries = ref.watch(authProvider).valueOrNull?.connection?.sonarrInstances ?? [];
    final complete = view != null && view.sourceSeasons.isNotEmpty && _selected != null &&
      view.sourceSeasons.keys.every(_seasons.containsKey) && _seasons.values.toSet().length == _seasons.length;
    return UnsavedChangesGuard(hasChanges: () => _dirty, isSaving: _busy, child: Scaffold(
      appBar: AppBar(title: const Text('Correct TV match')),
      body: CenteredContent(child: !allowed
        ? const Center(child: Text('TV corrections are not available for this account and server.'))
        : ListView(padding: const EdgeInsets.all(16), children: [
          if (libraries.isNotEmpty) DropdownButtonFormField<String>(
            key: ValueKey('validation-$_instanceId'),
            initialValue: libraries.any((i) => i.id == _instanceId) ? _instanceId : null,
            isExpanded: true, decoration: const InputDecoration(labelText: 'Sonarr library for validation'),
            items: [for (final library in libraries) DropdownMenuItem(value: library.id, child: Text(library.name))],
            onChanged: _busy ? null : (id) { setState(() { _instanceId = id; _view = null; }); _load(); }),
          const SizedBox(height: 16),
          if (_busy) const LinearProgressIndicator(),
          if (_error != null) _ErrorRetry(message: _error!, retry: _busy ? null : _load),
          if (view != null) ...[
            Text(view.match.title.isEmpty ? 'TMDB ${widget.tmdbId}' : view.match.title,
              style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 8),
            Text('${view.match.originLabel} · ${view.match.state}'),
            if (view.match.tvdbId > 0) Text('${view.match.targetTitle} · TVDB ${view.match.tvdbId}'),
            if (view.match.seasonMap.isNotEmpty) Text(tvSeasonMappingLabel(view.match.seasonMap)),
            if (view.match.message != null) Padding(padding: const EdgeInsets.only(top: 8), child: Text(view.match.message!)),
            const SizedBox(height: 16),
            const Text('Choose one target series, then select a Sonarr season for every source season. Saving a correction changes no library monitoring.'),
            const SizedBox(height: 12),
            TextField(controller: _query, enabled: !_busy,
              decoration: const InputDecoration(labelText: 'Search Sonarr or enter a TVDB ID'), onSubmitted: (_) => _search()),
            Wrap(spacing: 8, runSpacing: 8, children: [
              TextButton.icon(onPressed: _busy ? null : _search, icon: const Icon(Icons.search), label: const Text('Find series')),
              if (view.targetSeasons.isNotEmpty) TextButton(onPressed: _busy ? null : () => _select(TVMatchCandidate(
                tvdbId: view.match.tvdbId, title: view.match.targetTitle, year: 0, seasons: view.targetSeasons)), child: const Text('Edit current match')),
            ]),
            if (_candidates?.isEmpty == true) const Text('Sonarr returned no candidates for this search.'),
            for (final candidate in _candidates ?? <TVMatchCandidate>[]) Card(child: ListTile(
              title: Text(candidate.title), subtitle: Text('${candidate.year} · TVDB ${candidate.tvdbId}'),
              selected: _selected == candidate, onTap: _busy ? null : () => _select(candidate))),
            if (_selected != null) ...[
              const SizedBox(height: 12), Text('Target: ${_selected!.title} · TVDB ${_selected!.tvdbId}'),
              for (final season in view.sourceSeasons.entries) Padding(padding: const EdgeInsets.symmetric(vertical: 8),
                child: DropdownButtonFormField<int>(key: ValueKey('$_selectionRevision-${_selected!.tvdbId}-${season.key}'),
                  initialValue: _seasons[season.key], isExpanded: true,
                  decoration: InputDecoration(labelText: 'Source season ${season.key} → Sonarr season'),
                  items: [for (final n in _selected!.seasons) DropdownMenuItem(value: n, child: Text('Sonarr season $n'))],
                  onChanged: _busy ? null : (n) { if (n != null) setState(() { _seasons[season.key] = n; _dirty = true; }); })),
              Align(alignment: Alignment.centerLeft, child: FilledButton(onPressed: complete && !_busy ? () => _save('custom') : null,
                child: const Text('Save local correction'))),
            ],
            const SizedBox(height: 16),
            Wrap(spacing: 8, runSpacing: 8, children: [
              OutlinedButton(onPressed: _busy || view.match.state == 'paused' ? null : () => _save('paused'), child: const Text('Pause matching')),
              OutlinedButton(onPressed: _busy ? null : () => _save('default'), child: const Text('Restore bundled / default')),
            ]),
            const Divider(height: 40),
            Text('Affected requests', style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            const Text('Upgrading or saving a match does not re-request existing content. Review each proposed correction before creating it.'),
            if (_repairError != null) _ErrorRetry(message: _repairError!, retry: _busy ? null : _loadRepairs),
            if (_repairs?.isEmpty == true) const Padding(padding: EdgeInsets.symmetric(vertical: 12),
              child: Text('No affected accepted requests were found in the latest 200 requests for this title.')),
            for (final repair in _repairs ?? <TVRepairPreview>[]) Card(child: Padding(padding: const EdgeInsets.all(12),
              child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                Text('Request #${repair.requestId} · ${repair.instanceName}'),
                Text(repair.title), const SizedBox(height: 8),
                Text('Recorded: ${repair.recordedTargetKnown ? TVRepairPreview.describeTarget(repair.recordedTarget) : 'Unverified legacy target${repair.recordedTvdbId > 0 ? ' (TVDB ${repair.recordedTvdbId})' : ''}'}'),
                const SizedBox(height: 8), Text('Intended: ${TVRepairPreview.describeTarget(repair.intendedTarget)}'),
                const SizedBox(height: 8), Text(repair.message),
                if (repair.repairRequestId != null) Text('Corrective request #${repair.repairRequestId} already saved.')
                else TextButton(onPressed: !_busy && repair.canRepair ? () => _repair(repair) : null,
                  child: const Text('Review repair')),
              ]))),
          ],
        ])),
    ));
  }
}

class _ErrorRetry extends StatelessWidget {
  final String message;
  final VoidCallback? retry;
  const _ErrorRetry({required this.message, this.retry});
  @override
  Widget build(BuildContext context) => Padding(padding: const EdgeInsets.symmetric(vertical: 12),
    child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [Text(message),
      TextButton.icon(onPressed: retry, icon: const Icon(Icons.refresh), label: const Text('Refresh and retry'))]));
}
