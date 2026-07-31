import 'package:flutter/material.dart';

import '../theme/app_theme.dart';

/// A restrained elevated surface for grouped content.
///
/// Cantinarr uses depth selectively: this panel is intended for action docks,
/// auth forms, and grouped settings—not as a wrapper around every list row.
class AppPanel extends StatelessWidget {
  final Widget child;
  final EdgeInsetsGeometry padding;
  final EdgeInsetsGeometry? margin;
  final Color? accentColor;
  final double radius;

  const AppPanel({
    super.key,
    required this.child,
    this.padding = const EdgeInsets.all(AppTheme.spaceLg),
    this.margin,
    this.accentColor,
    this.radius = AppTheme.radiusLarge,
  });

  @override
  Widget build(BuildContext context) {
    final accent = accentColor ?? AppTheme.signal;
    return Container(
      margin: margin,
      padding: padding,
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [
            AppTheme.surfaceRaised.withValues(alpha: 0.94),
            AppTheme.surfaceVariant.withValues(alpha: 0.9),
          ],
        ),
        borderRadius: BorderRadius.circular(radius),
        border: Border.all(color: AppTheme.border),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: 0.25),
            blurRadius: 22,
            offset: const Offset(0, 10),
          ),
          BoxShadow(
            color: accent.withValues(alpha: 0.035),
            blurRadius: 24,
          ),
        ],
      ),
      child: child,
    );
  }
}
