import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/issues/data/agent_action_models.dart';
import 'package:cantinarr/features/issues/data/issue_models.dart';
import 'package:cantinarr/features/issues/data/issues_service.dart';
import 'package:cantinarr/features/issues/logic/issues_provider.dart';
import 'package:cantinarr/features/issues/ui/agent_run_screen.dart';
import 'package:cantinarr/features/issues/ui/issue_thread_screen.dart';
import 'package:cantinarr/features/issues/ui/issues_list_screen.dart';
import 'package:cantinarr/features/issues/ui/pending_agent_actions_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => _adminState;
}

Map<String, dynamic> _issueJson({
  String status = 'awaiting_approval',
  String resolution = '',
  String resolutionKind = '',
  String? closedAt,
}) =>
    {
      'id': 5,
      'source': 'auto',
      'status': status,
      'category': null,
      'reporter_id': null,
      'reporter_name': null,
      'tmdb_id': 123,
      'media_type': 'tv',
      'title': 'Example Show',
      'season_number': 2,
      'episode_number': 4,
      'detail': 'The download stopped importing.',
      'occurrences': 1,
      'read': false,
      'resolution': resolution,
      'resolution_kind': resolutionKind,
      'created_at': '2026-07-10T10:00:00Z',
      'updated_at': '2026-07-10T10:05:00Z',
      'closed_at': closedAt,
    };

AgentAction _proposedAction() => AgentAction.fromJson({
      'id': 12,
      'issue_id': 5,
      'run_id': 9,
      'kind': 'rescan',
      'params': {'media_type': 'tv', 'tmdb_id': 123},
      'rationale': 'Re-run the import scan.',
      'risk': 'mutating',
      'status': 'proposed',
      'can_decide': true,
      'issue_status': 'awaiting_approval',
      'instance_id': 'sonarr-main',
      'instance_name': 'Main TV',
      'instance_service_type': 'sonarr',
      'issue_title': 'Example Show',
      'issue_media_type': 'tv',
      'created_at': '2026-07-10T10:05:00Z',
    });

AgentRun _completedRun() => AgentRun.fromJson({
      'id': 9,
      'issue_id': 5,
      'trigger': 'user_report',
      'status': 'succeeded',
      'model': 'test-model',
      'step_count': 3,
      'input_tokens': 10,
      'output_tokens': 5,
      'cache_creation_tokens': 0,
      'cache_read_tokens': 0,
      'cost_micros': 100,
      'stop_reason': 'resolved',
      'started_at': '2026-07-10T10:00:00Z',
      'finished_at': '2026-07-10T10:01:00Z',
    });

AgentRun _resumePendingRun() => AgentRun.fromJson({
      'id': 9,
      'issue_id': 5,
      'trigger': 'user_report',
      'status': 'resume_pending',
      'model': 'test-model',
      'step_count': 3,
      'input_tokens': 10,
      'output_tokens': 5,
      'cache_creation_tokens': 0,
      'cache_read_tokens': 0,
      'cost_micros': 100,
      'stop_reason': null,
      'started_at': '2026-07-10T10:00:00Z',
      'finished_at': null,
    });

class _FakeIssuesService extends IssuesService {
  _FakeIssuesService({
    required this.thread,
    this.activity = const IssueAgentActivity(actions: [], runs: []),
    this.issues = const [],
    this.actions = const [],
    this.runDetail,
  }) : super(backendDio: Dio());

  IssueThread thread;
  IssueAgentActivity activity;
  List<Issue> issues;
  List<AgentAction> actions;
  AgentRunDetail? runDetail;
  bool failThread = false;
  bool failActivity = false;
  bool failIssues = false;
  bool failActions = false;
  bool failRun = false;
  Object? resolveError;
  IssueThread? threadOnResolveError;
  int threadLoads = 0;
  int runLoads = 0;
  int resolveCalls = 0;
  AdminIssueDisposition? lastDisposition;
  String? lastResolutionNote;

  @override
  Future<IssueThread> getThread(int id) async {
    threadLoads++;
    if (failThread) throw StateError('thread unavailable');
    return thread;
  }

  @override
  Future<IssueAgentActivity> getIssueActivity(int issueId) async {
    if (failActivity) throw StateError('activity unavailable');
    return activity;
  }

  @override
  Future<List<Issue>> listIssues({String? status}) async {
    if (failIssues) throw StateError('issues unavailable');
    return issues;
  }

  @override
  Future<List<AgentAction>> listAllActions() async {
    if (failActions) throw StateError('actions unavailable');
    return actions;
  }

  @override
  Future<List<AgentAction>> listPendingActions(
      {String status = 'proposed'}) async {
    if (failActions) throw StateError('actions unavailable');
    return actions.where((action) => action.canTakeAction).toList();
  }

  @override
  Future<AgentRunDetail> getRun(int id) async {
    runLoads++;
    if (failRun) throw StateError('run unavailable');
    return runDetail ??
        AgentRunDetail(run: _completedRun(), steps: const <AgentStep>[]);
  }

  @override
  Future<Issue> resolveIssue(
    int id, {
    required AdminIssueDisposition disposition,
    required String note,
  }) async {
    resolveCalls++;
    lastDisposition = disposition;
    lastResolutionNote = note;
    final error = resolveError;
    if (error != null) {
      if (threadOnResolveError != null) thread = threadOnResolveError!;
      throw error;
    }
    final status =
        disposition == AdminIssueDisposition.resolved ? 'resolved' : 'wont_fix';
    final issue = Issue.fromJson(_issueJson(
      status: status,
      resolution: note,
      resolutionKind: 'admin_completed',
      closedAt: '2026-07-10T11:00:00Z',
    ));
    thread = IssueThread(issue: issue, messages: const []);
    return issue;
  }
}

Future<void> _pumpScreen(
  WidgetTester tester, {
  required _FakeIssuesService service,
  required Widget screen,
}) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        issuesServiceProvider.overrideWithValue(service),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
      child: MaterialApp(home: screen),
    ),
  );
  await tester.pumpAndSettle();
}

Future<void> _resumeApp(WidgetTester tester) async {
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('needs_admin shows its instruction and activity failure',
      (tester) async {
    const instruction =
        'An approved action was interrupted. Verify the arr state manually.';
    final service = _FakeIssuesService(
      thread: IssueThread.fromJson({
        'issue': _issueJson(
          status: 'needs_admin',
          resolution: instruction,
        ),
        'thread': const [],
      }),
    )..failActivity = true;

    await _pumpScreen(
      tester,
      service: service,
      screen: const IssueThreadScreen(issueId: 5),
    );
    // Ensure the admin auth state has settled before the audit request.
    await _resumeApp(tester);

    expect(find.text('Needs a closer look'), findsWidgets);
    expect(find.text(instruction), findsOneWidget);
    expect(
      find.text(
          "Agent activity couldn't be refreshed. History may be incomplete."),
      findsOneWidget,
    );
    expect(find.text('The download stopped importing.'), findsOneWidget);
    expect(find.text('Complete after admin review'), findsOneWidget);
    expect(
        find.widgetWithText(ElevatedButton, 'Mark resolved'), findsOneWidget);
    expect(find.widgetWithText(OutlinedButton, 'Close without fix'),
        findsOneWidget);
  });

  testWidgets('admin completion requires a note and records resolved outcome',
      (tester) async {
    final service = _FakeIssuesService(
      thread: IssueThread.fromJson({
        'issue': _issueJson(
          status: 'needs_admin',
          resolution: 'Verify the uncertain action manually.',
        ),
        'thread': const [],
      }),
    );
    await _pumpScreen(
      tester,
      service: service,
      screen: const IssueThreadScreen(issueId: 5),
    );
    await _resumeApp(tester);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Mark resolved'));
    await tester.pumpAndSettle();
    final dialog = find.byType(AlertDialog);
    expect(
        find.descendant(
            of: dialog, matching: find.text('Mark this issue resolved?')),
        findsOneWidget);
    expect(find.textContaining('must be verified manually'), findsOneWidget);
    final confirm = find.descendant(
      of: dialog,
      matching: find.widgetWithText(ElevatedButton, 'Mark resolved'),
    );
    expect(tester.widget<ElevatedButton>(confirm).onPressed, isNull);

    const note =
        'Checked Sonarr: the replacement episode is imported and plays correctly.';
    await tester.enterText(
      find.descendant(of: dialog, matching: find.byType(TextField)),
      note,
    );
    await tester.pump();
    expect(tester.widget<ElevatedButton>(confirm).onPressed, isNotNull);
    await tester.tap(confirm);
    await tester.pumpAndSettle();

    expect(service.resolveCalls, 1);
    expect(service.lastDisposition, AdminIssueDisposition.resolved);
    expect(service.lastResolutionNote, note);
    expect(find.textContaining('Completed after review'), findsOneWidget);
    expect(find.text(note), findsOneWidget);
    expect(find.text('Complete after admin review'), findsNothing);
    expect(find.text('Issue marked resolved.'), findsOneWidget);
  });

  testWidgets('completion conflict reloads winner without claiming success',
      (tester) async {
    final winner = IssueThread.fromJson({
      'issue': _issueJson(
        status: 'resolved',
        resolution: 'The original queue signal cleared before admin review.',
        resolutionKind: 'arr_state_cleared',
        closedAt: '2026-07-10T11:00:00Z',
      ),
      'thread': const [],
    });
    final service = _FakeIssuesService(
      thread: IssueThread.fromJson({
        'issue': _issueJson(status: 'needs_admin'),
        'thread': const [],
      }),
    )
      ..threadOnResolveError = winner
      ..resolveError = DioException.badResponse(
        statusCode: 409,
        requestOptions: RequestOptions(path: '/resolve'),
        response: Response(
          requestOptions: RequestOptions(path: '/resolve'),
          statusCode: 409,
          data: {'error': 'issue completion conflict'},
        ),
      );
    await _pumpScreen(
      tester,
      service: service,
      screen: const IssueThreadScreen(issueId: 5),
    );
    await _resumeApp(tester);

    await tester.tap(find.widgetWithText(OutlinedButton, 'Close without fix'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.descendant(
        of: find.byType(AlertDialog),
        matching: find.byType(TextField),
      ),
      'Reviewed manually; no safe fix remains.',
    );
    await tester.pump();
    final confirm = find.descendant(
      of: find.byType(AlertDialog),
      matching: find.widgetWithText(ElevatedButton, 'Close without fix'),
    );
    await tester.tap(confirm);
    await tester.pumpAndSettle();

    expect(service.resolveCalls, 1);
    expect(find.textContaining('Media became available'), findsOneWidget);
    expect(
      find.text(
          'The issue changed before completion. Showing its current state.'),
      findsOneWidget,
    );
    expect(find.text('Issue closed without a fix.'), findsNothing);
  });

  testWidgets('thread preserves audit detail and polls a parked issue',
      (tester) async {
    final service = _FakeIssuesService(
      thread: IssueThread.fromJson({
        'issue': _issueJson(),
        'thread': const [],
      }),
      activity: IssueAgentActivity(actions: const [], runs: [_completedRun()]),
    );
    await _pumpScreen(
      tester,
      service: service,
      screen: const IssueThreadScreen(issueId: 5),
    );
    await _resumeApp(tester);
    expect(find.text('Investigation completed'), findsOneWidget);

    service.failActivity = true;
    final beforePoll = service.threadLoads;
    await tester.pump(const Duration(seconds: 10));
    await tester.pump();

    expect(service.threadLoads, greaterThan(beforePoll));
    expect(find.text('Investigation completed'), findsOneWidget);
    expect(
      find.text(
          "Agent activity couldn't be refreshed. History may be incomplete."),
      findsOneWidget,
    );
  });

  testWidgets('issues list keeps rows and warns when resume refresh fails',
      (tester) async {
    final service = _FakeIssuesService(
      thread: IssueThread.fromJson({
        'issue': _issueJson(),
        'thread': const [],
      }),
      issues: [Issue.fromJson(_issueJson())],
    );
    await _pumpScreen(
      tester,
      service: service,
      screen: const IssuesListScreen(),
    );
    expect(find.text('Example Show'), findsOneWidget);

    service.failIssues = true;
    await _resumeApp(tester);

    expect(find.text('Example Show'), findsOneWidget);
    expect(
      find.text("Couldn't refresh issues. Showing the last update."),
      findsOneWidget,
    );
  });

  testWidgets('tracking filter separates passive arr recovery from attention',
      (tester) async {
    final attention = Issue.fromJson({
      ..._issueJson(status: 'awaiting_approval'),
      'id': 5,
      'title': 'Needs Review',
    });
    final observing = Issue.fromJson({
      ..._issueJson(status: 'observing'),
      'id': 6,
      'title': 'Quiet Watch',
      'read': false,
    });
    final recovering = Issue.fromJson({
      ..._issueJson(status: 'recovering'),
      'id': 7,
      'title': 'Retry In Flight',
      'read': false,
    });
    final service = _FakeIssuesService(
      thread: IssueThread(issue: attention, messages: const []),
      issues: [attention, observing, recovering],
    );

    await _pumpScreen(
      tester,
      service: service,
      screen: const IssuesListScreen(),
    );

    expect(find.text('Needs Review'), findsOneWidget);
    expect(find.text('Quiet Watch'), findsNothing);
    expect(find.text('Retry In Flight'), findsNothing);

    await tester.tap(find.text('Tracking'));
    await tester.pumpAndSettle();

    expect(find.text('Needs Review'), findsNothing);
    expect(find.text('Quiet Watch'), findsOneWidget);
    expect(find.text('Retry In Flight'), findsOneWidget);
    final mutedTitle = tester.widget<Text>(find.text('Quiet Watch'));
    expect(mutedTitle.style?.color, AppTheme.textSecondary);
    expect(mutedTitle.style?.fontWeight, FontWeight.w600);
    expect(
      find.byWidgetPredicate(
        (widget) =>
            widget is Container &&
            widget.constraints?.minWidth == 8 &&
            widget.constraints?.maxWidth == 8 &&
            widget.constraints?.minHeight == 8 &&
            widget.constraints?.maxHeight == 8 &&
            (widget.decoration as BoxDecoration?)?.color == Colors.transparent,
      ),
      findsNWidgets(2),
    );
  });

  testWidgets('tracking thread is passive while arr recovery is in flight',
      (tester) async {
    final service = _FakeIssuesService(
      thread: IssueThread.fromJson({
        'issue': _issueJson(status: 'recovering'),
        'thread': const [],
      }),
    );

    await _pumpScreen(
      tester,
      service: service,
      screen: const IssueThreadScreen(issueId: 5),
    );
    await _resumeApp(tester);

    expect(find.text('Download recovery in progress'), findsWidgets);
    expect(
      find.textContaining('tracking this quietly'),
      findsOneWidget,
    );
    expect(find.text('Complete this issue'), findsNothing);
    expect(find.text('Complete after admin review'), findsNothing);
    expect(find.text('Add a reply…'), findsNothing);
    expect(find.byIcon(Icons.send_rounded), findsNothing);
    expect(find.text('Working on it…'), findsNothing);
    for (final implementationTerm in [
      'Radarr',
      'Sonarr',
      'agent',
      'proposal',
      'admin',
    ]) {
      expect(find.textContaining(implementationTerm), findsNothing);
    }
  });

  testWidgets('agent fixes keeps cards and warns when resume refresh fails',
      (tester) async {
    final service = _FakeIssuesService(
      thread: IssueThread.fromJson({
        'issue': _issueJson(),
        'thread': const [],
      }),
      actions: [_proposedAction()],
    );
    await _pumpScreen(
      tester,
      service: service,
      screen: const PendingAgentActionsScreen(),
    );
    expect(find.text('Start an automatic search for this title'), findsNothing);
    expect(find.text('Rescan the files on disk and re-run the import'),
        findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsOneWidget);

    service.failActions = true;
    await _resumeApp(tester);

    expect(find.text('Rescan the files on disk and re-run the import'),
        findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(
      find.text('This fix could not be refreshed. Retry before deciding.'),
      findsOneWidget,
    );
    expect(
      find.text("Couldn't refresh agent fixes. Showing the last update."),
      findsOneWidget,
    );
  });

  testWidgets('live agent activity polls and labels a retained stale snapshot',
      (tester) async {
    final service = _FakeIssuesService(
      thread: IssueThread.fromJson({
        'issue': _issueJson(),
        'thread': const [],
      }),
      runDetail: AgentRunDetail(
        run: _resumePendingRun(),
        steps: const <AgentStep>[],
      ),
    );
    await _pumpScreen(
      tester,
      service: service,
      screen: const AgentRunScreen(runId: 9),
    );
    expect(find.textContaining('Ready to continue'), findsOneWidget);

    service.failRun = true;
    final beforePoll = service.runLoads;
    await tester.pump(const Duration(seconds: 10));
    await tester.pump();

    expect(service.runLoads, greaterThan(beforePoll));
    expect(find.textContaining('Ready to continue'), findsOneWidget);
    expect(
      find.text("Couldn't refresh agent activity. Showing the last update."),
      findsOneWidget,
    );
  });
}
