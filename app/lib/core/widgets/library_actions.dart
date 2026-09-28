import 'package:flutter/material.dart';
import '../theme/app_theme.dart';
import 'action_sheet.dart';

enum LibraryAction { search, interactive, edit, monitoring, refresh, rescan, remove }

/// One vocabulary for a tile's popup and its long-press/detail action sheet.
List<SheetAction<LibraryAction>> libraryActions(String noun, {bool? monitored}) => [
  const SheetAction(LibraryAction.search, Icons.search, 'Automatic search'),
  const SheetAction(LibraryAction.interactive, Icons.manage_search, 'Interactive search'),
  SheetAction(LibraryAction.edit, Icons.edit_outlined, 'Edit $noun'),
  SheetAction(LibraryAction.monitoring,
      monitored == true ? Icons.bookmark_border : Icons.bookmark_outline,
      monitored == null ? 'Manage monitoring…'
          : '${monitored ? 'Unmonitor' : 'Monitor'} $noun'),
  const SheetAction(LibraryAction.refresh, Icons.refresh, 'Refresh metadata'),
  const SheetAction(LibraryAction.rescan, Icons.folder_open, 'Rescan files'),
  const SheetAction(LibraryAction.remove, Icons.delete_outline, 'Remove…',
      color: AppTheme.error),
];

class LibraryActionMenu extends StatelessWidget {
  const LibraryActionMenu({super.key, required this.title,
    required this.actions, required this.onSelected});

  final String title;
  final List<SheetAction<LibraryAction>> actions;
  final ValueChanged<LibraryAction> onSelected;

  @override
  Widget build(BuildContext context) => PopupMenuButton<LibraryAction>(
    tooltip: 'Actions for $title',
    icon: const Icon(Icons.more_vert, color: AppTheme.textSecondary),
    color: AppTheme.surfaceVariant,
    onSelected: onSelected,
    itemBuilder: (_) => [for (final action in actions) ...[
      if (action.value == LibraryAction.remove) const PopupMenuDivider(),
      PopupMenuItem(value: action.value, enabled: action.enabled,
        child: Row(children: [
          Icon(action.icon, size: 18, color: action.color ?? AppTheme.textSecondary),
          const SizedBox(width: 10),
          Flexible(child: Text(action.label, style: TextStyle(color: action.color))),
        ])),
    ]],
  );
}

Future<void> showLibraryActionMenu(BuildContext context, {
  required String title,
  required List<SheetAction<LibraryAction>> actions,
  required ValueChanged<LibraryAction> onSelected,
}) async {
  final action = await showActionSheet(context, title: title, actions: actions);
  if (action != null && context.mounted) onSelected(action);
}

/// Keeping files is the default; dismissal/cancellation performs no mutation.
Future<bool?> confirmLibraryRemoval(BuildContext context, {
  required String title, required String noun, required String service,
}) {
  var deleteFiles = false;
  return showDialog<bool>(context: context, builder: (ctx) => StatefulBuilder(
    builder: (ctx, setState) => AlertDialog(
      title: Text('Remove $noun'),
      content: Column(mainAxisSize: MainAxisSize.min, children: [
        Text('Remove "$title" from $service?'),
        const SizedBox(height: 8),
        CheckboxListTile(value: deleteFiles,
          onChanged: (value) => setState(() => deleteFiles = value ?? false),
          title: const Text('Also delete files from disk'),
          contentPadding: EdgeInsets.zero,
          controlAffinity: ListTileControlAffinity.leading,
          activeColor: AppTheme.error),
      ]),
      actions: [
        TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
        TextButton(onPressed: () => Navigator.pop(ctx, deleteFiles),
          style: TextButton.styleFrom(foregroundColor: AppTheme.error),
          child: const Text('Remove')),
      ],
    ),
  ));
}
