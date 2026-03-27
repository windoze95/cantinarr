import 'dart:math';
import 'package:flutter/material.dart';

/// Wraps a child widget with an animated shimmer glow border.
///
/// A bright gold arc sweeps continuously around the rounded border,
/// creating a "traveling light" effect. Two paint passes are used:
/// a sharp stroke and a blurred glow layer behind it.
class ShimmerBorderWrapper extends StatelessWidget {
  final Animation<double> animation;
  final double borderRadius;
  final Color accentColor;
  final Widget child;

  const ShimmerBorderWrapper({
    super.key,
    required this.animation,
    required this.borderRadius,
    required this.accentColor,
    required this.child,
  });

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: animation,
      builder: (context, child) {
        return CustomPaint(
          foregroundPainter: ShimmerBorderPainter(
            progress: animation.value,
            borderRadius: borderRadius,
            accentColor: accentColor,
          ),
          child: child,
        );
      },
      child: child,
    );
  }
}

class ShimmerBorderPainter extends CustomPainter {
  final double progress;
  final double borderRadius;
  final Color accentColor;

  ShimmerBorderPainter({
    required this.progress,
    required this.borderRadius,
    required this.accentColor,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final rect = Offset.zero & size;
    final rrect = RRect.fromRectAndRadius(
      rect.deflate(1.5),
      Radius.circular(borderRadius),
    );

    final startAngle = progress * 2 * pi;

    final gradient = SweepGradient(
      startAngle: startAngle,
      colors: [
        accentColor.withValues(alpha: 0.95),
        accentColor.withValues(alpha: 0.7),
        accentColor.withValues(alpha: 0.08),
        accentColor.withValues(alpha: 0.03),
        accentColor.withValues(alpha: 0.03),
        accentColor.withValues(alpha: 0.08),
        accentColor.withValues(alpha: 0.95),
      ],
      stops: const [0.0, 0.06, 0.15, 0.35, 0.70, 0.90, 1.0],
    );

    // Soft outer glow
    final glowPaint = Paint()
      ..shader = gradient.createShader(rect)
      ..style = PaintingStyle.stroke
      ..strokeWidth = 8.0
      ..maskFilter = const MaskFilter.blur(BlurStyle.normal, 6.0);
    canvas.drawRRect(rrect, glowPaint);

    // Sharp inner stroke
    final strokePaint = Paint()
      ..shader = gradient.createShader(rect)
      ..style = PaintingStyle.stroke
      ..strokeWidth = 2.0;
    canvas.drawRRect(rrect, strokePaint);
  }

  @override
  bool shouldRepaint(ShimmerBorderPainter old) => old.progress != progress;
}
