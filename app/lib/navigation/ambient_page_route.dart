import 'package:flutter/material.dart';

import '../core/widgets/app_ambient_background.dart';

/// A [MaterialPageRoute] whose page paints the shared ambient backdrop.
///
/// Cantinarr scaffolds are transparent by theme, so a route that doesn't paint
/// its own backdrop lets the screen beneath it show through while a transition
/// (or iOS swipe-back) is in flight — both screens render at once as a jarring
/// double exposure. Use this for every imperative full-screen push instead of
/// a bare [MaterialPageRoute]; the backdrop is pixel-identical to the app-level
/// ambient canvas, so the page looks unchanged at rest.
class AmbientPageRoute<T> extends MaterialPageRoute<T> {
  AmbientPageRoute({required WidgetBuilder builder})
      : super(
          builder: (context) => AppAmbientBackground(child: builder(context)),
        );
}
