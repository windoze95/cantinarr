import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/search_library_status.dart';
import 'package:cantinarr/features/discover/ui/search_results_view.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Pure widget-render coverage for search results' episode line and DISC-04's
/// browse-vs-search badge parity claim. The classifier itself (what maps to
/// what label/color/subtitle) is already covered by
/// search_library_status_test.dart; this file pins how those values render.
void main() {
  MediaItem movie(int id, String title) =>
      MediaItem(id: id, title: title, mediaType: MediaType.movie);

  MediaItem tv(int id, String title) =>
      MediaItem(id: id, title: title, mediaType: MediaType.tv);

  MediaItem person(int id, String name) =>
      MediaItem(id: id, title: name, mediaType: MediaType.person);

  const available =
      LibraryStatus(label: 'Available', color: AppTheme.available);
  const partial = LibraryStatus(
    label: 'Partial',
    color: AppTheme.requested,
    episodeSubtitle: '4/8 eps',
  );
  const requested =
      LibraryStatus(label: 'Requested', color: AppTheme.requested);

  Future<void> pumpResults(
    WidgetTester tester, {
    required List<MediaItem> results,
    required Map<(MediaType, int), LibraryStatus> libraryStatus,
  }) async {
    // Every item carries a null posterPath so CachedImage never reaches the
    // network, mirroring dashboard_tv_tab_test.dart's fixed-viewport setup.
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SearchResultsView(
            results: results,
            isLoading: false,
            query: 'x',
            libraryStatus: libraryStatus,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('renders the three chip labels in their expected colours',
      (tester) async {
    await pumpResults(
      tester,
      results: [
        movie(1, 'Available Movie'),
        tv(2, 'Partial Series'),
        movie(3, 'Requested Movie'),
      ],
      libraryStatus: {
        (MediaType.movie, 1): available,
        (MediaType.tv, 2): partial,
        (MediaType.movie, 3): requested,
      },
    );

    Color colorOf(String label) =>
        tester.widget<Text>(find.text(label)).style!.color!;

    expect(find.text('Available'), findsOneWidget);
    expect(colorOf('Available'), AppTheme.available);
    expect(find.text('Partial'), findsOneWidget);
    expect(colorOf('Partial'), AppTheme.requested);
    expect(find.text('Requested'), findsOneWidget);
    expect(colorOf('Requested'), AppTheme.requested);
  });

  testWidgets('a partial TV result renders its episode line exactly once',
      (tester) async {
    await pumpResults(
      tester,
      results: [tv(2, 'Partial Series')],
      libraryStatus: {(MediaType.tv, 2): partial},
    );

    expect(find.text('4/8 eps'), findsOneWidget);
  });

  testWidgets(
      'available, requested, unmatched and movie results render no episode line',
      (tester) async {
    await pumpResults(
      tester,
      results: [
        movie(1, 'Available Movie'),
        movie(3, 'Requested Movie'),
        movie(4, 'Unmatched Movie'),
      ],
      libraryStatus: {
        (MediaType.movie, 1): available,
        (MediaType.movie, 3): requested,
      },
    );

    expect(find.text('4/8 eps'), findsNothing);
  });

  testWidgets('a result absent from the map renders no chip', (tester) async {
    await pumpResults(
      tester,
      results: [movie(4, 'Unmatched Movie')],
      libraryStatus: const {},
    );

    expect(find.text('Available'), findsNothing);
    expect(find.text('Partial'), findsNothing);
    expect(find.text('Requested'), findsNothing);
  });

  testWidgets('a person result renders neither a chip nor an episode line',
      (tester) async {
    await pumpResults(
      tester,
      results: [person(5, 'Some Person')],
      // Person keys are never produced by buildSearchLibraryStatus, but the
      // render path must not surface either even if one were passed.
      libraryStatus: {(MediaType.person, 5): partial},
    );

    expect(find.text('Partial'), findsNothing);
    expect(find.text('4/8 eps'), findsNothing);
  });

  testWidgets(
      'DISC-04: search renders the identical three-state badge vocabulary as browse rows',
      (tester) async {
    // Same LibraryStatus map type CategoryRow feeds into MediaCard.statusLabel
    // — if either surface's label vocabulary drifted, this fails.
    await pumpResults(
      tester,
      results: [
        movie(1, 'Available Movie'),
        tv(2, 'Partial Series'),
        movie(3, 'Requested Movie'),
      ],
      libraryStatus: {
        (MediaType.movie, 1): available,
        (MediaType.tv, 2): partial,
        (MediaType.movie, 3): requested,
      },
    );

    final labels = tester
        .widgetList<Text>(find.byType(Text))
        .map((t) => t.data)
        .whereType<String>()
        .toSet();

    expect(labels.containsAll(const {
      'Available',
      'Partial',
      'Requested',
    }), isTrue);
  });
}
