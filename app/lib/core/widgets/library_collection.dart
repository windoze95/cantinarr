import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../storage/library_view_preferences.dart';
import '../theme/app_theme.dart';

/// Both presentations build only the visible portion of a library. A view
/// change aligns the first visible item in the new layout.
class LibraryCollection extends StatefulWidget {
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
  State<LibraryCollection> createState() => _LibraryCollectionState();
}

class _LibraryCollectionState extends State<LibraryCollection> {
  final _controller = ScrollController();
  final _viewportKey = GlobalKey();
  Map<int, GlobalKey> _rowKeys = {};
  int _columns = 1;
  int? _pendingAnchor;
  int _anchorAttempts = 0;

  @override
  void didUpdateWidget(LibraryCollection oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.scrollKey != widget.scrollKey) {
      _rowKeys = {};
      _pendingAnchor = null;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _controller.hasClients) _controller.jumpTo(0);
      });
    } else if (oldWidget.viewMode != widget.viewMode) {
      final atTop = !_controller.hasClients ||
          _controller.position.pixels <=
              _controller.position.minScrollExtent + 2;
      final visibleRow = atTop ? null : _firstVisibleRow();
      _pendingAnchor = visibleRow == null || widget.itemCount == 0
          ? null
          : math.min(widget.itemCount - 1, visibleRow * _columns);
      _rowKeys = {};
      _anchorAttempts = 0;
      if (_pendingAnchor != null) {
        WidgetsBinding.instance.addPostFrameCallback((_) => _alignAnchor());
      }
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  GlobalKey _rowKey(int index) =>
      _rowKeys.putIfAbsent(index, () => GlobalKey());

  RenderBox? get _viewportBox =>
      _viewportKey.currentContext?.findRenderObject() as RenderBox?;

  List<(int, double, double)> _visibleRows() {
    final viewport = _viewportBox;
    if (viewport == null || !viewport.hasSize) return [];
    final rows = <(int, double, double)>[];
    for (final entry in _rowKeys.entries) {
      final box = entry.value.currentContext?.findRenderObject() as RenderBox?;
      if (box == null || !box.hasSize) continue;
      final top = box.localToGlobal(Offset.zero, ancestor: viewport).dy;
      if (top < viewport.size.height && top + box.size.height > 0) {
        rows.add((entry.key, top, box.size.height));
      }
    }
    rows.sort((a, b) => a.$1.compareTo(b.$1));
    return rows;
  }

  int? _firstVisibleRow() {
    final rows = _visibleRows();
    return rows.isEmpty ? null : rows.first.$1;
  }

  void _alignAnchor() {
    if (!mounted || !_controller.hasClients || _pendingAnchor == null) return;
    final target = widget.viewMode == LibraryViewMode.grid
        ? _pendingAnchor! ~/ _columns : _pendingAnchor!;
    final targetContext = _rowKeys[target]?.currentContext;
    if (targetContext != null) {
      Scrollable.ensureVisible(targetContext, alignment: 0,
          duration: Duration.zero);
      _pendingAnchor = null;
      return;
    }
    final rows = _visibleRows();
    if (rows.isEmpty || _anchorAttempts++ >= 6) {
      _pendingAnchor = null;
      return;
    }
    final extent = rows.length > 1
        ? (rows.last.$2 - rows.first.$2) / (rows.last.$1 - rows.first.$1)
        : rows.first.$3 + (widget.viewMode == LibraryViewMode.grid ? 12 : 1);
    final position = _controller.position;
    final offset = (position.pixels + (target - rows.first.$1) * extent)
        .clamp(position.minScrollExtent, position.maxScrollExtent);
    if ((position.pixels - offset).abs() < 1) {
      _pendingAnchor = null;
      return;
    }
    _controller.jumpTo(offset);
    WidgetsBinding.instance.addPostFrameCallback((_) => _alignAnchor());
  }

  @override
  Widget build(BuildContext context) {
    final physics = widget.embedded
        ? const NeverScrollableScrollPhysics()
        : const AlwaysScrollableScrollPhysics();
    final key = PageStorageKey(widget.scrollKey);
    if (widget.itemCount == 0) {
      return SizedBox(key: _viewportKey, child: CustomScrollView(
        key: key,
        controller: _controller,
        shrinkWrap: widget.embedded,
        physics: physics,
        slivers: [
          SliverFillRemaining(
            hasScrollBody: false,
            child: Center(child: Text(widget.emptyMessage,
                style: const TextStyle(color: AppTheme.textSecondary))),
          ),
        ],
      ));
    }
    if (widget.viewMode == LibraryViewMode.list) {
      _columns = 1;
      return SizedBox(key: _viewportKey, child: ListView.separated(
        key: key,
        controller: _controller,
        shrinkWrap: widget.embedded,
        physics: physics,
        itemCount: widget.itemCount,
        separatorBuilder: (_, __) =>
            const Divider(color: AppTheme.border, height: 1),
        itemBuilder: (context, index) => KeyedSubtree(
          key: _rowKey(index),
          child: widget.itemBuilder(context, index),
        ),
      ));
    }
    return SizedBox(key: _viewportKey, child: LayoutBuilder(builder: (context, constraints) {
      const gap = 12.0;
      final width = constraints.maxWidth - 24;
      final columns = math.max(3, ((width + gap) / (200 + gap)).ceil());
      _columns = columns;
      // Lazy rows allow metadata to wrap at large accessibility text sizes
      // without guessing a fixed card height or building the whole library.
      return ListView.separated(
        key: key,
        controller: _controller,
        padding: const EdgeInsets.fromLTRB(12, 8, 12, 24),
        shrinkWrap: widget.embedded,
        physics: physics,
        itemCount: (widget.itemCount / columns).ceil(),
        separatorBuilder: (_, __) => const SizedBox(height: gap),
        itemBuilder: (context, row) => KeyedSubtree(
          key: _rowKey(row),
          child: IntrinsicHeight(
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (var column = 0; column < columns; column++) ...[
                if (column > 0) const SizedBox(width: gap),
                Expanded(child: row * columns + column < widget.itemCount
                    ? widget.itemBuilder(context, row * columns + column)
                    : const SizedBox.shrink()),
              ],
            ],
          ),
          ),
        ),
      );
    }));
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
        maxLines: 1,
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
