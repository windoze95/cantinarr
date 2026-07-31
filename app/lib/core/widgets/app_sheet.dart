import 'package:flutter/material.dart';

import '../theme/app_theme.dart';

/// Opens the app's standard bottom sheet.
///
/// The card itself — surface colour, hairline border, rounded top edge and the
/// single drag handle — comes from `AppTheme.dark`'s `bottomSheetTheme`, so
/// [builder] must not paint a background or draw a handle of its own. Doing
/// both is what used to render every sheet twice over: the themed card outline
/// floating 48px above the body's own card, with a drag handle on each.
///
/// Wrap the body in [AppSheet] so it fills the sheet's width, scrolls when the
/// content is taller than the screen allows, and keeps its last line clear of
/// the keyboard and the home indicator.
Future<T?> showAppSheet<T>(
  BuildContext context, {
  required WidgetBuilder builder,
  bool isDismissible = true,
  bool enableDrag = true,
  bool useRootNavigator = false,
}) {
  return showModalBottomSheet<T>(
    context: context,
    // [AppSheet] caps its own height. Without this the framework caps every
    // sheet at 9/16 of the screen and silently clips whatever doesn't fit.
    isScrollControlled: true,
    isDismissible: isDismissible,
    enableDrag: enableDrag,
    useRootNavigator: useRootNavigator,
    builder: builder,
  );
}

/// The body of a sheet opened with [showAppSheet]: full sheet width, capped at
/// [maxHeightFraction] of the screen, scrolling past that instead of clipping,
/// and padded clear of the keyboard and the home indicator.
///
/// Paints nothing — the sheet's card and drag handle come from the theme.
class AppSheet extends StatelessWidget {
  const AppSheet({
    super.key,
    required this.child,
    this.padding = const EdgeInsets.symmetric(horizontal: AppTheme.spaceXl),
    this.maxHeightFraction = 0.85,
  });

  /// The sheet's content. Usually a `Column(mainAxisSize: MainAxisSize.min)`.
  final Widget child;

  /// Padding around [child]. The bottom inset is added to whatever it asks
  /// for, so a sheet only states its own visual padding.
  final EdgeInsets padding;

  /// Height cap, as a fraction of the screen. The default leaves the status
  /// bar and a slice of the page visible behind a full-height sheet.
  final double maxHeightFraction;

  @override
  Widget build(BuildContext context) {
    final media = MediaQuery.of(context);
    return ConstrainedBox(
      constraints: BoxConstraints(
        maxHeight: media.size.height * maxHeightFraction,
      ),
      child: SingleChildScrollView(
        padding: padding.copyWith(
          bottom:
              padding.bottom + media.padding.bottom + media.viewInsets.bottom,
        ),
        // Fills the sheet's width; without it the sheet shrink-wraps its
        // widest line and floats as a narrow card in the middle of the screen.
        child: SizedBox(width: double.infinity, child: child),
      ),
    );
  }
}
