import 'package:flutter/material.dart';

import '../theme/app_theme.dart';

/// The ambient canvas behind every Cantinarr route.
///
/// The effect is intentionally static and pointer-transparent: it gives bare
/// and translucent surfaces a shared cinematic depth without adding perpetual
/// animation, hit-test cost, or accessibility noise.
class AppAmbientBackground extends StatelessWidget {
  const AppAmbientBackground({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return ColoredBox(
      color: AppTheme.background,
      child: Stack(
        fit: StackFit.expand,
        children: [
          const ExcludeSemantics(
            child: IgnorePointer(
              child: DecoratedBox(
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                    colors: [
                      Color(0xFF1D130C),
                      AppTheme.background,
                      Color(0xFF120B07),
                    ],
                    stops: [0, 0.52, 1],
                  ),
                ),
              ),
            ),
          ),
          const ExcludeSemantics(
            child: IgnorePointer(
              child: DecoratedBox(
                decoration: BoxDecoration(
                  gradient: RadialGradient(
                    center: Alignment(1.05, -1.05),
                    radius: 1.2,
                    colors: [
                      Color(0x24F47A2E),
                      Color(0x0BF47A2E),
                      Colors.transparent,
                    ],
                    stops: [0, 0.36, 1],
                  ),
                ),
              ),
            ),
          ),
          const ExcludeSemantics(
            child: IgnorePointer(
              child: DecoratedBox(
                decoration: BoxDecoration(
                  gradient: RadialGradient(
                    center: Alignment(-1.1, 1.15),
                    radius: 1.15,
                    colors: [
                      Color(0x1FF2AC2D),
                      Color(0x08F2AC2D),
                      Colors.transparent,
                    ],
                    stops: [0, 0.38, 1],
                  ),
                ),
              ),
            ),
          ),
          child,
        ],
      ),
    );
  }
}
