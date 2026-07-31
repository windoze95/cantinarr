import 'package:cantinarr/core/widgets/day_sections.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:intl/intl.dart';

void main() {
  group('groupItemsByDay', () {
    test('groups by calendar day, days ascending, items time ascending', () {
      final lateNight = DateTime(2026, 7, 2, 23, 30);
      final earlier = DateTime(2026, 7, 1, 9);
      final morning = DateTime(2026, 7, 2, 8, 15);

      final groups =
          groupItemsByDay([lateNight, earlier, morning], (d) => d);

      expect(groups.map((g) => g.key).toList(),
          [DateTime(2026, 7, 1), DateTime(2026, 7, 2)]);
      expect(groups[0].value, [earlier]);
      expect(groups[1].value, [morning, lateNight]);
    });

    test('returns no groups for no items', () {
      expect(groupItemsByDay(<DateTime>[], (d) => d), isEmpty);
    });
  });

  testWidgets('DaySectionHeader labels today, tomorrow and far dates',
      (tester) async {
    final today = DateTime(2026, 7, 28);
    final far = DateTime(2026, 8, 20);
    await tester.pumpWidget(
      MaterialApp(
        home: Column(
          children: [
            DaySectionHeader(day: today, today: today),
            DaySectionHeader(
                day: today.add(const Duration(days: 1)), today: today),
            DaySectionHeader(day: far, today: today),
          ],
        ),
      ),
    );

    expect(find.text('Today'), findsOneWidget);
    expect(find.text('Tomorrow'), findsOneWidget);
    expect(find.text(DateFormat('EEE, MMM d').format(far)), findsOneWidget);
  });
}
