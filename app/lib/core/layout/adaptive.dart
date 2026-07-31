import 'package:flutter/widgets.dart';

/// Shared responsive breakpoints and desktop content-width helpers.
///
/// The app is mobile-first; at [desktopMinWidth] and up the chrome adapts:
/// [AppShell] swaps the hamburger drawer for a persistent sidebar whose active
/// module expands into per-page items, and the module shells drop their bottom
/// nav (the sidebar covers page switching). Keep every desktop check on this
/// class so the whole app flips layout at the same width.
abstract final class AppBreakpoints {
  static const double desktopMinWidth = 900;

  /// Width of the persistent desktop sidebar in AppShell.
  static const double sidebarWidth = 280;

  /// Readable column width for line-length-sensitive surfaces (search
  /// results, chat transcripts, forms) on desktop.
  static const double readableContentWidth = 800;

  static bool isDesktop(BuildContext context) =>
      MediaQuery.sizeOf(context).width >= desktopMinWidth;

  /// Phone-shaped screens (drives e.g. the collapse-on-scroll search bar).
  static bool isMobile(BuildContext context) =>
      MediaQuery.sizeOf(context).shortestSide < 600;

  /// Horizontal padding that keeps content at most [maxContentWidth] wide and
  /// centered within [totalWidth], never dropping below [minPadding]. Use as
  /// scrollable padding so the scroll surface stays full width (wheel and
  /// drag scrolling keep working in the side gutters) while the content
  /// column stays readable on wide windows.
  static double centeredContentPadding(
    double totalWidth, {
    double maxContentWidth = readableContentWidth,
    double minPadding = 16,
  }) {
    final padding = (totalWidth - maxContentWidth) / 2;
    return padding > minPadding ? padding : minPadding;
  }
}

/// Caps [child] at [maxWidth] and centers it horizontally — a no-op on
/// layouts already narrower than that. Wrap page bodies (forms, detail
/// content) with this so they read as a column instead of stretching across
/// a desktop window. For long infinite-scroll surfaces prefer padding-based
/// centering ([AppBreakpoints.centeredContentPadding]) so wheel scrolling
/// keeps working in the side gutters.
class CenteredContent extends StatelessWidget {
  final double maxWidth;
  final Widget child;

  const CenteredContent({
    super.key,
    this.maxWidth = AppBreakpoints.readableContentWidth,
    required this.child,
  });

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.topCenter,
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: maxWidth),
        child: child,
      ),
    );
  }
}
