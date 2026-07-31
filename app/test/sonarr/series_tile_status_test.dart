import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_series_list.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// The library tile follows Sonarr's grammar: the badge is the airing status,
/// and completeness lives in the progress bar — green only for an ended series
/// with every monitored episode on disk, info/ember for a continuing series that is
/// merely caught up, red for monitored gaps, amber for unmonitored gaps.
SonarrSeries _series({
  String? status,
  bool monitored = true,
  int files = 0,
  int count = 0,
}) =>
    SonarrSeries(
      id: 1,
      title: 'Show',
      year: 2020,
      monitored: monitored,
      status: status,
      statistics:
          SonarrStatistics(episodeFileCount: files, episodeCount: count),
    );

Future<void> _pump(
  WidgetTester tester,
  SonarrSeries show, {
  void Function(int id, {bool deleteFiles})? onDelete,
}) {
  return tester.pumpWidget(MaterialApp(
    home: Scaffold(
      body: SonarrSeriesList(
        series: [show],
        onDelete: onDelete ?? (_, {bool deleteFiles = false}) {},
        onSearch: (_) {},
      ),
    ),
  ));
}

Future<void> _openRemoveConfirmation(WidgetTester tester) async {
  await tester.tap(find.byTooltip('Actions for Show'));
  await tester.pumpAndSettle();
  await tester.tap(find.text('Remove…'));
  await tester.pumpAndSettle();
}

Color? _barColor(WidgetTester tester) {
  final bar = tester
      .widget<LinearProgressIndicator>(find.byType(LinearProgressIndicator));
  return bar.valueColor?.value;
}

void main() {
  testWidgets('caught-up continuing series stays Continuing with an info bar',
      (tester) async {
    await _pump(tester, _series(status: 'continuing', files: 33, count: 33));
    expect(find.text('Continuing'), findsOneWidget);
    expect(find.text('Complete'), findsNothing);
    expect(_barColor(tester), AppTheme.downloading);
  });

  testWidgets('complete ended series shows Ended with a green bar',
      (tester) async {
    await _pump(tester, _series(status: 'ended', files: 62, count: 62));
    expect(find.text('Ended'), findsOneWidget);
    expect(_barColor(tester), AppTheme.available);
  });

  testWidgets('monitored series with missing episodes shows a red bar',
      (tester) async {
    await _pump(tester, _series(status: 'ended', files: 95, count: 96));
    expect(find.text('Ended'), findsOneWidget);
    expect(_barColor(tester), AppTheme.error);
  });

  testWidgets('unmonitored series with missing episodes shows an amber bar',
      (tester) async {
    await _pump(tester,
        _series(status: 'continuing', monitored: false, files: 5, count: 10));
    expect(_barColor(tester), AppTheme.requested);
  });

  testWidgets('upcoming series shows Upcoming and no bar', (tester) async {
    await _pump(tester, _series(status: 'upcoming'));
    expect(find.text('Upcoming'), findsOneWidget);
    expect(find.byType(LinearProgressIndicator), findsNothing);
  });

  testWidgets('remove menu opens a confirmation with file deletion off',
      (tester) async {
    await _pump(tester, _series(status: 'ended'));
    expect(find.byType(Dismissible), findsNothing);

    await _openRemoveConfirmation(tester);

    expect(find.text('Delete Series'), findsOneWidget);
    expect(find.text('Also delete files from disk'), findsOneWidget);
    final checkbox =
        tester.widget<CheckboxListTile>(find.byType(CheckboxListTile));
    expect(checkbox.value, isFalse);
  });

  testWidgets('cancelling explicit removal invokes no callback',
      (tester) async {
    ({int id, bool deleteFiles})? deletion;
    await _pump(
      tester,
      _series(status: 'ended'),
      onDelete: (id, {bool deleteFiles = false}) {
        deletion = (id: id, deleteFiles: deleteFiles);
      },
    );

    await _openRemoveConfirmation(tester);
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();

    expect(deletion, isNull);
    expect(find.text('Show'), findsOneWidget);
  });

  testWidgets('explicit removal keeps files by default and supports opt-in',
      (tester) async {
    final deletions = <({int id, bool deleteFiles})>[];
    await _pump(
      tester,
      _series(status: 'ended'),
      onDelete: (id, {bool deleteFiles = false}) {
        deletions.add((id: id, deleteFiles: deleteFiles));
      },
    );

    await _openRemoveConfirmation(tester);
    await tester.tap(find.text('Delete'));
    await tester.pumpAndSettle();
    expect(deletions, [(id: 1, deleteFiles: false)]);

    await _openRemoveConfirmation(tester);
    await tester.tap(find.text('Also delete files from disk'));
    await tester.pump();
    await tester.tap(find.text('Delete'));
    await tester.pumpAndSettle();
    expect(
      deletions,
      [(id: 1, deleteFiles: false), (id: 1, deleteFiles: true)],
    );
  });
}
