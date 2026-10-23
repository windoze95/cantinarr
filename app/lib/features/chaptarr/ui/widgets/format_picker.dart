import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../core/widgets/app_sheet.dart';
import '../../data/chaptarr_models.dart';
import 'book_status.dart';

/// Prompts for one format and returns every record that represents it. Duplicate
/// records are deliberately kept together so one automatic search reaches all
/// of their Chaptarr IDs without showing indistinguishable format choices.
Future<List<ChaptarrBook>?> pickFormatRecords(
  BuildContext context,
  List<ChaptarrBook> records,
) async {
  if (records.isEmpty) return null;
  final groups = _recordsByFormat(records);
  if (groups.length == 1) return groups.first;
  return showAppSheet<List<ChaptarrBook>>(
    context,
    builder: (_) => _FormatPickerSheet(groups: groups),
  );
}

/// Chooses one exact record for interactive release search. A title with only
/// one record skips both prompts. When the chosen format has duplicate records,
/// a second picker identifies each by record ID and current state.
Future<ChaptarrBook?> pickInteractiveFormatRecord(
  BuildContext context,
  List<ChaptarrBook> records,
) async {
  final chosenFormat = await pickFormatRecords(context, records);
  if (chosenFormat == null || !context.mounted) return null;
  if (chosenFormat.length == 1) return chosenFormat.first;
  return showAppSheet<ChaptarrBook>(
    context,
    builder: (_) => _RecordPickerSheet(records: chosenFormat),
  );
}

List<List<ChaptarrBook>> _recordsByFormat(List<ChaptarrBook> records) {
  final groups = <BookFormat, List<ChaptarrBook>>{};
  for (final record in records) {
    (groups[record.format] ??= []).add(record);
  }
  return groups.values.toList();
}

IconData chaptarrFormatIcon(BookFormat f) => switch (f) {
      BookFormat.audiobook => Icons.headphones,
      BookFormat.ebook => Icons.menu_book,
      BookFormat.unknown => Icons.help_outline,
    };

String chaptarrFormatLabel(BookFormat f, {AppLocalizations? strings}) =>
    switch (f) {
      BookFormat.audiobook =>
        (strings ?? lookupAppLocalizations(const Locale('en'))).adminAudiobook,
      BookFormat.ebook =>
        (strings ?? lookupAppLocalizations(const Locale('en'))).adminEBook,
      BookFormat.unknown =>
        (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminUnknownFormat,
    };

class _FormatPickerSheet extends StatelessWidget {
  final List<List<ChaptarrBook>> groups;
  const _FormatPickerSheet({required this.groups});

  @override
  Widget build(BuildContext context) {
    return AppSheet(
      padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.l10n.adminWhichFormat,
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 18,
              fontWeight: FontWeight.bold,
            ),
          ),
          const SizedBox(height: 14),
          for (final group in groups)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: ListTile(
                contentPadding: const EdgeInsets.symmetric(horizontal: 12),
                leading: Icon(chaptarrFormatIcon(group.first.format),
                    color: AppTheme.accent),
                title: Text(
                  chaptarrFormatLabel(group.first.format,
                      strings: context.l10n),
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                subtitle: group.length > 1
                    ? Text(
                        context.l10n.adminRecords((group.length).toString()),
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 12,
                        ),
                      )
                    : null,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                  side: const BorderSide(color: AppTheme.border),
                ),
                onTap: () => Navigator.of(context).pop(group),
              ),
            ),
        ],
      ),
    );
  }
}

class _RecordPickerSheet extends StatelessWidget {
  final List<ChaptarrBook> records;
  const _RecordPickerSheet({required this.records});

  @override
  Widget build(BuildContext context) {
    final format =
        chaptarrFormatLabel(records.first.format, strings: context.l10n);
    return AppSheet(
      padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.l10n.adminWhichRecord,
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 18,
              fontWeight: FontWeight.bold,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            context.l10n
                .adminHasMoreThanOneLibraryRecordChoose((format).toString()),
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 13,
              height: 1.35,
            ),
          ),
          const SizedBox(height: 14),
          for (final record in records)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: ListTile(
                contentPadding: const EdgeInsets.symmetric(horizontal: 12),
                leading: Icon(chaptarrFormatIcon(record.format),
                    color: AppTheme.accent),
                title: Text(
                  context.l10n.adminRecord((record.id).toString()),
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                subtitle: Text(
                  bookFileStatusLine(record, strings: context.l10n).text,
                  style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 12,
                  ),
                ),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                  side: const BorderSide(color: AppTheme.border),
                ),
                onTap: () => Navigator.of(context).pop(record),
              ),
            ),
        ],
      ),
    );
  }
}
