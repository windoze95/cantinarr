import 'dart:typed_data';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/models/library_sort.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/storage/library_sort_preferences.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/library_collection.dart';
import 'package:cantinarr/core/widgets/library_sort_menu.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/radarr/ui/radarr_home_screen.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_home_screen.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_home_screen.dart';
import 'package:cantinarr/features/lidarr/ui/lidarr_home_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'library_fixture.dart';

class SortingAdapter extends LibraryFixtureAdapter {
  SortingAdapter() : super(managementFixtures: true) {
    records = [
      {...libraryRecord(1, name: 'Zulu'), 'added': '2024-01-01', 'qualityProfileId': 1,
        'audiobookQualityProfileId': 1},
      {...libraryRecord(2, name: 'Alpha'), 'added': '2026-01-01', 'qualityProfileId': 2,
        'audiobookQualityProfileId': 2},
      {...libraryRecord(3, name: 'Beta', monitored: false), 'added': '2025-01-01', 'qualityProfileId': 1,
        'audiobookQualityProfileId': 1},
    ];
  }
  bool failProfiles = false;
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? stream,
      Future<void>? cancel) async {
    if (failProfiles && options.path.endsWith('/qualityprofile')) {
      return ResponseBody.fromString('{}', 503);
    }
    return super.fetch(options, stream, cancel);
  }
}

Widget screen(String module) => switch (module) {
  'radarr' => const RadarrHomeScreen(), 'sonarr' => const SonarrHomeScreen(),
  'chaptarr' => const ChaptarrHomeScreen(), _ => const LidarrHomeScreen(),
};

List<String> names(WidgetTester tester) => tester.widgetList<LibraryItem>(
    find.byType(LibraryItem)).map((item) => item.name).toList();

Future<void> choose(WidgetTester tester, String label) async {
  await tester.tap(find.byType(LibrarySortMenu));
  await tester.pumpAndSettle();
  await tester.ensureVisible(find.text(label).last);
  await tester.pumpAndSettle();
  await tester.tap(find.text(label).last);
  await tester.pumpAndSettle();
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  Future<ProviderContainer> pumpLibrary(WidgetTester tester, String module,
      SortingAdapter adapter, {double width = 390, double scale = 1}) async {
    tester.view.physicalSize = Size(width, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(LibraryFixtureAuth.new),
      backendClientProvider.overrideWithValue(Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..transformer = SyncTransformer()..httpClientAdapter = adapter),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    await tester.pumpWidget(UncontrolledProviderScope(container: container,
      child: MaterialApp(theme: AppTheme.dark,
        builder: (context, child) => MediaQuery(data: MediaQuery.of(context).copyWith(
          textScaler: TextScaler.linear(scale)), child: child!),
        home: Scaffold(body: screen(module)))));
    await tester.pumpAndSettle();
    return container;
  }

  for (final module in libraryModules) {
    testWidgets('$module toolbar, reversal, filters, views, refresh and instance changes', (tester) async {
      final adapter = SortingAdapter();
      final container = await pumpLibrary(tester, module, adapter);
      final filterX = tester.getCenter(find.byIcon(Icons.tune_rounded)).dx;
      final sortX = tester.getCenter(find.byType(LibrarySortMenu)).dx;
      final layoutX = tester.getCenter(find.byTooltip('Grid view')).dx;
      expect(filterX < sortX && sortX < layoutX, isTrue);
      expect(names(tester), ['Alpha', 'Beta', 'Zulu']);
      final requests = adapter.requests.length;
      await choose(tester, 'Date Added');
      expect(names(tester), ['Zulu', 'Beta', 'Alpha']);
      await choose(tester, 'Date Added');
      expect(names(tester), ['Alpha', 'Beta', 'Zulu']);
      expect(container.read(librarySortProvider(module)).ascending, isFalse);
      await tester.tap(find.byType(LibrarySortMenu));
      await tester.pumpAndSettle();
      expect(find.byIcon(Icons.arrow_downward), findsOneWidget);
      await tester.tap(find.text('Date Added'));
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('Grid view'));
      await tester.pumpAndSettle();
      expect(names(tester), ['Zulu', 'Beta', 'Alpha']);
      await tester.enterText(find.byType(TextField), 'a');
      await tester.tap(find.byIcon(Icons.tune_rounded));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Monitored'));
      await tester.pumpAndSettle();
      expect(names(tester), ['Alpha']);
      expect(adapter.requests.length, requests);
      await tester.enterText(find.byType(TextField), '');
      final refresh = tester.widget<RefreshIndicator>(find.byType(RefreshIndicator)).onRefresh();
      await tester.pumpAndSettle();
      await refresh;
      expect(names(tester), ['Zulu', 'Alpha']);
      final instances = container.read(instanceProvider.notifier);
      switch (module) {
        case 'radarr': instances.setActiveRadarrInstance('radarr-two');
        case 'sonarr': instances.setActiveSonarrInstance('sonarr-two');
        case 'chaptarr': instances.setActiveChaptarrInstance('chaptarr-two');
        case 'lidarr': instances.setActiveLidarrInstance('lidarr-two');
      }
      await tester.pumpAndSettle();
      expect(names(tester), ['Zulu', 'Beta', 'Alpha']);
      expect(container.read(librarySortProvider(module)),
        const LibrarySortSelection(field: LibrarySortField.added));
      expect(tester.takeException(), isNull);
    });

    testWidgets('$module restores saved sorting during screen initialization', (tester) async {
      SharedPreferences.setMockInitialValues({'library_sort_$module': 'added:asc'});
      await pumpLibrary(tester, module, SortingAdapter());
      expect(find.byTooltip('Sort: Date Added, ascending'), findsOneWidget);
      expect(names(tester), ['Zulu', 'Beta', 'Alpha']);
    });

    testWidgets('$module changing sort returns a scrolled library to the top', (tester) async {
      final adapter = SortingAdapter();
      adapter.records = [for (var i = 0; i < 80; i++) {
        ...libraryRecord(i, name: 'Record ${i.toString().padLeft(3, '0')}'),
        'added': DateTime(2026, 1, 80 - i).toIso8601String(),
      }];
      await pumpLibrary(tester, module, adapter);
      await tester.drag(find.byType(ListView), const Offset(0, -1000));
      await tester.pumpAndSettle();
      await choose(tester, 'Date Added');
      expect(names(tester).first, 'Record 079');
      expect(tester.state<ScrollableState>(find.byType(Scrollable).last).position.pixels, 0);
    });

    testWidgets('$module failed profiles leave library visible and retry restores selected sort', (tester) async {
      final adapter = SortingAdapter()..failProfiles = true;
      await pumpLibrary(tester, module, adapter);
      await choose(tester, module == 'chaptarr' ? 'Audiobook Quality Profile' : 'Quality Profile');
      expect(find.textContaining('Could not load quality profiles'), findsOneWidget);
      expect(names(tester), ['Alpha', 'Beta', 'Zulu']);
      adapter.failProfiles = false;
      await tester.tap(find.text('Retry'));
      await tester.pumpAndSettle();
      expect(find.textContaining('Could not load quality profiles'), findsNothing);
      // Audiobooks (id 2) precedes Standard (id 1), regardless of numeric ids.
      expect(names(tester), ['Alpha', 'Beta', 'Zulu']);
      await choose(tester, module == 'chaptarr' ? 'Audiobook Quality Profile' : 'Quality Profile');
      expect(names(tester), ['Beta', 'Zulu', 'Alpha']);
      expect(tester.takeException(), isNull);
    });

    for (final viewport in [(320.0, 1.0), (390.0, 1.7), (1440.0, 1.0)]) {
      testWidgets('$module menu fits ${viewport.$1} at scale ${viewport.$2}', (tester) async {
        await pumpLibrary(tester, module, SortingAdapter(), width: viewport.$1, scale: viewport.$2);
        await tester.tap(find.byType(LibrarySortMenu));
        await tester.pumpAndSettle();
        final fields = librarySortFields(module);
        expect(find.byType(PopupMenuItem<LibrarySortField>), findsNWidgets(fields.length));
        final last = find.text(fields.last.label(module));
        await tester.ensureVisible(last);
        await tester.pumpAndSettle();
        expect(last.hitTestable(), findsOneWidget);
        await tester.tap(last);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
      });
    }
  }
}
