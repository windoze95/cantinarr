import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/ai_assistant/data/codex_oauth_service.dart';
import 'package:cantinarr/features/ai_assistant/ui/codex_connection_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('connects, shows account usage, and disconnects', (tester) async {
    final service = _FakeCodexOAuthService();
    final auth = _FakeAuthNotifier();
    final opened = <Uri>[];
    await _pumpScreen(tester, service, auth, opened);

    expect(find.text('Connect ChatGPT'), findsOneWidget);

    await tester.tap(find.text('Connect ChatGPT'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 10));

    expect(find.byKey(const ValueKey('codex-user-code')), findsOneWidget);
    expect(find.text('ABCD-EFGH'), findsOneWidget);
    expect(opened.single.host, 'auth.openai.com');

    await tester.tap(find.text('Check now'));
    await tester.pump();
    await tester.pumpAndSettle();

    expect(find.text('Connected'), findsOneWidget);
    expect(find.text('viewer@example.com'), findsOneWidget);
    expect(find.text('ChatGPT Plus'), findsOneWidget);
    expect(find.text('37.5% used'), findsOneWidget);
    expect(auth.refreshCount, 1);

    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    final disconnect = find.text('Disconnect ChatGPT');
    await tester.drag(find.byType(ListView), const Offset(0, -400));
    await tester.pumpAndSettle();
    await tester.tap(disconnect);
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Disconnect'));
    await tester.pump();
    await tester.pumpAndSettle();

    expect(service.unlinkCalls, 1);
    expect(auth.refreshCount, 2);
    expect(find.text('Connect ChatGPT'), findsOneWidget);
  });

  testWidgets('a pending device flow can be reopened and cancelled',
      (tester) async {
    final service = _FakeCodexOAuthService(pollConnects: false);
    final auth = _FakeAuthNotifier();
    final opened = <Uri>[];
    await _pumpScreen(tester, service, auth, opened);

    await tester.tap(find.text('Connect ChatGPT'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 10));

    await tester.tap(find.text('Reopen ChatGPT'));
    await tester.pump();
    expect(opened, hasLength(2));

    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();

    expect(service.cancelledFlowIds, ['flow-1']);
    expect(find.text('Connect ChatGPT'), findsOneWidget);
  });

  testWidgets('leaving a pending device flow cancels it on the server',
      (tester) async {
    final service = _FakeCodexOAuthService(pollConnects: false);
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    await tester.tap(find.text('Connect ChatGPT'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 10));

    await tester.pumpWidget(const SizedBox());
    await tester.pump();

    expect(service.cancelledFlowIds, ['flow-1']);
  });

  testWidgets('a locally expired device flow is cancelled on the server',
      (tester) async {
    final service = _FakeCodexOAuthService(
      pollConnects: false,
      flowExpiresIn: const Duration(milliseconds: 10),
    );
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    await tester.tap(find.text('Connect ChatGPT'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));

    expect(service.cancelledFlowIds, ['flow-1']);
    expect(find.textContaining('one-time code expired'), findsOneWidget);
    expect(find.text('Connect ChatGPT'), findsOneWidget);
  });

  testWidgets('selected but unavailable Codex explains the runtime problem',
      (tester) async {
    final service = _FakeCodexOAuthService(available: false);
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    expect(find.textContaining('ChatGPT is selected'), findsOneWidget);
    expect(find.text('Connect ChatGPT'), findsNothing);
  });

  testWidgets('a linked account can disconnect when runtime is unavailable',
      (tester) async {
    final service = _FakeCodexOAuthService(
      available: false,
      connected: true,
    );
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    expect(find.text('Connected'), findsOneWidget);
    expect(find.text('viewer@example.com'), findsOneWidget);
    expect(
        find.textContaining('Codex is currently unavailable'), findsOneWidget);
    final disconnect = find.text('Disconnect ChatGPT');
    expect(disconnect, findsOneWidget);

    await tester.drag(find.byType(ListView), const Offset(0, -400));
    await tester.pumpAndSettle();
    await tester.tap(disconnect);
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Disconnect'));
    await tester.pumpAndSettle();

    expect(service.unlinkCalls, 1);
  });

  testWidgets('a linked account stays removable after the provider changes',
      (tester) async {
    final service = _FakeCodexOAuthService(
      selected: false,
      available: false,
      connected: true,
    );
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    expect(find.text('Connected'), findsOneWidget);
    expect(find.text('viewer@example.com'), findsOneWidget);
    expect(find.textContaining('different AI provider'), findsOneWidget);
    expect(find.text('Disconnect ChatGPT'), findsOneWidget);
  });

  testWidgets('labels a cached usage snapshot as stale', (tester) async {
    final service = _FakeCodexOAuthService(connected: true, stale: true);
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    expect(find.textContaining('snapshot may be out of date'), findsOneWidget);
  });
}

Future<void> _pumpScreen(
  WidgetTester tester,
  _FakeCodexOAuthService service,
  _FakeAuthNotifier auth,
  List<Uri> opened,
) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        codexOAuthServiceProvider.overrideWithValue(service),
        codexExternalUrlLauncherProvider.overrideWithValue((uri) async {
          opened.add(uri);
          return true;
        }),
        authProvider.overrideWith(() => auth),
      ],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: const CodexConnectionScreen(),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

class _FakeCodexOAuthService extends CodexOAuthService {
  _FakeCodexOAuthService({
    this.pollConnects = true,
    this.selected = true,
    this.available = true,
    this.connected = false,
    this.stale = false,
    this.flowExpiresIn = const Duration(minutes: 15),
  }) : super(backendDio: Dio());

  final bool pollConnects;
  final bool selected;
  final bool available;
  final Duration flowExpiresIn;
  final bool stale;
  bool connected;
  int unlinkCalls = 0;
  final cancelledFlowIds = <String>[];

  @override
  Future<CodexConnectionStatus> getStatus() async => CodexConnectionStatus(
        selected: selected,
        available: available,
        connected: connected,
        accountEmail: connected ? 'viewer@example.com' : '',
        planType: connected ? 'plus' : '',
        stale: stale,
        rateLimits: connected
            ? const CodexRateLimits(
                primary: CodexRateLimitWindow(usedPercent: 37.5),
                secondary: CodexRateLimitWindow(usedPercent: 64),
              )
            : null,
      );

  @override
  Future<CodexDeviceAuthorization> beginDeviceAuthorization() async =>
      CodexDeviceAuthorization(
        flowId: 'flow-1',
        verificationUri: Uri.parse('https://auth.openai.com/codex/device'),
        userCode: 'ABCD-EFGH',
        expiresIn: flowExpiresIn,
        pollInterval: const Duration(minutes: 1),
      );

  @override
  Future<CodexDeviceFlowResult> checkDeviceAuthorization(String flowId) async {
    if (!pollConnects) {
      return const CodexDeviceFlowResult(
        status: CodexDeviceFlowStatus.pending,
      );
    }
    connected = true;
    return const CodexDeviceFlowResult(
      status: CodexDeviceFlowStatus.connected,
      accountEmail: 'viewer@example.com',
    );
  }

  @override
  Future<void> cancelDeviceAuthorization(String flowId) async {
    cancelledFlowIds.add(flowId);
  }

  @override
  Future<void> unlink() async {
    unlinkCalls++;
    connected = false;
  }
}

class _FakeAuthNotifier extends AuthNotifier {
  int refreshCount = 0;

  @override
  Future<AuthState> build() async => const AuthState();

  @override
  Future<void> refreshConfig() async {
    refreshCount++;
  }
}
