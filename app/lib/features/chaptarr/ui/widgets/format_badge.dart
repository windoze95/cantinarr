import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/chaptarr_models.dart';

/// A small themed chip marking a book/edition/file's medium: an audiobook
/// (headphones) or an ebook (book). Audiobooks are first-class, so they get the
/// accent colour; ebooks read as a quieter warm info tone. Unknown formats
/// render nothing.
class ChaptarrFormatBadge extends StatelessWidget {
  final BookFormat format;

  const ChaptarrFormatBadge({super.key, required this.format});

  @override
  Widget build(BuildContext context) {
    final ({IconData icon, String label, Color color})? spec = switch (format) {
      BookFormat.audiobook => (
          icon: Icons.headphones,
          label: 'Audiobook',
          color: AppTheme.accent,
        ),
      BookFormat.ebook => (
          icon: Icons.menu_book,
          label: 'eBook',
          color: AppTheme.downloading,
        ),
      BookFormat.unknown => null,
    };
    if (spec == null) return const SizedBox.shrink();

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: spec.color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(spec.icon, size: 12, color: spec.color),
          const SizedBox(width: 4),
          Text(
            spec.label,
            style: TextStyle(
                color: spec.color, fontSize: 10.5, fontWeight: FontWeight.w500),
          ),
        ],
      ),
    );
  }
}
