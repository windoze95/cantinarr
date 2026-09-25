import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';

import '../../../core/config/app_config.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../../core/widgets/cached_image.dart';
import '../../request/data/request_service.dart' show BookRequestFormat;
import '../data/request_history_service.dart';

class RequestHistoryScreen extends ConsumerStatefulWidget {
  const RequestHistoryScreen({super.key});

  @override
  ConsumerState<RequestHistoryScreen> createState() => _RequestHistoryScreenState();
}

class _RequestHistoryScreenState extends ConsumerState<RequestHistoryScreen> {
  final _search = TextEditingController();
  final _scroll = ScrollController();
  List<RequestHistoryItem>? _items;
  List<HistoryRequester> _requesters = [];
  String _mediaType = '';
  String _decision = '';
  int? _userId;
  int? _nextBefore;
  bool _loading = true;
  bool _loadingOlder = false;
  String? _error;
  bool _retryOlder = false;
  Timer? _debounce;
  CancelToken? _cancel;
  int _generation = 0;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _cancel?.cancel();
    _search.dispose();
    _scroll.dispose();
    super.dispose();
  }

  Future<void> _load({bool older = false}) async {
    _debounce?.cancel();
    final generation = ++_generation;
    _cancel?.cancel();
    final cancel = _cancel = CancelToken();
    setState(() {
      _loading = !older;
      _loadingOlder = older;
      _error = null;
      _retryOlder = older;
    });
    try {
      final page = await RequestHistoryService(ref.read(backendClientProvider)).list(
        query: _search.text,
        mediaType: _mediaType,
        decision: _decision,
        userId: _userId,
        before: older ? _nextBefore : null,
        cancelToken: cancel,
      );
      if (!mounted || generation != _generation) return;
      setState(() {
        _items = older ? [...?_items, ...page.requests] : page.requests;
        _requesters = page.requesters;
        _nextBefore = page.nextBefore;
        _loading = _loadingOlder = false;
      });
    } catch (e) {
      if (!mounted || generation != _generation) return;
      setState(() {
        _loading = _loadingOlder = false;
        _error = e is DioException && e.response?.statusCode == 404
            ? 'Request history needs a newer server. Update Cantinarr and try again.'
            : older
                ? 'Couldn’t load older requests. Try again.'
                : _items == null
                    ? 'Couldn’t load request history. Try again.'
                    : 'Couldn’t refresh request history. Showing the last update.';
      });
    }
  }

  void _changeFilters({bool debounce = false}) {
    _debounce?.cancel();
    _cancel?.cancel();
    ++_generation;
    setState(() {
      _items = null;
      _nextBefore = null;
      _error = null;
      _loading = true;
      _loadingOlder = false;
    });
    if (_scroll.hasClients) _scroll.jumpTo(0);
    if (debounce) {
      _debounce = Timer(const Duration(milliseconds: 300), _load);
    } else {
      _load();
    }
  }

  bool get _filtered => _search.text.trim().isNotEmpty ||
      _mediaType.isNotEmpty || _decision.isNotEmpty || _userId != null;

  void _clearFilters() {
    _search.clear();
    _mediaType = _decision = '';
    _userId = null;
    _changeFilters();
  }

  Future<void> _open(RequestHistoryItem item) async {
    final route = await showAppSheet<String>(context,
      builder: (_) => _HistoryDetail(item: item));
    if (mounted && route != null) context.push(route);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Request history'),
        leading: BackButton(onPressed: () {
          if (context.canPop()) {
            context.pop();
          } else {
            context.go('/approvals');
          }
        }),
        actions: [
          IconButton(
            tooltip: 'Refresh history',
            onPressed: _loading || _loadingOlder ? null : _load,
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: CenteredContent(
        child: Column(children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              TextField(
                controller: _search,
                maxLength: 200,
                onChanged: (_) => _changeFilters(debounce: true),
                onSubmitted: (_) => _load(),
                decoration: InputDecoration(
                  labelText: 'Search titles',
                  counterText: '',
                  prefixIcon: const Icon(Icons.search),
                  suffixIcon: _search.text.isEmpty ? null : IconButton(
                    tooltip: 'Clear search',
                    onPressed: () { _search.clear(); _changeFilters(); },
                    icon: const Icon(Icons.close),
                  ),
                ),
              ),
              const SizedBox(height: 10),
              Wrap(spacing: 8, runSpacing: 8, children: [
                _FilterMenu<String>(
                  label: 'Requester',
                  value: _userId?.toString() ?? '',
                  choices: {
                    '': 'All requesters',
                    for (final user in _requesters) '${user.id}': user.label,
                    if (_userId != null && !_requesters.any((u) => u.id == _userId))
                      '$_userId': 'Selected requester',
                  },
                  onChanged: (value) { _userId = int.tryParse(value); _changeFilters(); },
                ),
                _FilterMenu<String>(
                  label: 'Media type',
                  value: _mediaType,
                  choices: const {'': 'All media', ...historyMediaTypes},
                  onChanged: (value) { _mediaType = value; _changeFilters(); },
                ),
                _FilterMenu<String>(
                  label: 'Decision',
                  value: _decision,
                  choices: const {'': 'All decisions', ...historyDecisions},
                  onChanged: (value) { _decision = value; _changeFilters(); },
                ),
                if (_filtered)
                  TextButton(onPressed: _clearFilters, child: const Text('Clear filters')),
              ]),
            ]),
          ),
          const Divider(height: 1),
          if (_loading && _items != null) const LinearProgressIndicator(),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.all(16),
              child: Column(children: [
                Text(_error!, style: const TextStyle(color: AppTheme.error)),
                TextButton(
                  onPressed: () => _load(older: _retryOlder),
                  child: const Text('Retry'),
                ),
              ]),
            ),
          Expanded(child: _items == null
              ? _loading ? const Center(child: CircularProgressIndicator()) : const SizedBox()
              : RefreshIndicator(
                  onRefresh: _load,
                  child: ListView.builder(
                    controller: _scroll,
                    physics: const AlwaysScrollableScrollPhysics(),
                    itemCount: _items!.isEmpty ? 1 : _items!.length + 1,
                    itemBuilder: (context, index) {
                      if (_items!.isEmpty) {
                        return Padding(
                          padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 64),
                          child: Column(children: [
                            const Icon(Icons.history, size: 40, color: AppTheme.textMuted),
                            const SizedBox(height: 16),
                            Text(_filtered ? 'No matching requests' : 'No saved requests yet',
                              style: Theme.of(context).textTheme.titleMedium),
                            const SizedBox(height: 8),
                            Text(_filtered
                                ? 'Try a different title or clear the filters.'
                                : 'Requests from everyone on this server will appear here, including automatically approved requests.',
                              textAlign: TextAlign.center,
                              style: const TextStyle(color: AppTheme.textSecondary)),
                          ]),
                        );
                      }
                      if (index == _items!.length) {
                        return Padding(
                          padding: const EdgeInsets.all(20),
                          child: Center(child: _nextBefore == null
                              ? const Text('All matching saved requests shown',
                                  style: TextStyle(color: AppTheme.textMuted, fontSize: 12))
                              : OutlinedButton.icon(
                                  onPressed: _loadingOlder || _loading ? null : () => _load(older: true),
                                  icon: _loadingOlder
                                      ? const SizedBox(width: 16, height: 16,
                                          child: CircularProgressIndicator(strokeWidth: 2))
                                      : const Icon(Icons.expand_more),
                                  label: const Text('Load older requests'),
                                )),
                        );
                      }
                      final item = _items![index];
                      return _HistoryTile(item: item, onTap: () => _open(item));
                    },
                  ),
                )),
        ]),
      ),
    );
  }
}

class _FilterMenu<T> extends StatelessWidget {
  final String label;
  final T value;
  final Map<T, String> choices;
  final ValueChanged<T> onChanged;
  const _FilterMenu({required this.label, required this.value,
    required this.choices, required this.onChanged});

  @override
  Widget build(BuildContext context) => PopupMenuButton<T>(
    tooltip: label,
    initialValue: value,
    onSelected: onChanged,
    itemBuilder: (_) => choices.entries.map((entry) => PopupMenuItem<T>(
      value: entry.key, child: Text(entry.value))).toList(),
    child: Chip(
      label: Text(choices[value]!),
      avatar: const Icon(Icons.expand_more, size: 18),
    ),
  );
}

Color _decisionColor(String decision) => switch (decision) {
  'approved' => AppTheme.success,
  'pending' => AppTheme.warning,
  'denied' => AppTheme.error,
  _ => AppTheme.textMuted,
};

String _date(DateTime? date) => date == null ? 'Date not recorded' : DateFormat.yMMMd().add_jm().format(date);

class _HistoryTile extends StatelessWidget {
  final RequestHistoryItem item;
  final VoidCallback onTap;
  const _HistoryTile({required this.item, required this.onTap});

  @override
  Widget build(BuildContext context) => InkWell(
    onTap: onTap,
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(6),
          child: SizedBox(width: 48, height: 72,
            child: CachedImage(
              url: item.posterPath.isEmpty ? null : AppConfig.tmdbPoster(item.posterPath, width: 185),
              fit: BoxFit.cover,
              icon: switch (item.mediaType) {
                'tv' => Icons.tv,
                'book' => Icons.menu_book,
                'music' => Icons.album,
                _ => Icons.movie,
              },
            )),
        ),
        const SizedBox(width: 14),
        Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text(item.title, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 15)),
          const SizedBox(height: 4),
          Wrap(spacing: 10, runSpacing: 4, children: [
            Text(item.decisionLabel, style: TextStyle(color: _decisionColor(item.decision), fontSize: 12, fontWeight: FontWeight.w600)),
            Text(item.mediaLabel, style: const TextStyle(color: AppTheme.textMuted, fontSize: 12)),
            if (item.scopeLabel.isNotEmpty)
              Text(item.scopeLabel, style: const TextStyle(color: AppTheme.textMuted, fontSize: 12)),
          ]),
          const SizedBox(height: 6),
          Text('Requested by ${item.requesterLabel}', style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
          Text(_date(item.requestedAt), style: const TextStyle(color: AppTheme.textMuted, fontSize: 12)),
          Text(item.libraryLabel, style: const TextStyle(color: AppTheme.textMuted, fontSize: 12)),
        ])),
        const Padding(padding: EdgeInsets.only(top: 4, left: 8),
          child: Icon(Icons.chevron_right, size: 18, color: AppTheme.textMuted)),
      ]),
    ),
  );
}

class _HistoryDetail extends StatelessWidget {
  final RequestHistoryItem item;
  const _HistoryDetail({required this.item});

  @override
  Widget build(BuildContext context) {
    final route = item.detailRoute;
    return AppSheet(child: Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(item.title, style: Theme.of(context).textTheme.titleLarge),
        const SizedBox(height: 8),
        Text(item.decisionLabel, style: TextStyle(color: _decisionColor(item.decision), fontWeight: FontWeight.w600)),
        const SizedBox(height: 20),
        _field(context, 'Requested', _date(item.requestedAt)),
        _field(context, 'Requesters', item.requesters.isEmpty ? 'Unknown requester' : item.requesters.map((user) {
          final format = BookRequestFormat.tryFromValue(user.bookFormat);
          return '${user.label}${format == null || item.mediaType != 'book' ? '' : ' (${format.label})'}';
        }).join('\n')),
        _field(context, 'Library', item.libraryLabel),
        _field(context, 'Media', item.mediaLabel),
        if (item.scopeLabel.isNotEmpty) _field(context, 'Requested scope', item.scopeLabel),
        if (item.decision != 'pending') ...[
          _field(context, 'Reviewed by', item.decidedBy.isEmpty ? 'No reviewer recorded' : item.decidedBy),
          _field(context, 'Decision date', _date(item.decidedAt)),
        ],
        if (item.denyReason.isNotEmpty) _field(context, item.decision == 'cancelled' ? 'Reason' : 'Denial reason', item.denyReason),
        if (route != null) ...[
          const SizedBox(height: 4),
          const Text('Open the title to check current availability.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
          const SizedBox(height: 12),
          FilledButton.icon(
            onPressed: () => Navigator.of(context).pop(route),
            icon: const Icon(Icons.open_in_new),
            label: const Text('View title'),
          ),
        ],
        const SizedBox(height: 20),
      ],
    ));
  }

  Widget _field(BuildContext context, String label, String value) => Padding(
    padding: const EdgeInsets.only(bottom: 14),
    child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(label, style: const TextStyle(color: AppTheme.textMuted, fontSize: 12)),
      const SizedBox(height: 3),
      Text(value),
    ]),
  );
}
