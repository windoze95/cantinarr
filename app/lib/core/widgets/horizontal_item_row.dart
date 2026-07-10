import 'package:flutter/material.dart';
import '../layout/adaptive.dart';
import '../theme/app_theme.dart';
import 'shimmer_loading.dart';

/// A horizontal scrolling row of items with optional loading state.
class HorizontalItemRow<T> extends StatefulWidget {
  final List<T> items;
  final bool isLoading;
  final Widget Function(T item) itemBuilder;
  final void Function(T item)? onItemAppear;
  final double height;
  final double itemSpacing;

  const HorizontalItemRow({
    super.key,
    required this.items,
    required this.isLoading,
    required this.itemBuilder,
    this.onItemAppear,
    this.height = 218,
    this.itemSpacing = 14,
  });

  @override
  State<HorizontalItemRow<T>> createState() => _HorizontalItemRowState<T>();
}

class _HorizontalItemRowState<T> extends State<HorizontalItemRow<T>> {
  final ScrollController _controller = ScrollController();
  int _lastPrefetchedLength = -1;
  bool _canScrollBack = false;
  bool _canScrollForward = false;

  @override
  void initState() {
    super.initState();
    _controller.addListener(_onScroll);
    _scheduleMetricsCheck();
  }

  @override
  void didUpdateWidget(covariant HorizontalItemRow<T> oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.items.length != widget.items.length ||
        oldWidget.isLoading != widget.isLoading) {
      _scheduleMetricsCheck();
    }
  }

  @override
  void dispose() {
    _controller
      ..removeListener(_onScroll)
      ..dispose();
    super.dispose();
  }

  void _scheduleMetricsCheck() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _onScroll();
    });
  }

  void _onScroll() {
    if (!_controller.hasClients) return;
    final position = _controller.position;
    final canScrollBack = position.pixels > position.minScrollExtent + 2;
    final canScrollForward = position.pixels < position.maxScrollExtent - 2;
    if (canScrollBack != _canScrollBack ||
        canScrollForward != _canScrollForward) {
      setState(() {
        _canScrollBack = canScrollBack;
        _canScrollForward = canScrollForward;
      });
    }

    // Trigger pagination once per item count, outside itemBuilder. Calling a
    // notifier while the framework is building the row causes fragile nested
    // rebuilds and duplicate network requests.
    if (widget.onItemAppear != null &&
        !widget.isLoading &&
        widget.items.isNotEmpty &&
        position.extentAfter < 520 &&
        _lastPrefetchedLength != widget.items.length) {
      _lastPrefetchedLength = widget.items.length;
      widget.onItemAppear!(widget.items.last);
    }
  }

  void _scrollBy(double fraction) {
    if (!_controller.hasClients) return;
    final position = _controller.position;
    final target = (_controller.offset + position.viewportDimension * fraction)
        .clamp(position.minScrollExtent, position.maxScrollExtent);
    _controller.animateTo(
      target,
      duration: MediaQuery.disableAnimationsOf(context)
          ? Duration.zero
          : AppTheme.motionMedium,
      curve: Curves.easeOutCubic,
    );
  }

  @override
  Widget build(BuildContext context) {
    final desktop = AppBreakpoints.isDesktop(context);
    final horizontalPadding = desktop ? 24.0 : 16.0;

    if (widget.items.isEmpty && widget.isLoading) {
      return SizedBox(
        height: widget.height,
        child: ListView.separated(
          scrollDirection: Axis.horizontal,
          padding: EdgeInsets.symmetric(horizontal: horizontalPadding),
          itemCount: 6,
          separatorBuilder: (_, __) => SizedBox(width: widget.itemSpacing),
          itemBuilder: (_, __) => const ShimmerCard(width: 100),
        ),
      );
    }

    return SizedBox(
      height: widget.height,
      child: Stack(
        children: [
          ListView.separated(
            controller: _controller,
            scrollDirection: Axis.horizontal,
            physics: const BouncingScrollPhysics(
              parent: AlwaysScrollableScrollPhysics(),
            ),
            padding: EdgeInsets.symmetric(horizontal: horizontalPadding),
            itemCount: widget.items.length + (widget.isLoading ? 2 : 0),
            separatorBuilder: (_, __) => SizedBox(width: widget.itemSpacing),
            itemBuilder: (context, index) {
              if (index >= widget.items.length) {
                return const ShimmerCard(width: 100);
              }
              return widget.itemBuilder(widget.items[index]);
            },
          ),
          if (desktop && _canScrollBack)
            _ShelfArrow(
              alignment: Alignment.centerLeft,
              icon: Icons.chevron_left_rounded,
              tooltip: 'Scroll backward',
              onPressed: () => _scrollBy(-0.72),
            ),
          if (desktop && _canScrollForward)
            _ShelfArrow(
              alignment: Alignment.centerRight,
              icon: Icons.chevron_right_rounded,
              tooltip: 'Scroll forward',
              onPressed: () => _scrollBy(0.72),
            ),
        ],
      ),
    );
  }
}

class _ShelfArrow extends StatelessWidget {
  final Alignment alignment;
  final IconData icon;
  final String tooltip;
  final VoidCallback onPressed;

  const _ShelfArrow({
    required this.alignment,
    required this.icon,
    required this.tooltip,
    required this.onPressed,
  });

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: alignment,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8),
        child: DecoratedBox(
          decoration: BoxDecoration(
            color: AppTheme.surfaceRaised.withValues(alpha: 0.94),
            shape: BoxShape.circle,
            border: Border.all(color: AppTheme.borderStrong),
            boxShadow: [
              BoxShadow(
                color: Colors.black.withValues(alpha: 0.32),
                blurRadius: 14,
              ),
            ],
          ),
          child: IconButton(
            onPressed: onPressed,
            tooltip: tooltip,
            icon: Icon(icon),
            color: AppTheme.textPrimary,
          ),
        ),
      ),
    );
  }
}
