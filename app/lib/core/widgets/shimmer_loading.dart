import 'package:flutter/material.dart';
import 'package:shimmer/shimmer.dart';
import '../theme/app_theme.dart';

/// A shimmering placeholder card used during loading states.
class ShimmerCard extends StatelessWidget {
  final double width;
  final double? height;

  const ShimmerCard({super.key, required this.width, this.height});

  @override
  Widget build(BuildContext context) {
    final placeholder = SizedBox(
      width: width,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          AspectRatio(
            aspectRatio: 2 / 3,
            child: Container(
              decoration: BoxDecoration(
                color: AppTheme.surfaceVariant,
                borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
              ),
            ),
          ),
          const SizedBox(height: 9),
          Container(
            height: 12,
            width: width * 0.8,
            decoration: BoxDecoration(
              color: AppTheme.surfaceVariant,
              borderRadius: BorderRadius.circular(4),
            ),
          ),
        ],
      ),
    );
    if (MediaQuery.disableAnimationsOf(context)) return placeholder;
    return Shimmer.fromColors(
      baseColor: AppTheme.surfaceVariant,
      highlightColor: AppTheme.surfaceRaised,
      child: placeholder,
    );
  }
}

/// A shimmering placeholder for text lines.
class ShimmerLine extends StatelessWidget {
  final double width;
  final double height;

  const ShimmerLine({
    super.key,
    this.width = double.infinity,
    this.height = 14,
  });

  @override
  Widget build(BuildContext context) {
    final placeholder = Container(
      width: width,
      height: height,
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(4),
      ),
    );
    if (MediaQuery.disableAnimationsOf(context)) return placeholder;
    return Shimmer.fromColors(
      baseColor: AppTheme.surfaceVariant,
      highlightColor: AppTheme.surfaceRaised,
      child: placeholder,
    );
  }
}
