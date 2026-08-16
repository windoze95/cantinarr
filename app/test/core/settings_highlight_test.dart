import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/settings_highlight.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// The decoration ring the highlight paints while active. Scoped to the
/// wrapper so ambient Material decorations can never match.
Finder _activeRing() => find.descendant(
      of: find.byType(SettingsHighlight),
      matching: find.byWidgetPredicate(
        (widget) =>
            widget is DecoratedBox &&
            widget.decoration is BoxDecoration &&
            (widget.decoration as BoxDecoration).border != null,
      ),
    );

Widget _harness({
  required String? highlightId,
  bool disableAnimations = false,
  StateSetter? Function(StateSetter setState)? captureSetState,
}) {
  return MaterialApp(
    theme: AppTheme.dark,
    home: MediaQuery(
      data: MediaQueryData(disableAnimations: disableAnimations),
      child: Scaffold(
        body: StatefulBuilder(
          builder: (context, setState) {
            captureSetState?.call(setState);
            return ListView(
              // Mirror the screen recipe: anchors far below the fold only
              // mount when the list builds everything.
              cacheExtent: SettingsHighlight.cacheExtentFor(highlightId),
              children: [
                for (var i = 0; i < 30; i++) SizedBox(height: 100, key: Key('filler-$i')),
                SettingsHighlight(
                  anchorId: 'test.target',
                  highlightId: highlightId,
                  child: const ListTile(title: Text('target')),
                ),
                for (var i = 0; i < 5; i++) const SizedBox(height: 100),
              ],
            );
          },
        ),
      ),
    ),
  );
}

ScrollableState _scrollable(WidgetTester tester) =>
    tester.state<ScrollableState>(find.byType(Scrollable).first);

void main() {
  testWidgets('scrolls to the matched anchor and flashes a fading ring',
      (tester) async {
    await tester.pumpWidget(_harness(highlightId: 'test.target'));
    // The post-frame trigger started the scroll and the flash. The first
    // pump gives their tickers a start time; the second lands the scroll,
    // putting the target onstage with the ring still mid-fade.
    await tester.pump();
    await tester.pump(AppTheme.motionSlow);
    expect(_scrollable(tester).position.pixels, greaterThan(2000));
    expect(_activeRing(), findsOneWidget);
    // Lands near the top of the viewport (alignment 0.1).
    final targetTop = tester.getTopLeft(find.text('target')).dy;
    expect(targetTop, greaterThanOrEqualTo(0));
    expect(targetTop, lessThan(150));

    // The ring decays to nothing once the fade completes.
    await tester.pumpAndSettle();
    expect(_activeRing(), findsNothing);
  });

  testWidgets('is pixel-inert when the highlight id does not match',
      (tester) async {
    await tester.pumpWidget(_harness(highlightId: 'test.other'));
    await tester.pumpAndSettle();
    expect(_scrollable(tester).position.pixels, 0);
    expect(_activeRing(), findsNothing);
  });

  testWidgets('is pixel-inert when no highlight is requested', (tester) async {
    await tester.pumpWidget(_harness(highlightId: null));
    await tester.pumpAndSettle();
    expect(_scrollable(tester).position.pixels, 0);
    expect(_activeRing(), findsNothing);
  });

  testWidgets('fires once: rebuilds do not re-scroll or re-flash',
      (tester) async {
    StateSetter? rebuild;
    await tester.pumpWidget(_harness(
      highlightId: 'test.target',
      captureSetState: (setState) => rebuild = setState,
    ));
    await tester.pumpAndSettle();
    expect(_scrollable(tester).position.pixels, greaterThan(2000));

    _scrollable(tester).position.jumpTo(0);
    await tester.pump();
    rebuild!(() {});
    await tester.pumpAndSettle();

    expect(_scrollable(tester).position.pixels, 0);
    expect(_activeRing(), findsNothing);
  });

  testWidgets('reduced motion jumps, holds the ring, then clears it',
      (tester) async {
    await tester.pumpWidget(
      _harness(highlightId: 'test.target', disableAnimations: true),
    );
    // The jump is synchronous in the post-frame trigger; one pump renders it.
    await tester.pump();
    expect(_scrollable(tester).position.pixels, greaterThan(2000));
    expect(_activeRing(), findsOneWidget);

    // The static ring clears via a plain Timer, which pumpAndSettle would
    // never flush — advance past it explicitly.
    await tester.pump(const Duration(seconds: 2));
    await tester.pump();
    expect(_activeRing(), findsNothing);
  });
}
