import 'dart:async';
import 'dart:io';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/app_ambient_background.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/media_access/data/media_access_service.dart';
import 'package:cantinarr/features/media_access/data/media_user_import_controller.dart';
import 'package:cantinarr/features/media_access/ui/media_user_import_screen.dart';
import 'package:cantinarr/features/settings/ui/user_onboarding_status.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _server = ServiceInstance(
    id: 'jellyfin-1', serviceType: 'jellyfin', name: 'Family library');
UserSummary _user(int id, String name) => UserSummary(
      id: id,
      username: name,
      role: 'user',
      permissions: const [],
      createdAt: '',
      deviceCount: 0,
      hasPassword: false,
      passwordEnabled: false,
      passkeyEnabled: false,
      hasPendingInvite: true,
    );

class _Media extends MediaAccessService {
  _Media() : super(backendDio: Dio());
  List<RemoteMediaServerUser> remote = [];
  List<MediaServerAccountRow> accounts = [];
  List<UserSummary> users = [];
  bool failRead = false;
  final imports = <List<String>>[];
  final importManagement = <bool?>[];
  final links = <(int, String, bool?)>[];
  Future<List<MediaServerImportResult>> Function(List<String>)? onImport;
  Future<MediaServerAccountRow> Function(int, String)? onLink;

  @override
  Future<List<RemoteMediaServerUser>> listRemoteUsers(
          String instanceId) async =>
      remote;
  @override
  Future<List<MediaServerAccountRow>> listAccounts() async {
    if (failRead) throw Exception('offline');
    return accounts;
  }

  @override
  Future<List<MediaServerImportResult>> importAccounts(
      {required String instanceId,
      required List<String> remoteUserIds,
      required String serverUrl,
      bool? manageAccess}) async {
    imports.add(remoteUserIds);
    importManagement.add(manageAccess);
    return onImport?.call(remoteUserIds) ?? [];
  }

  @override
  Future<MediaServerAccountRow> link(
      {required int userId,
      required String instanceId,
      required String remoteUserId,
      bool? manageAccess}) async {
    links.add((userId, remoteUserId, manageAccess));
    if (onLink != null) return onLink!(userId, remoteUserId);
    final account = MediaServerAccountRow(
        userId: userId,
        instanceId: instanceId,
        instanceName: 'Family library',
        serviceType: 'jellyfin',
        remoteUserId: remoteUserId,
        remoteUsername: remoteUserId,
        granted: true,
        manageAccess: manageAccess ?? false);
    accounts.add(account);
    return account;
  }
}

MediaUserImportController _controller(_Media service,
        {bool capable = true, String type = 'jellyfin'}) =>
    MediaUserImportController(
        service: service,
        readUsers: () async => service.users,
        server: ServiceInstance(
            id: 'jellyfin-1', name: 'Family library', serviceType: type),
        serverUrl: 'https://cantinarr.example',
        supportsExplicitLinking: capable,
        supportsManagement: true);

class _Auth extends AuthNotifier {
  _Auth(this.media, {this.instances = const [_server], this.capable = true});
  final _Media media;
  final List<ServiceInstance> instances;
  final bool capable;
  List<ServiceInstance>? refreshedInstances;
  bool failRefresh = false;
  int configRefreshes = 0;
  @override
  Future<AuthState> build() async => AuthState(
          connection: BackendConnection(
        serverUrl: 'https://cantinarr.example',
        accessToken: 'test',
        refreshToken: 'test',
        instances: instances,
        mediaUserImportExplicitLinking: capable,
        mediaAccountManagement: true,
      ));
  @override
  Future<List<UserSummary>> listUsers() async => media.users;
  @override
  Future<void> refreshConfig() async {
    configRefreshes++;
    if (failRefresh) throw Exception('configuration unavailable');
    final current = state.value!;
    state = AsyncData(current.copyWith(
        connection: current.connection!
            .copyWith(instances: refreshedInstances ?? instances)));
  }
}

Future<void> _pump(WidgetTester tester, _Media media,
    {List<ServiceInstance> servers = const [_server],
    bool capable = true,
    _Auth? auth,
    String? instanceId,
    Size size = const Size(1100, 900)}) async {
  // Pin physical size and DPR so MediaQuery and screenshot dimensions describe
  // the requested viewport, with one raster pixel per logical pixel.
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(ProviderScope(
      overrides: [
        authProvider.overrideWith(
            () => auth ?? _Auth(media, instances: servers, capable: capable)),
        mediaAccessServiceProvider.overrideWithValue(media),
        backendClientProvider.overrideWithValue(Dio()
          ..interceptors.add(InterceptorsWrapper(onRequest: (options, handler) {
            handler.reject(DioException(requestOptions: options));
          }))),
      ],
      child: MaterialApp(
          theme: AppTheme.dark.copyWith(platform: TargetPlatform.android),
          home: AppAmbientBackground(
              child: MediaUserImportScreen(instanceId: instanceId)))));
  await tester.pumpAndSettle();
}

void main() {
  for (final viewport in <(String, Size)>[
    ('phone', const Size(390, 844)),
    ('desktop', const Size(1100, 900)),
  ]) {
    testWidgets('import workflow screenshot ${viewport.$1}', (tester) async {
      final media = _Media()
        ..users = [_user(1, 'Alice')]
        ..remote = const [
          RemoteMediaServerUser(id: 'a', name: 'Alice'),
          RemoteMediaServerUser(id: 'b', name: 'Bob'),
          RemoteMediaServerUser(id: 'c', name: 'Charlie', isDisabled: true),
        ];
      await _pump(tester, media, size: viewport.$2);
      // These text-rich scenes retain full coverage with renderer-specific
      // baselines. macOS and Linux rasterize Ahem/icon edges differently even
      // with identical layout; both use the unchanged repository comparator.
      await expectLater(
          find.byType(MaterialApp),
          matchesGoldenFile('goldens/${Platform.operatingSystem}/'
              'media_user_import_${viewport.$1}.png'));
      expect(tester.takeException(), isNull);
    });
  }

  for (final type in ['plex', 'jellyfin', 'emby', 'audiobookshelf']) {
    test('$type requires an explicit mapping for a normalized namesake',
        () async {
      final media = _Media()
        ..users = [
          _user(1, 'alice'),
          _user(2, 'Another user'),
          _user(3, 'blank-id')
        ]
        ..remote = const [
          RemoteMediaServerUser(id: 'a', name: '  alice  '),
          RemoteMediaServerUser(id: 'blank-id', name: '  ')
        ];
      final controller = _controller(media, type: type);
      addTearDown(controller.dispose);
      await controller.load();
      controller.selectAll(controller.choices);
      expect(controller.selected, isEmpty);
      expect(controller.choices.last.username, 'blank-id');
      final choice = controller.choices.first;
      expect(choice.targetUserId, isNull);
      controller.mapTo(choice, 2);
      controller.select(choice, true);
      await controller.submit();
      expect(media.imports, isEmpty);
      expect(media.links, [(2, 'a', false)]);
      expect(choice.complete, isTrue);
    });
  }

  test('select all excludes administrators and caps the batch at 200',
      () async {
    final media = _Media()
      ..remote = [
        const RemoteMediaServerUser(
            id: 'admin', name: 'Administrator', isAdministrator: true),
        for (var i = 0; i < 220; i++)
          RemoteMediaServerUser(id: '$i', name: 'User $i'),
      ];
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    expect(controller.selected, hasLength(200));
    expect(controller.choices.first.selected, isFalse);
    controller.select(controller.choices.last, true);
    expect(controller.selected, hasLength(200));
    controller.clearSelection();
    controller.select(controller.choices.first, true);
    expect(controller.selected.single.remote.isAdministrator, isTrue);
  });

  test('completed receipts do not consume the next batch selection limit',
      () async {
    final media = _Media()
      ..remote = [
        for (var i = 0; i < 240; i++)
          RemoteMediaServerUser(id: '$i', name: 'User $i')
      ];
    media.onImport = (ids) async => [
          for (final id in ids)
            MediaServerImportResult(
                remoteUserId: id,
                remoteUsername: 'User $id',
                userId: int.parse(id) + 1,
                username: 'User $id',
                created: true,
                linked: true)
        ];
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    await controller.submit();
    expect(controller.successfulUserIds, hasLength(200));
    controller.selectAll(controller.choices);
    expect(controller.pending, hasLength(40));
    expect(controller.selectedIncompleteCount, 40);
    await controller.submit();
    expect(media.imports.map((batch) => batch.length), [200, 40]);
    expect(controller.successfulUserIds, hasLength(240));
  });

  test('successful rows and invitations survive a failed link retry', () async {
    final media = _Media()
      ..remote = const [
        RemoteMediaServerUser(id: 'a', name: 'Alice'),
        RemoteMediaServerUser(id: 'b', name: 'Bob'),
      ];
    media.onImport = (_) async {
      media.users = [_user(10, 'Alice'), _user(11, 'Bob')];
      media.accounts = [
        const MediaServerAccountRow(
            userId: 10,
            instanceId: 'jellyfin-1',
            instanceName: 'Family',
            serviceType: 'jellyfin',
            remoteUserId: 'a',
            remoteUsername: 'Alice')
      ];
      return const [
        MediaServerImportResult(
            remoteUserId: 'a',
            remoteUsername: 'Alice',
            userId: 10,
            username: 'Alice',
            created: true,
            linked: true,
            link: 'invite-a'),
        MediaServerImportResult(
            remoteUserId: 'b',
            remoteUsername: 'Bob',
            userId: 11,
            username: 'Bob',
            created: true,
            error: 'link_failed',
            link: 'invite-b'),
      ];
    };
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    await controller.submit();
    expect(controller.successfulUserIds, [10]);
    expect(controller.pending.single.remote.id, 'b');
    controller.mapTo(controller.choices.last, 10);
    expect(controller.choices.last.targetUserId, 11);
    await controller.submit();
    expect(media.imports, [
      ['a', 'b']
    ]);
    expect(media.links, [(11, 'b', false)]);
    expect(controller.choices.last.result!.link, 'invite-b');
    expect(controller.successfulUserIds, [10, 11]);
  });

  test('lost import response reconciles links without replaying creation',
      () async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    media.onImport = (_) async {
      media.users = [_user(10, 'Alice')];
      media.accounts = [
        const MediaServerAccountRow(
            userId: 10,
            instanceId: 'jellyfin-1',
            instanceName: 'Family',
            serviceType: 'jellyfin',
            remoteUserId: 'a',
            remoteUsername: 'Alice')
      ];
      throw Exception('lost response');
    };
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    await controller.submit();
    expect(controller.choices.single.complete, isTrue);
    expect(controller.choices.single.result!.link, isEmpty);
    await controller.submit();
    expect(media.imports, hasLength(1));
    expect(media.links, isEmpty);
  });

  test('lost response with only a namesake requires explicit selection',
      () async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    media.onImport = (_) async {
      media.users = [_user(10, 'Alice')];
      throw Exception('unknown partial outcome');
    };
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    await controller.submit();
    expect(controller.choices.single.targetUserId, isNull);
    expect(controller.choices.single.requiresMapping, isTrue);
    expect(controller.canSubmit, isFalse);
    controller.mapTo(controller.choices.single, 10);
    controller.select(controller.choices.single, true);
    await controller.submit();
    expect(media.imports, hasLength(1));
    expect(media.links.single.$1, 10);
  });

  test('unavailable reconciliation blocks mutation until state can be read',
      () async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    media.onImport = (_) async {
      media.failRead = true;
      throw Exception('lost response');
    };
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    await controller.submit();
    expect(controller.error, contains('Could not verify'));
    expect(controller.choices.single.uncertain, isTrue);
    expect(controller.canSubmit, isFalse);
    await controller.submit();
    expect(media.imports, hasLength(1));
    media.failRead = false;
    await controller.reconcile();
    expect(controller.canSubmit, isTrue);
  });

  test('older servers cannot import through the new workflow', () async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    final controller = _controller(media, capable: false);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    await controller.submit();
    expect(media.imports, isEmpty);
    expect(media.links, isEmpty);
  });

  test('cleared uncertain rows still reconcile without replaying creation',
      () async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    media.onImport = (_) async {
      media.failRead = true;
      throw Exception('lost response');
    };
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    await controller.submit();
    controller.clearSelection();
    expect(controller.choices.single.selected, isFalse);
    expect(controller.choices.single.uncertain, isTrue);
    media.failRead = false;
    media.users = [_user(10, 'Alice')];
    media.accounts = [
      const MediaServerAccountRow(
          userId: 10,
          instanceId: 'jellyfin-1',
          instanceName: 'Family',
          serviceType: 'jellyfin',
          remoteUserId: 'a',
          remoteUsername: 'Alice')
    ];
    await controller.reconcile();
    expect(controller.choices.single.uncertain, isFalse);
    expect(controller.choices.single.complete, isTrue);
    expect(controller.successfulUserIds, [10]);
    await controller.submit();
    expect(media.imports, hasLength(1));
    expect(media.links, isEmpty);
  });

  test('reconciliation blocks mutations until current state has been read',
      () async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    Completer<List<UserSummary>>? pendingRead;
    final controller = MediaUserImportController(
        service: media,
        readUsers: () async =>
            pendingRead == null ? media.users : pendingRead.future,
        server: _server,
        serverUrl: 'https://cantinarr.example',
        supportsExplicitLinking: true,
        supportsManagement: true);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    pendingRead = Completer<List<UserSummary>>();
    final read = controller.reconcile();
    expect(controller.reconciling, isTrue);
    expect(controller.accountsCurrent, isFalse);
    expect(controller.canSubmit, isFalse);
    await controller.submit();
    expect(media.imports, isEmpty);
    pendingRead.complete(media.users);
    await read;
    expect(controller.accountsCurrent, isTrue);
    expect(controller.canSubmit, isTrue);
  });

  test('a persisted existing-user link keeps its confirmed retry target',
      () async {
    final media = _Media()
      ..users = [_user(10, 'First'), _user(11, 'Second')]
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    media.onLink = (id, remote) async {
      final row = MediaServerAccountRow(
          userId: id,
          instanceId: 'jellyfin-1',
          instanceName: 'Family',
          serviceType: 'jellyfin',
          remoteUserId: remote,
          remoteUsername: 'Alice',
          granted: media.links.length > 1);
      media.accounts = [row];
      return row;
    };
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    final choice = controller.choices.single;
    controller.mapTo(choice, 10);
    controller.select(choice, true);
    await controller.submit();
    expect(choice.result!.created, isFalse);
    expect(choice.result!.linked, isFalse);
    expect(choice.result!.userId, 10);
    controller.mapTo(choice, 11);
    expect(choice.targetUserId, 10);
    await controller.submit();
    expect(media.links.map((call) => call.$1), [10, 10]);
    expect(choice.complete, isTrue);
  });

  test(
      'a deleted explicit target requires a fresh choice without implicit creation',
      () async {
    final media = _Media()
      ..users = [_user(10, 'First'), _user(11, 'Second')]
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    media.onLink = (id, remote) async {
      media.users = [_user(11, 'Second')];
      throw const MediaAccessException(status: 404);
    };
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    final choice = controller.choices.single;
    controller.mapTo(choice, 10);
    controller.select(choice, true);
    await controller.submit();
    expect(choice.targetUserId, isNull);
    expect(choice.mappingUnavailable, isTrue);
    expect(choice.requiresMapping, isTrue);
    expect(choice.selected, isFalse);
    expect(controller.canSubmit, isFalse);
    controller.mapTo(choice, null);
    expect(choice.requiresMapping, isTrue);
    controller.mapTo(choice, 11);
    controller.select(choice, true);
    expect(controller.canSubmit, isTrue);
    expect(media.imports, isEmpty);
  });

  testWidgets(
      'a failed current-state check clears stale active status and refreshes next steps',
      (tester) async {
    final media = _Media()
      ..users = [_user(9, 'Existing')]
      ..remote = const [
        RemoteMediaServerUser(id: 'old', name: 'Existing'),
        RemoteMediaServerUser(
            id: 'a', name: 'Alice', isDisabled: true, pending: true),
      ]
      ..accounts = const [
        MediaServerAccountRow(
            userId: 9,
            instanceId: 'jellyfin-1',
            instanceName: 'Family',
            serviceType: 'jellyfin',
            remoteUserId: 'old',
            remoteUsername: 'Existing',
            verified: true)
      ];
    media.onImport = (_) async {
      media.users = [_user(9, 'Existing'), _user(10, 'Alice')];
      media.accounts = [
        const MediaServerAccountRow(
            userId: 9,
            instanceId: 'jellyfin-1',
            instanceName: 'Family',
            serviceType: 'jellyfin',
            remoteUserId: 'old',
            remoteUsername: 'Existing',
            verified: true),
        const MediaServerAccountRow(
            userId: 10,
            instanceId: 'jellyfin-1',
            instanceName: 'Family',
            serviceType: 'jellyfin',
            remoteUserId: 'a',
            remoteUsername: 'Alice',
            verified: true)
      ];
      return const [
        MediaServerImportResult(
            remoteUserId: 'a',
            remoteUsername: 'Alice',
            userId: 10,
            username: 'Alice',
            created: true,
            linked: true,
            link: 'original-invitation')
      ];
    };
    await _pump(tester, media);
    await tester.tap(find.text('Select all'));
    await tester.pump();
    await tester.tap(find.text('Import selected accounts'));
    await tester.pumpAndSettle();
    final freshAccount = tester.widget<CheckboxListTile>(find
        .descendant(
            of: find.byKey(const ValueKey('account-a')),
            matching: find.byType(CheckboxListTile))
        .first);
    expect((freshAccount.subtitle! as Text).data, contains('Active on server'));
    expect((freshAccount.subtitle! as Text).data, isNot(contains('Disabled')));
    expect((freshAccount.subtitle! as Text).data, isNot(contains('Awaiting')));
    final before =
        tester.widget<UserOnboardingStatus>(find.byType(UserOnboardingStatus));
    media.failRead = true;
    await tester.ensureVisible(find.text('Check current state'));
    await tester.tap(find.text('Check current state'));
    await tester.pumpAndSettle();
    final account = tester.widget<CheckboxListTile>(find
        .descendant(
            of: find.byKey(const ValueKey('account-a')),
            matching: find.byType(CheckboxListTile))
        .first);
    expect((account.subtitle! as Text).data,
        contains('Server access unconfirmed'));
    expect(
        (account.subtitle! as Text).data, isNot(contains('Active on server')));
    expect(find.textContaining('Last known link to Existing'), findsOneWidget);
    expect(find.textContaining('Already linked to Existing'), findsNothing);
    final after =
        tester.widget<UserOnboardingStatus>(find.byType(UserOnboardingStatus));
    expect(after.key, isNot(before.key));
    expect(after.invitationLink, 'original-invitation');
    expect(find.textContaining('Could not verify media-server accounts'),
        findsOneWidget);
    expect(media.imports, hasLength(1));
  });

  test('refresh keeps completed receipts for removed remote accounts',
      () async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    media.onImport = (_) async => const [
          MediaServerImportResult(
              remoteUserId: 'a',
              remoteUsername: 'Alice',
              userId: 10,
              username: 'Alice',
              created: true,
              linked: true,
              link: 'original-invitation')
        ];
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    await controller.submit();
    media.remote = [];
    await controller.load();
    expect(controller.choices.single.listed, isFalse);
    expect(controller.choices.single.complete, isTrue);
    expect(controller.choices.single.result!.link, 'original-invitation');
  });

  test('refresh replaces remote status metadata', () async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    media.remote = const [
      RemoteMediaServerUser(
          id: 'a', name: 'Alice', isAdministrator: true, isDisabled: true)
    ];
    await controller.load();
    expect(controller.choices.single.remote.isDisabled, isTrue);
    controller.selectAll(controller.choices);
    expect(controller.selected, isEmpty);
  });

  test('another account owner never discards a newly created user invitation',
      () async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    media.onImport = (_) async {
      media.users = [_user(10, 'Alice'), _user(20, 'Other owner')];
      media.accounts = [
        const MediaServerAccountRow(
            userId: 20,
            instanceId: 'jellyfin-1',
            instanceName: 'Family',
            serviceType: 'jellyfin',
            remoteUserId: 'a',
            remoteUsername: 'Alice')
      ];
      return const [
        MediaServerImportResult(
            remoteUserId: 'a',
            remoteUsername: 'Alice',
            userId: 10,
            username: 'Alice',
            created: true,
            error: 'link_failed',
            link: 'invite-a')
      ];
    };
    final controller = _controller(media);
    addTearDown(controller.dispose);
    await controller.load();
    controller.selectAll(controller.choices);
    await controller.submit();
    expect(controller.choices.single.result!.userId, 10);
    expect(controller.choices.single.result!.link, 'invite-a');
    expect(controller.choices.single.alreadyLinked, isTrue);
    expect(controller.choices.single.selectable, isFalse);
    expect(controller.canSubmit, isFalse);
  });

  testWidgets('a newly created preselected instance refreshes stale config',
      (tester) async {
    final media = _Media();
    final auth = _Auth(media, instances: [])..refreshedInstances = [_server];
    await _pump(tester, media, auth: auth, instanceId: _server.id);
    expect(auth.configRefreshes, 1);
    expect(find.text('Import Jellyfin users'), findsOneWidget);
    expect(find.textContaining('no longer configured'), findsNothing);
  });

  testWidgets('failed config refresh reports unknown instead of removed',
      (tester) async {
    final media = _Media();
    final auth = _Auth(media, instances: [])..failRefresh = true;
    await _pump(tester, media, auth: auth, instanceId: _server.id);
    expect(find.textContaining('Could not load the connected'), findsOneWidget);
    expect(find.textContaining('no longer configured'), findsNothing);
  });

  testWidgets('no servers offers a clearly labelled connect action',
      (tester) async {
    await _pump(tester, _Media(), servers: []);
    expect(find.text('Import users'), findsOneWidget);
    expect(find.text('Connect a media server'), findsOneWidget);
    expect(
        find.textContaining('Connect a media server before'), findsOneWidget);
  });

  testWidgets('multiple servers show provider and instance names',
      (tester) async {
    await _pump(tester, _Media(), servers: [
      _server,
      const ServiceInstance(id: 'plex-1', serviceType: 'plex', name: 'Cinema')
    ]);
    expect(find.text('Family library'), findsOneWidget);
    expect(find.text('Cinema'), findsOneWidget);
    expect(find.text('Jellyfin'), findsOneWidget);
    expect(find.text('Plex'), findsOneWidget);
    await tester.tap(find.text('Cinema'));
    await tester.pumpAndSettle();
    expect(find.text('Import Plex users'), findsOneWidget);
  });

  testWidgets(
      'phone picker searches accounts and shows status and explicit mapping',
      (tester) async {
    final media = _Media()
      ..users = [_user(1, 'Alice')]
      ..remote = const [
        RemoteMediaServerUser(id: 'a', name: 'Alice', pending: true),
        RemoteMediaServerUser(id: 'b', name: 'Bob', isDisabled: true),
        RemoteMediaServerUser(id: 'c', name: 'Charles', isAdministrator: true),
      ];
    await _pump(tester, media, size: const Size(390, 844));
    expect(find.text('Import Jellyfin users'), findsOneWidget);
    expect(find.textContaining('password does not become'), findsOneWidget);
    await tester.enterText(find.byType(TextField), 'Alice');
    await tester.pumpAndSettle();
    expect(
        find.descendant(
            of: find.byType(CheckboxListTile), matching: find.text('Alice')),
        findsOneWidget);
    expect(find.text('Bob'), findsNothing);
    expect(
        find.textContaining('Awaiting invitation acceptance'), findsOneWidget);
    expect(find.text('Choose a Cantinarr user'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('old-server explanation prevents submission', (tester) async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    await _pump(tester, media, capable: false);
    expect(find.textContaining('Update the Cantinarr server'), findsOneWidget);
    await tester.tap(find.text('Select all'));
    await tester.pump();
    expect(
        tester
            .widget<FilledButton>(
                find.widgetWithText(FilledButton, 'Import selected accounts'))
            .onPressed,
        isNull);
    expect(media.imports, isEmpty);
  });

  testWidgets('unavailable account read is not presented as no accounts',
      (tester) async {
    await _pump(tester, _Media()..failRead = true);
    expect(find.textContaining('existing links are unknown'), findsOneWidget);
    expect(find.textContaining('reported no accounts'), findsNothing);
    expect(find.text('Refresh accounts'), findsOneWidget);
  });

  testWidgets('linked accounts keep readable owner and status text',
      (tester) async {
    final media = _Media()
      ..users = [_user(10, 'Alice')]
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')]
      ..accounts = const [
        MediaServerAccountRow(
          userId: 10,
          instanceId: 'jellyfin-1',
          instanceName: 'Family library',
          serviceType: 'jellyfin',
          remoteUserId: 'a',
          remoteUsername: 'Alice',
          verified: true,
          manageAccess: false,
        )
      ];
    await _pump(tester, media);
    final account = find.descendant(
        of: find.byKey(const ValueKey('account-a')),
        matching: find.byType(CheckboxListTile));
    final tile = tester.widget<CheckboxListTile>(account);
    expect(tile.onChanged, isNull);
    expect((tile.title! as Text).style!.color, AppTheme.textPrimary);
    expect((tile.subtitle! as Text).style!.color, AppTheme.textSecondary);
    expect(find.textContaining('Already linked to Alice'), findsOneWidget);
    expect(find.textContaining('Active on server'), findsOneWidget);
  });

  testWidgets('account controls have labels and support keyboard selection',
      (tester) async {
    final media = _Media()
      ..remote = const [RemoteMediaServerUser(id: 'a', name: 'Alice')];
    await _pump(tester, media);
    final semantics = tester.ensureSemantics();
    try {
      expect(find.bySemanticsLabel('Search accounts'), findsOneWidget);
      expect(find.bySemanticsLabel(RegExp('Alice')), findsWidgets);
      await tester.tap(find.byType(TextField));
      await tester.sendKeyEvent(LogicalKeyboardKey.tab);
      await tester.sendKeyEvent(LogicalKeyboardKey.enter);
      await tester.pumpAndSettle();
      final account = find.descendant(
          of: find.byKey(const ValueKey('account-a')),
          matching: find.byType(CheckboxListTile));
      expect(tester.widget<CheckboxListTile>(account).value, isTrue);
    } finally {
      semantics.dispose();
    }
  });
}
