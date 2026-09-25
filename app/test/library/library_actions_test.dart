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
      testWidgets('$module ${mode.name} keeps navigation, keyboard actions and deletion guards', (tester) async {
        final calls = <String>[];
        final raw = libraryRecord(7, name: 'Example');
        void open(int id) => calls.add('open:$id');
        void search(int id) => calls.add('search:$id');
        void remove(int id, {bool deleteFiles = false}) => calls.add('remove:$id:$deleteFiles');
        final Widget collection = switch (module) {
          'radarr' => RadarrMovieList(
            viewMode: mode, movies: [RadarrMovie.fromJson(raw)],
            onOpen: (item) => open(item.id), onSearch: search, onDelete: remove,
            onInteractiveSearch: (item) => calls.add('interactive:${item.id}'),
            onLongPress: (item) => calls.add('more:${item.id}'),
          ),
          'sonarr' => SonarrSeriesList(
            viewMode: mode, series: [SonarrSeries.fromJson(raw)],
            onOpen: (item) => open(item.id), onSearch: search, onDelete: remove,
            onInteractiveSearch: (item) => calls.add('interactive:${item.id}'),
            onLongPress: (item) => calls.add('more:${item.id}'),
          ),
          'chaptarr' => ChaptarrAuthorList(
            viewMode: mode, authors: [ChaptarrAuthor.fromJson(raw)],
            onTap: (item) => open(item.id), onSearch: (item) => search(item.id),
            onDelete: (item, {bool deleteFiles = false}) => remove(item.id, deleteFiles: deleteFiles),
          ),
          _ => LidarrArtistList(
            viewMode: mode, artists: [LidarrArtist.fromJson(raw)],
            onTap: (item) => open(item.id), onSearch: (item) => search(item.id),
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
        if (module == 'lidarr') {
          expect(calls, ['open:7', 'search:7']);
        } else {
          expect(find.byType(PopupMenuItem<String>), findsWidgets);
          await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
          await tester.sendKeyEvent(LogicalKeyboardKey.enter);
          await tester.pumpAndSettle();
          expect(calls, ['open:7', 'search:7']);
          Future<void> choose(String label) async {
            await tester.tap(find.byTooltip('Actions for Example'));
            await tester.pumpAndSettle();
            await tester.tap(find.text(label));
            await tester.pumpAndSettle();
          }
          if (module == 'radarr' || module == 'sonarr') {
            await choose('Interactive search');
            await choose('More actions…');
            await tester.longPress(find.text('Example'));
            await tester.pumpAndSettle();
            expect(calls.where((c) => c == 'interactive:7'), hasLength(1));
            expect(calls.where((c) => c == 'more:7'), hasLength(2));
          }
          final label = module == 'radarr' ? 'Delete…' : 'Remove…';
          await choose(label);
          expect(tester.widget<CheckboxListTile>(find.byType(CheckboxListTile)).value, isFalse);
          await tester.tap(find.text('Cancel'));
          await tester.pumpAndSettle();
          expect(calls.where((c) => c.startsWith('remove')), isEmpty);
          await choose(label);
          await tester.tap(find.text('Delete'));
          await tester.pumpAndSettle();
          await choose(label);
          await tester.tap(find.byType(CheckboxListTile));
          await tester.pump();
          await tester.tap(find.text('Delete'));
          await tester.pumpAndSettle();
          expect(calls.where((c) => c.startsWith('remove')),
              ['remove:7:false', 'remove:7:true']);
        }
        await tester.tap(find.text('Example'));
        await tester.pumpAndSettle();
        expect(calls.where((c) => c == 'open:7'), hasLength(2));
        if (module != 'radarr') {
          if (module == 'sonarr' && mode == LibraryViewMode.grid) {
            expect(find.byType(LinearProgressIndicator), findsNothing);
          } else {
            final bar = tester.widget<LinearProgressIndicator>(
                find.byType(LinearProgressIndicator));
            expect(bar.value, 0.5);
            expect(bar.valueColor?.value, AppTheme.error);
          }
          expect(find.text(module == 'lidarr' ? 'Active' : 'Continuing'), findsOneWidget);
        } else {
          expect(find.text('Missing'), findsOneWidget);
        }
      });
    }
  }
}
