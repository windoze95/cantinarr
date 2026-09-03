import 'dart:async';

import 'package:cantinarr/core/config/app_config.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/ui/tag_picker_field.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// The type-to-add field behind Keywords and Studios: one lookup per pause,
/// suggestions inline, chosen values as removable chips.
void main() {
  testWidgets('searches once after a pause, not per keystroke', (tester) async {
    final queries = <String>[];
    await _pump(tester, search: (q) async {
      queries.add(q);
      return [const TaggedId(id: 1, name: 'heist')];
    });

    await tester.enterText(find.byType(TextField), 'he');
    await tester.enterText(find.byType(TextField), 'hei');
    await tester.pump(AppConfig.searchDebounce ~/ 2);
    expect(queries, isEmpty);

    await tester.pump(AppConfig.searchDebounce);
    await tester.pump();
    expect(queries, ['hei']);
    expect(find.text('heist'), findsOneWidget);
  });

  testWidgets('shows at most eight suggestions and never one already chosen',
      (tester) async {
    await _pump(
      tester,
      values: const [TaggedId(id: 1, name: 'chosen')],
      search: (_) async => [
        for (var i = 1; i <= 10; i++) TaggedId(id: i, name: 'tag $i'),
      ],
    );

    await tester.enterText(find.byType(TextField), 'tag');
    await tester.pump(AppConfig.searchDebounce);
    await tester.pump();

    expect(find.byType(ListTile), findsNWidgets(8));
    expect(find.widgetWithText(ListTile, 'tag 1'), findsNothing);
    expect(find.widgetWithText(ListTile, 'tag 2'), findsOneWidget);
  });

  testWidgets('tapping a suggestion adds it and clears the field',
      (tester) async {
    List<TaggedId>? changed;
    await _pump(
      tester,
      search: (_) async => [const TaggedId(id: 1, name: 'heist')],
      onChanged: (values) => changed = values,
    );

    await tester.enterText(find.byType(TextField), 'hei');
    await tester.pump(AppConfig.searchDebounce);
    await tester.pump();
    await tester.tap(find.widgetWithText(ListTile, 'heist'));
    await tester.pump();

    expect(changed, [const TaggedId(id: 1, name: 'heist')]);
    expect(tester.widget<TextField>(find.byType(TextField)).controller!.text,
        isEmpty);
    expect(find.byType(ListTile), findsNothing);
  });

  testWidgets('a slow answer to an earlier query never replaces a newer one',
      (tester) async {
    final pending = <String, Completer<List<TaggedId>>>{};
    await _pump(tester, search: (q) {
      final completer = Completer<List<TaggedId>>();
      pending[q] = completer;
      return completer.future;
    });

    await tester.enterText(find.byType(TextField), 'he');
    await tester.pump(AppConfig.searchDebounce);
    await tester.enterText(find.byType(TextField), 'hei');
    await tester.pump(AppConfig.searchDebounce);

    pending['hei']!.complete([const TaggedId(id: 2, name: 'heist')]);
    await tester.pump();
    pending['he']!.complete([const TaggedId(id: 1, name: 'health')]);
    await tester.pump();

    expect(find.text('heist'), findsOneWidget);
    expect(find.text('health'), findsNothing);
  });

  testWidgets('deleting a chip reports the remaining values', (tester) async {
    List<TaggedId>? changed;
    await _pump(
      tester,
      values: const [TaggedId(id: 1, name: 'heist'), TaggedId(id: 2)],
      onChanged: (values) => changed = values,
    );

    expect(find.text('keyword 2'), findsOneWidget);
    await tester.tap(find.descendant(
      of: find.widgetWithText(InputChip, 'heist'),
      matching: find.byIcon(Icons.close),
    ));
    expect(changed, [const TaggedId(id: 2)]);
  });

  testWidgets('a failed lookup says so instead of showing nothing',
      (tester) async {
    await _pump(tester, search: (_) async => throw StateError('offline'));

    await tester.enterText(find.byType(TextField), 'hei');
    await tester.pump(AppConfig.searchDebounce);
    await tester.pump();

    expect(find.text('Keywords could not be searched.'), findsOneWidget);
    expect(find.byType(ListTile), findsNothing);
  });
}

Future<void> _pump(
  WidgetTester tester, {
  List<TaggedId> values = const [],
  Future<List<TaggedId>> Function(String query)? search,
  ValueChanged<List<TaggedId>>? onChanged,
}) async {
  await tester.pumpWidget(
    MaterialApp(
      theme: AppTheme.dark,
      home: Scaffold(
        body: SingleChildScrollView(
          child: TagPickerField(
            kind: 'keyword',
            values: values,
            hint: 'Add a keyword',
            failureMessage: 'Keywords could not be searched.',
            search: search ?? (_) async => const [],
            onChanged: onChanged ?? (_) {},
          ),
        ),
      ),
    ),
  );
}
