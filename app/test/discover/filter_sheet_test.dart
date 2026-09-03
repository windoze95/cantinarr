import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/app_sheet.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/browse_query.dart';
import 'package:cantinarr/features/discover/ui/filter_sheet.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

const _genres = [Genre(id: 28, name: 'Action'), Genre(id: 35, name: 'Comedy')];

void main() {
  testWidgets('Apply returns the chosen genres and rating', (tester) async {
    final result = await _open(tester, BrowseFilters.none, () async {
      await tester.tap(find.widgetWithText(FilterChip, 'Action'));
      await tester.tap(find.widgetWithText(FilterChip, '7+'));
      await tester.pump();
      await tester.tap(find.text('Apply'));
    });

    expect(result?.genreIds, [28]);
    expect(result?.minRating, 7);
    expect(result?.yearFrom, isNull);
    expect(result?.yearTo, isNull);
  });

  testWidgets('Clear returns no filters', (tester) async {
    final result = await _open(
      tester,
      const BrowseFilters(genreIds: [35], minRating: 8),
      () => tester.tap(find.text('Clear')),
    );

    expect(result, isNotNull);
    expect(result!.isEmpty, isTrue);
  });

  testWidgets('opens on the filters already applied', (tester) async {
    await _open(
      tester,
      const BrowseFilters(genreIds: [35], yearFrom: 2010, yearTo: 2019),
      () async {
        final comedy =
            tester.widget<FilterChip>(find.widgetWithText(FilterChip, 'Comedy'));
        expect(comedy.selected, isTrue);
        final action =
            tester.widget<FilterChip>(find.widgetWithText(FilterChip, 'Action'));
        expect(action.selected, isFalse);
        expect(find.text('2010 to 2019'), findsOneWidget);
        // The theme owns the sheet's card and handle; the body paints neither.
        expect(find.byType(AppSheet), findsOneWidget);
        expect(find.byType(DraggableScrollableSheet), findsNothing);
        await tester.tap(find.text('Apply'));
      },
    );
  });

  testWidgets('a full year range reads as any year and applies no bounds',
      (tester) async {
    final result = await _open(tester, BrowseFilters.none, () async {
      expect(find.text('Any year'), findsOneWidget);
      await tester.tap(find.text('Apply'));
    });
    expect(result?.yearFrom, isNull);
    expect(result?.yearTo, isNull);
  });
}

/// Opens the sheet over a host page, runs [interact] inside it, and returns
/// what the sheet resolved to.
Future<BrowseFilters?> _open(
  WidgetTester tester,
  BrowseFilters initial,
  Future<void> Function() interact,
) async {
  tester.view.physicalSize = const Size(400, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);

  BrowseFilters? result;
  await tester.pumpWidget(
    MaterialApp(
      theme: AppTheme.dark,
      home: Builder(
        builder: (context) => Scaffold(
          body: TextButton(
            onPressed: () async {
              result = await FilterSheet.show(
                context,
                genres: _genres,
                initial: initial,
              );
            },
            child: const Text('open'),
          ),
        ),
      ),
    ),
  );
  await tester.tap(find.text('open'));
  await tester.pumpAndSettle();
  expect(find.byType(FilterSheet), findsOneWidget);

  await interact();
  await tester.pumpAndSettle();
  expect(find.byType(FilterSheet), findsNothing);
  return result;
}
