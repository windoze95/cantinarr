import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/ui/request_options_sheet.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// The request sheet's Library section: shown only for multi-library users,
/// switching libraries refetches that library's own quality profiles (and
/// drops a stale profile pick), and the submitted result names the library.
void main() {
  const options = RequestOptions(
    canChooseSeason: false,
    canChooseQuality: true,
    defaultSeasonScope: SeasonScope.all,
    qualityProfiles: [
      QualityProfileOption(id: 7, name: 'HD-1080p'),
    ],
  );
  const twoLibraries = [
    LibraryChoice(id: 'radarr-main', name: 'Movies'),
    LibraryChoice(id: 'radarr-4k', name: '4K Movies'),
  ];

  // Opens the sheet and returns a getter for the awaited pop result (the
  // result only materializes once the sheet closes, so a plain return value
  // captured at open time would always be null).
  Future<RequestOptionsResult? Function()> openSheet(
    WidgetTester tester, {
    List<LibraryChoice> libraries = twoLibraries,
    Future<RequestOptions?> Function(String libraryId)? onLibraryOptions,
  }) async {
    RequestOptionsResult? result;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Builder(
            builder: (context) => Center(
              child: ElevatedButton(
                onPressed: () async {
                  result = await showModalBottomSheet<RequestOptionsResult>(
                    context: context,
                    builder: (_) => RequestOptionsSheet(
                      options: options,
                      libraries: libraries,
                      selectedLibraryId: 'radarr-main',
                      onLibraryOptions: onLibraryOptions,
                    ),
                  );
                },
                child: const Text('open'),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    return () => result;
  }

  testWidgets(
      'multi-library sheet offers the Library section and submits the '
      'selected library', (tester) async {
    final result = await openSheet(tester);

    expect(find.text('Library'), findsOneWidget);
    expect(find.text('Movies'), findsOneWidget);
    expect(find.text('4K Movies'), findsOneWidget);

    await tester.tap(find.text('4K Movies'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Request'));
    await tester.pumpAndSettle();

    expect(result()?.instanceId, 'radarr-4k');
  });

  testWidgets(
      'selecting a library refetches its own quality profiles and drops the '
      'stale pick', (tester) async {
    final requestedLibraries = <String>[];
    final result = await openSheet(
      tester,
      onLibraryOptions: (libraryId) async {
        requestedLibraries.add(libraryId);
        return const RequestOptions(
          canChooseSeason: false,
          canChooseQuality: true,
          defaultSeasonScope: SeasonScope.all,
          qualityProfiles: [
            QualityProfileOption(id: 42, name: '4K Remux'),
          ],
        );
      },
    );

    await tester.tap(find.text('4K Movies'));
    await tester.pumpAndSettle();
    expect(requestedLibraries, ['radarr-4k']);

    // The refetched library's profile list replaced the original.
    await tester.tap(find.byType(DropdownButtonFormField<int?>));
    await tester.pumpAndSettle();
    expect(find.text('4K Remux'), findsOneWidget);
    expect(find.text('HD-1080p'), findsNothing);
    await tester.tap(find.text('Default').last);
    await tester.pumpAndSettle();

    await tester.tap(find.text('Request'));
    await tester.pumpAndSettle();
    expect(result()?.instanceId, 'radarr-4k');
    expect(result()?.qualityProfileId, isNull);
  });

  testWidgets('a single-library user never sees the Library section',
      (tester) async {
    await openSheet(
      tester,
      libraries: const [LibraryChoice(id: 'radarr-main', name: 'Movies')],
    );

    expect(find.text('Library'), findsNothing);
    expect(find.text('Movies'), findsNothing);
  });
}
