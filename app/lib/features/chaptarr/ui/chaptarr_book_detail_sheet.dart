import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../../core/widgets/status_pill.dart';
import '../../../navigation/ambient_page_route.dart';
import '../../media_detail/logic/title_links.dart';
import '../../media_access/ui/book_listen_actions.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';
import '../logic/book_links.dart';
import 'chaptarr_releases_screen.dart';
import 'widgets/book_link_chips.dart';
import 'widgets/book_status.dart';
import 'widgets/format_badge.dart';
import 'widgets/format_picker.dart';
import 'widgets/search_actions.dart';

/// Opens the book detail bottom sheet for a title. Chaptarr stores a title's
/// ebook and audiobook as separate records (same foreignBookId); [records] holds
/// the 1–2 records. Returns `true` when the caller should refresh (an
/// Automatic/Interactive search was started). Mirrors the Sonarr episode sheet.
Future<bool?> showChaptarrBookDetailSheet(
  BuildContext context, {
  required String instanceId,
  required List<ChaptarrBook> records,
  String? bookTitle,
}) {
  return showAppSheet<bool>(
    context,
    builder: (_) => _ChaptarrBookDetailSheet(
      instanceId: instanceId,
      records: records,
      bookTitle: bookTitle,
    ),
  );
}

/// Bottom sheet for one title: status, formats, overview and recent history,
/// with Automatic/Interactive search actions that prompt for a format when the
/// title has both an ebook and an audiobook record.
class _ChaptarrBookDetailSheet extends ConsumerStatefulWidget {
  final String instanceId;
  final List<ChaptarrBook> records;
  final String? bookTitle;

  const _ChaptarrBookDetailSheet({
    required this.instanceId,
    required this.records,
    this.bookTitle,
  });

  @override
  ConsumerState<_ChaptarrBookDetailSheet> createState() =>
      _ChaptarrBookDetailSheetState();
}

class _ChaptarrBookDetailSheetState
    extends ConsumerState<_ChaptarrBookDetailSheet> {
  late final ChaptarrApiService _service;
  late List<ChaptarrBook> _records;
  List<({ChaptarrHistoryRecord record, BookFormat format})> _history = [];
  bool _historyLoading = true;
  String? _historyError;

  ChaptarrBook get _primary => _records.first;

  /// The first record that names an outside page. A title's ebook and
  /// audiobook records normally carry the same provider ids, but only one of
  /// them may have been matched to an edition Chaptarr knows a page for.
  List<TitleLink> get _links {
    for (final record in _records) {
      final links = bookLinks(record);
      if (links.isNotEmpty) return links;
    }
    return const [];
  }

  @override
  void initState() {
    super.initState();
    _service = ChaptarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    _records = List<ChaptarrBook>.from(widget.records);
    WidgetsBinding.instance.addPostFrameCallback((_) => _refreshDetails());
  }

  Future<void> _refreshDetails() async {
    await _refreshRecords();
    await _loadHistory();
  }

  Future<void> _refreshRecords() async {
    try {
      final records = await Future.wait(
        _records.map((record) => _service.getBookById(record.id)),
      );
      if (!mounted) return;
      setState(() => _records = records);
    } catch (_) {
      // Keep the records that opened the sheet. History still loads below and
      // one secondary endpoint must not block the visible book state.
    }
  }

  Future<void> _loadHistory() async {
    if (mounted) {
      setState(() {
        _historyLoading = true;
        _historyError = null;
      });
    }
    final results = await Future.wait(_records.map((record) async {
      try {
        final events = await _service.getBookHistory(record.id);
        return (events: events, format: record.format, failed: false);
      } catch (_) {
        return (
          events: <ChaptarrHistoryRecord>[],
          format: record.format,
          failed: true,
        );
      }
    }));
    final history = <({ChaptarrHistoryRecord record, BookFormat format})>[];
    final failedFormats = <BookFormat>{};
    for (final result in results) {
      if (result.failed) failedFormats.add(result.format);
      history.addAll(result.events.map(
        (event) => (record: event, format: result.format),
      ));
    }
    history.sort((a, b) =>
        (b.record.date ?? DateTime(0)).compareTo(a.record.date ?? DateTime(0)));
    final error = _historyErrorMessage(failedFormats, results.length);
    if (!mounted) return;
    setState(() {
      _history = history.take(8).toList();
      _historyError = error;
      _historyLoading = false;
    });
  }

  String? _historyErrorMessage(Set<BookFormat> failed, int total) {
    if (failed.isEmpty) return null;
    if (failed.length == total || failed.contains(BookFormat.unknown)) {
      return context.l10n.adminCouldNotLoadHistory;
    }
    final labels = [BookFormat.ebook, BookFormat.audiobook]
        .where(failed.contains)
        .map((format) => chaptarrFormatLabel(format, strings: context.l10n))
        .join(context.l10n.polishAndSeparator);
    return context.l10n.adminCouldNotLoadHistory2((labels).toString());
  }

  // Pick the format (when the title has both), close the sheet, then search.
  Future<void> _automaticSearch() async {
    final translations = context.l10n;
    final chosen = await pickFormatRecords(context, _records);
    if (chosen == null || !mounted) return;
    final primary = chosen.first;
    final messenger = ScaffoldMessenger.of(context);
    try {
      await _service.searchBook(chosen.map((record) => record.id).toList());
      messenger.showSnackBar(SnackBar(
          content: Text(translations.adminSearchingFor(
              (chaptarrFormatLabel(primary.format, strings: translations))
                  .toString(),
              (primary.title).toString()))));
    } catch (e) {
      messenger.showSnackBar(SnackBar(
          content:
              Text(translations.adminFailedToStartSearch((e).toString()))));
    }
    if (mounted) Navigator.of(context).pop(true);
  }

  Future<void> _interactiveSearch() async {
    final chosen = await pickInteractiveFormatRecord(context, _records);
    if (chosen == null || !mounted) return;
    final nav = Navigator.of(context, rootNavigator: true);
    Navigator.of(context).pop(true);
    nav.push(
      AmbientPageRoute(
        builder: (_) => ChaptarrReleasesScreen(
          instanceId: widget.instanceId,
          bookId: chosen.id,
          bookTitle: chosen.title,
        ),
      ),
    );
  }

  ({String label, Color color}) get _shortStatus {
    final line = bookFileStatusLine(_primary, strings: context.l10n);
    return (label: line.text, color: line.color);
  }

  @override
  Widget build(BuildContext context) {
    final title = _primary.title.isNotEmpty
        ? _primary.title
        : (widget.bookTitle ?? context.l10n.adminBook);
    final status = _shortStatus;
    final release = _primary.releaseDate;
    final knownRecords = _records
        .where((record) => record.format != BookFormat.unknown)
        .toList();
    final hasUnknownFormat =
        _records.any((record) => record.format == BookFormat.unknown);
    final links = _links;

    return AppSheet(
      padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 20,
                fontWeight: FontWeight.bold),
          ),
          if (_primary.author?.authorName.isNotEmpty ?? false) ...[
            const SizedBox(height: 4),
            Text(_primary.author!.authorName,
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 14)),
          ],
          const SizedBox(height: 6),
          if (release != null)
            Text(DateFormat('MMMM d, yyyy').format(release),
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 13)),
          const SizedBox(height: 12),
          Wrap(
            spacing: 6,
            runSpacing: 4,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              if (hasUnknownFormat)
                StatusPill(
                  text: context.l10n.adminBookFormatNeedsAttention,
                  color: AppTheme.requested,
                ),
              if (knownRecords.isEmpty && !hasUnknownFormat)
                StatusPill(text: status.label, color: status.color),
              if (knownRecords.isNotEmpty)
                ...[BookFormat.ebook, BookFormat.audiobook]
                    .where((format) =>
                        knownRecords.any((record) => record.format == format))
                    .map((format) {
                  final formatStatus = bookFormatStatusLine(
                      knownRecords, format,
                      strings: context.l10n);
                  return StatusPill(
                    text:
                        '${chaptarrFormatLabel(format, strings: context.l10n)}: ${formatStatus.text}',
                    color: formatStatus.color,
                  );
                }),
            ],
          ),
          if (_records.any((book) =>
                  book.format == BookFormat.audiobook && book.hasFile) &&
              (_primary.foreignBookId?.isNotEmpty ?? false))
            BookListenActions(
                instanceId: widget.instanceId,
                foreignBookId: _primary.foreignBookId!),
          if (links.isNotEmpty) ...[
            const SizedBox(height: 12),
            BookLinkChips(links),
          ],
          if (_primary.displayOverview case final overview?) ...[
            const SizedBox(height: 14),
            Text(overview,
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontSize: 14, height: 1.4)),
          ],
          const SizedBox(height: 16),
          Text(context.l10n.adminHistory,
              style: TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 13,
                  fontWeight: FontWeight.w600)),
          const SizedBox(height: 8),
          _buildHistory(),
          const SizedBox(height: 16),
          ChaptarrSearchActions(
            onFindAutomatically: _automaticSearch,
            onChooseDownload: _interactiveSearch,
          ),
        ],
      ),
    );
  }

  Widget _buildHistory() {
    if (_historyLoading) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 12),
        child: Center(
            child: SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(
                    strokeWidth: 2, color: AppTheme.accent))),
      );
    }
    if (_history.isEmpty && _historyError == null) {
      return Text(context.l10n.adminNoHistoryYet2,
          style: TextStyle(color: AppTheme.textSecondary, fontSize: 13));
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (_historyError != null)
          Row(
            children: [
              Expanded(
                child: Text(
                  _historyError!,
                  style:
                      const TextStyle(color: AppTheme.requested, fontSize: 13),
                ),
              ),
              TextButton(
                  onPressed: _loadHistory,
                  child: Text(context.l10n.adminRetry)),
            ],
          ),
        ..._history
            .map((h) => _HistoryTile(record: h.record, format: h.format)),
      ],
    );
  }
}

/// Exported for the wire-vocabulary test: every event name Chaptarr's
/// history enum can emit must map to a readable label, never the raw name.
String chaptarrBookHistoryEventLabel(ChaptarrHistoryRecord r,
    {AppLocalizations? strings}) {
  switch (r.eventType) {
    case 'grabbed':
      final indexer = r.indexer;
      return indexer != null && indexer.isNotEmpty
          ? (strings ?? lookupAppLocalizations(const Locale('en')))
              .adminGrabbedFrom((indexer).toString())
          : (strings ?? lookupAppLocalizations(const Locale('en')))
              .adminGrabbed;
    // Chaptarr's history enum spells whole-download imports downloadImported;
    // the previously matched downloadFolderImported never occurs on the wire.
    case 'bookFileImported':
    case 'downloadImported':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminImported;
    case 'bookImportIncomplete':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminImportIncomplete;
    case 'downloadFailed':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminDownloadFailed;
    case 'bookFileDeleted':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminFileDeleted;
    case 'bookFileRenamed':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminFileRenamed;
    case 'bookFileRetagged':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminFileRetagged;
    case 'bookFileConverted':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminFileConverted;
    case 'bookFileConversionFailed':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminConversionFailed;
    case 'downloadIgnored':
      return (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminDownloadIgnored;
    default:
      return r.eventType.isEmpty
          ? (strings ?? lookupAppLocalizations(const Locale('en'))).adminEvent
          : r.eventType;
  }
}

String _historyTime(DateTime? date, {AppLocalizations? strings}) {
  if (date == null) return '';
  final local = date.toLocal();
  final diff = DateTime.now().difference(local);
  final String rel;
  if (diff.inMinutes < 1) {
    rel = (strings ?? lookupAppLocalizations(const Locale('en'))).adminJustNow;
  } else if (diff.inMinutes < 60) {
    rel = (strings ?? lookupAppLocalizations(const Locale('en')))
        .adminMAgo((diff.inMinutes).toString());
  } else if (diff.inHours < 24) {
    rel = (strings ?? lookupAppLocalizations(const Locale('en')))
        .adminHAgo((diff.inHours).toString());
  } else if (diff.inDays < 30) {
    rel = (strings ?? lookupAppLocalizations(const Locale('en')))
        .adminDAgo((diff.inDays).toString());
  } else {
    rel = DateFormat.yMMMd(strings?.dateLocale ?? 'en_US').format(local);
  }
  return '$rel • ${DateFormat('MMM d, yyyy • h:mm a').format(local)}';
}

class _HistoryTile extends StatelessWidget {
  final ChaptarrHistoryRecord record;
  final BookFormat format;
  const _HistoryTile({required this.record, required this.format});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            record.sourceTitle.isNotEmpty
                ? record.sourceTitle
                : chaptarrBookHistoryEventLabel(record, strings: context.l10n),
            style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w500),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
          const SizedBox(height: 2),
          Text(_historyTime(record.date, strings: context.l10n),
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 11)),
          const SizedBox(height: 2),
          Text(chaptarrBookHistoryEventLabel(record, strings: context.l10n),
              style: const TextStyle(color: AppTheme.requested, fontSize: 12)),
          if (format != BookFormat.unknown) ...[
            const SizedBox(height: 4),
            ChaptarrFormatBadge(format: format),
          ],
        ],
      ),
    );
  }
}
