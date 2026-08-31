import 'package:flutter/material.dart';

import '../theme/app_theme.dart';

/// The order control that sits on the right of a browse row's heading.
///
/// It names the current order rather than showing a bare icon: a shelf of
/// covers or faces gives no clue what it is ordered by, and an unlabelled
/// control leaves the reader to infer the order from the cards.
class SectionSortMenu<T> extends StatelessWidget {
  final List<T> options;
  final T selected;

  /// The menu label for an option.
  final String Function(T option) labelOf;
  final ValueChanged<T> onSelected;

  /// Describes what is being ordered, e.g. 'Sort authors'.
  final String tooltip;

  const SectionSortMenu({
    super.key,
    required this.options,
    required this.selected,
    required this.labelOf,
    required this.onSelected,
    required this.tooltip,
  });

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<T>(
      tooltip: tooltip,
      initialValue: selected,
      onSelected: onSelected,
      position: PopupMenuPosition.under,
      itemBuilder: (_) => [
        for (final option in options)
          PopupMenuItem(
            value: option,
            child: Row(
              children: [
                if (option == selected)
                  const Icon(Icons.check, size: 18, color: AppTheme.accent)
                else
                  const SizedBox(width: 18),
                const SizedBox(width: 8),
                Text(labelOf(option)),
              ],
            ),
          ),
      ],
      child: Container(
        padding: const EdgeInsets.fromLTRB(12, 7, 8, 7),
        decoration: BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: AppTheme.border),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              labelOf(selected),
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12.5,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(width: 4),
            const Icon(Icons.arrow_drop_down,
                size: 18, color: AppTheme.textSecondary),
          ],
        ),
      ),
    );
  }
}

/// The "showing N of M" note a capped browse row carries.
///
/// A row that simply stops at its cap reads as the whole library, so a row
/// holding some back says how many. Renders nothing when none are hidden.
class SectionTruncationNote extends StatelessWidget {
  final int shown;
  final int total;

  const SectionTruncationNote({
    super.key,
    required this.shown,
    required this.total,
  });

  @override
  Widget build(BuildContext context) {
    if (total <= shown) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(right: 10),
      child: Text(
        '$shown of $total',
        style: const TextStyle(
          color: AppTheme.textMuted,
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
