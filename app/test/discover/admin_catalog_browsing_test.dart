import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/providers/config_sync_provider.dart';
import 'package:cantinarr/features/discover/ui/catalog_setup_footer.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/core/widgets/search_bar.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_books_tab.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_music_tab.dart';
import 'package:cantinarr/features/dashboard/ui/library_artists_row.dart';
import 'package:cantinarr/features/dashboard/ui/recently_added_books_row.dart';
import 'package:cantinarr/features/discover/logic/discovery_access.dart';
import 'package:cantinarr/features/settings/ui/instance_edit_screen.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const _albumId = 'c9e8c1f7-36f2-4e32-8fc3-ab36b6a49061';
const _types = ['radarr', 'sonarr', 'chaptarr', 'lidarr'];
ServiceInstance _instance(String type) =>
    ServiceInstance(id: type, name: type, serviceType: type, isDefault: true);
AuthState _auth({
  String role = 'admin',
  bool capability = true,
  bool child = false,
  List<String>? hidden = const [],
  bool confirmed = true,
  List<String> types = const [],
  int userId = 1,
  List<String> permissions = const ['media:discover', 'media:request'],
}) =>
    AuthState(
      connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'test-access',
          refreshToken: 'test-refresh',
          adminCatalogBrowsing: capability,
          hiddenDiscoverTabs: hidden,
          configConfirmed: confirmed,
          services: AvailableServices(
              radarr: types.contains('radarr'),
              sonarr: types.contains('sonarr'),
              chaptarr: types.contains('chaptarr'),
              lidarr: types.contains('lidarr')),
          instances: types.map(_instance).toList()),
      user: UserProfile(
          id: userId,
          username: 'tester',
          role: role,
          child: child,
          permissions: permissions),
    );

class _Auth extends AuthNotifier {
  final AuthState initial;
  final _Backend backend;
  _Auth(this.initial, this.backend);
  @override
  Future<AuthState> build() async => initial;
  void replace(AuthState next) => state = AsyncData(next);
  int configRefreshes = 0;
  @override
  Future<void> refreshConfig() async {
    configRefreshes++;
    final current = state.requireValue;
    replace(current.copyWith(
        connection: current.connection!.copyWith(
      instances: backend.types.map(_instance).toList(),
      services: AvailableServices(
          radarr: backend.types.contains('radarr'),
          sonarr: backend.types.contains('sonarr'),
          chaptarr: backend.types.contains('chaptarr'),
          lidarr: backend.types.contains('lidarr')),
      hiddenDiscoverTabs: [
        for (final tab in discoverCatalogs)
          if ((backend.hidden[tab.mediaType] ?? false) &&
              !backend.types.contains(tab.serviceType))
            tab.mediaType
      ],
      configConfirmed: true,
    )));
  }

  @override
  Future<List<UserSummary>> listUsers() async => [];
}

void main() {
  test('capability defaults off independently of configured services', () {
    expect(ServerConfig.fromJson({}).adminCatalogBrowsing, isFalse);
    final config = ServerConfig.fromJson({'admin_catalog_browsing': true});
    expect(config.adminCatalogBrowsing, isTrue);
    expect(config.services.chaptarr, isFalse);
    expect(config.services.lidarr, isFalse);
  });

  for (final types in [
    <String>[],
    ..._types.map((t) => [t]),
    _types
  ]) {
    testWidgets('admin navigation with ${types.join(',')} configured',
        (t) async {
      final h = await _pump(t, state: _auth(types: types));
      final nav =
          t.widget<BottomNavigationBar>(find.byType(BottomNavigationBar));
      expect(nav.items.map((i) => i.label),
          ['Movies', 'TV Shows', 'Releases', 'Books', 'Music']);
      await t.tap(find.text('Books').last);
      await t.pumpAndSettle();
      expect(find.byType(DashboardBooksTab), findsOneWidget);
      expect(find.text('Popular on Open Library'), findsNothing);
      expect(find.byType(RecentlyAddedBooksRow),
          types.contains('chaptarr') ? findsOneWidget : findsNothing);
      await t.tap(find.text('Music').last);
      await t.pumpAndSettle();
      expect(find.byType(DashboardMusicTab), findsOneWidget);
      expect(find.text('Popular Albums'), findsOneWidget);
      expect(find.byType(LibraryArtistsRow),
          types.contains('lidarr') ? findsOneWidget : findsNothing);
      if (types.isEmpty) {
        expect(h.backend.libraryReads, isEmpty);
        expect(find.byType(CantinarrSearchBar), findsOneWidget);
        expect(find.text('Set up Lidarr'), findsOneWidget);
        expect(
            h.backend.catalogReads
                .every((r) => !r.queryParameters.containsKey('instance_id')),
            isTrue);
      }
    });
  }

  for (final tab in discoverCatalogs) {
    final path = [
      '/dashboard/movies',
      '/dashboard/tv',
      '/dashboard/releases',
      '/dashboard/books',
      '/dashboard/music'
    ][tab.branch];
    testWidgets(
        '${tab.label} footer uses the matching setup and returns on cancel/save',
        (t) async {
      final h = await _pump(t, location: path);
      final label = 'Set up ${tab.serviceName}';
      expect(find.text(label), findsOneWidget);
      expect(find.text('Hide this tab'), findsOneWidget);
      await t.tap(find.text(label));
      await t.pumpAndSettle();
      expect(
          t
              .widget<InstanceEditScreen>(find.byType(InstanceEditScreen))
              .initialServiceType,
          tab.serviceType);
      h.router.pop();
      await t.pumpAndSettle();
      expect(h.auth.configRefreshes, 1);
      expect(h.router.routerDelegate.currentConfiguration.uri.path, path);
      expect(find.text(label), findsOneWidget);
      await _connect(t, tab.serviceType, label);
      expect(h.router.routerDelegate.currentConfiguration.uri.path, path);
      expect(find.byType(CatalogSetupFooter), findsNothing);
    });
    testWidgets('${tab.label} footer requires confirmed absence and an admin',
        (t) async {
      final h = await _pump(t, state: _auth(confirmed: false), location: path);
      expect(find.byType(CatalogSetupFooter), findsNothing);
      h.auth.replace(_auth(types: [tab.serviceType]));
      await t.pumpAndSettle();
      expect(find.byType(CatalogSetupFooter), findsNothing,
          reason:
              'a configured service qualifies even if no library/health read succeeds');
      h.auth.replace(_auth(role: 'user'));
      await t.pumpAndSettle();
      expect(find.byType(CatalogSetupFooter), findsNothing);
    });
  }

  testWidgets(
      'Hide shows progress, preserves the tab on failure, and retries a partial write',
      (t) async {
    final h = await _pump(t, location: '/dashboard/books');
    h.backend.failHide = true;
    h.backend.hideWait = Completer<void>();
    await t.tap(find.text('Hide this tab'));
    await t.pump();
    expect(find.text('Hiding…'), findsOneWidget);
    expect(h.router.routerDelegate.currentConfiguration.uri.path,
        '/dashboard/books');
    h.backend.hideWait!.complete();
    await t.pumpAndSettle();
    expect(find.text('Could not hide this tab. Try again.'), findsOneWidget);
    expect(find.byType(DashboardBooksTab), findsOneWidget);
    h.backend.failHide = false;
    await t.tap(find.text('Hide this tab'));
    await t.pumpAndSettle();
    expect(h.backend.hidden['book'], isTrue);
    expect(
        h.container
            .read(authProvider)
            .requireValue
            .connection!
            .hiddenDiscoverTabs,
        contains('book'));
    expect(h.router.routerDelegate.currentConfiguration.uri.path,
        '/dashboard/movies');
    expect(
        h.backend.reads
            .where((r) => r.path == '/api/admin/discovery-settings')
            .last
            .data,
        {
          'hidden_when_unconfigured': {'book': true}
        });
    expect(h.container.read(discoveryAccessProvider).showBooks, isFalse);
    h.backend.types.add('chaptarr');
    await h.auth.refreshConfig();
    await t.pumpAndSettle();
    expect(h.container.read(discoveryAccessProvider).showBooks, isTrue);
  });

  testWidgets('old servers keep setup and explain why Hide is disabled',
      (t) async {
    await _pump(t, state: _auth(hidden: null));
    expect(find.text('Set up Radarr'), findsOneWidget);
    expect(find.text(discoverVisibilityUpdateMessage), findsOneWidget);
    expect(
        t
            .widget<OutlinedButton>(
                find.widgetWithText(OutlinedButton, 'Hide this tab'))
            .onPressed,
        isNull);
  });

  for (final size in [const Size(320, 850), const Size(1200, 900)]) {
    testWidgets(
        'footer stays beneath scrolling content at $size with large text',
        (t) async {
      await _pump(t, location: '/dashboard/books', size: size, textScale: 1.8);
      final footer = find.byType(CatalogSetupFooter);
      final before = t.getRect(footer);
      await t.drag(find.byType(DashboardBooksTab), const Offset(0, -250));
      await t.pumpAndSettle();
      expect(t.getRect(footer), before);
      expect(t.getTopLeft(find.text('Hide this tab')).dy,
          greaterThan(t.getBottomLeft(find.text('Set up Chaptarr')).dy));
      if (size.width < 1000) {
        expect(
            before.bottom,
            lessThanOrEqualTo(
                t.getTopLeft(find.byType(BottomNavigationBar)).dy));
      } else {
        expect(find.byType(BottomNavigationBar), findsNothing);
        expect(before.bottom, size.height);
      }
      expect(t.takeException(), isNull);
    });
  }

  for (final role in ['admin', 'user']) {
    for (final size in [const Size(390, 900), const Size(1200, 900)]) {
      testWidgets('$role hidden routes and all-hidden navigation at $size',
          (t) async {
        final h = await _pump(t,
            state: _auth(role: role, hidden: ['movie', 'tv', 'book', 'music']),
            size: size);
        expect(h.router.routerDelegate.currentConfiguration.uri.path,
            '/dashboard');
        expect(find.text('No Discover tabs to show'), findsOneWidget);
        expect(find.text('Discover settings'),
            role == 'admin' ? findsOneWidget : findsNothing);
        expect(find.byType(BottomNavigationBar), findsNothing);
        expect(h.backend.catalogReads, isEmpty,
            reason: 'hidden catalogs must not preload');
        for (final route in [
          '/dashboard/movies',
          '/dashboard/tv',
          '/dashboard/releases',
          '/dashboard/books',
          '/dashboard/music',
          '/login',
        ]) {
          h.router.go(route);
          await t.pumpAndSettle();
          expect(h.router.routerDelegate.currentConfiguration.uri.path,
              '/dashboard');
        }
        if (role == 'admin') {
          await t.tap(find.text('Discover settings'));
          await t.pumpAndSettle();
          expect(h.router.routerDelegate.currentConfiguration.uri.path,
              '/settings/discovery');
          h.router.go('/dashboard');
          await t.pumpAndSettle();
        }
        h.auth
            .replace(_auth(role: role, types: ['chaptarr'], hidden: ['movie']));
        await t.pumpAndSettle();
        expect(h.router.routerDelegate.currentConfiguration.uri.path,
            '/dashboard/tv');
        expect(find.text('No Discover tabs to show'), findsNothing);
        expect(
            h.container.read(discoveryAccessProvider).pages.map((p) => p.label),
            role == 'admin'
                ? ['TV Shows', 'Releases', 'Books', 'Music']
                : ['TV Shows', 'Releases', 'Books']);
        h.router.go('/dashboard/movies');
        await t.pumpAndSettle();
        expect(h.router.routerDelegate.currentConfiguration.uri.path,
            '/dashboard/tv');
      });

      testWidgets('$role Books alone does not keep Releases visible at $size',
          (t) async {
        final h = await _pump(t,
            state: _auth(
                role: role,
                types: ['chaptarr'],
                hidden: ['movie', 'tv', 'music']),
            location: '/dashboard/releases',
            size: size);
        expect(h.router.routerDelegate.currentConfiguration.uri.path,
            '/dashboard/books');
        expect(
            h.container.read(discoveryAccessProvider).pages.map((p) => p.label),
            ['Books']);
        expect(find.byType(DashboardBooksTab), findsOneWidget);
        expect(find.text('Releases'), findsNothing);
        expect(find.byType(BottomNavigationBar), findsNothing);
        expect(h.backend.reads.where((r) => r.path.endsWith('/calendar')),
            isEmpty);
        expect(t.takeException(), isNull);
      });
    }

    for (final tab
        in discoverCatalogs.where((tab) => tab.mediaType != 'book')) {
      testWidgets(
          '$role empty ${tab.serviceName} schedule keeps Releases visible',
          (t) async {
        final h = await _pump(t,
            state: _auth(role: role, types: [
              tab.serviceType
            ], hidden: [
              for (final other in discoverCatalogs)
                if (other.mediaType != tab.mediaType) other.mediaType,
            ]));
        if (tab.mediaType == 'music') {
          expect(h.router.routerDelegate.currentConfiguration.uri.path,
              '/dashboard/releases',
              reason: 'Releases retains its place in the landing order');
        }
        h.router.go('/dashboard/releases');
        await t.pumpAndSettle();
        expect(h.router.routerDelegate.currentConfiguration.uri.path,
            '/dashboard/releases');
        expect(find.text('No upcoming releases'), findsOneWidget);
        expect(
            t
                .widget<BottomNavigationBar>(find.byType(BottomNavigationBar))
                .items
                .map((item) => item.label),
            tab.mediaType == 'music'
                ? ['Releases', 'Music']
                : [tab.label, 'Releases']);
        expect(h.backend.reads.where((r) => r.path.endsWith('/calendar')),
            isNotEmpty);

        h.auth.replace(
            _auth(role: role, hidden: ['movie', 'tv', 'book', 'music']));
        await t.pumpAndSettle();
        expect(h.router.routerDelegate.currentConfiguration.uri.path,
            '/dashboard');
        expect(find.text('No Discover tabs to show'), findsOneWidget);
        expect(find.text('Releases'), findsNothing);
        expect(t.takeException(), isNull);
      });
    }
  }

  testWidgets('a music-only requester selects the Music branch', (t) async {
    final h = await _pump(t, state: _auth(role: 'user', types: ['lidarr']));
    expect(
        t
            .widget<BottomNavigationBar>(find.byType(BottomNavigationBar))
            .items
            .map((i) => i.label),
        ['Movies', 'TV Shows', 'Releases', 'Music']);
    await t.tap(find.text('Music').last);
    await t.pumpAndSettle();
    expect(h.router.routerDelegate.currentConfiguration.uri.path,
        '/dashboard/music');
    expect(find.byType(DashboardMusicTab), findsOneWidget);
    expect(find.byType(CantinarrSearchBar), findsOneWidget);
  });

  testWidgets('old book links show retirement and offer native search',
      (t) async {
    final h = await _pump(t, location: '/detail/book/ol:OL1W?title=Book+1');
    expect(find.textContaining('Open Library book discovery has retired'),
        findsOneWidget);
    expect(find.text('Search books'), findsOneWidget);
    expect(find.text('Connect Chaptarr to request books'), findsOneWidget);
    expect(h.backend.catalogReads, isEmpty);
    expect(h.backend.libraryReads, isEmpty);
  });

  testWidgets('cold album keeps artwork and title through Lidarr setup',
      (t) async {
    final h = await _pump(t, location: '/detail/album/$_albumId');
    expect(find.text('Catalog Album'), findsOneWidget);
    expect(h.backend.libraryReads, isEmpty);
    final cover = t.widget<CachedImage>(find.byType(CachedImage).first);
    expect(Uri.parse(cover.url!).path, '/api/discover/music/artwork/$_albumId');
    expect(Uri.parse(cover.url!).queryParameters, isEmpty);
    expect(cover.headers, {'Authorization': 'Bearer test-access'});
    await _connect(t, 'lidarr', 'Connect Lidarr to request music');
    expect(find.text('Catalog Album'), findsOneWidget);
    expect(h.router.routerDelegate.currentConfiguration.uri.path,
        '/detail/album/$_albumId');
    expect(find.text('Request'), findsOneWidget);
    await t.tap(find.text('Request'));
    await t.pumpAndSettle();
    expect(h.backend.requests.single, containsPair('foreign_id', _albumId));
    expect(h.backend.requests.single, containsPair('instance_id', 'lidarr'));
  });

  for (final (type, route, label) in [
    ('radarr', 'movie', 'Connect Radarr to request movies'),
    ('sonarr', 'tv', 'Connect Sonarr to request TV shows'),
  ]) {
    testWidgets('$route details offer the matching setup before requests',
        (t) async {
      final h = await _pump(t, location: '/detail/$route/1');
      expect(find.text('Catalog Title'), findsOneWidget);
      expect(h.backend.libraryReads, isEmpty);
      await _connect(t, type, label);
      expect(find.text(label), findsNothing);
      expect(find.text('Catalog Title'), findsOneWidget);
      expect(h.backend.libraryReads, isNotEmpty);
    });
  }

  testWidgets('older server retains tabs with an update notice and setup',
      (t) async {
    final h = await _pump(t,
        state: _auth(capability: false, hidden: null),
        location: '/dashboard/books');
    expect(find.text(adminCatalogUpdateMessage), findsNothing);
    expect(find.text('Set up Chaptarr'), findsOneWidget);
    h.router.go('/dashboard/music');
    await t.pumpAndSettle();
    expect(find.text(adminCatalogUpdateMessage), findsOneWidget);
    expect(h.backend.catalogReads, isEmpty);
    expect(h.backend.libraryReads, isEmpty);
  });

  testWidgets('role and permission changes clear visible catalogs', (t) async {
    final h = await _pump(t, location: '/dashboard/books');
    expect(find.text('Set up Chaptarr'), findsOneWidget);
    h.auth.replace(_auth(role: 'user', child: true));
    await t.pumpAndSettle();
    expect(find.text('Book 1'), findsNothing);
    expect(find.text('Books'), findsNothing);
    final before = h.backend.catalogReads.length;
    h.router.go('/detail/book/ol:OL1W');
    await t.pumpAndSettle();
    expect(h.backend.catalogReads.length, before);
    h.auth.replace(_auth(role: 'user', types: ['lidarr']));
    h.router.go('/dashboard/music');
    await t.pumpAndSettle();
    expect(find.text('Catalog Album'), findsWidgets);
    h.auth.replace(_auth(role: 'user', types: ['lidarr'], permissions: []));
    await t.pumpAndSettle();
    expect(find.text('Catalog Album'), findsNothing);
  });

  testWidgets('explicit unknown and wrong-type IDs never use admin fallback',
      (t) async {
    final h = await _pump(t,
        state: _auth(types: ['radarr']),
        location: '/detail/book/ol:OL1W?instance_id=radarr');
    expect(find.text('Book 1'), findsNothing);
    h.router
        .go('/detail/album/$_albumId?instance_id=deleted&title=Catalog+Album');
    await t.pumpAndSettle();
    expect(find.text('Catalog Album'), findsNothing);
    expect(h.backend.catalogReads, isEmpty,
        reason: h.backend.catalogReads.map((r) => r.uri).join(', '));
    expect(
        h.backend.libraryReads
            .where((r) => r.path != '/api/instances/radarr/api/v3/movie'),
        isEmpty,
        reason:
            'the existing movie library may load; no book/music library may load');
  });
}

Future<void> _connect(WidgetTester t, String type, String label) async {
  await t.ensureVisible(find.text(label));
  await t.tap(find.text(label));
  await t.pumpAndSettle();
  expect(
      t
          .widget<InstanceEditScreen>(find.byType(InstanceEditScreen))
          .initialServiceType,
      type);
  Finder field(String label) => find.byWidgetPredicate(
      (w) => w is TextField && w.decoration?.labelText == label);
  await t.enterText(field('Name'), 'Test $type');
  await t.enterText(field('URL'), 'http://$type:1234');
  await t.enterText(field('API Key'), 'test-key');
  t.testTextInput.hide();
  await t.pumpAndSettle();
  final save = find.widgetWithText(ElevatedButton, 'Add Instance');
  await t.scrollUntilVisible(save, 300,
      scrollable: find
          .descendant(
              of: find.byType(InstanceEditScreen),
              matching: find.byType(Scrollable))
          .first);
  await t.pumpAndSettle();
  await t.tap(save);
  await t.pumpAndSettle();
  expect(find.byType(InstanceEditScreen), findsNothing,
      reason:
          t.widgetList<Text>(find.byType(Text)).map((w) => w.data).join(' | '));
}

Future<
        ({
          ProviderContainer container,
          GoRouter router,
          _Backend backend,
          _Auth auth
        })>
    _pump(WidgetTester t,
        {AuthState? state,
        String location = '/dashboard/movies',
        Size size = const Size(390, 900),
        double textScale = 1}) async {
  t.view.physicalSize = size;
  t.view.devicePixelRatio = 1;
  addTearDown(() {
    t.view.resetPhysicalSize();
    t.view.resetDevicePixelRatio();
  });
  final backend = _Backend();
  backend.types
      .addAll(state?.connection?.instances.map((i) => i.serviceType) ?? []);
  for (final media in state?.connection?.hiddenDiscoverTabs ?? <String>[]) {
    backend.hidden[media] = true;
  }
  final auth = _Auth(state ?? _auth(), backend);
  final c = ProviderContainer(overrides: [
    authProvider.overrideWith(() => auth),
    configSyncProvider.overrideWith((_) {}),
    backendClientProvider.overrideWithValue(
        Dio(BaseOptions(baseUrl: 'http://localhost'))
          ..httpClientAdapter = backend),
    libraryChangedEventsProvider.overrideWith((_) => const Stream.empty()),
  ]);
  await c.read(authProvider.future);
  await c.pump();
  final router = c.read(appRouterProvider)..go(location);
  await t.pumpWidget(UncontrolledProviderScope(
      container: c,
      child: _OwnedContainer(
          container: c,
          child: MaterialApp.router(
              routerConfig: router,
              builder: (context, child) => MediaQuery(
                  data: MediaQuery.of(context)
                      .copyWith(textScaler: TextScaler.linear(textScale)),
                  child: child!)))));
  await t.pumpAndSettle();
  return (container: c, router: router, backend: backend, auth: auth);
}

class _OwnedContainer extends StatefulWidget {
  final ProviderContainer container;
  final Widget child;
  const _OwnedContainer({required this.container, required this.child});
  @override
  State<_OwnedContainer> createState() => _OwnedContainerState();
}

class _OwnedContainerState extends State<_OwnedContainer> {
  @override
  void dispose() {
    widget.container.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}

class _Backend implements HttpClientAdapter {
  final reads = <RequestOptions>[];
  final requests = <Map<String, dynamic>>[];
  final types = <String>[];
  final hidden = <String, bool>{};
  bool failHide = false;
  Completer<void>? hideWait;
  Iterable<RequestOptions> get catalogReads => reads.where((r) =>
      r.path.startsWith('/api/discover/books/') ||
      r.path.startsWith('/api/discover/music/') ||
      r.path.startsWith('/api/media/book/') ||
      r.path.startsWith('/api/media/music/') ||
      r.path == '/api/genres/book' ||
      r.path == '/api/genres/music');
  Iterable<RequestOptions> get libraryReads => reads.where((r) =>
      r.path.startsWith('/api/requests/') ||
      r.path.endsWith('/request-target') ||
      RegExp(r'/api/instances/[^/]+/api/').hasMatch(r.path));
  Map<String, dynamic> book(int n) => {
        'foreign_id': 'ol:OL${n}W',
        'title': 'Book $n',
        'authors': ['An Author'],
        'year': 2001
      };
  final album = {
    'foreign_id': _albumId,
    'title': 'Catalog Album',
    'artist': 'Catalog Artist',
    'artwork': '/artwork/$_albumId'
  };
  @override
  Future<ResponseBody> fetch(
      RequestOptions o, Stream<Uint8List>? stream, Future<void>? cancel) async {
    reads.add(o);
    Object data = <String, dynamic>{};
    if (o.path == '/api/admin/discovery-settings') {
      if (o.method == 'PUT') {
        if (hideWait != null) await hideWait!.future;
        if (failHide) return ResponseBody.fromString('{}', 503);
        hidden.addAll(
            (o.data as Map)['hidden_when_unconfigured'].cast<String, bool>());
      }
      data = {'hidden_when_unconfigured': hidden};
    } else if (o.path == '/api/instances') {
      if (o.method == 'POST') {
        final type = (o.data as Map)['service_type'] as String;
        types.add(type);
        data = _instance(type).toJson();
      } else {
        data = types.map((t) => _instance(t).toJson()).toList();
      }
    } else if (o.path.startsWith('/api/discover/books/')) {
      final p = o.queryParameters['page'] as int;
      data = {
        'page': p,
        'total_results': 40,
        if (p == 1) 'next_page': 2,
        'results': [for (var n = (p - 1) * 20 + 1; n <= p * 20; n++) book(n)]
      };
    } else if (o.path == '/api/genres/book') {
      data = {
        'genres': [
          {'id': 'fantasy', 'name': 'Fantasy'}
        ]
      };
    } else if (o.path.endsWith('/request-target')) {
      data = {
        'candidates': [
          {'foreign_id': 'gr:1', 'title': 'Book 1', 'author': 'An Author'}
        ]
      };
    } else if (o.path.startsWith('/api/media/book/')) {
      data = book(int.parse(RegExp(r'OL(\d+)W').firstMatch(o.path)!.group(1)!));
    } else if (o.path.startsWith('/api/discover/music/')) {
      data = {
        'page': o.queryParameters['page'],
        'results': [album]
      };
    } else if (o.path == '/api/genres/music') {
      data = {
        'genres': [
          {'id': 'rock', 'name': 'Rock', 'tag': 'rock'}
        ]
      };
    } else if (o.path.startsWith('/api/media/music/')) {
      data = album;
    } else if (o.path == '/api/requests/book-status') {
      data = {
        'status_known': true,
        'status': 'available',
        'book_formats': {
          'ebook': 'available',
          'audiobook': requests.isEmpty ? 'unavailable' : 'requested'
        }
      };
    } else if (o.path == '/api/requests' && o.method == 'POST') {
      requests.add(Map<String, dynamic>.from(o.data as Map));
      data = {'status': 'requested'};
    } else if (o.path.startsWith('/api/requests/') &&
        o.path.endsWith('status')) {
      data = {
        'status_known': true,
        'status': requests.isEmpty ? 'unavailable' : 'requested'
      };
    } else if (o.path == '/api/requests/options') {
      data = {'can_choose_season': false, 'can_choose_quality': false};
    } else if (o.path.startsWith('/api/requests/book-')) {
      data = {'titles': [], 'books': [], 'authors': [], 'series': []};
    } else if (o.path.startsWith('/api/requests/music-')) {
      data = {'albums': [], 'items': [], 'artists': []};
    } else if (o.path.startsWith('/api/media/movie/') ||
        o.path.startsWith('/api/media/tv/')) {
      data = {
        'id': 1,
        'title': 'Catalog Title',
        'name': 'Catalog Title',
        'overview': 'A test description',
        'genres': [],
        'seasons': []
      };
    } else if (o.path.contains('/api/v') ||
        o.path == '/api/requests' ||
        o.path.contains('/library') ||
        o.path == '/api/users') {
      data = [];
    } else if (o.path.startsWith('/api/discover') ||
        o.path.startsWith('/api/trakt')) {
      data = {'results': [], 'page': 1, 'total_pages': 1};
    }
    return ResponseBody.fromString(jsonEncode(data), 200, headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType]
    });
  }

  @override
  void close({bool force = false}) {}
}
