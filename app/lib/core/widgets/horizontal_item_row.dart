import 'package:flutter/material.dart';
import 'shimmer_loading.dart';

/// A horizontal scrolling row of items with optional loading state.
class HorizontalItemRow<T> extends StatelessWidget {
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
    this.height = 190,
    this.itemSpacing = 12,
  });

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty && isLoading) {
      return SizedBox(
        height: height,
        child: ListView.separated(
          scrollDirection: Axis.horizontal,
          padding: const EdgeInsets.symmetric(horizontal: 16),
          itemCount: 6,
          separatorBuilder: (_, __) => SizedBox(width: itemSpacing),
          itemBuilder: (_, __) => const ShimmerCard(width: 100),
        ),
      );
    }

    return SizedBox(
      height: height,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        itemCount: items.length + (isLoading ? 2 : 0),
        separatorBuilder: (_, __) => SizedBox(width: itemSpacing),
        itemBuilder: (context, index) {
          if (index >= items.length) {
            return const ShimmerCard(width: 100);
          }
          final item = items[index];
          // Trigger prefetch callback when nearing the end.
          if (onItemAppear != null && index >= items.length - 5) {
            onItemAppear!(item);
          }
          return itemBuilder(item);
        },
      ),
    );
  }
}
