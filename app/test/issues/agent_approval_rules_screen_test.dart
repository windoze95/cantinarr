import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/issues/data/agent_approval_rule_models.dart';
import 'package:cantinarr/features/issues/data/issues_service.dart';
import 'package:cantinarr/features/issues/logic/issues_provider.dart';
import 'package:cantinarr/features/issues/ui/agent_approval_rules_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

AgentApprovalRule _rule({
  int id = 1,
  String status = 'active',
  String? pausedReason,
}) =>
    AgentApprovalRule.fromJson({
      'id': id,
      'problem_kind': 'Waiting to import',
      'action_kind': 'manual_import',
      'action_facet': '',
      'label': 'Manual import · Waiting to import',
      'status': status,
      if (pausedReason != null) 'paused_reason': pausedReason,
      'created_by_name': 'admin',
      'approved_count': 14,
      'resolved_count': 13,
    });

class _FakeIssuesService extends IssuesService {
  _FakeIssuesService() : super(backendDio: Dio());

  List<AgentApprovalRule> rules = [];
  Object? pauseError;
  int pauseCalls = 0;
  int resumeCalls = 0;
  int deleteCalls = 0;

  @override
  Future<List<AgentApprovalRule>> listApprovalRules() async =>
      List.of(rules);

  @override
  Future<AgentApprovalRule> pauseApprovalRule(int id) async {
    pauseCalls++;
    final error = pauseError;
    if (error != null) throw error;
    return _rule(id: id, status: 'paused', pausedReason: 'Paused by an administrator.');
  }

  @override
  Future<AgentApprovalRule> resumeApprovalRule(int id) async {
    resumeCalls++;
    return _rule(id: id);
  }

  @override
  Future<void> deleteApprovalRule(int id) async {
    deleteCalls++;
    rules = [
      for (final r in rules)
        if (r.id != id) r,
    ];
  }
}

Future<void> _pump(WidgetTester tester, _FakeIssuesService service) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        issuesServiceProvider.overrideWithValue(service),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
      child: const MaterialApp(home: AgentApprovalRulesScreen()),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('renders a rule with its label, state, and track record',
      (tester) async {
    final service = _FakeIssuesService()
      ..rules = [
        _rule(),
        _rule(
          id: 2,
          status: 'paused',
          pausedReason: 'An auto-approved fix failed to execute.',
        ),
      ];
    await _pump(tester, service);

    expect(find.text('Manual import · Waiting to import'), findsNWidgets(2));
    expect(find.text('Active'), findsOneWidget);
    expect(find.text('Paused'), findsOneWidget);
    expect(
      find.text('An auto-approved fix failed to execute.'),
      findsOneWidget,
    );
    expect(find.textContaining('Approved 14 · Resolved 13'), findsNWidgets(2));
    // The paused rule offers Resume; the active one offers Pause.
    expect(find.widgetWithText(OutlinedButton, 'Pause'), findsOneWidget);
    expect(find.widgetWithText(OutlinedButton, 'Resume'), findsOneWidget);
  });

  testWidgets('empty state explains how a rule is created', (tester) async {
    await _pump(tester, _FakeIssuesService());
    expect(find.text('No standing rules yet.'), findsOneWidget);
    expect(find.textContaining('Always approve this fix'), findsOneWidget);
  });

  testWidgets('pause calls the service and adopts the returned state',
      (tester) async {
    final service = _FakeIssuesService()..rules = [_rule()];
    await _pump(tester, service);

    await tester.tap(find.widgetWithText(OutlinedButton, 'Pause'));
    await tester.pumpAndSettle();

    expect(service.pauseCalls, 1);
    expect(find.text('Paused'), findsOneWidget);
    expect(find.widgetWithText(OutlinedButton, 'Resume'), findsOneWidget);
  });

  testWidgets('a failed pause surfaces an error and reloads the truth',
      (tester) async {
    final service = _FakeIssuesService()
      ..rules = [_rule()]
      ..pauseError = DioException(
        requestOptions: RequestOptions(path: '/pause'),
        type: DioExceptionType.connectionError,
      );
    await _pump(tester, service);

    await tester.tap(find.widgetWithText(OutlinedButton, 'Pause'));
    await tester.pumpAndSettle();

    // The reload restored the authoritative (still active) state.
    expect(find.text('Active'), findsOneWidget);
    expect(find.byType(SnackBar), findsOneWidget);
  });

  testWidgets('delete asks for confirmation and removes the rule',
      (tester) async {
    final service = _FakeIssuesService()..rules = [_rule()];
    await _pump(tester, service);

    await tester.tap(find.widgetWithText(TextButton, 'Delete'));
    await tester.pumpAndSettle();
    expect(find.text('Delete this rule?'), findsOneWidget);

    // Cancel keeps it.
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    expect(service.deleteCalls, 0);
    expect(find.text('Manual import · Waiting to import'), findsOneWidget);

    // Confirm removes it.
    await tester.tap(find.widgetWithText(TextButton, 'Delete'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Delete'));
    await tester.pumpAndSettle();
    expect(service.deleteCalls, 1);
    expect(find.text('No standing rules yet.'), findsOneWidget);
  });
}
