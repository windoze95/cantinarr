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

  testWidgets('4K tag keeps clear of the status badge and rating, and is read',
      (tester) async {
    final semantics = tester.ensureSemantics();
    Future<void> pump({required bool is4K}) => tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: Center(
                child: MediaCard(
                  id: 42,
                  title: 'Project Hail Mary',
                  subtitle: '2026',
                  statusLabel: 'Available',
                  rating: 7.9,
                  is4K: is4K,
                  onTap: () {},
                ),
              ),
            ),
          ),
        );

    await pump(is4K: false);
    expect(find.text('4K'), findsNothing);

    await pump(is4K: true);
    final tag = tester.getRect(find.byKey(const ValueKey('media-card-4k')));
    final status = tester.getRect(find.text('Available'));
    final rating = tester.getRect(find.text('7.9'));
    expect(tag.overlaps(status), isFalse);
    expect(tag.overlaps(rating), isFalse);
    expect(tag.center.dx, greaterThan(rating.center.dx));
    expect(tag.center.dy, greaterThan(status.center.dy));
    expect(
      tester.getSemantics(find.byType(MediaCard)),
      matchesSemantics(
        label: 'Project Hail Mary, 2026, Available in 4K, Rated 7.9',
        isButton: true,
        hasTapAction: true,
      ),
    );
    semantics.dispose();
  });
}
