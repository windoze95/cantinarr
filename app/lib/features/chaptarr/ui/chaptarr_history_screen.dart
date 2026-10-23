import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';
import 'widgets/format_badge.dart';

/// Paginated Chaptarr history: grabs, imports, failures, deletions, renames.
class ChaptarrHistoryScreen extends ConsumerStatefulWidget {
  const ChaptarrHistoryScreen({super.key});

  @override
  ConsumerState<ChaptarrHistoryScreen> createState() =>
      _ChaptarrHistoryScreenState();
}

class _ChaptarrHistoryScreenState extends ConsumerState<ChaptarrHistoryScreen> {
  static const _pageSize = 50;

  final _scrollController = ScrollController();
  List<ChaptarrHistoryRecord> _records = [];
  int _totalRecords = 0;
  int _page = 1;
  bool _isLoading = true;
  bool _isLoadingMore = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _scrollController.addListener(_onScroll);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    _scrollController.dispose();
    super.dispose();
  }

  void _onScroll() {
    if (_scrollController.position.extentAfter < 400) _loadMore();
  }

  ChaptarrApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeChaptarrInstance?.id;
    if (instanceId == null) return null;
    return ChaptarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<void> _load() async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = context.l10n.adminNoChaptarrInstanceConfigured;
      });
      return;
    }

    setState(() => _isLoading = true);
    try {
      final page = await service.getHistory(page: 1, pageSize: _pageSize);
      if (!mounted) return;
      setState(() {
        _records = page.records;
        _totalRecords = page.totalRecords;
        _page = 1;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = context.l10n.adminFailedToLoadHistory2((e).toString());
      });
    }
  }

  Future<void> _loadMore() async {
    if (_isLoading || _isLoadingMore || !_hasMore) return;
    final service = _buildService();
    if (service == null) return;

    setState(() => _isLoadingMore = true);
    try {
      final next =
          await service.getHistory(page: _page + 1, pageSize: _pageSize);
      if (!mounted) return;
      setState(() {
        _page += 1;
        _records = [..._records, ...next.records];
        _totalRecords = next.totalRecords;
        _isLoadingMore = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _isLoadingMore = false);
    }
  }

  bool get _hasMore => _records.length < _totalRecords;

  @override
  Widget build(BuildContext context) {
    // Reload when the active instance changes.
    ref.listen(instanceProvider.select((s) => s.activeChaptarrInstanceId),
        (_, __) => _load());

    if (_isLoading && _records.isEmpty) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null && _records.isEmpty) {
      return FullScreenError(message: _error!, onRetry: _load);
    }
    if (_records.isEmpty) {
      return RefreshIndicator(
        onRefresh: _load,
        color: AppTheme.accent,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: [
            SizedBox(height: 160),
            Icon(Icons.history, size: 48, color: AppTheme.textSecondary),
            SizedBox(height: 12),
            Center(
              child: Text(context.l10n.adminNoHistoryYet,
                  style:
                      TextStyle(color: AppTheme.textSecondary, fontSize: 16)),
            ),
          ],
        ),
      );
    }

    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView.builder(
        controller: _scrollController,
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: _records.length + (_hasMore ? 1 : 0),
        itemBuilder: (context, index) {
          if (index >= _records.length) {
            return const Padding(
              padding: EdgeInsets.all(16),
              child: Center(
                child: SizedBox(
                  width: 24,
                  height: 24,
                  child: CircularProgressIndicator(
                      color: AppTheme.accent, strokeWidth: 2.5),
                ),
              ),
            );
          }

          final record = _records[index];
          final showHeader =
              index == 0 || !_sameDay(record.date, _records[index - 1].date);

          return Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (showHeader && record.date != null)
                _DayHeader(
                    label: _dayLabel(record.date!.toLocal(),
                        strings: context.l10n)),
              _HistoryTile(record: record),
            ],
          );
        },
      ),
    );
  }
}

bool _sameDay(DateTime? a, DateTime? b) {
  if (a == null || b == null) return a == b;
  final la = a.toLocal();
  final lb = b.toLocal();
  return la.year == lb.year && la.month == lb.month && la.day == lb.day;
}

String _dayLabel(DateTime localDate, {AppLocalizations? strings}) {
  final now = DateTime.now();
  final today = DateTime(now.year, now.month, now.day);
  final day = DateTime(localDate.year, localDate.month, localDate.day);
  final diff = today.difference(day).inDays;
  if (diff == 0)
    return (strings ?? lookupAppLocalizations(const Locale('en'))).adminToday;
  if (diff == 1)
    return (strings ?? lookupAppLocalizations(const Locale('en')))
        .adminYesterday;
  if (localDate.year == now.year) {
    return ((strings?.localeName ?? 'en') == 'en'
        ? DateFormat('EEEE, MMM d', 'en_US')
        : DateFormat.MMMMEEEEd(strings!.dateLocale)).format(localDate);
  }
  return DateFormat.yMMMd(strings?.dateLocale ?? 'en_US').format(localDate);
}

String _timeLabel(DateTime? date, {AppLocalizations? strings}) {
  if (date == null) return '';
  final local = date.toLocal();
  final diff = DateTime.now().difference(local);
  if (_sameDay(local, DateTime.now())) {
    if (diff.inMinutes < 1)
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminJustNow2;
    if (diff.inMinutes < 60)
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminMAgo((diff.inMinutes).toString());
    return (strings ?? lookupAppLocalizations(const Locale('en')))
        .adminHAgo((diff.inHours).toString());
  }
  return DateFormat.jm(strings?.dateLocale ?? 'en_US').format(local);
}

/// Exported for the wire-vocabulary test: every event name Chaptarr's
/// history enum can emit must map to a styled label, never the raw fallback.
({IconData icon, Color color, String label}) chaptarrHistoryEventStyle(
    String eventType,
    {AppLocalizations? strings}) {
  switch (eventType) {
    case 'grabbed':
      return (
        icon: Icons.download_outlined,
        color: AppTheme.downloading,
        label:
            (strings ?? lookupAppLocalizations(const Locale('en'))).adminGrabbed
      );
    // Import events as Chaptarr's history enum actually spells them:
    // bookFileImported per file, downloadImported when the whole download
    // lands. The previously matched bookImported / downloadFolderImported /
    // authorFolderImported never occur on the wire, so every import rendered
    // through the generic fallback.
    case 'bookFileImported':
    case 'downloadImported':
      return (
        icon: Icons.check_circle_outline,
        color: AppTheme.available,
        label: (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminImported
      );
    case 'bookImportIncomplete':
      return (
        icon: Icons.warning_amber,
        color: AppTheme.warning,
        label: (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminImportIncomplete
      );
    case 'downloadFailed':
      return (
        icon: Icons.error_outline,
        color: AppTheme.error,
        label: (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminDownloadFailed
      );
    case 'bookFileDeleted':
      return (
        icon: Icons.delete_outline,
        color: AppTheme.unavailable,
        label: (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminFileDeleted
      );
    case 'bookFileRenamed':
      return (
        icon: Icons.drive_file_rename_outline,
        color: AppTheme.accent,
        label:
            (strings ?? lookupAppLocalizations(const Locale('en'))).adminRenamed
      );
    case 'bookFileRetagged':
      return (
        icon: Icons.sell_outlined,
        color: AppTheme.accent,
        label: (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminRetagged
      );
    case 'bookFileConverted':
      return (
        icon: Icons.autorenew,
        color: AppTheme.available,
        label: (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminConverted
      );
    case 'bookFileConversionFailed':
      return (
        icon: Icons.error_outline,
        color: AppTheme.error,
        label: (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminConversionFailed
      );
    case 'downloadIgnored':
      return (
        icon: Icons.block,
        color: AppTheme.unavailable,
        label:
            (strings ?? lookupAppLocalizations(const Locale('en'))).adminIgnored
      );
    default:
      return (
        icon: Icons.history,
        color: AppTheme.textSecondary,
        label: eventType.isEmpty
            ? (strings ?? lookupAppLocalizations(const Locale('en')))
                .adminUnknown
            : eventType
      );
  }
}

class _DayHeader extends StatelessWidget {
  final String label;

  const _DayHeader({required this.label});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 4),
      child: Text(
        label,
        style: const TextStyle(
          color: AppTheme.accent,
          fontSize: 12,
          fontWeight: FontWeight.w600,
          letterSpacing: 0.5,
        ),
      ),
    );
  }
}

class _HistoryTile extends StatelessWidget {
  final ChaptarrHistoryRecord record;

  const _HistoryTile({required this.record});

  @override
  Widget build(BuildContext context) {
    final style =
        chaptarrHistoryEventStyle(record.eventType, strings: context.l10n);
    final subtitleParts = [
      style.label,
      if (record.quality != null && record.quality!.isNotEmpty) record.quality!,
    ];

    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 2),
      leading: Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: style.color.withValues(alpha: 0.15),
          shape: BoxShape.circle,
        ),
        child: Icon(style.icon, color: style.color, size: 20),
      ),
      title: Text(
        record.sourceTitle.isNotEmpty ? record.sourceTitle : style.label,
        style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 13.5,
            fontWeight: FontWeight.w500),
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Wrap(
        spacing: 6,
        runSpacing: 4,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          Text(
            subtitleParts.join(' • '),
            style: TextStyle(color: style.color, fontSize: 12),
          ),
          if (record.format != BookFormat.unknown)
            ChaptarrFormatBadge(format: record.format),
        ],
      ),
      trailing: Text(
        _timeLabel(record.date, strings: context.l10n),
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 11),
      ),
    );
  }
}
