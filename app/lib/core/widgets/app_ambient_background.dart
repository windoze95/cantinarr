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
///
/// A routed copy paints only while its route is moving. Once an opaque route
/// settles the navigator stops painting the page beneath it, and an ancestor
/// copy already paints these exact pixels, so a second copy would only repeat
/// four full-screen fills that the web renderer redraws on every frame. The
/// outermost copy has no ancestor and always paints.
class AppAmbientBackground extends StatefulWidget {
  const AppAmbientBackground({super.key, required this.child});

  final Widget child;

  @override
  State<AppAmbientBackground> createState() => _AppAmbientBackgroundState();
}

class _AppAmbientBackgroundState extends State<AppAmbientBackground> {
  /// The enclosing route's transition when an ancestor copy already paints
  /// beneath this one. Null means this copy always paints.
  Animation<double>? _routeAnimation;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    final ancestorPaints =
        context.dependOnInheritedWidgetOfExactType<_AmbientCanvasScope>() !=
            null;
    final animation = ancestorPaints && route != null && route.opaque
        ? route.animation
        : null;
    if (animation == _routeAnimation) return;
    _routeAnimation?.removeStatusListener(_routeStatusChanged);
    _routeAnimation = animation?..addStatusListener(_routeStatusChanged);
  }

  void _routeStatusChanged(AnimationStatus status) => setState(() {});

  @override
  void dispose() {
    _routeAnimation?.removeStatusListener(_routeStatusChanged);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    // Mirrors the navigator: a route that is not completed lets the page
    // beneath it paint, so its copy is needed exactly then.
    final paint = !(_routeAnimation?.isCompleted ?? false);
    final screen = MediaQuery.sizeOf(context);
    return _AmbientCanvasScope(
      // A transparent box still hit-tests as opaque, so a settled page keeps
      // absorbing taps on its bare areas.
      child: ColoredBox(
        color: paint ? AppTheme.background : Colors.transparent,
        child: Stack(
          fit: StackFit.expand,
          children: [
            ExcludeSemantics(
              child: IgnorePointer(
                child: paint
                    ? ClipRect(
                        child: OverflowBox(
                          alignment: Alignment.bottomRight,
                          minWidth: screen.width,
                          maxWidth: screen.width,
                          minHeight: screen.height,
                          maxHeight: screen.height,
                          child: const _AmbientGradients(),
                        ),
                      )
                    : const SizedBox.shrink(),
              ),
            ),
            widget.child,
          ],
        ),
      ),
    );
  }
}

/// Marks that an ancestor copy paints beneath its descendants. The outermost
/// copy finds none, so it always paints.
class _AmbientCanvasScope extends InheritedWidget {
  const _AmbientCanvasScope({required super.child});

  @override
  bool updateShouldNotify(_AmbientCanvasScope oldWidget) => false;
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
