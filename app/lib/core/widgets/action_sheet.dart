import 'package:flutter/material.dart';
import '../theme/app_theme.dart';
import 'app_sheet.dart';

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
  return showAppSheet<T>(
    context,
    // The rows run edge to edge, so the sheet pads only the title.
    builder: (ctx) => AppSheet(
      padding: EdgeInsets.zero,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 0, 20, 4),
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
                        color: a.color ?? AppTheme.textPrimary, fontSize: 15)),
                onTap: () => Navigator.pop(ctx, a.value),
              )),
          const SizedBox(height: AppTheme.spaceSm),
        ],
      ),
    ),
  );
}
