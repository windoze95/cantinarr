import 'package:flutter/material.dart';
import '../theme/app_theme.dart';

/// One row in an [showActionSheet] menu.
class SheetAction<T> {
  final T value;
  final IconData icon;
  final String label;
  final Color? color;

  const SheetAction(this.value, this.icon, this.label, {this.color});
}

/// Bottom sheet of actions for one item (a series, a season, …): drag handle,
/// item title, then a tappable row per action. Resolves to the chosen action's
/// value, or null when dismissed.
Future<T?> showActionSheet<T>(
  BuildContext context, {
  required String title,
  required List<SheetAction<T>> actions,
}) {
  return showModalBottomSheet<T>(
    context: context,
    backgroundColor: Colors.transparent,
    builder: (ctx) => Container(
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      padding: EdgeInsets.only(bottom: MediaQuery.of(ctx).padding.bottom + 8),
      // Scrolls when the actions don't fit the sheet's max height (short or
      // landscape screens) instead of overflowing.
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const SizedBox(height: 12),
            Container(
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: AppTheme.textSecondary,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 16, 20, 4),
              child: Text(
                title,
                style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 17,
                    fontWeight: FontWeight.bold),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            ...actions.map((a) => ListTile(
                  leading: Icon(a.icon, color: a.color ?? AppTheme.accent),
                  title: Text(a.label,
                      style: TextStyle(
                          color: a.color ?? AppTheme.textPrimary,
                          fontSize: 15)),
                  onTap: () => Navigator.pop(ctx, a.value),
                )),
          ],
        ),
      ),
    ),
  );
}
