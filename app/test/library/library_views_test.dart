import 'dart:async';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/storage/library_view_preferences.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/core/widgets/error_banner.dart';
import 'package:cantinarr/core/widgets/library_collection.dart';
import 'package:cantinarr/core/widgets/library_command_header.dart';
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

String firstVisibleItem(WidgetTester tester) {
  final viewport = tester.getRect(find.byType(ListView));
  final visible = <(double, String)>[];
  for (final element in find.byType(LibraryItem).evaluate()) {
    final item = element.widget as LibraryItem;
    final rect = tester.getRect(find.byWidget(item));
    if (rect.bottom > viewport.top && rect.top < viewport.bottom) {
      visible.add((rect.top, item.name));
    }
  }
  visible.sort((a, b) => a.$1.compareTo(b.$1));
  return visible.first.$2;
}

bool itemIsVisible(WidgetTester tester, String name) {
  final viewport = tester.getRect(find.byType(ListView));
  final item = find.byType(LibraryItem).evaluate()
      .where((element) => (element.widget as LibraryItem).name == name)
      .first.widget;
  final rect = tester.getRect(find.byWidget(item));
  return rect.bottom > viewport.top && rect.top < viewport.bottom;
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  Future<ProviderContainer> pumpLibrary(WidgetTester tester, String module,
      LibraryFixtureAdapter adapter, {double scale = 1, bool disableAnimations = false}) async {
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
          data: MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(scale),
              disableAnimations: disableAnimations),
          child: child!,
        ),
        home: Scaffold(body: libraryScreen(module))),
    ));
    await tester.pump();
    return container;
  }

  for (final module in libraryModules) {
    testWidgets('$module phone view control toggles in one slot', (tester) async {
      tester.view.physicalSize = const Size(390, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await pumpLibrary(tester, module, LibraryFixtureAdapter());
      await tester.pumpAndSettle();

      expect(find.byType(SegmentedButton<LibraryViewMode>), findsNothing);
      expect(tester.getSize(find.byTooltip('Grid view')).width,
          closeTo(48, 2));
      final header = find.byType(LibraryCommandHeader);
      final expandedHeight = tester.getSize(header).height;
      await tester.tap(find.byTooltip('Grid view'));
      await tester.pumpAndSettle();
      expect(tester.widget<LibraryCollection>(find.byType(LibraryCollection)).viewMode,
          LibraryViewMode.grid);
      expect(tester.getSize(header).height, expandedHeight);
      expect(find.byTooltip('Grid view'), findsNothing);
      await tester.tap(find.byTooltip('List view'));
      await tester.pumpAndSettle();
      expect(tester.widget<LibraryCollection>(find.byType(LibraryCollection)).viewMode,
          LibraryViewMode.list);
    });

    testWidgets('$module phone keeps the visible item when changing views',
        (tester) async {
      tester.view.physicalSize = const Size(390, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final adapter = LibraryFixtureAdapter()..records = [
        for (var i = 0; i < 100; i++) libraryRecord(i),
      ];
      await pumpLibrary(tester, module, adapter);
      await tester.pumpAndSettle();
      await tester.drag(find.byType(ListView), const Offset(0, -650));
      await tester.pumpAndSettle();
      final listAnchor = firstVisibleItem(tester);
      await tester.tap(find.byTooltip('Grid view'));
      await tester.pumpAndSettle();
      expect(itemIsVisible(tester, listAnchor), isTrue);

      await tester.drag(find.byType(ListView), const Offset(0, -450));
      await tester.pumpAndSettle();
      final gridAnchor = firstVisibleItem(tester);
      await tester.tap(find.byTooltip('List view'));
      await tester.pumpAndSettle();
      expect(itemIsVisible(tester, gridAnchor), isTrue);
    });

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
      testWidgets('$module ${mode.name} collapses the phone summary and restores it on upward scroll', (tester) async {
        tester.view.physicalSize = const Size(390, 844);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        SharedPreferences.setMockInitialValues({'library_view_$module': mode.name});
        final adapter = LibraryFixtureAdapter()..records = [
          for (var i = 0; i < 100; i++) libraryRecord(i),
        ];
        await pumpLibrary(tester, module, adapter);
        await tester.pumpAndSettle();
        final header = find.byType(LibraryCommandHeader);
        final expandedHeight = tester.getSize(header).height;
        final requests = adapter.requests.length;
        final gesture = await tester.startGesture(tester.getCenter(find.byType(ListView)));
        await gesture.moveBy(const Offset(0, -24));
        await gesture.moveBy(const Offset(0, -180));
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 80));
        final transitioningHeight = tester.getSize(header).height;
        expect(transitioningHeight, lessThan(expandedHeight));
        await gesture.up();
        await tester.pumpAndSettle();
        final collapsedHeight = tester.getSize(header).height;
        expect(collapsedHeight, lessThan(transitioningHeight));
        expect(expandedHeight - collapsedHeight, greaterThan(100));
        expect(find.byType(TextField).hitTestable(), findsOneWidget);
        expect(find.byTooltip(mode == LibraryViewMode.list
            ? 'Grid view' : 'List view').hitTestable(), findsOneWidget);
        expect(find.byIcon(Icons.tune_rounded).hitTestable(), findsOneWidget);

        // Upward scrolling restores the summary before reaching the top.
        await tester.drag(find.byType(ListView), const Offset(0, -600));
        await tester.pumpAndSettle();
        await tester.drag(find.byType(ListView), const Offset(0, 100));
        await tester.pumpAndSettle();
        expect(tester.state<ScrollableState>(find.byType(Scrollable).last).position.pixels,
            greaterThan(0));
        expect(tester.getSize(header).height, expandedHeight);
        expect(adapter.requests.length, requests);
        expect(tester.takeException(), isNull);
      });

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
        final toggle = tester.getCenter(width < 600
            ? find.byTooltip('List view')
            : find.byType(SegmentedButton<LibraryViewMode>));
        expect(toggle.dy, closeTo(field.dy, 1));
        expect(tester.getSize(find.byType(TextField)).width, greaterThanOrEqualTo(100));
        expect(find.byType(SegmentedButton<LibraryViewMode>), width < 600
            ? findsNothing : findsOneWidget);
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

  testWidgets('phone summary ignores nested scrolls, respects reduced motion and resets for another instance', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final adapter = LibraryFixtureAdapter()..records = [
      for (var i = 0; i < 100; i++) libraryRecord(i),
    ];
    final container = await pumpLibrary(tester, 'radarr', adapter, disableAnimations: true);
    await tester.pumpAndSettle();
    final header = find.byType(LibraryCommandHeader);
    final expandedHeight = tester.getSize(header).height;
    final context = tester.element(find.byType(ListView));
    for (final horizontal in [true, false]) {
      ScrollUpdateNotification(
        metrics: FixedScrollMetrics(minScrollExtent: 0, maxScrollExtent: 1000,
          pixels: 100, viewportDimension: 300, devicePixelRatio: 1,
          axisDirection: horizontal ? AxisDirection.right : AxisDirection.down),
        context: context, scrollDelta: 100, depth: horizontal ? 0 : 1,
      ).dispatch(context);
      await tester.pumpAndSettle();
      expect(tester.getSize(header).height, expandedHeight);
    }
    await tester.drag(find.byType(ListView), const Offset(0, -400));
    await tester.pump();
    expect(tester.getSize(header).height, lessThan(expandedHeight - 100));
    expect(tester.binding.transientCallbackCount, 0);
    container.read(instanceProvider.notifier).setActiveRadarrInstance('radarr-two');
    await tester.pumpAndSettle();
    expect(tester.getSize(header).height, expandedHeight);
  });

  testWidgets('desktop summary stays visible while browsing', (tester) async {
    tester.view.physicalSize = const Size(1440, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final adapter = LibraryFixtureAdapter()..records = [
      for (var i = 0; i < 100; i++) libraryRecord(i),
    ];
    await pumpLibrary(tester, 'radarr', adapter);
    await tester.pumpAndSettle();
    final header = find.byType(LibraryCommandHeader);
    final height = tester.getSize(header).height;
    await tester.drag(find.byType(ListView), const Offset(0, -400));
    await tester.pumpAndSettle();
    expect(tester.getSize(header).height, height);
  });

  testWidgets('view changes keep visible items in a large lazy library', (tester) async {
    final adapter = LibraryFixtureAdapter()..records = [
      for (var i = 0; i < 2000; i++) libraryRecord(i),
    ];
    await pumpLibrary(tester, 'radarr', adapter);
    await tester.pumpAndSettle();
    await tester.drag(find.byType(ListView), const Offset(0, -800));
    await tester.pumpAndSettle();
    final listAnchor = firstVisibleItem(tester);
    await tester.tap(find.byTooltip('Grid view'));
    await tester.pumpAndSettle();
    expect(find.byType(LibraryItem).evaluate().length, lessThan(40));
    expect(itemIsVisible(tester, listAnchor), isTrue);
    await tester.drag(find.byType(ListView), const Offset(0, -600));
    await tester.pumpAndSettle();
    final gridAnchor = firstVisibleItem(tester);
    await tester.tap(find.byTooltip('List view'));
    await tester.pumpAndSettle();
    expect(itemIsVisible(tester, gridAnchor), isTrue);
    await tester.drag(find.byType(ListView), const Offset(0, -400));
    await tester.pumpAndSettle();
    final nextListAnchor = firstVisibleItem(tester);
    await tester.tap(find.byTooltip('Grid view'));
    await tester.pumpAndSettle();
    expect(itemIsVisible(tester, nextListAnchor), isTrue);
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
