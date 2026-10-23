import 'package:cantinarr/features/media_detail/logic/tv_schedule.dart';
import 'package:cantinarr/features/chaptarr/logic/book_publication.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/browse_query.dart';
import 'package:cantinarr/features/discover/ui/browse_labels.dart';
import 'package:cantinarr/features/media_detail/logic/release_window.dart';
import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/ui/request_button.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../media_detail/tv_schedule_fixtures.dart';

void main() {
  testWidgets('French filter wording preserves browse query and device country',
      (tester) async {
    tester.platformDispatcher.localeTestValue = const Locale('en', 'CA');
    tester.platformDispatcher.localesTestValue = const [Locale('en', 'CA')];
    addTearDown(tester.platformDispatcher.clearLocaleTestValue);
    addTearDown(tester.platformDispatcher.clearLocalesTestValue);
    const filters = BrowseFilters(
      genreIds: [28],
      yearFrom: 2010,
      yearTo: 2019,
      minRating: 7,
      language: 'ko',
      providerIds: [8],
      watchRegion: 'CA',
    );
    const query = BrowseQuery(
      type: MediaType.movie,
      feed: BrowseFeed.discover,
      title: 'Action',
      sort: BrowseSort.newest,
      filters: filters,
    );
    final originalLocation = query.toLocation();
    String? region;
    await tester.pumpWidget(_LocalizedProbe(
      locale: const Locale('fr'),
      builder: (context) {
        region = watchRegionFor(
            WidgetsBinding.instance.platformDispatcher.locale.countryCode);
        return Text(localizedBrowseFilters(context, filters, {28: 'Action'},
            languages: {'ko': 'Korean'}, providers: {8: 'Netflix'}));
      },
    ));
    await tester.pumpAndSettle();
    expect(find.textContaining('De 2010 à 2019'), findsOneWidget);
    expect(find.textContaining('note de 7 ou plus'), findsOneWidget);
    expect(find.textContaining('sur Netflix'), findsOneWidget);
    expect(region, 'CA');
    expect(query.toLocation(), originalLocation);
    final roundTrip = BrowseQuery.tryParse(Uri.parse(query.toLocation()))!;
    expect(roundTrip.sort, BrowseSort.newest);
    expect(roundTrip.filters.language, 'ko');
    expect(roundTrip.filters.watchRegion, 'CA');
    expect(roundTrip.filters.genreIds, [28]);
    expect(roundTrip.filters.providerIds, [8]);
  });

  testWidgets('French release dates and counts preserve source metadata',
      (tester) async {
    final releaseDay = DateTime(2026, 6, 12);
    final milestones = [PendingRelease(label: 'In cinemas', date: releaseDay)];
    const customSeason = Season(
        id: 4, seasonNumber: 2, name: 'Chapter One', airDate: '2019-04-01');
    await tester.pumpWidget(_LocalizedProbe(
      locale: const Locale('fr'),
      builder: (context) => Column(children: [
        Text(localizedPendingReleases(context, milestones,
            now: DateTime(2026, 6, 1))),
        Text(browseLabel(context, '2 of 4 books available')),
        Text(upcomingTVDateLabel(TVDetail.fromJson(returningTVFixture),
            now: DateTime(2026, 9, 12), strings: context.l10n)!),
        Text(localizedBookPublicationSummary(
            context, const BookPublication(year: 3000, pages: 250))),
        Text(localizedSeasonRowLabel(context,
            const Season(id: 1, seasonNumber: 1, airDate: '2019-04-01'))),
        Text(localizedSeasonRowLabel(context, customSeason)),
      ]),
    ));
    await tester.pumpAndSettle();
    expect(find.textContaining('Au cinéma'), findsOneWidget);
    expect(find.textContaining('12 juin'), findsOneWidget);
    expect(find.textContaining('Prochain épisode S2 E3'), findsOneWidget);
    expect(find.textContaining('14 oct.'), findsOneWidget);
    expect(
        upcomingTVDateLabel(TVDetail.fromJson(returningTVFixture),
            now: DateTime(2026, 9, 12)),
        'Next episode S2 E3 · Oct 14, 2026');
    expect(find.text('2 livres disponibles sur 4'), findsOneWidget);
    expect(find.text('Date non confirmée (3000) · 250 pages'), findsOneWidget);
    expect(find.text('Saison 1 · 2019'), findsOneWidget);
    expect(find.text('Chapter One · 2019'), findsOneWidget);
    expect(milestones.single.date, releaseDay);
    expect(milestones.single.label, 'In cinemas');
    expect(customSeason.name, 'Chapter One');
  });

  testWidgets('French request controls fit a 390px screen', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(_LocalizedProbe(
      locale: const Locale('fr'),
      builder: (context) => MediaQuery(
        data: MediaQuery.of(context)
            .copyWith(textScaler: const TextScaler.linear(1.5)),
        child: const Padding(
          padding: EdgeInsets.all(16),
          child: Column(children: [
            RequestButton(status: RequestStatus.partial),
            SizedBox(height: 16),
            RequestButton(status: RequestStatus.downloading),
          ]),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    expect(find.text('Demander davantage'), findsOneWidget);
    expect(find.text('Téléchargement en cours'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('standalone request widgets keep the English fallback',
      (tester) async {
    await tester.pumpWidget(const MaterialApp(
      home: Scaffold(
          body: RequestButton(
        status: RequestStatus.unavailable,
      )),
    ));
    expect(find.text('Request'), findsOneWidget);
  });
}

class _LocalizedProbe extends StatelessWidget {
  final Locale locale;
  final WidgetBuilder builder;
  const _LocalizedProbe({required this.locale, required this.builder});

  @override
  Widget build(BuildContext context) => MaterialApp(
        locale: locale,
        supportedLocales: AppLocalizations.supportedLocales,
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        home: Scaffold(body: Builder(builder: builder)),
      );
}
