import 'package:cantinarr/core/storage/preferences.dart';
import 'package:cantinarr/features/profile_proposals/data/profile_proposal_models.dart';
import 'package:cantinarr/features/profile_proposals/data/profile_proposals_service.dart';
import 'package:cantinarr/features/profile_proposals/ui/profile_proposals_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

ProfileChangeProposal _proposal({
  int id = 12,
  String status = 'pending',
}) =>
    ProfileChangeProposal.fromJson({
      'id': id,
      'status': status,
      'service': 'radarr',
      'instance_id': 'movies-a',
      'instance_name': 'Movies',
      'profile_id': 6,
      'profile_name': 'HD-1080p',
      'proposed_by_name': 'julian',
      'source_client': 'MCP: Claude Desktop',
      'diff': const ['upgrade policy: on -> off'],
      'created_at': '2026-08-08T20:00:00Z',
      'expires_at': '2026-08-15T20:00:00Z',
    });

class _FakeProposalsService extends ProfileProposalsService {
  _FakeProposalsService(this.proposals) : super(backendDio: Dio());

  List<ProfileChangeProposal> proposals;
  final approved = <int>[];
  final rejected = <int>[];
  String? lastRejectNote;

  @override
  Future<List<ProfileChangeProposal>> listProposals({
    String status = 'all',
  }) async =>
      proposals;

  @override
  Future<ProfileChangeProposal> approveProposal(int id) async {
    approved.add(id);
    final updated = _proposal(id: id, status: 'applied');
    proposals = [updated];
    return updated;
  }

  @override
  Future<ProfileChangeProposal> rejectProposal(int id, {String? note}) async {
    rejected.add(id);
    lastRejectNote = note;
    final updated = _proposal(id: id, status: 'rejected');
    proposals = [updated];
    return updated;
  }
}

Future<void> _pump(WidgetTester tester, _FakeProposalsService service) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        profileProposalsServiceProvider.overrideWithValue(service),
      ],
      child: const MaterialApp(home: ProfileProposalsScreen()),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('renders a pending proposal with its server-rendered diff',
      (tester) async {
    final service = _FakeProposalsService([_proposal()]);
    await _pump(tester, service);

    expect(find.text('HD-1080p'), findsOneWidget);
    expect(find.text('upgrade policy: on -> off'), findsOneWidget);
    expect(find.textContaining('Proposed by julian'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsOneWidget);
    expect(find.widgetWithText(OutlinedButton, 'Reject'), findsOneWidget);
  });

  testWidgets('approving confirms first, then submits the decision',
      (tester) async {
    final service = _FakeProposalsService([_proposal()]);
    await _pump(tester, service);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    // The confirmation names the profile so the admin knows what they are
    // consenting to.
    expect(find.text('Apply this profile change?'), findsOneWidget);
    expect(find.textContaining('HD-1080p'), findsWidgets);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve').last);
    await tester.pumpAndSettle();

    expect(service.approved, [12]);
    expect(find.text('Applied and verified on Movies.'), findsOneWidget);
    // The decided proposal moved to the Recent section.
    expect(find.text('Recent'), findsOneWidget);
    expect(find.text('Applied'), findsWidgets);
  });

  testWidgets('cancelling the confirmation submits nothing', (tester) async {
    final service = _FakeProposalsService([_proposal()]);
    await _pump(tester, service);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();

    expect(service.approved, isEmpty);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsOneWidget);
  });

  testWidgets('rejecting sends the optional note', (tester) async {
    final service = _FakeProposalsService([_proposal()]);
    await _pump(tester, service);

    await tester.tap(find.widgetWithText(OutlinedButton, 'Reject'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'not now');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Reject'));
    await tester.pumpAndSettle();

    expect(service.rejected, [12]);
    expect(service.lastRejectNote, 'not now');
    expect(find.text('Proposal rejected. Nothing was changed.'), findsOneWidget);
  });

  testWidgets('empty queue explains what will appear here', (tester) async {
    final service = _FakeProposalsService(const []);
    await _pump(tester, service);

    expect(find.text('Nothing awaiting approval'), findsOneWidget);
  });

  testWidgets('footer switch survives the empty state and flips the pref',
      (tester) async {
    SharedPreferences.setMockInitialValues({});
    final service = _FakeProposalsService(const []);
    final container = ProviderContainer(overrides: [
      profileProposalsServiceProvider.overrideWithValue(service),
    ]);
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: ProfileProposalsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    final toggle = find.byKey(
      const ValueKey('profileApprovals-conditional-menu-visibility'),
    );
    expect(toggle, findsOneWidget);
    expect(tester.widget<Switch>(toggle).value, isFalse);

    await tester.tap(toggle);
    await tester.pumpAndSettle();

    expect(
      container.read(profileApprovalsMenuOnlyWhenPendingProvider),
      isTrue,
    );
    expect(tester.widget<Switch>(toggle).value, isTrue);
  });
}
