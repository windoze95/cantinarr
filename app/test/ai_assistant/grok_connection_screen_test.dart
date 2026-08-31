import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/ai_assistant/data/grok_oauth_service.dart';
import 'package:cantinarr/features/ai_assistant/ui/codex_connection_screen.dart'
    show codexExternalUrlLauncherProvider;
import 'package:cantinarr/features/ai_assistant/ui/grok_connection_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('connects, shows the account, and disconnects', (tester) async {
    final service = _FakeGrokOAuthService();
    final auth = _FakeAuthNotifier();
    final opened = <Uri>[];
    await _pumpScreen(tester, service, auth, opened);

    expect(find.text('Connect xAI Grok'), findsOneWidget);

    await tester.tap(find.text('Connect xAI Grok'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 10));

    expect(find.byKey(const ValueKey('grok-user-code')), findsOneWidget);
    expect(find.text('GROK-1234'), findsOneWidget);
    expect(opened.single.host, 'accounts.x.ai');

    await tester.tap(find.text('Check now'));
    await tester.pump();
    await tester.pumpAndSettle();

    expect(find.text('viewer@example.com'), findsOneWidget);
    expect(find.text('Plan: supergrok'), findsOneWidget);
    expect(auth.refreshCount, 1);

    final disconnect = find.text('Disconnect xAI Grok');
    await tester.ensureVisible(disconnect);
    await tester.pumpAndSettle();
    await tester.tap(disconnect);
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Disconnect'));
    await tester.pump();
    await tester.pumpAndSettle();

    expect(service.unlinkCalls, 1);
    expect(auth.refreshCount, 2);
    expect(find.text('Connect xAI Grok'), findsOneWidget);
  });

  testWidgets('a pending device flow can be reopened and cancelled',
      (tester) async {
    final service = _FakeGrokOAuthService(pollConnects: false);
    final auth = _FakeAuthNotifier();
    final opened = <Uri>[];
    await _pumpScreen(tester, service, auth, opened);

    await tester.tap(find.text('Connect xAI Grok'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 10));

    await tester.tap(find.text('Reopen xAI sign-in'));
    await tester.pump();
    expect(opened, hasLength(2));

    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();

    expect(service.cancelledFlowIds, ['flow-1']);
    expect(find.text('Connect xAI Grok'), findsOneWidget);
  });

  testWidgets('leaving a pending device flow cancels it on the server',
      (tester) async {
    final service = _FakeGrokOAuthService(pollConnects: false);
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    await tester.tap(find.text('Connect xAI Grok'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 10));

    await tester.pumpWidget(const SizedBox());
    await tester.pump();

    expect(service.cancelledFlowIds, ['flow-1']);
  });

  testWidgets('a locally expired device flow is cancelled on the server',
      (tester) async {
    final service = _FakeGrokOAuthService(
      pollConnects: false,
      flowExpiresIn: const Duration(milliseconds: 10),
    );
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    await tester.tap(find.text('Connect xAI Grok'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));

    expect(service.cancelledFlowIds, ['flow-1']);
    expect(find.textContaining('one-time code expired'), findsOneWidget);
    expect(find.text('Connect xAI Grok'), findsOneWidget);
  });

  testWidgets('a begin conflict surfaces the server explanation verbatim',
      (tester) async {
    final service = _FakeGrokOAuthService(
      beginError: 'An xAI sign-in is already in progress',
    );
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    await tester.tap(find.text('Connect xAI Grok'));
    await tester.pumpAndSettle();

    expect(
      find.text('An xAI sign-in is already in progress'),
      findsOneWidget,
    );
  });

  testWidgets('an unavailable server disables connecting and says why',
      (tester) async {
    final service = _FakeGrokOAuthService(available: false);
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    expect(
      find.text('Grok OAuth is unavailable on this server right now.'),
      findsOneWidget,
    );
    final button = tester.widget<ElevatedButton>(
      find.widgetWithText(ElevatedButton, 'Connect xAI Grok'),
    );
    expect(button.onPressed, isNull);
  });

  testWidgets('keeps a connected account manageable when its model test fails',
      (tester) async {
    final service = _FakeGrokOAuthService(validationFails: true);
    await _pumpScreen(tester, service, _FakeAuthNotifier(), []);

    await tester.tap(find.text('Connect xAI Grok'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 10));
    await tester.tap(find.text('Check now'));
    await tester.pumpAndSettle();

    expect(find.textContaining('selected model could not complete'),
        findsOneWidget);
    expect(find.text('Disconnect xAI Grok'), findsOneWidget);
  });

  testWidgets('shared scope brands the flow as the shared account',
      (tester) async {
    final service = _FakeGrokOAuthService();
    await _pumpScreen(
      tester,
      service,
      _FakeAuthNotifier(),
      [],
      scope: GrokOAuthScope.adminShared,
    );

    expect(find.text('Shared xAI Grok (OAuth)'), findsOneWidget);
    expect(find.text('Connect shared xAI Grok'), findsOneWidget);
  });
}

Future<void> _pumpScreen(
  WidgetTester tester,
  _FakeGrokOAuthService service,
  _FakeAuthNotifier auth,
  List<Uri> opened, {
  GrokOAuthScope scope = GrokOAuthScope.personal,
}) async {
  await tester.binding.setSurfaceSize(const Size(900, 900));
  addTearDown(() => tester.binding.setSurfaceSize(null));
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        grokOAuthServiceProvider.overrideWithValue(service),
        adminGrokOAuthServiceProvider.overrideWithValue(service),
        codexExternalUrlLauncherProvider.overrideWithValue((uri) async {
          opened.add(uri);
          return true;
        }),
        authProvider.overrideWith(() => auth),
      ],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: GrokConnectionScreen(scope: scope),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

class _FakeGrokOAuthService extends GrokOAuthService {
  _FakeGrokOAuthService({
    this.pollConnects = true,
    this.available = true,
    this.validationFails = false,
    this.beginError,
    this.flowExpiresIn = const Duration(minutes: 15),
  }) : super(backendDio: Dio());

  final bool pollConnects;
  final bool selected = true;
  final bool available;
  final Duration flowExpiresIn;
  final bool validationFails;
  final String? beginError;
  bool connected = false;
  int unlinkCalls = 0;
  final cancelledFlowIds = <String>[];

  @override
  Future<GrokConnectionStatus> getStatus() async => GrokConnectionStatus(
        selected: selected,
        available: available,
        connected: connected,
        accountEmail: connected ? 'viewer@example.com' : '',
        planType: connected ? 'supergrok' : '',
      );

  @override
  Future<GrokDeviceAuthorization> beginDeviceAuthorization() async {
    if (beginError != null) {
      final options = RequestOptions(path: '/api/ai/grok/device/begin');
      throw DioException(
        requestOptions: options,
        response: Response(
          requestOptions: options,
          statusCode: 409,
          data: {'error': beginError},
        ),
      );
    }
    return GrokDeviceAuthorization(
      flowId: 'flow-1',
      verificationUri:
          Uri.parse('https://accounts.x.ai/oauth2/device?user_code=GROK'),
      userCode: 'GROK-1234',
      expiresIn: flowExpiresIn,
      pollInterval: const Duration(minutes: 1),
    );
  }

  @override
  Future<GrokDeviceFlowResult> checkDeviceAuthorization(String flowId) async {
    if (!pollConnects) {
      return const GrokDeviceFlowResult(
        status: GrokDeviceFlowStatus.pending,
      );
    }
    connected = true;
    if (validationFails) {
      return const GrokDeviceFlowResult(
        status: GrokDeviceFlowStatus.failed,
        error:
            'xAI connected, but the selected model could not complete a test message',
      );
    }
    return const GrokDeviceFlowResult(
      status: GrokDeviceFlowStatus.connected,
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
