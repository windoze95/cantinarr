import 'package:flutter/material.dart';

import '../theme/app_theme.dart';

/// The "See all" affordance on a discovery row's heading, opening the row's
/// feed as a full grid. Compact enough (28px) to sit inside the heading's
/// existing height, so adding it never moves the row.
class SeeAllButton extends StatelessWidget {
  const SeeAllButton({
    super.key,
    required this.rowTitle,
    required this.onPressed,
  });

  /// The row it belongs to, for the accessibility label.
  final String rowTitle;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      label: 'See all $rowTitle',
      excludeSemantics: true,
      child: TextButton(
        onPressed: onPressed,
        style: TextButton.styleFrom(
          foregroundColor: AppTheme.textSecondary,
          minimumSize: const Size(0, 28),
          padding: const EdgeInsets.symmetric(horizontal: 8),
          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
          visualDensity: VisualDensity.compact,
        ),
        child: const Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              'See all',
              style: TextStyle(fontSize: 12.5, fontWeight: FontWeight.w600),
            ),
            SizedBox(width: 2),
            Icon(Icons.chevron_right_rounded, size: 16),
          ],
        ),
      ),
    );
  }
}
