import 'package:flutter/material.dart';

import '../theme/app_theme.dart';

/// The ambient canvas behind every Cantinarr route.
///
/// The effect is intentionally static and pointer-transparent: it gives bare
/// and translucent surfaces a shared cinematic depth without adding perpetual
/// animation, hit-test cost, or accessibility noise.
///
/// Routed pages paint their own copy so transitions occlude the page beneath
/// (scaffolds are transparent by theme). Copies must be pixel-identical to the
/// app-level canvas even when painted in a box smaller than the screen (the
/// shell's content area sits below the top bar / right of the sidebar), so the
/// gradient layers are laid out at full-screen size anchored bottom-right —
/// the one corner every such box shares with the screen — and clipped to the
/// box. Anchoring by alignment alone would re-center the glows per box and
/// leave a brightness seam at the chrome boundary.
class AppAmbientBackground extends StatelessWidget {
  const AppAmbientBackground({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    final screen = MediaQuery.sizeOf(context);
    return ColoredBox(
      color: AppTheme.background,
      child: Stack(
        fit: StackFit.expand,
        children: [
          ExcludeSemantics(
            child: IgnorePointer(
              child: ClipRect(
                child: OverflowBox(
                  alignment: Alignment.bottomRight,
                  minWidth: screen.width,
                  maxWidth: screen.width,
                  minHeight: screen.height,
                  maxHeight: screen.height,
                  child: const _AmbientGradients(),
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

class _AmbientGradients extends StatelessWidget {
  const _AmbientGradients();

  @override
  Widget build(BuildContext context) {
    return const Stack(
      fit: StackFit.expand,
      children: [
        DecoratedBox(
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
        DecoratedBox(
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
        DecoratedBox(
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
      ],
    );
  }
}
