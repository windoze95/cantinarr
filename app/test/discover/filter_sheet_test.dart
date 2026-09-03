import 'package:cantinarr/core/config/app_config.dart';
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

  testWidgets(
      'Apply returns the language, services with their region, keywords and studios',
      (tester) async {
    final result = await _open(
      tester,
      BrowseFilters.none,
      () async {
        await _scrollTo(tester, find.byKey(const ValueKey('language-menu')));
        await tester.tap(find.byKey(const ValueKey('language-menu')));
        await tester.pumpAndSettle();
        await tester.tap(find.widgetWithText(MenuItemButton, 'Korean').last);
        await tester.pumpAndSettle();

        await _scrollTo(tester, find.widgetWithText(FilterChip, 'Netflix'));
        await tester.tap(find.widgetWithText(FilterChip, 'Netflix'));
        await tester.pump();

        await _typeAndPick(tester, 'keyword-field', 'hei', 'heist');
        await _typeAndPick(tester, 'studio-field', 'a2', 'A24');

        await _scrollTo(tester, find.text('Apply'));
        await tester.tap(find.text('Apply'));
      },
      lookups: FilterLookups(
        providersFor: (_) async => const [_netflix],
        searchKeywords: (_) async => const [TaggedId(id: 1, name: 'heist')],
        searchCompanies: (_) async => const [TaggedId(id: 2, name: 'A24')],
      ),
    );

    expect(result?.language, 'ko');
    expect(result?.providerIds, [8]);
    expect(result?.watchRegion, 'US');
    expect(result?.keywords, [const TaggedId(id: 1, name: 'heist')]);
    expect(result?.companies, [const TaggedId(id: 2, name: 'A24')]);
    expect(result?.count, 4);
  });

  testWidgets('changing the region refetches its services', (tester) async {
    final asked = <String>[];
    final result = await _open(
      tester,
      BrowseFilters.none,
      () async {
        await _scrollTo(tester, find.byKey(const ValueKey('region-menu')));
        await tester.tap(find.byKey(const ValueKey('region-menu')));
        await tester.pumpAndSettle();
        await tester
            .tap(find.widgetWithText(MenuItemButton, 'United Kingdom').last);
        await tester.pumpAndSettle();
        expect(asked, ['GB']);

        await _scrollTo(tester, find.widgetWithText(FilterChip, 'Sky Go'));
        await tester.tap(find.widgetWithText(FilterChip, 'Sky Go'));
        await tester.pump();
        await _scrollTo(tester, find.text('Apply'));
        await tester.tap(find.text('Apply'));
      },
      lookups: FilterLookups(
        providersFor: (region) async {
          asked.add(region);
          return const [_netflix, _skyGo];
        },
        searchKeywords: (_) async => const [],
        searchCompanies: (_) async => const [],
      ),
    );

    expect(result?.providerIds, [39]);
    expect(result?.watchRegion, 'GB');
  });

  testWidgets('no services selected means no region', (tester) async {
    final result = await _open(tester, BrowseFilters.none, () async {
      await _scrollTo(tester, find.text('Apply'));
      await tester.tap(find.text('Apply'));
    });
    expect(result?.watchRegion, isNull);
    expect(result?.isEmpty, isTrue);
  });

  testWidgets('lists that failed to load hide their sections but keep the values',
      (tester) async {
    const initial = BrowseFilters(
      language: 'ko',
      providerIds: [8],
      watchRegion: 'GB',
      keywords: [TaggedId(id: 1)],
    );
    final result = await _open(
      tester,
      initial,
      () async {
        expect(find.text('Language'), findsNothing);
        expect(find.byKey(const ValueKey('region-menu')), findsNothing);
        expect(find.widgetWithText(InputChip, 'keyword 1'), findsOneWidget);
        await _scrollTo(tester, find.text('Apply'));
        await tester.tap(find.text('Apply'));
      },
      languages: const [],
      regions: const [],
      providers: const [],
    );

    expect(result?.language, 'ko');
    expect(result?.providerIds, [8]);
    expect(result?.watchRegion, 'GB');
    expect(result?.keywords, [const TaggedId(id: 1)]);
  });

  testWidgets('a failed service refetch keeps the current selection',
      (tester) async {
    final result = await _open(
      tester,
      const BrowseFilters(providerIds: [8]),
      () async {
        await _scrollTo(tester, find.byKey(const ValueKey('region-menu')));
        await tester.tap(find.byKey(const ValueKey('region-menu')));
        await tester.pumpAndSettle();
        await tester
            .tap(find.widgetWithText(MenuItemButton, 'United Kingdom').last);
        await tester.pumpAndSettle();

        expect(find.text('Streaming services could not be loaded.'),
            findsOneWidget);
        final netflix =
            tester.widget<FilterChip>(find.widgetWithText(FilterChip, 'Netflix'));
        expect(netflix.selected, isTrue);
        await _scrollTo(tester, find.text('Apply'));
        await tester.tap(find.text('Apply'));
      },
      lookups: FilterLookups(
        providersFor: (_) async => throw StateError('offline'),
        searchKeywords: (_) async => const [],
        searchCompanies: (_) async => const [],
      ),
    );

    expect(result?.providerIds, [8]);
    expect(result?.watchRegion, 'GB');
  });
}

Future<void> _scrollTo(WidgetTester tester, Finder finder) async {
  await tester.ensureVisible(finder);
  await tester.pumpAndSettle();
}

/// Types into a picker field, waits out the debounce, and taps the one
/// suggestion.
Future<void> _typeAndPick(
  WidgetTester tester,
  String fieldKey,
  String typed,
  String suggestion,
) async {
  final field = find.descendant(
    of: find.byKey(ValueKey(fieldKey)),
    matching: find.byType(TextField),
  );
  await _scrollTo(tester, field);
  await tester.enterText(field, typed);
  await tester.pump(AppConfig.searchDebounce);
  await tester.pump();
  await _scrollTo(tester, find.widgetWithText(ListTile, suggestion));
  await tester.tap(find.widgetWithText(ListTile, suggestion));
  await tester.pump();
}

const _languages = [
  TmdbLanguage(code: 'en', englishName: 'English'),
  TmdbLanguage(code: 'ko', englishName: 'Korean'),
];
const _regions = [
  WatchRegion(code: 'GB', name: 'United Kingdom'),
  WatchRegion(code: 'US', name: 'United States'),
];
const _netflix = WatchProvider(providerId: 8, providerName: 'Netflix');
const _skyGo = WatchProvider(providerId: 39, providerName: 'Sky Go');

/// Opens the sheet over a host page, runs [interact] inside it, and returns
/// what the sheet resolved to.
Future<BrowseFilters?> _open(
  WidgetTester tester,
  BrowseFilters initial,
  Future<void> Function() interact, {
  List<Genre> genres = _genres,
  List<TmdbLanguage> languages = _languages,
  List<WatchRegion> regions = _regions,
  List<WatchProvider> providers = const [_netflix],
  String region = 'US',
  FilterLookups? lookups,
}) async {
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
                genres: genres,
                initial: initial,
                languages: languages,
                regions: regions,
                providers: providers,
                region: region,
                lookups: lookups,
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
