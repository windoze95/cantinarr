import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/widgets/search_bar.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_chat_service.dart';
import 'package:cantinarr/features/ai_assistant/data/codex_oauth_service.dart';
import 'package:cantinarr/features/ai_assistant/logic/ai_chat_provider.dart';
import 'package:cantinarr/features/ai_assistant/ui/ai_chat_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/shell/ui/app_shell.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  testWidgets(
      'programmatic form scrolling keeps focus while a user drag dismisses it',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final focusNode = FocusNode();
    final scrollController = ScrollController();
    addTearDown(focusNode.dispose);
    addTearDown(scrollController.dispose);

    final router = GoRouter(
      initialLocation: '/settings/form',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/settings/form',
              builder: (_, __) => Scaffold(
                body: ListView(
                  controller: scrollController,
                  children: [
                    TextField(focusNode: focusNode),
                    const SizedBox(height: 1200),
                  ],
                ),
              ),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_authenticatedAiState),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    focusNode.requestFocus();
    await tester.pump();
    expect(focusNode.hasFocus, isTrue);

    scrollController.animateTo(
      100,
      duration: const Duration(milliseconds: 100),
      curve: Curves.linear,
    );
    await tester.pumpAndSettle();
    expect(
      focusNode.hasFocus,
      isTrue,
      reason: 'automatic field reveal must not dismiss the keyboard',
    );

    await tester.drag(find.byType(ListView), const Offset(0, -100));
    await tester.pumpAndSettle();
    expect(focusNode.hasFocus, isFalse);
  });

  testWidgets('side-scrolling a shelf leaves the search bar alone',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    const pageScroll = ValueKey('page-scroll');
    const posterRow = ValueKey('poster-row');

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => Scaffold(
                body: ListView(
                  key: pageScroll,
                  children: [
                    const SizedBox(height: 400),
                    SizedBox(
                      height: 200,
                      child: ListView.builder(
                        key: posterRow,
                        scrollDirection: Axis.horizontal,
                        itemCount: 20,
                        itemBuilder: (_, __) => const SizedBox(width: 120),
                      ),
                    ),
                    const SizedBox(height: 1600),
                  ],
                ),
              ),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_authenticatedAiState),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    final topBar = find.byKey(const ValueKey('module-top-bar'));
    final expanded = tester.getSize(topBar).height;
    expect(expanded, greaterThan(0));

    await tester.drag(find.byKey(posterRow), const Offset(-200, 0));
    await tester.pumpAndSettle();
    expect(
      tester.getSize(topBar).height,
      expanded,
      reason: 'swiping a shelf forward must not hide the search bar',
    );

    // The page's own vertical scroll still owns the chrome.
    await tester.drag(find.byKey(pageScroll), const Offset(0, -200));
    await tester.pumpAndSettle();
    expect(tester.getSize(topBar).height, lessThan(expanded));

    await tester.drag(find.byKey(posterRow), const Offset(200, 0));
    await tester.pumpAndSettle();
    expect(
      tester.getSize(topBar).height,
      lessThan(expanded),
      reason: 'a shelf back at its start must not pop the search bar open',
    );
  });

  testWidgets('scrolling a pushed route leaves the module search bar alone',
      (tester) async {
    final router = await _pumpScrollShell(tester);
    final topBar = find.byKey(const ValueKey('module-top-bar'));
    final expanded = tester.getSize(topBar).height;
    expect(expanded, greaterThan(0));

    router.push('/movie/1');
    await tester.pumpAndSettle();
    await tester.drag(find.byKey(const ValueKey('detail-scroll')),
        const Offset(0, -300));
    await tester.pumpAndSettle();

    router.pop();
    await tester.pumpAndSettle();
    expect(
      tester.getSize(topBar).height,
      expanded,
      reason: 'the dashboard is still at the top, so its search bar is too',
    );
  });

  testWidgets('opening another module page brings the search bar back',
      (tester) async {
    final router = await _pumpScrollShell(tester);
    final topBar = find.byKey(const ValueKey('module-top-bar'));
    final expanded = tester.getSize(topBar).height;

    await tester.drag(
        find.byKey(const ValueKey('dash-scroll')), const Offset(0, -300));
    await tester.pumpAndSettle();
    expect(tester.getSize(topBar).height, lessThan(expanded));

    router.go('/radarr/library');
    await tester.pumpAndSettle();
    expect(
      tester.getSize(topBar).height,
      expanded,
      reason: 'a fresh module page starts at the top with its search bar shown',
    );
  });

  testWidgets(
      'search-bar assistant submit opens route over previous shell content',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final chatNotifier = _FakeAiChatNotifier();
    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
          ],
        ),
        GoRoute(
          path: '/assistant',
          builder: (_, __) => const AiChatScreen(aiAvailable: true),
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_authenticatedAiState),
          ),
          aiChatProvider.overrideWith((ref) {
            ref.keepAlive();
            return chatNotifier;
          }),
          codexConnectionStatusProvider.overrideWith(
            (_) => const CodexConnectionStatus(
              selected: false,
              available: false,
              connected: false,
            ),
          ),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Dashboard home'), findsOneWidget);

    await tester.enterText(
      find.byType(TextField).first,
      'What should I watch tonight?',
    );
    await tester.pump();
    await tester.tap(find.byIcon(Icons.send_rounded));
    await tester.pumpAndSettle();

    expect(find.byType(AiChatScreen), findsOneWidget);
    expect(find.byTooltip('Exit assistant'), findsOneWidget);
    expect(chatNotifier.sentMessage, 'What should I watch tonight?');
    expect(
      chatNotifier.sentWireContent,
      isNull,
      reason: 'Movies carries no wire override — its payload stays '
          'byte-identical to before the Books-tab hand-off existed',
    );

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();

    expect(find.text('Dashboard home'), findsOneWidget);
    expect(find.byType(AiChatScreen), findsNothing);
    expect(find.text('What should I watch tonight?'), findsNothing);
  });

  testWidgets(
      'a Music-tab question reaches the assistant framed while the bubble '
      'shows the raw text', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final chatNotifier = _FakeAiChatNotifier();
    final router = GoRouter(
      initialLocation: '/dashboard/music',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/music',
              builder: (_, __) => const Scaffold(body: Text('Music home')),
            ),
          ],
        ),
        GoRoute(
          path: '/assistant',
          builder: (_, __) => const AiChatScreen(aiAvailable: true),
        ),
      ],
    );

    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = const _JsonAdapter();

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(_musicAiState)),
          backendClientProvider.overrideWithValue(dio),
          aiChatProvider.overrideWith((ref) {
            ref.keepAlive();
            return chatNotifier;
          }),
          codexConnectionStatusProvider.overrideWith(
            (_) => const CodexConnectionStatus(
              selected: false,
              available: false,
              connected: false,
            ),
          ),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    await tester.enterText(
        find.byType(TextField).first, 'what should I listen to next?');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();
    // Arm the pause detector behind the Ask AI pill; bounded pumps only from
    // here — the pill's shimmer repeats forever while aiReady, so
    // pumpAndSettle would never return.
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    await tester.tap(find.text('Ask AI'));
    await tester.pump();

    await tester.tap(find.byIcon(Icons.send_rounded));
    await tester.pumpAndSettle();

    expect(
      chatNotifier.sentMessage,
      'what should I listen to next?',
      reason: 'the bubble renders the raw text, never the framing — '
          'whole-string equality',
    );
    expect(chatNotifier.sentWireContent, isNotNull);
    expect(
      chatNotifier.sentWireContent,
      startsWith('Context: this question was asked from the Music tab'),
    );
    expect(
      chatNotifier.sentWireContent,
      endsWith('what should I listen to next?'),
      reason: "the user's own words are appended unchanged, never reworded",
    );
    expect(
      find.byType(AiChatScreen),
      findsOneWidget,
      reason: 'the assistant still opens with the prompt in flight',
    );
  });

  testWidgets(
      'Ask AI pill surfaces on typed pause and never lingers on an empty field',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_authenticatedAiState),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    // Hidden while unfocused, on focus, and even after a long focused
    // pause: only typing arms the pill.
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    await tester.tap(find.byType(TextField).first);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    // Typing, then stopping, surfaces it — never while keys are coming.
    await tester.enterText(find.byType(TextField).first, 'Severance');
    await tester.pump();
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsOneWidget);

    // The clear X wipes the query in place (no onChanged fires): the pill
    // must vanish with it, not linger over the emptied field.
    await tester.tap(find.byIcon(Icons.close_rounded));
    await tester.pump();
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    // And an empty field never re-offers it, no matter how long the wait.
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    // Fresh typing re-arms the pause detector.
    await tester.enterText(find.byType(TextField).first, 'Severance');
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsOneWidget);

    // The shimmer repeats while aiReady, so bounded pumps only from here.
    await tester.tap(find.text('Ask AI'));
    await tester.pump();

    // AI mode engaged keeping the typed text, and the field kept focus.
    expect(find.text('Press send to ask AI'), findsOneWidget);
    final field = tester.widget<TextField>(find.byType(TextField).first);
    expect(field.focusNode!.hasFocus, isTrue);
    expect(find.text('Ask AI').hitTestable(), findsNothing);

    // A title-like edit doesn't knock the explicit choice back to search.
    await tester.enterText(find.byType(TextField).first, 'Sev');
    await tester.pump();
    expect(find.text('Press send to ask AI'), findsOneWidget);

    // Deleting to zero characters flicks AI mode back off — and the
    // emptied field stays pill-free until the user types again.
    await tester.enterText(find.byType(TextField).first, '');
    await tester.pump();
    expect(find.text('Press send to ask AI'), findsNothing);
    await tester.pump(const Duration(milliseconds: 1200));
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.text('Ask AI').hitTestable(), findsNothing);
    await tester.pumpAndSettle();
  });

  testWidgets('non-admin drawer hides instance app modules', (tester) async {
    final semantics = tester.ensureSemantics();
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_multiRadarrState(isAdmin: false)),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();

    expect(find.text('Discover'), findsOneWidget);
    expect(find.bySemanticsIdentifier('nav-module-dashboard'), findsOneWidget);
    expect(find.bySemanticsIdentifier('nav-action-settings'), findsOneWidget);
    expect(find.text('Radarr'), findsNothing);
    expect(find.bySemanticsIdentifier('nav-module-radarr'), findsNothing);
    expect(find.text('Main Radarr'), findsNothing);
    expect(find.text('4K Radarr'), findsNothing);
    expect(find.byTooltip('Choose Radarr instance'), findsNothing);
    semantics.dispose();
  });

  testWidgets('the drawer offers the media server guide only when one is shared',
      (tester) async {
    final semantics = tester.ensureSemantics();
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    Future<void> pump(AuthState state) async {
      final router = GoRouter(
        initialLocation: '/dashboard/movies',
        routes: [
          ShellRoute(
            builder: (context, state, child) =>
                AppShell(currentPath: state.uri.path, child: child),
            routes: [
              GoRoute(
                path: '/dashboard/movies',
                builder: (_, __) =>
                    const Scaffold(body: Text('Dashboard home')),
              ),
            ],
          ),
        ],
      );
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            authProvider.overrideWith(() => _FakeAuthNotifier(state)),
            backendClientProvider.overrideWithValue(_fakeDio()),
          ],
          child: MaterialApp.router(routerConfig: router),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byIcon(Icons.menu));
      await tester.pumpAndSettle();
    }

    // A granted media server (the backend lists it only for granted
    // users) puts the guide in the menu, titled by the product.
    await pump(_mediaServerState());
    expect(find.bySemanticsIdentifier('nav-action-media-servers'),
        findsOneWidget);
    expect(find.text('Watch on Jellyfin'), findsOneWidget);

    // Without one there is nothing to open. Tear the first tree down first:
    // pumping into it would keep its ProviderScope overrides and state.
    await tester.pumpWidget(const SizedBox());
    await tester.pumpAndSettle();
    await pump(_multiRadarrState(isAdmin: false));
    expect(find.bySemanticsIdentifier('nav-action-media-servers'),
        findsNothing);
    expect(find.text('Watch on Jellyfin'), findsNothing);
    semantics.dispose();
  });

  testWidgets('the media server guide route carries its own breadcrumb',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
            GoRoute(
              path: '/media-servers',
              builder: (_, __) => const Scaffold(body: Text('Guide body')),
            ),
          ],
        ),
      ],
    );
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_mediaServerState()),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    router.push('/media-servers');
    await tester.pumpAndSettle();

    expect(find.text('MEDIA SERVER ACCESS'), findsOneWidget);
  });

  testWidgets('admin drawer selector switches the active app instance',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(
          () => _FakeAuthNotifier(_multiRadarrState(isAdmin: true)),
        ),
        backendClientProvider.overrideWithValue(_fakeDio()),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
    );
    addTearDown(container.dispose);

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
            GoRoute(
              path: '/radarr/library',
              builder: (_, __) => const Scaffold(body: Text('Radarr library')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();

    expect(find.text('Radarr'), findsOneWidget);
    expect(find.text('Main Radarr'), findsOneWidget);
    expect(find.text('4K Radarr'), findsNothing);

    await tester.tap(find.byTooltip('Choose Radarr instance'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('4K Radarr'));
    await tester.pumpAndSettle();

    expect(
      container.read(instanceProvider).activeRadarrInstanceId,
      'radarr-4k',
    );
    expect(find.text('Radarr library'), findsOneWidget);
  });

  // A long instance name once squeezed the module label until "Chaptarr"
  // wrapped mid-word to "Chapta / rr" in the desktop sidebar. The chip is the
  // side that gives way now, so the module name stays whole on one line.
  testWidgets('a long instance name never truncates the module label',
      (tester) async {
    tester.view.physicalSize = const Size(1400, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(
          () => _FakeAuthNotifier(_longInstanceNameState()),
        ),
        backendClientProvider.overrideWithValue(_fakeDio()),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
    );
    addTearDown(container.dispose);

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    // "Settings" carries no instance chip, so its height is the one-line
    // reference. The original defect made this label twice as tall.
    expect(
      tester.getSize(find.text('Radarr')).height,
      tester.getSize(find.text('Settings')).height,
      reason: 'the module name must stay on a single line',
    );

    // Absolute widths are not assertable here: the test font is a fixed-width
    // placeholder that renders every label far wider than Roboto does. What
    // must hold in any font is the priority -- the module name is the
    // navigation target and outranks a user-supplied instance name, so it is
    // the chip that gives way when the 280px sidebar runs out of room.
    final labelWidth = tester.getSize(find.text('Radarr')).width;
    final chipWidth =
        tester.getSize(find.byTooltip('Choose Radarr instance')).width;
    expect(
      labelWidth,
      greaterThan(chipWidth),
      reason: 'the instance chip must not out-size the module name',
    );
  });

  testWidgets('the admin queues collapse behind one Needs attention row',
      (tester) async {
    await _pumpAdminDrawer(tester);

    // One row stands for all of them until asked; the queues themselves are
    // what pushed the modules off a phone screen.
    expect(find.text('Needs attention'), findsOneWidget);
    expect(find.text('Approvals'), findsNothing);
    expect(find.text('Issues'), findsNothing);
    expect(find.text('Agent fixes'), findsNothing);
    expect(find.text('Profile approvals'), findsNothing);

    await tester.tap(find.text('Needs attention'));
    await tester.pumpAndSettle();

    expect(find.text('Approvals'), findsOneWidget);
    expect(find.text('Issues'), findsOneWidget);
    expect(find.text('Agent fixes'), findsOneWidget);
    expect(find.text('Profile approvals'), findsOneWidget);

    // The expanded queues scroll with the navigation instead of sitting in a
    // fixed block above it: that block is what squeezed the module list down
    // to a sliver, hiding Discover and the libraries behind it.
    Element scrollableOf(String label) => find
        .ancestor(of: find.text(label), matching: find.byType(Scrollable))
        .evaluate()
        .first;
    expect(scrollableOf('Approvals'), same(scrollableOf('Discover')));

    await tester.tap(find.text('Needs attention'));
    await tester.pumpAndSettle();

    expect(find.text('Approvals'), findsNothing);
    expect(find.text('Profile approvals'), findsNothing);
  });

  testWidgets('closing the drawer collapses the attention group again',
      (tester) async {
    await _pumpAdminDrawer(tester);

    await tester.tap(find.text('Needs attention'));
    await tester.pumpAndSettle();
    expect(find.text('Approvals'), findsOneWidget);

    // Tap the scrim beside the 304px drawer to dismiss it, then reopen.
    await tester.tapAt(const Offset(370, 400));
    await tester.pumpAndSettle();
    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();

    expect(
      find.text('Approvals'),
      findsNothing,
      reason: 'the group is a peek, so a reopened drawer starts collapsed',
    );
  });

  testWidgets('the collapsed row totals the queues it hides', (tester) async {
    await _pumpAdminDrawer(
      tester,
      requests: const [
        {'id': 1, 'title': 'One'},
        {'id': 2, 'title': 'Two'},
      ],
      issues: const [
        {
          'id': 1,
          'status': 'open',
          'media_type': 'movie',
          'tmdb_id': 1,
          'title': 'Broken movie',
        },
      ],
    );

    expect(find.text('3'), findsOneWidget);

    await tester.tap(find.text('Needs attention'));
    await tester.pumpAndSettle();

    // Still one 3 (the collapsed row keeps its total), now beside the two
    // counts that add up to it.
    expect(find.text('3'), findsOneWidget);
    expect(find.text('2'), findsOneWidget);
    expect(find.text('1'), findsOneWidget);
  });

  testWidgets(
      'conditional attention entries and empty section hide after empty loads',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
      'profile_approvals_menu_only_when_pending': true,
    });

    await _pumpAdminDrawer(tester);

    expect(find.text('Approvals'), findsNothing);
    expect(find.text('Issues'), findsNothing);
    expect(find.text('Agent fixes'), findsNothing);
    expect(find.text('Profile approvals'), findsNothing);
    expect(find.text('Needs attention'), findsNothing);
  });

  testWidgets('conditional attention entries fail open when queues are unknown',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
      'profile_approvals_menu_only_when_pending': true,
    });

    await _pumpAdminDrawer(tester, failAttentionQueues: true);

    expect(find.text('Needs attention'), findsOneWidget);

    await tester.tap(find.text('Needs attention'));
    await tester.pumpAndSettle();

    expect(find.text('Approvals'), findsOneWidget);
    expect(find.text('Issues'), findsOneWidget);
    expect(find.text('Agent fixes'), findsOneWidget);
    expect(find.text('Profile approvals'), findsOneWidget);
  });

  testWidgets(
      'conditional attention entries stay hidden while queues first load',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
      'profile_approvals_menu_only_when_pending': true,
    });

    // An in-flight first load must not flash the entries fail-open: the
    // cold-start assumption is empty-until-proven, and only a FAILED
    // refresh makes a queue unknowable enough to show them.
    await _pumpAdminDrawer(tester, hangAttentionQueues: true);

    expect(find.text('Needs attention'), findsNothing);
    expect(find.text('Approvals'), findsNothing);
    expect(find.text('Issues'), findsNothing);
    expect(find.text('Agent fixes'), findsNothing);
    expect(find.text('Profile approvals'), findsNothing);
  });

  testWidgets('tracking-only issue restores Issues without an attention badge',
      (tester) async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': true,
      'issues_menu_only_when_active': true,
      'agent_fixes_menu_only_when_awaiting_review': true,
      'profile_approvals_menu_only_when_pending': true,
    });

    await _pumpAdminDrawer(
      tester,
      issues: const [
        {
          'id': 1,
          'status': 'observing',
          'media_type': 'movie',
          'tmdb_id': 1,
          'title': 'Tracked movie',
        },
      ],
    );

    expect(find.text('Needs attention'), findsOneWidget);

    await tester.tap(find.text('Needs attention'));
    await tester.pumpAndSettle();

    expect(find.text('Approvals'), findsNothing);
    expect(find.text('Agent fixes'), findsNothing);
    expect(find.text('Profile approvals'), findsNothing);
    expect(find.text('Issues'), findsOneWidget);

    // Tracking is not an alert: neither the Issues row nor the total above it
    // counts a passively observed issue.
    expect(
      find.descendant(of: find.byType(Drawer), matching: find.text('1')),
      findsNothing,
    );
  });

  testWidgets(
      'the Movies tab toolbar still returns combined movie, TV and person results with their availability pills',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _SearchPinAdapter();

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_searchPinState),
          ),
          backendClientProvider.overrideWithValue(dio),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField).first, 'matrix');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      find.text('Fight Club'),
      findsOneWidget,
      reason: 'SEARCH-05: the Movies toolbar still returns combined TMDB '
          'multi-search results',
    );
    expect(
      find.text('The Matrix'),
      findsOneWidget,
      reason: 'SEARCH-05: the Movies toolbar still returns combined TMDB '
          'multi-search results',
    );
    expect(
      find.text('Game of Thrones'),
      findsOneWidget,
      reason: 'SEARCH-05: the Movies toolbar still returns combined TMDB '
          'multi-search results',
    );
    expect(
      find.text('Brad Pitt'),
      findsOneWidget,
      reason: 'SEARCH-05: the Movies toolbar still returns combined TMDB '
          'multi-search results',
    );

    expect(
      find.text('Available'),
      findsOneWidget,
      reason: 'SEARCH-05: the Available pill still renders on the Movies tab',
    );
    expect(
      find.text('Requested'),
      findsOneWidget,
      reason: 'SEARCH-05: the Requested pill still renders on the Movies tab',
    );
    expect(
      find.text('Partial'),
      findsOneWidget,
      reason: 'SEARCH-05: the Partial pill still renders on the Movies tab',
    );
  });

  testWidgets(
      'switching discovery tabs clears the search bar and closes the '
      'results overlay without firing a search',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    final adapter = _SearchPinAdapter();
    dio.httpClientAdapter = adapter;

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Movies tab')),
            ),
            GoRoute(
              path: '/dashboard/books',
              builder: (_, __) => const Scaffold(body: Text('Books tab')),
            ),
          ],
        ),
        GoRoute(
          path: '/detail/anything',
          builder: (_, __) => const Scaffold(body: Text('Detail page')),
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_searchPinState),
          ),
          backendClientProvider.overrideWithValue(dio),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField).first, 'matrix');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      find.text('Fight Club'),
      findsOneWidget,
      reason: 'SEARCH-04: typing on the Movies tab returns a result before '
          'the tab switch this case is about to make',
    );

    final searchesBeforeSwitch = adapter.searchRequests;
    final bookLookupsBeforeSwitch = adapter.bookLookupRequests;

    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    expect(
      tester.widget<TextField>(find.byType(TextField).first).controller!.text,
      isEmpty,
      reason: 'SEARCH-04: switching discovery tabs clears the toolbar text',
    );
    expect(
      find.text('Fight Club'),
      findsNothing,
      reason: 'SEARCH-04: switching discovery tabs closes the results '
          'overlay',
    );
    expect(
      adapter.searchRequests,
      searchesBeforeSwitch,
      reason: 'SEARCH-04: the clear-on-switch itself must not fire a TMDB '
          'search',
    );
    expect(
      adapter.bookLookupRequests,
      bookLookupsBeforeSwitch,
      reason: 'SEARCH-04: the clear-on-switch itself must not fire a book '
          'lookup',
    );

    router.go('/dashboard/movies');
    await tester.pumpAndSettle();

    expect(
      tester.widget<TextField>(find.byType(TextField).first).controller!.text,
      isEmpty,
      reason: 'SEARCH-04: the clear works switching back the other '
          'direction too',
    );
    expect(
      find.text('Fight Club'),
      findsNothing,
      reason: 'SEARCH-04: the overlay stays closed after switching back',
    );
    expect(
      adapter.searchRequests,
      searchesBeforeSwitch,
      reason: 'SEARCH-04: switching back fires no TMDB search either',
    );
    expect(
      adapter.bookLookupRequests,
      bookLookupsBeforeSwitch,
      reason: 'SEARCH-04: switching back fires no book lookup either',
    );

    await tester.enterText(find.byType(TextField).first, 'inception');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    router.push('/detail/anything');
    await tester.pumpAndSettle();
    expect(find.text('Detail page'), findsOneWidget);
    router.pop();
    await tester.pumpAndSettle();

    expect(
      tester.widget<TextField>(find.byType(TextField).first).controller!.text,
      'inception',
      reason: 'SEARCH-04: a pushed-route push/pop is not a discovery-tab '
          'switch — the toolbar query survives it',
    );
  });

  testWidgets('typing on the Movies tab fires no Chaptarr book lookup',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    final adapter = _SearchPinAdapter();
    dio.httpClientAdapter = adapter;

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Movies tab')),
            ),
            GoRoute(
              path: '/dashboard/books',
              builder: (_, __) => const Scaffold(body: Text('Books tab')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_searchPinState),
          ),
          backendClientProvider.overrideWithValue(dio),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField).first, 'matrix');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      adapter.bookLookupRequests,
      0,
      reason: 'WR-04: typing on the Movies tab must never reach the '
          'Chaptarr book-lookup notifier',
    );
    expect(
      adapter.searchRequests,
      greaterThan(0),
      reason: 'the Movies tab keystroke reaches the TMDB notifier as usual',
    );
  });

  testWidgets(
      'SEARCH-03: the prefix badge carries each dashboard tab\'s own icon, '
      'and the generic glyph on a non-dashboard module and an unmatched '
      'Books route', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Movies tab')),
            ),
            GoRoute(
              path: '/dashboard/tv',
              builder: (_, __) => const Scaffold(body: Text('TV tab')),
            ),
            GoRoute(
              path: '/dashboard/releases',
              builder: (_, __) => const Scaffold(body: Text('Releases tab')),
            ),
            GoRoute(
              path: '/dashboard/books',
              builder: (_, __) => const Scaffold(body: Text('Books tab')),
            ),
            GoRoute(
              path: '/radarr',
              builder: (_, __) => const Scaffold(body: Text('Radarr module')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_searchPinState),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    Finder scopedIcon(IconData icon) => find.descendant(
          of: find.byType(CantinarrSearchBar),
          matching: find.byIcon(icon),
        );
    Finder toolbar() => find.descendant(
          of: find.byType(CantinarrSearchBar),
          matching: find.byType(TextField),
        );

    expect(scopedIcon(Icons.movie), findsOneWidget,
        reason: '/dashboard/movies shows its own tab icon');
    expect(
      tester.widget<TextField>(toolbar()).decoration!.hintText,
      'Search by title or person...',
      reason: 'D-04: the Movies placeholder is unchanged with no AI '
          'capability',
    );

    router.go('/dashboard/tv');
    await tester.pumpAndSettle();
    expect(scopedIcon(Icons.tv), findsOneWidget,
        reason: '/dashboard/tv shows its own tab icon');

    router.go('/dashboard/releases');
    await tester.pumpAndSettle();
    expect(scopedIcon(Icons.event), findsOneWidget,
        reason: '/dashboard/releases shows its own tab icon');

    router.go('/radarr');
    await tester.pumpAndSettle();
    expect(scopedIcon(Icons.search_rounded), findsOneWidget,
        reason: 'SEARCH-06 fence: a non-dashboard module keeps the generic '
            'search glyph');
    expect(scopedIcon(Icons.movie), findsNothing,
        reason: 'a non-dashboard module never borrows a dashboard tab icon');

    router.go('/dashboard/books');
    await tester.pumpAndSettle();
    expect(scopedIcon(Icons.search_rounded), findsOneWidget,
        reason: 'A-03: with no Chaptarr grant, modulePagesFor emits no '
            'Books page, so the shell falls back to the generic glyph '
            'rather than borrowing the Movies icon');
    expect(scopedIcon(Icons.menu_book), findsNothing,
        reason: 'A-03: the unmatched route must not borrow another tab\'s '
            'icon');
  });

  testWidgets(
      'SEARCH-03: an AI-capable server shows no sparkle outside AI mode, '
      'and the sparkle once the bar actually enters AI mode', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final router = GoRouter(
      initialLocation: '/dashboard/movies',
      routes: [
        ShellRoute(
          builder: (context, state, child) =>
              AppShell(currentPath: state.uri.path, child: child),
          routes: [
            GoRoute(
              path: '/dashboard/movies',
              builder: (_, __) => const Scaffold(body: Text('Movies tab')),
            ),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_authenticatedAiState),
          ),
          backendClientProvider.overrideWithValue(_fakeDio()),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    Finder scopedIcon(IconData icon) => find.descendant(
          of: find.byType(CantinarrSearchBar),
          matching: find.byIcon(icon),
        );
    Finder toolbar() => find.descendant(
          of: find.byType(CantinarrSearchBar),
          matching: find.byType(TextField),
        );

    expect(scopedIcon(Icons.auto_awesome_rounded), findsNothing,
        reason: 'SEARCH-03 criterion 3: a plain, untouched field on an '
            'AI-capable server does not advertise AI in the prefix badge');
    expect(scopedIcon(Icons.movie), findsOneWidget,
        reason: 'outside AI mode the badge still names the active tab');
    expect(
      tester.widget<TextField>(toolbar()).decoration!.hintText,
      'Search or ask AI...',
      reason: 'D-04: the Movies placeholder is unchanged for an AI-capable '
          'server outside AI mode',
    );

    await tester.enterText(
      find.byType(TextField).first,
      'What should I watch tonight?',
    );
    await tester.pump();
    // The shimmer border repeats while aiReady — bounded pump only, never
    // pumpAndSettle.

    expect(scopedIcon(Icons.auto_awesome_rounded), findsOneWidget,
        reason: 'once the bar is actually in AI mode, the sparkle replaces '
            'the tab icon');
    expect(scopedIcon(Icons.movie), findsNothing);
    expect(
      tester.widget<TextField>(toolbar()).decoration!.hintText,
      'Ask the AI anything...',
    );
  });
}

Future<void> _pumpAdminDrawer(
  WidgetTester tester, {
  List<Map<String, dynamic>> requests = const [],
  List<Map<String, dynamic>> issues = const [],
  bool failAttentionQueues = false,
  bool hangAttentionQueues = false,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final router = GoRouter(
    initialLocation: '/dashboard/movies',
    routes: [
      ShellRoute(
        builder: (context, state, child) =>
            AppShell(currentPath: state.uri.path, child: child),
        routes: [
          GoRoute(
            path: '/dashboard/movies',
            builder: (_, __) => const Scaffold(body: Text('Dashboard home')),
          ),
        ],
      ),
    ],
  );

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(
          () => _FakeAuthNotifier(_authenticatedAiState),
        ),
        backendClientProvider.overrideWithValue(_fakeDio(
          requests: requests,
          issues: issues,
          failAttentionQueues: failAttentionQueues,
          hangAttentionQueues: hangAttentionQueues,
        )),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();

  await tester.tap(find.byIcon(Icons.menu));
  await tester.pumpAndSettle();
}

/// Shell over long scrollable pages: two module routes and one pushed route.
Future<GoRouter> _pumpScrollShell(WidgetTester tester) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  Widget page(String key) => Scaffold(
        body: ListView(
          key: ValueKey(key),
          children: const [SizedBox(height: 3000)],
        ),
      );

  final router = GoRouter(
    initialLocation: '/dashboard/movies',
    routes: [
      ShellRoute(
        builder: (context, state, child) =>
            AppShell(currentPath: state.uri.path, child: child),
        routes: [
          GoRoute(
            path: '/dashboard/movies',
            builder: (_, __) => page('dash-scroll'),
          ),
          GoRoute(
            path: '/radarr/library',
            builder: (_, __) => page('radarr-scroll'),
          ),
          GoRoute(path: '/movie/1', builder: (_, __) => page('detail-scroll')),
        ],
      ),
    ],
  );

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(
          () => _FakeAuthNotifier(_authenticatedAiState),
        ),
        backendClientProvider.overrideWithValue(_fakeDio()),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  return router;
}

const _authenticatedAiState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(ai: true),
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'admin'),
);

/// The music twin of [_authenticatedAiState]: AI plus a Lidarr grant, so the
/// Music tab exists to hand a question off from.
const _musicAiState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(ai: true, lidarr: true),
    instances: [
      ServiceInstance(
        id: 'music-1',
        serviceType: 'lidarr',
        name: 'Music',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

/// SEARCH-05 pin fixture: AI is off (all services false) so `SearchMode`
/// stays `SearchMode.search` and the pin never races the Ask AI
/// pause-detector heuristic. Carries a default Radarr and Sonarr instance so
/// `AppShell._initLibraries` builds real library notifiers for
/// `buildSearchLibraryStatus` to compute pills from.
const _searchPinState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
    instances: [
      ServiceInstance(
        id: 'radarr-1',
        serviceType: 'radarr',
        name: 'Radarr',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'sonarr-1',
        serviceType: 'sonarr',
        name: 'Sonarr',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

AuthState _multiRadarrState({required bool isAdmin}) {
  return AuthState(
    connection: const BackendConnection(
      serverUrl: 'http://localhost',
      accessToken: 'access',
      refreshToken: 'refresh',
      instances: [
        ServiceInstance(
          id: 'radarr-main',
          serviceType: 'radarr',
          name: 'Main Radarr',
          isDefault: true,
        ),
        ServiceInstance(
          id: 'radarr-4k',
          serviceType: 'radarr',
          name: '4K Radarr',
        ),
      ],
    ),
    user: UserProfile(
      id: 1,
      username: isAdmin ? 'admin' : 'viewer',
      role: isAdmin ? 'admin' : 'user',
    ),
  );
}

/// A requester whose only listed instance is a granted media server.
AuthState _mediaServerState() {
  return const AuthState(
    connection: BackendConnection(
      serverUrl: 'http://localhost',
      accessToken: 'access',
      refreshToken: 'refresh',
      instances: [
        ServiceInstance(
          id: 'jf-a',
          serviceType: 'jellyfin',
          name: 'Home Jellyfin',
        ),
      ],
    ),
    user: UserProfile(id: 2, username: 'viewer', role: 'user'),
  );
}

/// Two instances (so the selector chip renders) where the active one carries a
/// name long enough to compete with the module label for the sidebar's width.
AuthState _longInstanceNameState() {
  return const AuthState(
    connection: BackendConnection(
      serverUrl: 'http://localhost',
      accessToken: 'access',
      refreshToken: 'refresh',
      instances: [
        ServiceInstance(
          id: 'radarr-main',
          serviceType: 'radarr',
          name: 'A Very Long Radarr Library Name',
          isDefault: true,
        ),
        ServiceInstance(
          id: 'radarr-4k',
          serviceType: 'radarr',
          name: '4K Radarr',
        ),
      ],
    ),
    user: UserProfile(id: 1, username: 'admin', role: 'admin'),
  );
}

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

class _FakeAiChatNotifier extends AiChatNotifier {
  String? sentMessage;
  String? sentWireContent;

  _FakeAiChatNotifier() : super(chatService: AiChatService(backendDio: Dio()));

  @override
  Future<void> sendMessage(String text, {String? wireContent}) async {
    sentMessage = text;
    sentWireContent = wireContent;
  }
}

Dio _fakeDio({
  List<Map<String, dynamic>> requests = const [],
  List<Map<String, dynamic>> issues = const [],
  bool failAttentionQueues = false,
  bool hangAttentionQueues = false,
}) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _JsonAdapter(
    requests: requests,
    issues: issues,
    failAttentionQueues: failAttentionQueues,
    hangAttentionQueues: hangAttentionQueues,
  );
  return dio;
}

class _JsonAdapter implements HttpClientAdapter {
  const _JsonAdapter({
    this.requests = const [],
    this.issues = const [],
    this.failAttentionQueues = false,
    this.hangAttentionQueues = false,
  });

  final List<Map<String, dynamic>> requests;
  final List<Map<String, dynamic>> issues;
  final bool failAttentionQueues;
  final bool hangAttentionQueues;

  static bool _isAttentionQueue(String path) =>
      path == '/api/admin/requests' ||
      path == '/api/admin/issues' ||
      path == '/api/admin/agent-actions' ||
      path == '/api/admin/profile-change-proposals';

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    if (hangAttentionQueues && _isAttentionQueue(path)) {
      // A first load still in flight: the response simply never arrives.
      return Completer<ResponseBody>().future;
    }
    if (failAttentionQueues && _isAttentionQueue(path)) {
      return ResponseBody.fromString(
        '{"error":"temporarily unavailable"}',
        503,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    final Object body;
    if (path.endsWith('/movie') || path.endsWith('/series')) {
      body = [];
    } else if (path == '/api/admin/requests') {
      body = requests;
    } else if (path == '/api/admin/issues') {
      body = {'issues': issues};
    } else if (path == '/api/admin/agent-actions') {
      body = {'actions': []};
    } else if (path == '/api/admin/profile-change-proposals') {
      body = {'proposals': []};
    } else {
      body = [];
    }
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

/// SEARCH-05 pin fixture. Dispatches on `options.path`: TMDB multi-search
/// (movie/tv/person, matching `/api/search`) plus the Radarr/Sonarr library
/// reads `AppShell._initLibraries` issues for the default instances in
/// `_searchPinState`, so `buildSearchLibraryStatus` computes real
/// Available/Partial/Requested pills. Mirrors `_BooksSearchAdapter` in
/// dashboard_books_tab_test.dart.
class _SearchPinAdapter implements HttpClientAdapter {
  _SearchPinAdapter();

  /// Count of `/api/search` (TMDB multi-search) requests served — used by
  /// SEARCH-04's clear-on-tab-switch pin to prove the clear itself never
  /// fires a search.
  int searchRequests = 0;

  /// Count of Chaptarr `book/lookup` requests served — same purpose as
  /// [searchRequests], for the book-search side of the toolbar.
  int bookLookupRequests = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    Object body;
    if (path == '/api/search') {
      searchRequests++;
      body = {
        'page': 1,
        'total_pages': 1,
        'total_results': 4,
        'results': [
          {'id': 550, 'title': 'Fight Club', 'media_type': 'movie'},
          {'id': 603, 'title': 'The Matrix', 'media_type': 'movie'},
          {
            'id': 1399,
            'name': 'Game of Thrones',
            'media_type': 'tv',
            'first_air_date': '2011-04-17',
          },
          {'id': 287, 'name': 'Brad Pitt', 'media_type': 'person'},
        ],
      };
    } else if (path.contains('/api/v1/book/lookup')) {
      bookLookupRequests++;
      body = <Object>[];
    } else if (path == '/api/instances/radarr-1/api/v3/movie') {
      // tmdbId 550 -> Available (hasFile). tmdbId 603 -> Requested
      // (monitored, no file).
      body = [
        {
          'id': 1,
          'title': 'Fight Club',
          'year': 1999,
          'tmdbId': 550,
          'hasFile': true,
        },
        {
          'id': 2,
          'title': 'The Matrix',
          'year': 1999,
          'tmdbId': 603,
          'hasFile': false,
          'monitored': true,
        },
      ];
    } else if (path == '/api/instances/sonarr-1/api/v3/series') {
      // tmdbId 1399 -> Partial: files (4) strictly below total (10), both
      // non-zero, via SonarrSeries.episodeTotals reading top-level
      // `statistics` (no seasons on this fixture).
      body = [
        {
          'id': 1,
          'title': 'Game of Thrones',
          'tmdbId': 1399,
          'monitored': true,
          'statistics': {
            'episodeFileCount': 4,
            'episodeCount': 10,
            'totalEpisodeCount': 10,
          },
        },
      ];
    } else if (path.endsWith('/movie') || path.endsWith('/series')) {
      body = <Object>[];
    } else {
      body = <String, Object>{};
    }
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
