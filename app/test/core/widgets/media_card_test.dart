import 'package:cantinarr/core/widgets/media_card.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('media card exposes one label and activates from the keyboard',
      (tester) async {
    final semantics = tester.ensureSemantics();
    var activationCount = 0;

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MediaCard(
            id: 42,
            title: 'Project Hail Mary',
            subtitle: '2026',
            statusLabel: 'Available',
            rating: 7.9,
            onTap: () => activationCount++,
          ),
        ),
      ),
    );

    expect(
      tester.getSemantics(find.byType(MediaCard)),
      matchesSemantics(
        label: 'Project Hail Mary, 2026, Available, Rated 7.9',
        isButton: true,
        hasTapAction: true,
      ),
    );

    await tester.sendKeyEvent(LogicalKeyboardKey.tab);
    await tester.pump();
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();

    expect(activationCount, 1);
    semantics.dispose();
  });
}
