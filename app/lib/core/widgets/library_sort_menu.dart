import 'package:cantinarr/l10n/shell_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../models/library_sort.dart';
import '../theme/app_theme.dart';

class LibrarySortMenu extends StatelessWidget {
  const LibrarySortMenu(
      {super.key,
      required this.module,
      required this.selection,
      required this.onSelected});
  final String module;
  final LibrarySortSelection selection;
  final ValueChanged<LibrarySortField> onSelected;

  @override
  Widget build(BuildContext context) => PopupMenuButton<LibrarySortField>(
        // Keep the menu's automatic and user scrolling outside AppShell's page
        // scroll listener, which controls the collapsing search bar.
        useRootNavigator: true,
        tooltip: context.l10n.commonSortDescription(
            localizeShellLabel(context, selection.field.label(module)),
            selection.ascending
                ? context.l10n.commonAscending.toLowerCase()
                : context.l10n.commonDescending.toLowerCase()),
        icon: const Icon(Icons.sort_rounded),
        initialValue: selection.field,
        color: AppTheme.surfaceRaised,
        constraints: BoxConstraints(
            maxWidth: math.min(340, MediaQuery.sizeOf(context).width - 32)),
        onSelected: onSelected,
        itemBuilder: (_) => [
          for (final field in librarySortFields(module))
            PopupMenuItem(
                value: field,
                child: Row(children: [
                  Expanded(
                      child: Text(
                          localizeShellLabel(context, field.label(module)),
                          style: TextStyle(
                              color: field == selection.field
                                  ? AppTheme.accent
                                  : AppTheme.textPrimary))),
                  const SizedBox(width: 12),
                  if (field == selection.field)
                    Icon(
                        selection.ascending
                            ? Icons.arrow_upward
                            : Icons.arrow_downward,
                        size: 18,
                        color: AppTheme.accent,
                        semanticLabel: selection.ascending
                            ? context.l10n.commonAscending
                            : context.l10n.commonDescending)
                  else
                    const SizedBox(width: 18),
                ])),
        ],
      );
}
