import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../storage/library_view_preferences.dart';
import '../theme/app_theme.dart';

/// Both presentations build only the visible portion of a library. Scroll
/// offsets belong to the instance and layout, never to a different library.
class LibraryCollection extends StatelessWidget {
  final LibraryViewMode viewMode;
  final int itemCount;
  final IndexedWidgetBuilder itemBuilder;
  final String emptyMessage;
  final String scrollKey;
  final bool embedded;

  const LibraryCollection({
    super.key,
    required this.viewMode,
    required this.itemCount,
    required this.itemBuilder,
    required this.emptyMessage,
    required this.scrollKey,
    this.embedded = false,
  });

  @override
  Widget build(BuildContext context) {
    final physics = embedded
        ? const NeverScrollableScrollPhysics()
        : const AlwaysScrollableScrollPhysics();
    final key = PageStorageKey('$scrollKey-${viewMode.name}');
    if (itemCount == 0) {
      return CustomScrollView(
        key: key,
        shrinkWrap: embedded,
        physics: physics,
        slivers: [
          SliverFillRemaining(
            hasScrollBody: false,
            child: Center(child: Text(emptyMessage,
                style: const TextStyle(color: AppTheme.textSecondary))),
          ),
        ],
      );
    }
    if (viewMode == LibraryViewMode.list) {
      return ListView.separated(
        key: key,
        shrinkWrap: embedded,
        physics: physics,
        itemCount: itemCount,
        separatorBuilder: (_, __) =>
            const Divider(color: AppTheme.border, height: 1),
        itemBuilder: itemBuilder,
      );
    }
    return LayoutBuilder(builder: (context, constraints) {
      const gap = 12.0;
      final width = constraints.maxWidth - 24;
      final columns = math.max(3, ((width + gap) / (200 + gap)).ceil());
      // Lazy rows allow metadata to wrap at large accessibility text sizes
      // without guessing a fixed card height or building the whole library.
      return ListView.separated(
        key: key,
        padding: const EdgeInsets.fromLTRB(12, 8, 12, 24),
        shrinkWrap: embedded,
        physics: physics,
        itemCount: (itemCount / columns).ceil(),
        separatorBuilder: (_, __) => const SizedBox(height: gap),
        itemBuilder: (context, row) => IntrinsicHeight(
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (var column = 0; column < columns; column++) ...[
                if (column > 0) const SizedBox(width: gap),
                Expanded(child: row * columns + column < itemCount
                    ? itemBuilder(context, row * columns + column)
                    : const SizedBox.shrink()),
              ],
            ],
          ),
        ),
      );
    });
  }
}

/// Shared presentation keeps the module's status and actions identical in
/// either layout. The action button is a separate keyboard/pointer target.
class LibraryItem extends StatelessWidget {
  final bool grid;
  final String name;
  final Widget artwork;
  final Widget details;
  final Widget? actions;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;
  final double artworkAspectRatio;

  const LibraryItem({
    super.key,
    required this.grid,
    required this.name,
    required this.artwork,
    required this.details,
    this.actions,
    this.onTap,
    this.onLongPress,
    this.artworkAspectRatio = 2 / 3,
  });

  @override
  Widget build(BuildContext context) {
    final title = Tooltip(
      message: name,
      // Hover/semantics expose the full name; touch long-press belongs to
      // the library's existing item action sheet.
      triggerMode: TooltipTriggerMode.manual,
      child: Text(name,
        maxLines: grid ? 2 : 1,
        overflow: TextOverflow.ellipsis,
        style: TextStyle(color: AppTheme.textPrimary,
            fontSize: grid ? 14 : null, height: grid ? 1.25 : null,
            fontWeight: FontWeight.w500),
      ),
    );
    if (!grid) {
      return ListTile(
        onTap: onTap,
        onLongPress: onLongPress,
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        leading: ClipRRect(
          borderRadius: BorderRadius.circular(6),
          child: SizedBox(width: 45, height: 67, child: artwork),
        ),
        title: title,
        subtitle: details,
        trailing: actions,
      );
    }
    return Material(
      color: AppTheme.surface,
      borderRadius: BorderRadius.circular(AppTheme.radiusSmall),
      clipBehavior: Clip.antiAlias,
      child: Stack(
        fit: StackFit.passthrough,
        children: [
          InkWell(
            onTap: onTap,
            onLongPress: onLongPress,
            focusColor: AppTheme.accent.withValues(alpha: 0.2),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                AspectRatio(aspectRatio: artworkAspectRatio, child: artwork),
                Padding(
                  padding: const EdgeInsets.fromLTRB(8, 10, 8, 8),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [title, const SizedBox(height: 6), details],
                  ),
                ),
              ],
            ),
          ),
          if (actions != null)
            Positioned(
              top: 4, right: 4,
              child: Material(
                color: AppTheme.surface.withValues(alpha: 0.92),
                borderRadius: BorderRadius.circular(AppTheme.radiusSmall),
                child: actions,
              ),
            ),
        ],
      ),
    );
  }
}
