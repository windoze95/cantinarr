import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/issues/data/agent_action_models.dart';
import 'package:cantinarr/features/issues/data/issues_service.dart';
import 'package:cantinarr/features/issues/logic/issues_provider.dart';
import 'package:cantinarr/features/issues/ui/pending_agent_actions_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// A minimal actionable proposal: recognized kind, well-formed params, verified
/// sonarr target matching the tv issue — so `canTakeAction` is true.
AgentAction _proposal({required int id, required int issueId}) =>
    AgentAction.fromJson({
      'id': id,
      'issue_id': issueId,
      'kind': 'trigger_search',
      'params': {
        'media_type': 'tv',
        'tmdb_id': 42,
        'season': 2,
      },
      'rationale': 'Search for a replacement.',
      'status': 'proposed',
      'can_decide': true,
      'issue_status': 'awaiting_approval',
      'issue_title': 'The Show $issueId',
      'issue_media_type': 'tv',
      'instance_id': 'sonarr-living-room',
      'instance_name': 'Living Room TV',
      'instance_service_type': 'sonarr',
    });

AgentActionBatchResult _result(int id, String status) =>
    AgentActionBatchResult.fromJson({'id': id, 'status': status});

class _FakeIssuesService extends IssuesService {
  _FakeIssuesService() : super(backendDio: Dio());

  List<AgentAction> actions = [];

  /// The queue snapshot served after a batch, so tests can watch it drain.
  List<AgentAction> afterBatch = [];
  List<int>? lastBatchIds;
  List<AgentActionBatchResult> batchResults = [];

  @override
  Future<List<AgentAction>> listAllActions() async => List.of(actions);

  @override
  Future<List<AgentActionBatchResult>> approveActionsBatch(
      List<int> ids) async {
    lastBatchIds = List.of(ids);
    actions = List.of(afterBatch);
    return batchResults;
  }
}

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;
  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

Future<void> _pump(WidgetTester tester, _FakeIssuesService service) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_adminState)),
        issuesServiceProvider.overrideWithValue(service),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
      child: const MaterialApp(home: PendingAgentActionsScreen()),
    ),
  );
  await tester.pumpAndSettle();
}

Finder get _appBarButton => find.widgetWithText(TextButton, 'Approve all');
Finder get _dialogConfirm => find.widgetWithText(ElevatedButton, 'Approve all');

void main() {
  testWidgets('Approve all appears only with two or more actionable proposals',
      (tester) async {
    final service = _FakeIssuesService()
      ..actions = [_proposal(id: 21, issueId: 5)];
    await _pump(tester, service);
    expect(_appBarButton, findsNothing);

    service.actions = [
      _proposal(id: 21, issueId: 5),
      _proposal(id: 22, issueId: 6),
    ];
    await tester.drag(
        find.byType(RefreshIndicator), const Offset(0, 300));
    await tester.pumpAndSettle();
    expect(_appBarButton, findsOneWidget);

    // The bulk affordance belongs to the awaiting queue, not History.
    await tester.tap(find.text('History'));
    await tester.pumpAndSettle();
    expect(_appBarButton, findsNothing);
  });

  testWidgets('Approve all confirms, submits the reviewed ids, and reports',
      (tester) async {
    final service = _FakeIssuesService()
      ..actions = [
        _proposal(id: 21, issueId: 5),
        _proposal(id: 22, issueId: 6),
      ]
      ..batchResults = [
        _result(21, 'executed'),
        _result(22, 'executed'),
      ];
    await _pump(tester, service);

    await tester.tap(_appBarButton);
    await tester.pumpAndSettle();
    expect(find.text('Approve all 2 fixes?'), findsOneWidget);

    await tester.tap(_dialogConfirm);
    await tester.pumpAndSettle();

    expect(service.lastBatchIds, [21, 22]);
    expect(find.text('All 2 fixes applied.'), findsOneWidget);
    // The reload adopted the drained queue, so the affordance is gone.
    expect(_appBarButton, findsNothing);
  });

  testWidgets('cancelling the confirmation submits nothing', (tester) async {
    final service = _FakeIssuesService()
      ..actions = [
        _proposal(id: 21, issueId: 5),
        _proposal(id: 22, issueId: 6),
      ];
    await _pump(tester, service);

    await tester.tap(_appBarButton);
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();

    expect(service.lastBatchIds, isNull);
    expect(_appBarButton, findsOneWidget);
  });

  testWidgets('a mixed batch reports applied, skipped, and needs attention',
      (tester) async {
    final service = _FakeIssuesService()
      ..actions = [
        _proposal(id: 21, issueId: 5),
        _proposal(id: 22, issueId: 6),
        _proposal(id: 23, issueId: 7),
      ]
      ..batchResults = [
        _result(21, 'executed'),
        _result(22, 'skipped'),
        _result(23, 'outcome_unknown'),
      ];
    await _pump(tester, service);

    await tester.tap(_appBarButton);
    await tester.pumpAndSettle();
    await tester.tap(_dialogConfirm);
    await tester.pumpAndSettle();

    expect(
      find.text('Approve all: 1 applied · 1 skipped · 1 needs attention.'),
      findsOneWidget,
    );
  });
}
