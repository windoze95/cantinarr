import 'package:cantinarr/core/widgets/horizontal_item_row.dart';
import 'package:cantinarr/core/widgets/media_card.dart';
import 'package:cantinarr/core/widgets/section_header.dart';
import 'package:cantinarr/core/widgets/see_all_button.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/ui/category_row.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// WR-01 regression coverage: the row's reserved height must be correct on
/// the very first (loading, `items: const []`) frame, not just after
/// `pumpAndSettle()`. Every other test in this phase pumps to settled state
/// before asserting height, which is exactly why this bug shipped unnoticed —
/// `hasTvItems` derived from an empty `items` list is always `false` on that
/// frame, so a TV row's shimmer placeholder rendered at the movie height and
/// then jumped 14px the instant the feed resolved. `isTvRow` is a static
/// per-call-site flag instead, so it is correct before any items arrive.
void main() {
  testWidgets('See all renders only when the row offers it, at the same height',
      (tester) async {
    await _setViewport(tester);
    Future<double> headerHeight({VoidCallback? onSeeAll}) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: CategoryRow(
              title: 'Top Rated',
              items: const [],
              isLoading: false,
              isTvRow: false,
              onSeeAll: onSeeAll,
            ),
          ),
        ),
      );
      await tester.pump();
      return tester.getSize(find.byType(SectionHeader)).height;
    }

    final plain = await headerHeight();
    expect(find.byType(SeeAllButton), findsNothing);

    final withSeeAll = await headerHeight(onSeeAll: () {});
    expect(find.byType(SeeAllButton), findsOneWidget);
    expect(find.bySemanticsLabel('See all Top Rated'), findsOneWidget);
    // The button fits inside the heading's own height, so rows never shift
    // when a feed gains a grid.
    expect(withSeeAll, plain);
  });

  testWidgets(
      'a TV row reserves the taller height on the loading frame, before any items arrive',
      (tester) async {
    await _setViewport(tester);
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(
          body: CategoryRow(
            title: 'Most Anticipated',
            items: [],
            isLoading: true,
            isTvRow: true,
          ),
        ),
      ),
    );
    // Deliberately no pumpAndSettle(): this is the placeholder frame itself.

    final row = tester.widget<HorizontalItemRow<MediaItem>>(
      find.byType(HorizontalItemRow<MediaItem>),
    );
    expect(row.height, _cardWidth * 1.5 + MediaCard.subtitleRowExtraHeight);
  });

  testWidgets(
      'a movie row reserves the plain height on the loading frame, before any items arrive',
      (tester) async {
    await _setViewport(tester);
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(
          body: CategoryRow(
            title: 'Top Rated',
            items: [],
            isLoading: true,
            isTvRow: false,
          ),
        ),
      ),
    );

    final row = tester.widget<HorizontalItemRow<MediaItem>>(
      find.byType(HorizontalItemRow<MediaItem>),
    );
    expect(row.height, _cardWidth * 1.5 + MediaCard.plainRowExtraHeight);
  });

  testWidgets(
      'a TV row keeps the same reserved height once items load (no second jump)',
      (tester) async {
    await _setViewport(tester);
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(
          body: CategoryRow(
            title: 'Most Anticipated',
            items: [
              MediaItem(id: 1, title: 'Some Show', mediaType: MediaType.tv),
            ],
            isLoading: false,
            isTvRow: true,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    final row = tester.widget<HorizontalItemRow<MediaItem>>(
      find.byType(HorizontalItemRow<MediaItem>),
    );
    expect(row.height, _cardWidth * 1.5 + MediaCard.subtitleRowExtraHeight);
  });
}

/// 390px viewport: matches dashboard_tv_tab_test.dart / dashboard_movies_tab_test.dart
/// so cardWidth resolves to the same `< 600` bucket (108) CategoryRow uses.
const _cardWidth = 108.0;

Future<void> _setViewport(WidgetTester tester) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
}
