import 'dart:async';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/storage/library_view_preferences.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/core/widgets/error_banner.dart';
import 'package:cantinarr/core/widgets/library_collection.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_home_screen.dart';
import 'package:cantinarr/features/lidarr/ui/lidarr_home_screen.dart';
import 'package:cantinarr/features/radarr/ui/radarr_home_screen.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_home_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'library_fixture.dart';

Widget libraryScreen(String module) => switch (module) {
  'radarr' => const RadarrHomeScreen(),
  'sonarr' => const SonarrHomeScreen(),
  'chaptarr' => const ChaptarrHomeScreen(),
  _ => const LidarrHomeScreen(),
};

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  Future<ProviderContainer> pumpLibrary(WidgetTester tester, String module,
      LibraryFixtureAdapter adapter, {double scale = 1}) async {
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(LibraryFixtureAuth.new),
      backendClientProvider.overrideWithValue(
          Dio(BaseOptions(baseUrl: 'http://localhost'))
            ..transformer = SyncTransformer()
            ..httpClientAdapter = adapter),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp(theme: AppTheme.dark,
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(scale)),
          child: child!,
        ),
        home: Scaffold(body: libraryScreen(module))),
    ));
    await tester.pump();
    return container;
  }

  for (final module in libraryModules) {
    testWidgets('$module switches filtered results without fetching and retains module choice', (tester) async {
      final adapter = LibraryFixtureAdapter();
      final container = await pumpLibrary(tester, module, adapter);
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), 'Example');
      await tester.tap(find.byIcon(Icons.tune_rounded));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Monitored'));
      await tester.pumpAndSettle();
      expect(find.text('Example one'), findsOneWidget);
      expect(find.text('Example two'), findsNothing);
      final requests = adapter.requests.length;
      for (final label in ['Grid', 'List', 'Grid']) {
        await tester.tap(find.byTooltip('$label view'));
        await tester.pumpAndSettle();
        expect(find.text('Example one'), findsOneWidget);
        expect(find.text('Example two'), findsNothing);
        expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, 'Example');
        expect(adapter.requests.length, requests);
      }
      // Recreating the screen is the same preference path as leaving a module.
      await tester.pumpWidget(const SizedBox());
      await tester.pumpWidget(UncontrolledProviderScope(container: container,
        child: MaterialApp(theme: AppTheme.dark,
          home: Scaffold(body: libraryScreen(module)))));
      await tester.pumpAndSettle();
      expect(tester.widget<LibraryCollection>(find.byType(LibraryCollection)).viewMode, LibraryViewMode.grid);
      final instances = container.read(instanceProvider.notifier);
      switch (module) {
        case 'radarr': instances.setActiveRadarrInstance('radarr-two');
        case 'sonarr': instances.setActiveSonarrInstance('sonarr-two');
        case 'chaptarr': instances.setActiveChaptarrInstance('chaptarr-two');
        case 'lidarr': instances.setActiveLidarrInstance('lidarr-two');
      }
      await tester.pumpAndSettle();
      expect(tester.widget<LibraryCollection>(find.byType(LibraryCollection)).viewMode, LibraryViewMode.grid);
      expect(adapter.requests.last, contains('$module-two'));
    });

    for (final mode in LibraryViewMode.values) {
      testWidgets('$module ${mode.name} distinguishes loading, empty and failed reads', (tester) async {
        SharedPreferences.setMockInitialValues({'library_view_$module': mode.name});
        final load = Completer<void>();
        final adapter = LibraryFixtureAdapter()..waitFor = load.future..records = [];
        await pumpLibrary(tester, module, adapter);
        await tester.pump();
        expect(find.byType(CircularProgressIndicator), findsOneWidget);
        load.complete();
        await tester.pumpAndSettle();
        expect(find.textContaining('No '), findsOneWidget);
        adapter.status = 503;
        await tester.drag(find.byType(CustomScrollView), const Offset(0, 500));
        await tester.pumpAndSettle();
        expect(find.byType(ErrorBanner), findsOneWidget);
        expect(find.textContaining('No '), findsNothing);
        adapter.status = 200;
        adapter.records = [libraryRecord(1)];
        await tester.tap(find.text('Retry'));
        await tester.pumpAndSettle();
        expect(find.text('Example 1'), findsOneWidget);
        adapter.status = 503;
        await tester.drag(find.byType(Scrollable).last, const Offset(0, 500));
        await tester.pumpAndSettle();
        expect(find.byType(ErrorBanner), findsOneWidget);
        expect(find.text('Example 1'), findsOneWidget);
      });
    }

    for (final width in [320.0, 390.0, 1440.0]) {
      testWidgets('$module grid fits $width pixels with enlarged text', (tester) async {
        tester.view.physicalSize = Size(width, 1600);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        SharedPreferences.setMockInitialValues({'library_view_$module': 'grid'});
        final adapter = LibraryFixtureAdapter()..records = [
          for (var i = 1; i <= 12; i++)
            libraryRecord(i, name: 'A very long library name with many words $i'),
        ];
        await pumpLibrary(tester, module, adapter, scale: 2);
        await tester.pumpAndSettle();
        final field = tester.getCenter(find.byType(TextField));
        final toggle = tester.getCenter(find.byType(SegmentedButton<LibraryViewMode>));
        expect(toggle.dy, closeTo(field.dy, 1));
        expect(tester.getSize(find.byType(TextField)).width, greaterThanOrEqualTo(100));
        expect(find.text('List'), width < 600 ? findsNothing : findsOneWidget);
        expect(find.text('Grid'), width < 600 ? findsNothing : findsOneWidget);
        final cards = find.byType(LibraryItem);
        final top = tester.getTopLeft(cards.at(0)).dy;
        final columns = cards.evaluate().where((element) =>
            tester.getTopLeft(find.byWidget(element.widget)).dy == top).length;
        expect(columns, width < 600 ? 3 : greaterThan(3));
        expect(tester.takeException(), isNull);
      });
    }
  }

  testWidgets('grid builds large libraries lazily and restores each layout offset', (tester) async {
    final adapter = LibraryFixtureAdapter()..records = [
      for (var i = 0; i < 2000; i++) libraryRecord(i),
    ];
    await pumpLibrary(tester, 'radarr', adapter);
    await tester.pumpAndSettle();
    await tester.drag(find.byType(ListView), const Offset(0, -800));
    await tester.pumpAndSettle();
    final listOffset = tester.state<ScrollableState>(find.byType(Scrollable).last).position.pixels;
    await tester.tap(find.byTooltip('Grid view'));
    await tester.pumpAndSettle();
    expect(find.byType(LibraryItem).evaluate().length, lessThan(40));
    await tester.drag(find.byType(ListView), const Offset(0, -600));
    await tester.pumpAndSettle();
    final gridOffset = tester.state<ScrollableState>(find.byType(Scrollable).last).position.pixels;
    await tester.tap(find.byTooltip('List view'));
    await tester.pumpAndSettle();
    expect(tester.state<ScrollableState>(find.byType(Scrollable).last).position.pixels, listOffset);
    await tester.tap(find.byTooltip('Grid view'));
    await tester.pumpAndSettle();
    expect(tester.state<ScrollableState>(find.byType(Scrollable).last).position.pixels, gridOffset);
  });

  testWidgets('artist grid resolves authenticated instance artwork', (tester) async {
    SharedPreferences.setMockInitialValues({'library_view_lidarr': 'grid'});
    final adapter = LibraryFixtureAdapter()..records = [
      {...libraryRecord(1), 'images': [{'coverType': 'poster', 'url': '/MediaCover/1/poster.jpg'}]},
    ];
    await pumpLibrary(tester, 'lidarr', adapter);
    await tester.pumpAndSettle();
    final image = tester.widget<CachedImage>(find.byType(CachedImage));
    expect(image.url, 'http://localhost/api/instances/lidarr-one/api/v1/MediaCover/1/poster.jpg');
    expect(image.headers, {'Authorization': 'Bearer library-fixture'});
    // Widget-test HTTP returns a failure; the artist fallback stays visible.
    expect(find.byIcon(Icons.mic_external_on), findsOneWidget);
  });
}
