import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_chat_capabilities.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_models.dart';
import 'package:cantinarr/features/ai_assistant/logic/ai_chat_provider.dart';
import 'package:cantinarr/features/ai_assistant/ui/ai_chat_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  testWidgets('new chats refresh welcome and suggestions from server access',
      (tester) async {
    var reads = 0;
    await tester.pumpWidget(ProviderScope(
      overrides: [
        aiSettingsProvider.overrideWith((_) async => _availableShared(
          capabilities: reads++ == 0
              ? const AiChatCapabilities(discoverMediaTypes: ['book'])
              : const AiChatCapabilities(discoverMediaTypes: ['movie']),
        )),
      ],
      child: MaterialApp.router(routerConfig: _chatRouter()),
    ));
    await tester.pumpAndSettle();
    expect(find.textContaining('discover books'), findsOneWidget);
    expect(find.text('Find a book'), findsOneWidget);
    expect(find.text('Recommend a movie'), findsNothing);

    await tester.tap(find.byTooltip('New chat'));
    await tester.pumpAndSettle();
    expect(reads, 2);
    expect(find.textContaining('discover books'), findsNothing);
    expect(find.text('Find a book'), findsNothing);
    expect(find.textContaining('discover movies'), findsOneWidget);
    expect(find.text('Recommend a movie'), findsOneWidget);
  });

  testWidgets('full capability welcome fits a narrow phone', (tester) async {
    tester.view.physicalSize = const Size(320, 700);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await _pump(tester, _chatRouter(), _availableShared(
      capabilities: const AiChatCapabilities(
        discoverMediaTypes: ['movie', 'tv', 'book', 'music'],
        requestMediaTypes: ['movie', 'tv', 'book', 'music'],
        checkAvailability: true, browseLibraries: true,
        manageDownloads: true, troubleshoot: true, configureServices: true,
      ),
    ));
    expect(find.textContaining('configure connected services'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('assistant screen exposes exit and composer controls',
      (tester) async {
    final router = _chatRouter();
    await _pump(tester, router, _availableShared());

    expect(find.byTooltip('Exit assistant'), findsOneWidget);
    expect(find.byType(TextField), findsOneWidget);
    expect(find.text(aiChatWelcomeFallback), findsOneWidget);
    expect(find.byType(ActionChip), findsNothing);

    await tester.tap(find.byTooltip('Exit assistant'));
    await tester.pumpAndSettle();
    expect(find.text('Open assistant'), findsOneWidget);
  });

  testWidgets('assistant conversation persists after route close and reopen',
      (tester) async {
    final router = _chatRouter(initialLocation: '/dashboard/movies');
    await _pump(tester, router, _availableShared());

    await tester.tap(find.text('Open assistant'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('New chat'));
    await tester.pumpAndSettle();
    expect(find.text(aiChatWelcomeFallback), findsOneWidget);
    final container = ProviderScope.containerOf(
      tester.element(find.byType(AiChatScreen)),
    );
    final notifier = container.read(aiChatProvider);
    notifier.state = notifier.state.copyWith(messages: [
      ...notifier.state.messages,
      ChatMessage(id: 'kept', role: ChatRole.user,
          content: 'Keep this question', timestamp: DateTime.now()),
    ]);
    await tester.pumpAndSettle();

    await tester.tap(find.byTooltip('Exit assistant'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Open assistant'));
    await tester.pumpAndSettle();
    expect(find.text('Keep this question'), findsOneWidget);
  });

  testWidgets('broken personal provider gets an AI Access action',
      (tester) async {
    final router = _chatRouter(withSettingsRoute: true);
    await _pump(tester, router, _brokenPersonal());

    expect(find.textContaining('did not fall back'), findsOneWidget);
    expect(find.text('Set up AI access'), findsOneWidget);
    expect(find.byType(TextField), findsNothing);

    await tester.tap(find.text('Set up AI access'));
    await tester.pumpAndSettle();
    expect(find.text('AI access route'), findsOneWidget);
  });

  testWidgets('unavailable included provider does not expose composer',
      (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          aiSettingsProvider.overrideWith((_) async => _unavailableShared()),
        ],
        child: const MaterialApp(home: AiChatScreen(aiAvailable: false)),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('temporarily unavailable'), findsOneWidget);
    expect(find.byType(TextField), findsNothing);
  });

  testWidgets('stale cached availability cannot bypass personal failure',
      (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          aiSettingsProvider.overrideWith((_) async => _brokenPersonal()),
        ],
        child: const MaterialApp(home: AiChatScreen(aiAvailable: true)),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('did not fall back'), findsOneWidget);
    expect(find.byType(TextField), findsNothing);
  });

  testWidgets('status failure preserves cached provider availability',
      (tester) async {
    final router = _chatRouter();
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          aiSettingsProvider.overrideWith(
            (_) => Future<AiSettings>.error('offline'),
          ),
        ],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byType(TextField), findsOneWidget);
  });
}

GoRouter _chatRouter({
  String initialLocation = '/assistant',
  bool withSettingsRoute = false,
}) =>
    GoRouter(
      initialLocation: initialLocation,
      routes: [
        GoRoute(
          path: '/dashboard/movies',
          builder: (context, __) => Scaffold(
            body: Center(
              child: ElevatedButton(
                onPressed: () => context.push('/assistant'),
                child: const Text('Open assistant'),
              ),
            ),
          ),
        ),
        GoRoute(
          path: '/assistant',
          builder: (_, __) => const AiChatScreen(aiAvailable: true),
        ),
        if (withSettingsRoute)
          GoRoute(
            path: '/settings/ai',
            builder: (_, __) => const Scaffold(body: Text('AI access route')),
          ),
      ],
    );

Future<void> _pump(
  WidgetTester tester,
  GoRouter router,
  AiSettings settings,
) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        aiSettingsProvider.overrideWith((_) async => settings),
      ],
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
}

AiSettings _availableShared({AiChatCapabilities? capabilities}) => AiSettings(
      chatCapabilities: capabilities,
      providers: [],
      personal: const PersonalAiSettings(
        selected: false,
        config: null,
        credentials: {},
      ),
      shared: const SharedAiSettings(
        granted: true,
        configured: true,
        config: AiProviderConfig(provider: 'openai', model: 'gpt-5.4-mini'),
      ),
      effective: const EffectiveAiSettings(
        available: true,
        source: AiAccessSource.shared,
        provider: 'openai',
        model: 'gpt-5.4-mini',
        reason: '',
      ),
    );

AiSettings _brokenPersonal() => const AiSettings(
      providers: [],
      personal: PersonalAiSettings(
        selected: true,
        config: AiProviderConfig(provider: 'codex', model: 'default'),
        credentials: {'codex': false},
      ),
      shared: SharedAiSettings(
        granted: true,
        configured: true,
        config: AiProviderConfig(provider: 'openai', model: 'gpt-5.4-mini'),
      ),
      effective: EffectiveAiSettings(
        available: false,
        source: AiAccessSource.personal,
        provider: 'codex',
        model: 'default',
        reason: 'personal_codex_disconnected',
      ),
    );

AiSettings _unavailableShared() => const AiSettings(
      providers: [],
      personal: PersonalAiSettings(
        selected: false,
        config: null,
        credentials: {},
      ),
      shared: SharedAiSettings(
        granted: true,
        configured: true,
        config: AiProviderConfig(provider: 'codex', model: 'default'),
      ),
      effective: EffectiveAiSettings(
        available: false,
        source: AiAccessSource.none,
        provider: '',
        model: '',
        reason: 'codex_unavailable',
      ),
    );
