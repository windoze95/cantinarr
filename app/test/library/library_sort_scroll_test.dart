import 'package:cantinarr/core/models/library_sort.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/storage/library_sort_preferences.dart';
import 'package:cantinarr/core/storage/library_view_preferences.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/library_collection.dart';
import 'package:cantinarr/core/widgets/library_command_header.dart';
import 'package:cantinarr/core/widgets/library_sort_menu.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_home_screen.dart';
import 'package:cantinarr/features/lidarr/ui/lidarr_home_screen.dart';
import 'package:cantinarr/features/radarr/ui/radarr_home_screen.dart';
import 'package:cantinarr/features/shell/ui/app_shell.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_home_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'library_fixture.dart';

void main() {
  for (final module in libraryModules) {
    for (final mode in LibraryViewMode.values) {
      testWidgets('$module ${mode.name} sort menu leaves page scrolling alone',
          (tester) async {
        // Every module's menu must scroll, including the shorter book/music
        // menus. The nested navigator matches the shell's routing boundary.
        tester.view.physicalSize = const Size(390, 640);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        SharedPreferences.setMockInitialValues({
          'library_view_$module': mode.name,
        });
        final adapter = LibraryFixtureAdapter(managementFixtures: true)
          ..records = [for (var i = 0; i < 80; i++) libraryRecord(i)];
        final container = ProviderContainer(overrides: [
          authProvider.overrideWith(LibraryFixtureAuth.new),
          backendClientProvider.overrideWithValue(
            Dio(BaseOptions(baseUrl: 'http://localhost'))
              ..transformer = SyncTransformer()
              ..httpClientAdapter = adapter),
          realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
        ]);
        addTearDown(container.dispose);
        await container.read(authProvider.future);
        final router = GoRouter(
          initialLocation: '/$module/library',
          routes: [ShellRoute(
            builder: (_, state, child) =>
                AppShell(currentPath: state.uri.path, child: child),
            routes: [GoRoute(
              path: '/$module/library',
              builder: (_, __) => switch (module) {
                'radarr' => const RadarrHomeScreen(),
                'sonarr' => const SonarrHomeScreen(),
                'chaptarr' => const ChaptarrHomeScreen(),
                _ => const LidarrHomeScreen(),
              },
            )],
          )],
        );
        addTearDown(router.dispose);
        await tester.pumpWidget(UncontrolledProviderScope(
          container: container,
          child: MaterialApp.router(theme: AppTheme.dark, routerConfig: router),
        ));
        await tester.pumpAndSettle();

        final topBar = find.byKey(const ValueKey('module-top-bar'));
        final header = find.byType(LibraryCommandHeader);
        final library = find.descendant(of: find.byType(LibraryCollection),
            matching: find.byType(Scrollable));
        final expandedSearchHeight = tester.getSize(topBar).height;
        final expandedHeaderHeight = tester.getSize(header).height;
        expect(expandedSearchHeight, greaterThan(0));
        final fields = librarySortFields(module);

        for (final selected in [fields.first, fields.last]) {
          await container.read(librarySortProvider(module).notifier)
              .set(LibrarySortSelection(field: selected));
          await tester.pumpAndSettle();

          for (final collapsed in [false, true]) {
            if (collapsed) {
              await tester.drag(library, const Offset(0, -400));
              await tester.pumpAndSettle();
              expect(tester.getSize(topBar).height, 0);
              expect(tester.getSize(header).height, lessThan(expandedHeaderHeight));
            }
            final searchHeight = tester.getSize(topBar).height;
            final headerHeight = tester.getSize(header).height;
            final position = tester.state<ScrollableState>(library).position;
            final offset = position.pixels;

            Future<void> expectStable() async {
              // Check during the animations too: a final-state-only assertion
              // can miss the header jumping closed and then opening again.
              for (var frame = 0; frame < 10; frame++) {
                await tester.pump(const Duration(milliseconds: 40));
                expect(tester.getSize(topBar).height, searchHeight);
                expect(tester.getSize(header).height, headerHeight);
                expect(position.pixels, offset);
              }
            }

            await tester.tap(find.byType(LibrarySortMenu));
            await expectStable();
            final menu = find.ancestor(
              of: find.byType(PopupMenuItem<LibrarySortField>).first,
              matching: find.byType(SingleChildScrollView),
            );
            final menuPosition = tester.state<ScrollableState>(
              find.descendant(of: menu, matching: find.byType(Scrollable)),
            ).position;
            final menuOffset = menuPosition.pixels;
            final delta = selected == fields.first ? -500.0 : 500.0;
            await tester.drag(menu, Offset(0, delta));
            await expectStable();
            expect(menuPosition.pixels, isNot(menuOffset));
            await tester.drag(menu, Offset(0, -delta));
            await expectStable();
            await tester.tapAt(const Offset(2, 320));
            await expectStable();
            expect(find.byType(PopupMenuItem<LibrarySortField>), findsNothing);

            if (collapsed) {
              await tester.drag(library, const Offset(0, 150));
              await tester.pumpAndSettle();
              expect(tester.getSize(topBar).height, expandedSearchHeight);
              expect(tester.getSize(header).height, expandedHeaderHeight);
            }
          }
        }

        // Selecting a field still applies it and resets the reordered results
        // to the top, as opposed to scrolling inside the menu itself.
        await tester.tap(find.byType(LibrarySortMenu));
        await tester.pumpAndSettle();
        final added = find.text('Date Added');
        await tester.ensureVisible(added);
        await tester.pumpAndSettle();
        await tester.tap(added);
        await tester.pumpAndSettle();
        expect(container.read(librarySortProvider(module)).field, LibrarySortField.added);
        expect(tester.state<ScrollableState>(library).position.pixels, 0);
        expect(tester.takeException(), isNull);
      });
    }
  }
}
