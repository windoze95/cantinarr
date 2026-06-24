import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/chaptarr_models.dart';

/// Prompts the user to choose which format record (ebook vs audiobook) to act on
/// for a title Chaptarr stores as two records. Returns the chosen record, the
/// sole record when there's only one (no prompt), or null if dismissed.
Future<ChaptarrBook?> pickFormatRecord(
  BuildContext context,
  List<ChaptarrBook> records,
) async {
  if (records.isEmpty) return null;
  if (records.length == 1) return records.first;
  return showModalBottomSheet<ChaptarrBook>(
    context: context,
    backgroundColor: Colors.transparent,
    builder: (_) => _FormatPickerSheet(records: records),
  );
}

IconData chaptarrFormatIcon(BookFormat f) => switch (f) {
      BookFormat.audiobook => Icons.headphones,
      BookFormat.ebook => Icons.menu_book,
      BookFormat.unknown => Icons.help_outline,
    };

String chaptarrFormatLabel(BookFormat f) => switch (f) {
      BookFormat.audiobook => 'Audiobook',
      BookFormat.ebook => 'eBook',
      BookFormat.unknown => 'Unknown format',
    };

class _FormatPickerSheet extends StatelessWidget {
  final List<ChaptarrBook> records;
  const _FormatPickerSheet({required this.records});

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Container(
        padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
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
            const SizedBox(height: 18),
            const Text(
              'Which format?',
              style: TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 18,
                fontWeight: FontWeight.bold,
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
                    chaptarrFormatLabel(record.format),
                    style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontWeight: FontWeight.w600,
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
      ),
    );
  }
}
