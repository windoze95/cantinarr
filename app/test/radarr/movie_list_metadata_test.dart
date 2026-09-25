import 'package:cantinarr/core/storage/library_view_preferences.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/radarr/ui/radarr_movie_list.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  const movie = RadarrMovie(
    id: 1,
    title: 'Example Movie',
    year: 2020,
    hasFile: true,
    movieFile: RadarrMovieFile(id: 2, size: 2 * 1024 * 1024 * 1024),
  );

  for (final mode in LibraryViewMode.values) {
    testWidgets('Radarr ${mode.name} disk size visibility', (tester) async {
      await tester.pumpWidget(MaterialApp(
        home: Scaffold(
          body: RadarrMovieList(
            movies: const [movie],
            viewMode: mode,
            onDelete: (_, {bool deleteFiles = false}) {},
            onSearch: (_) {},
          ),
        ),
      ));

      expect(find.text('2.0 GB'), mode == LibraryViewMode.list
          ? findsOneWidget : findsNothing);
    });
  }
}
