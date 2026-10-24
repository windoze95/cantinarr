import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/app_ambient_background.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Each painting copy lays out three gradient layers.
int _paintingCopies(WidgetTester tester) =>
    tester
        .widgetList<DecoratedBox>(find.byType(DecoratedBox))
        .where((box) => (box.decoration as BoxDecoration?)?.gradient != null)
        .length ~/
    3;

void main() {
  testWidgets('a copy with no canvas above it always paints', (tester) async {
    await tester.pumpWidget(const MaterialApp(
      home: AppAmbientBackground(child: Text('page')),
    ));
    await tester.pumpAndSettle();

    expect(_paintingCopies(tester), 1);
    final base = tester.widget<ColoredBox>(find
        .descendant(
            of: find.byType(AppAmbientBackground),
            matching: find.byType(ColoredBox))
        .first);
    expect(base.color, AppTheme.background);
  });

  testWidgets('a routed copy paints only while its route is moving',
      (tester) async {
    final navigator = GlobalKey<NavigatorState>();
    await tester.pumpWidget(MaterialApp(
      navigatorKey: navigator,
      builder: (context, child) => AppAmbientBackground(child: child!),
      home: const AppAmbientBackground(child: Text('home')),
    ));
    await tester.pumpAndSettle();

    // At rest the app canvas already shows the routed copy's exact pixels.
    expect(_paintingCopies(tester), 1);

    navigator.currentState!.push(MaterialPageRoute<void>(
      builder: (_) => const AppAmbientBackground(child: Text('next')),
    ));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 50));

    // Mid-transition the incoming page must occlude the one beneath it.
    expect(find.text('home'), findsOneWidget);
    expect(_paintingCopies(tester), 2);

    await tester.pumpAndSettle();
    expect(find.text('home'), findsNothing);
    expect(_paintingCopies(tester), 1);

    navigator.currentState!.pop();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 50));
    expect(_paintingCopies(tester), 2);

    await tester.pumpAndSettle();
    expect(find.text('next'), findsNothing);
    expect(_paintingCopies(tester), 1);
  });

  testWidgets('a copy in a see-through route keeps painting', (tester) async {
    final navigator = GlobalKey<NavigatorState>();
    await tester.pumpWidget(MaterialApp(
      navigatorKey: navigator,
      builder: (context, child) => AppAmbientBackground(child: child!),
      home: const Text('home'),
    ));
    navigator.currentState!.push(PageRouteBuilder<void>(
      opaque: false,
      pageBuilder: (_, __, ___) =>
          const AppAmbientBackground(child: Text('overlay')),
    ));
    await tester.pumpAndSettle();

    // The page beneath stays painted, so only this copy can hide it.
    expect(find.text('home'), findsOneWidget);
    expect(_paintingCopies(tester), 2);
  });
}
