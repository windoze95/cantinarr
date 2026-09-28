import 'package:cantinarr/core/widgets/library_actions.dart';
import 'package:cantinarr/core/storage/library_view_preferences.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_author_list.dart';
import 'package:cantinarr/features/lidarr/data/lidarr_models.dart';
import 'package:cantinarr/features/lidarr/ui/lidarr_artist_list.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/radarr/ui/radarr_movie_list.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_series_list.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'library_fixture.dart';

void main() {
  for (final module in libraryModules) {
    for (final mode in LibraryViewMode.values) {
      testWidgets('$module ${mode.name} keeps navigation and all actions accessible by keyboard and long press', (tester) async {
        final calls = <String>[];
        final raw = libraryRecord(7, name: 'Example');
        void open(int id) => calls.add('open:$id');
        void action(int id, LibraryAction action) => calls.add('${action.name}:$id');
        final Widget collection = switch (module) {
          'radarr' => RadarrMovieList(
            viewMode: mode, movies: [RadarrMovie.fromJson(raw)],
            onOpen: (item) => open(item.id),
            onAction: (item, value) => action(item.id, value),
          ),
          'sonarr' => SonarrSeriesList(
            viewMode: mode, series: [SonarrSeries.fromJson(raw)],
            onOpen: (item) => open(item.id),
            onAction: (item, value) => action(item.id, value),
          ),
          'chaptarr' => ChaptarrAuthorList(
            viewMode: mode, authors: [ChaptarrAuthor.fromJson(raw)],
            onTap: (item) => open(item.id),
            onAction: (item, value) => action(item.id, value),
          ),
          _ => LidarrArtistList(
            viewMode: mode, artists: [LidarrArtist.fromJson(raw)],
            onTap: (item) => open(item.id),
            onAction: (item, value) => action(item.id, value),
          ),
        };
        await tester.pumpWidget(MaterialApp(theme: AppTheme.dark,
          home: Scaffold(body: collection)));
        await tester.pumpAndSettle();
        await tester.sendKeyEvent(LogicalKeyboardKey.tab);
        await tester.sendKeyEvent(LogicalKeyboardKey.enter);
        await tester.pumpAndSettle();
        expect(calls, ['open:7']);
        await tester.sendKeyEvent(LogicalKeyboardKey.tab);
        await tester.sendKeyEvent(LogicalKeyboardKey.space);
        await tester.pumpAndSettle();
        expect(find.byType(PopupMenuItem<LibraryAction>), findsNWidgets(7));
        await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
        await tester.sendKeyEvent(LogicalKeyboardKey.enter);
        await tester.pumpAndSettle();
        expect(calls, ['open:7', 'search:7']);
        final noun = switch (module) {
          'radarr' => 'movie', 'sonarr' => 'series', 'chaptarr' => 'author', _ => 'artist',
        };
        final actions = libraryActions(noun, monitored: module == 'chaptarr' ? null : true);
        for (final entry in actions.skip(1)) {
          await tester.tap(find.byTooltip('Actions for Example'));
          await tester.pumpAndSettle();
          await tester.tap(find.text(entry.label));
          await tester.pumpAndSettle();
          expect(calls.last, '${entry.value.name}:7');
        }
        await tester.longPress(find.text('Example'));
        await tester.pumpAndSettle();
        for (final entry in actions) {
          expect(find.text(entry.label), findsOneWidget);
        }
        await tester.tap(find.text('Rescan files'));
        await tester.pumpAndSettle();
        expect(calls.last, 'rescan:7');
        await tester.tap(find.text('Example'));
        await tester.pumpAndSettle();
        expect(calls.where((c) => c == 'open:7'), hasLength(2));
        final status = module == 'radarr'
            ? 'Missing'
            : module == 'lidarr' ? 'Active' : 'Continuing';
        if (mode == LibraryViewMode.grid) {
          expect(find.byType(LinearProgressIndicator), findsNothing);
          expect(find.text(status), findsNothing);
          expect(tester.widget<Text>(find.text('Example')).maxLines, 1);
        } else {
          expect(find.text(status), findsOneWidget);
          if (module != 'radarr') {
            final bar = tester.widget<LinearProgressIndicator>(
                find.byType(LinearProgressIndicator));
            expect(bar.value, 0.5);
            expect(bar.valueColor?.value, AppTheme.error);
          }
        }
      });
    }
  }
}
