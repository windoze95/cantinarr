import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';
import '../../../core/network/api_error_message.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/tdarr_api_service.dart';
import '../data/tdarr_models.dart';

/// Both views share visibility, cancellation and freshness behavior. A hidden
/// indexed-stack branch or background app never polls its upstream service.
class TdarrScreen extends ConsumerStatefulWidget {
  final bool activity;
  const TdarrScreen({super.key, required this.activity});

  @override
  ConsumerState<TdarrScreen> createState() => _TdarrScreenState();
}

class _TdarrScreenState extends ConsumerState<TdarrScreen>
    with WidgetsBindingObserver {
  TdarrActivity? _activity;
  TdarrLibraries? _libraries;
  TdarrStats? _stats;
  String? _libraryId, _error;
  bool _loading = false, _visible = false, _tickerEnabled = true;
  bool _foreground = true;
  int _generation = 0;
  Timer? _timer;
  CancelToken? _cancel;
  GoRouter? _router;

  String get _path => widget.activity ? '/tdarr/activity' : '/tdarr/libraries';
  DateTime? get _observedAt => widget.activity ? _activity?.observedAt : _stats?.observedAt;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _foreground = lifecycle == null || lifecycle == AppLifecycleState.resumed;
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _tickerEnabled = TickerMode.valuesOf(context).enabled;
    final router = GoRouter.maybeOf(context);
    if (_router != router) {
      _router?.routeInformationProvider.removeListener(_scheduleVisibility);
      _router = router;
      _router?.routeInformationProvider.addListener(_scheduleVisibility);
    }
    _scheduleVisibility();
  }

  void _scheduleVisibility() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _syncVisibility();
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
    _syncVisibility();
  }

  void _syncVisibility() {
    if (!mounted) return;
    final visible = _foreground && _tickerEnabled &&
        (_router == null || _router!.routeInformationProvider.value.uri.path == _path);
    if (visible == _visible) return;
    _visible = visible;
    _timer?.cancel();
    if (!visible) {
      _cancel?.cancel();
      _generation++;
      setState(() => _loading = false);
      return;
    }
    _load();
    _timer = Timer.periodic(Duration(seconds: widget.activity ? 10 : 30), (_) => _load());
  }

  void _selectionChanged({bool instance = false, String? libraryId}) {
    _cancel?.cancel();
    _generation++;
    setState(() {
      _loading = false;
      _error = null;
      _stats = null;
      _libraryId = libraryId;
      if (instance) { _activity = null; _libraries = null; }
    });
    _load();
  }

  Future<void> _load() async {
    if (!mounted || !_visible || _loading) return;
    final instance = ref.read(instanceProvider).activeTdarrInstance;
    if (instance == null) return;
    final generation = ++_generation;
    final token = CancelToken();
    _cancel = token;
    setState(() => _loading = true);
    try {
      final service = TdarrApiService(ref.read(backendClientProvider), instance.id);
      TdarrActivity? activity;
      TdarrLibraries? libraries;
      TdarrStats? stats;
      if (widget.activity) {
        activity = await service.activity(token);
      } else {
        // Fetch membership first: a removed selection is reported explicitly,
        // never silently replaced by totals for a different library.
        libraries = await service.libraries(token);
        if (!mounted || generation != _generation) return;
        if (_libraryId != null && !libraries.items.any((i) => i.id == _libraryId)) {
          if (!mounted || generation != _generation) return;
          setState(() => _libraries = libraries);
          throw StateError('Selected library no longer exists. Select another library.');
        }
        stats = await service.stats(token, _libraryId);
      }
      if (!mounted || generation != _generation) return;
      setState(() {
        if (widget.activity) { _activity = activity; }
        else { _libraries = libraries; _stats = stats; }
        _error = null;
      });
    } catch (e) {
      if (!mounted || generation != _generation || (e is DioException && CancelToken.isCancel(e))) return;
      setState(() => _error = e is StateError ? e.message.toString() :
          e is DioException ? apiErrorMessage(e) : 'Tdarr returned an incompatible response. Check the server version.');
    } finally {
      if (mounted && generation == _generation) setState(() => _loading = false);
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _router?.routeInformationProvider.removeListener(_scheduleVisibility);
    _timer?.cancel();
    _cancel?.cancel();
    _generation++;
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final instance = ref.watch(instanceProvider).activeTdarrInstance;
    ref.listen(instanceProvider.select((s) => s.activeTdarrInstance?.id),
        (_, __) => _selectionChanged(instance: true));
    if (instance == null) {
      return const Center(child: Padding(padding: EdgeInsets.all(24), child:
        Text('No Tdarr instance configured. Add one from Settings > Add Instance.', textAlign: TextAlign.center)));
    }
    final timestamp = _observedAt;
    return Column(children: [
      Padding(padding: const EdgeInsets.fromLTRB(16, 12, 8, 4), child: Row(children: [
        Expanded(child: Text(timestamp == null ? 'Tdarr progress' :
            'Updated ${DateFormat.yMd().add_jms().format(timestamp.toLocal())}',
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12))),
        IconButton(tooltip: 'Refresh', onPressed: _loading ? null : _load,
            icon: const Icon(Icons.refresh)),
      ])),
      if (_loading) const LinearProgressIndicator(minHeight: 2),
      if (_error != null) Padding(padding: const EdgeInsets.all(16), child: Column(
        crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text(timestamp == null ? 'Tdarr unavailable' : 'Showing stale data',
              style: const TextStyle(color: AppTheme.warning, fontWeight: FontWeight.w600)),
          const SizedBox(height: 4),
          Text(_error!, style: const TextStyle(color: AppTheme.textSecondary)),
          TextButton(onPressed: _loading ? null : _load, child: const Text('Retry')),
        ])),
      Expanded(child: RefreshIndicator(onRefresh: _load, child: ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
        children: widget.activity ? _activityWidgets() : _libraryWidgets(),
      ))),
    ]);
  }

  List<Widget> _activityWidgets() {
    final activity = _activity;
    if (activity == null) return const [];
    if (activity.nodes.isEmpty) {
      return const [
        _EmptyState(icon: Icons.dns_outlined, title: 'No nodes connected',
            detail: 'Tdarr reported no connected nodes. Library statistics are available on the Libraries tab.'),
      ];
    }
    return [
      Text('${activity.activeWorkers} active ${activity.activeWorkers == 1 ? 'worker' : 'workers'}',
          style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 4),
      const Text('Progress and ETA describe the current processing step.',
          style: TextStyle(color: AppTheme.textSecondary)),
      if (activity.activeWorkers == 0) const Padding(padding: EdgeInsets.symmetric(vertical: 16),
          child: Text('Nothing is processing. Check Libraries for queued or held files.')),
      for (final node in activity.nodes) ...[
        Padding(padding: const EdgeInsets.only(top: 20, bottom: 8), child: Row(children: [
          const Icon(Icons.dns_outlined, size: 18), const SizedBox(width: 8),
          Expanded(child: Text(node.name, style: const TextStyle(fontWeight: FontWeight.w600))),
          Text(node.paused ? 'Paused' : node.workers.isEmpty ? 'Idle' : 'Processing',
              style: const TextStyle(color: AppTheme.textSecondary)),
        ])),
        for (final worker in node.workers) _WorkerCard(key: ValueKey('${node.id}/${worker.id}'), worker: worker),
      ],
    ];
  }

  List<Widget> _libraryWidgets() {
    final libraries = _libraries;
    final stats = _stats;
    return [
      if (libraries != null) ...[
        DropdownButtonFormField<String>(
          key: ValueKey('${ref.read(instanceProvider).activeTdarrInstance?.id}:$_libraryId:${libraries.items.map((i) => i.id).join(',')}'),
          initialValue: _libraryId ?? '',
          isExpanded: true,
          decoration: const InputDecoration(labelText: 'Library'),
          items: [
            const DropdownMenuItem(value: '', child: Text('All libraries')),
            if (_libraryId != null && !libraries.items.any((i) => i.id == _libraryId))
              DropdownMenuItem(value: _libraryId, enabled: false, child: const Text('Removed library')),
            for (final library in libraries.items)
              DropdownMenuItem(value: library.id, child: Text(library.name, overflow: TextOverflow.ellipsis)),
          ],
          onChanged: (id) => _selectionChanged(libraryId: id == '' ? null : id),
        ),
        const SizedBox(height: 20),
        if (libraries.items.isEmpty) const _EmptyState(icon: Icons.video_library_outlined,
            title: 'No libraries configured', detail: 'Tdarr reported no libraries. Add a library in Tdarr to begin.'),
      ],
      if (stats != null) ...[
        Text('${NumberFormat.decimalPattern().format(stats.totalFiles)} files',
            style: Theme.of(context).textTheme.headlineSmall),
        const SizedBox(height: 4),
        const Text('Current library state reported by Tdarr.', style: TextStyle(color: AppTheme.textSecondary)),
        if (stats.note.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8),
            child: Text(stats.note, style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12))),
        if (stats.totalFiles == 0 && libraries?.items.isNotEmpty == true)
          const Padding(padding: EdgeInsets.only(top: 12), child: Text('No files have been scanned in this selection.')),
        _Counts(title: 'Transcodes', counts: stats.transcodes),
        _Counts(title: 'Health checks', counts: stats.healthChecks),
      ],
    ];
  }
}

class _WorkerCard extends StatelessWidget {
  final TdarrWorker worker;
  const _WorkerCard({super.key, required this.worker});

  @override
  Widget build(BuildContext context) => Card(child: Padding(
    padding: const EdgeInsets.all(16), child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(worker.filename.isEmpty ? 'Preparing job' : worker.filename,
          style: const TextStyle(fontWeight: FontWeight.w600)),
      const SizedBox(height: 8),
      Wrap(spacing: 12, runSpacing: 4, children: [
        Text('${worker.kind}${worker.compute.isEmpty ? '' : ' · ${worker.compute}'}'),
        if (worker.status.isNotEmpty) Text(worker.status),
        if (worker.flow) const Text('Flow'),
      ]),
      const SizedBox(height: 12),
      if (worker.progress != null) ...[
        LinearProgressIndicator(value: (worker.progress! / 100).clamp(0, 1)),
        const SizedBox(height: 8),
      ],
      Wrap(spacing: 16, runSpacing: 4, children: [
        Text(worker.progress == null ? 'Progress unavailable' : '${worker.progress!.toStringAsFixed(1)}%'),
        if (worker.fps != null) Text('${worker.fps!.toStringAsFixed(0)} FPS'),
        if (worker.eta.isNotEmpty) Text('ETA ${worker.eta}'),
      ]),
      if (worker.file.isNotEmpty) ExpansionTile(tilePadding: EdgeInsets.zero,
          shape: const Border(), collapsedShape: const Border(),
          backgroundColor: Colors.transparent, collapsedBackgroundColor: Colors.transparent,
          title: const Text('Source file'), children: [
            Align(alignment: Alignment.centerLeft, child: SelectableText(worker.file)),
          ]),
    ]),
  ));
}

class _Counts extends StatelessWidget {
  final String title;
  final List<TdarrCount> counts;
  const _Counts({required this.title, required this.counts});

  @override
  Widget build(BuildContext context) => Padding(padding: const EdgeInsets.only(top: 24),
    child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(title, style: Theme.of(context).textTheme.titleMedium),
      const SizedBox(height: 8),
      if (counts.isEmpty) const Text('No status counts reported.', style: TextStyle(color: AppTheme.textSecondary)),
      for (final count in counts) Padding(padding: const EdgeInsets.symmetric(vertical: 7),
        child: Row(children: [Expanded(child: Text(count.label)), const SizedBox(width: 12),
          Text(count.value == null ? 'Unavailable' : NumberFormat.decimalPattern().format(count.value)),
        ])),
    ]),
  );
}

class _EmptyState extends StatelessWidget {
  final IconData icon;
  final String title, detail;
  const _EmptyState({required this.icon, required this.title, required this.detail});

  @override
  Widget build(BuildContext context) => Padding(padding: const EdgeInsets.symmetric(vertical: 40),
    child: Column(children: [Icon(icon, size: 40, color: AppTheme.textSecondary),
      const SizedBox(height: 12), Text(title, style: Theme.of(context).textTheme.titleMedium),
      const SizedBox(height: 8), Text(detail, textAlign: TextAlign.center,
          style: const TextStyle(color: AppTheme.textSecondary)),
    ]),
  );
}
