import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/tdarr/ui/tdarr_screen.dart';
import 'package:cantinarr/features/tdarr/ui/tdarr_module_shell.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _instance = ServiceInstance(id: 'one', serviceType: 'tdarr', name: 'Tdarr');
const _second = ServiceInstance(id: 'two', serviceType: 'tdarr', name: 'Other');
// Local wall time keeps date text identical on developer and CI machines.
const _observed = '2026-09-15T12:00:00';

class _Instances extends InstanceNotifier {
  @override
  InstanceState build() => const InstanceState(
      tdarrInstances: [_instance, _second], activeTdarrInstanceId: 'one');
}

class _Adapter implements HttpClientAdapter {
  final List<RequestOptions> requests = [];
  bool offline = false;
  Future<Map<String, dynamic>> Function(RequestOptions)? answer;
  Map<String, dynamic> activity = _activity();
  List<Map<String, dynamic>> libraries = [
    {'id': 'films', 'name': 'Films'},
    {'id': 'series', 'name': 'Series'},
  ];

  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    requests.add(options);
    final body = answer != null ? await answer!(options) : offline
        ? {'error': 'Could not reach Tdarr'}
        : options.path.endsWith('/activity') ? activity
        : options.path.endsWith('/libraries')
        ? {'observed_at': _observed, 'items': libraries}
        : {
          'observed_at': _observed,
          'library_id': options.queryParameters['library_id'] ?? '',
          'total_files': options.queryParameters['library_id'] == 'films' ? 5 : 12,
          'note': 'Status counts are not an overall completion percentage.',
          'transcodes': [
            {'label': 'Queued', 'value': 2},
            {'label': 'Not required', 'value': 3},
            {'label': 'Future state', 'value': null},
          ],
          'health_checks': <dynamic>[],
        };
    return ResponseBody.fromString(jsonEncode(body), offline ? 502 : 200,
        headers: {'content-type': ['application/json']});
  }

  @override
  void close({bool force = false}) {}
}

Map<String, dynamic> _activity({bool working = true, bool noNodes = false}) => {
  'observed_at': _observed,
  'nodes': noNodes ? <dynamic>[] : [
    {'id': 'node', 'name': 'Local node', 'paused': false, 'workers': working ? [
      {'id': 'worker', 'file': r'C:\media\sample.mkv', 'kind': 'Transcode',
        'compute': 'CPU', 'status': 'Transcoding', 'eta': '', 'flow': true,
        'progress_percent': null, 'fps': null},
    ] : <dynamic>[]},
  ],
};

Future<ProviderContainer> _pump(WidgetTester tester, _Adapter adapter,
    {bool activity = true, bool shell = false, ValueNotifier<bool>? visible}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))..httpClientAdapter = adapter;
  final container = ProviderContainer(overrides: [
    instanceProvider.overrideWith(_Instances.new),
    backendClientProvider.overrideWithValue(dio),
  ]);
  addTearDown(container.dispose);
  Widget screen = TdarrScreen(activity: activity);
  if (shell) {
    screen = TdarrModuleShell(currentIndex: activity ? 0 : 1,
        onTabChanged: (_) {}, child: screen);
  }
  await tester.pumpWidget(UncontrolledProviderScope(container: container,
    child: RepaintBoundary(key: const ValueKey('tdarr-golden'),
      child: MaterialApp(theme: AppTheme.dark, debugShowCheckedModeBanner: false,
        home: Scaffold(body: visible == null ? screen :
      ValueListenableBuilder<bool>(valueListenable: visible, child: screen,
        builder: (_, enabled, child) => TickerMode(enabled: enabled, child: child!))))),
  ));
  await tester.pumpAndSettle();
  return container;
}

Future<void> _dispose(WidgetTester tester) async => tester.pumpWidget(const SizedBox());

void main() {
  for (final activity in [true, false]) {
    testWidgets('${activity ? "activity" : "libraries"} fits a narrow phone', (tester) async {
      tester.view.physicalSize = const Size(390, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final adapter = _Adapter();
      if (activity) {
        final worker = (adapter.activity['nodes'] as List).first['workers'][0] as Map<String, dynamic>;
        worker['progress_percent'] = 42.5;
        worker['fps'] = 68;
        worker['eta'] = '0:02:14';
      }
      await _pump(tester, adapter, activity: activity, shell: true);
      expect(tester.takeException(), isNull);
      // Capture at a phone's 3x density: one-pixel Ahem glyph-edge differences
      // between macOS and Linux then remain raster noise, without changing
      // the shared comparator's tolerance or masking any content/layout.
      final boundary = tester.renderObject<RenderRepaintBoundary>(
          find.byKey(const ValueKey('tdarr-golden')));
      final capture = boundary.toImage(pixelRatio: 3);
      try {
        await expectLater(capture, matchesGoldenFile(
            'goldens/tdarr_${activity ? "activity" : "libraries"}_phone.png'));
      } finally {
        (await capture).dispose();
      }
      await _dispose(tester);
    });
  }

  testWidgets('worker progress is step-scoped and missing values stay unknown', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    expect(adapter.requests.single.path, '/api/tdarr/one/activity');
    expect(find.text('sample.mkv'), findsOneWidget);
    expect(find.text('Transcode · CPU'), findsOneWidget);
    expect(find.text('Progress unavailable'), findsOneWidget);
    expect(find.text('0.0%'), findsNothing);
    expect(find.textContaining('current processing step'), findsOneWidget);
    await tester.tap(find.text('Source file'));
    await tester.pumpAndSettle();
    expect(find.text(r'C:\media\sample.mkv'), findsOneWidget);
    await _dispose(tester);
  });

  testWidgets('stale snapshot survives an outage and retry recovers it', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    adapter.offline = true;
    await tester.pump(const Duration(seconds: 10));
    await tester.pumpAndSettle();
    expect(find.text('Showing stale data'), findsOneWidget);
    expect(find.text('sample.mkv'), findsOneWidget);
    expect(find.textContaining('Updated '), findsOneWidget);
    adapter.offline = false;
    adapter.activity = _activity(working: false);
    await tester.tap(find.text('Retry'));
    await tester.pumpAndSettle();
    expect(find.text('Showing stale data'), findsNothing);
    expect(find.text('Idle'), findsOneWidget);
    expect(find.textContaining('Nothing is processing'), findsOneWidget);
    await _dispose(tester);
  });

  testWidgets('offline is not confused with an empty node list', (tester) async {
    final adapter = _Adapter()..offline = true;
    await _pump(tester, adapter);
    expect(find.text('Tdarr unavailable'), findsOneWidget);
    expect(find.text('No nodes connected'), findsNothing);
    adapter.offline = false;
    adapter.activity = _activity(noNodes: true);
    await tester.tap(find.text('Retry'));
    await tester.pumpAndSettle();
    expect(find.text('No nodes connected'), findsOneWidget);
    expect(find.text('Tdarr unavailable'), findsNothing);
    await _dispose(tester);
  });

  testWidgets('libraries fetch only the selection and retain unknown status counts', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter, activity: false);
    expect(adapter.requests.map((r) => r.path), [
      '/api/tdarr/one/libraries', '/api/tdarr/one/stats',
    ]);
    expect(find.text('12 files'), findsOneWidget);
    expect(find.text('Status counts are not an overall completion percentage.'), findsOneWidget);
    expect(find.text('Future state'), findsOneWidget);
    expect(find.text('Unavailable'), findsOneWidget);
    expect(adapter.requests.last.queryParameters, isEmpty);
    await tester.tap(find.text('All libraries'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Films').last);
    await tester.pumpAndSettle();
    expect(find.text('5 files'), findsOneWidget);
    expect(adapter.requests.last.queryParameters, {'library_id': 'films'});
    expect(adapter.requests.where((r) => r.queryParameters['library_id'] == 'series'), isEmpty);
    adapter.libraries = [];
    final statsReads = adapter.requests.where((r) => r.path.endsWith('/stats')).length;
    await tester.pump(const Duration(seconds: 30));
    await tester.pumpAndSettle();
    expect(find.text('Showing stale data'), findsOneWidget);
    expect(find.textContaining('Selected library no longer exists'), findsOneWidget);
    expect(find.text('Removed library'), findsOneWidget);
    expect(adapter.requests.where((r) => r.path.endsWith('/stats')).length, statsReads);
    await _dispose(tester);
  });

  testWidgets('hidden and background views do not poll and resume immediately', (tester) async {
    final adapter = _Adapter();
    final visible = ValueNotifier(true);
    addTearDown(visible.dispose);
    await _pump(tester, adapter, visible: visible);
    visible.value = false;
    await tester.pumpAndSettle();
    await tester.pump(const Duration(seconds: 40));
    expect(adapter.requests, hasLength(1));
    visible.value = true;
    await tester.pumpAndSettle();
    expect(adapter.requests, hasLength(2));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(seconds: 40));
    expect(adapter.requests, hasLength(2));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(adapter.requests, hasLength(3));
    await _dispose(tester);
  });

  testWidgets('instance changes discard old data and outstanding responses', (tester) async {
    final adapter = _Adapter();
    final container = await _pump(tester, adapter);
    final pending = Completer<Map<String, dynamic>>();
    adapter.answer = (r) => r.path.contains('/one/') ? pending.future :
        Future.value(_activity(noNodes: true));
    await tester.tap(find.byTooltip('Refresh'));
    await tester.pump();
    container.read(instanceProvider.notifier).setActiveTdarrInstance('two');
    await tester.pumpAndSettle();
    expect(find.text('sample.mkv'), findsNothing);
    expect(find.text('No nodes connected'), findsOneWidget);
    pending.complete(_activity());
    await tester.pumpAndSettle();
    expect(find.text('sample.mkv'), findsNothing);
    expect(adapter.requests.last.path, '/api/tdarr/two/activity');
    await _dispose(tester);
  });
}
