import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_provider_models.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
import 'package:cantinarr/features/ai_assistant/ui/ai_access_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  testWidgets('broken personal selection is explicit and never falls back',
      (tester) async {
    final service = _FakeAiSettingsService(_personalBroken());
    await _pump(tester, service);

    expect(find.textContaining('Personal · OpenAI'), findsOneWidget);
    expect(find.textContaining('did not fall back'), findsOneWidget);
    expect(find.text('Included · OpenAI (OAuth)'), findsNothing);

    final useIncluded = find.text('Use included access');
    await tester.ensureVisible(useIncluded);
    await tester.tap(useIncluded);
    await tester.pumpAndSettle();

    expect(service.includedCalls, 1);
    expect(find.textContaining('Included · OpenAI (OAuth)'), findsOneWidget);
    expect(service.current.personal.isConfigured('openai'), isFalse);
  });

  testWidgets('saves a personal provider without included access',
      (tester) async {
    final service = _FakeAiSettingsService(_notGrantedGlobalCodex());
    await _pump(tester, service);

    expect(find.text('Anthropic'), findsOneWidget);
    expect(find.text('OpenAI'), findsOneWidget);
    expect(find.text('Google Gemini'), findsOneWidget);
    expect(find.text('OpenAI (OAuth)'), findsWidgets);

    await tester.enterText(
      find.widgetWithText(TextField, 'OpenAI API key'),
      'sk-personal',
    );
    await tester.tap(find.text('Save key & use'));
    await tester.pumpAndSettle();

    expect(service.savedKeys, [('openai', 'sk-personal')]);
    expect(service.personalSelections, [('openai', 'gpt-5.4-mini')]);
    expect(find.textContaining('Personal · OpenAI'), findsOneWidget);
  });

  testWidgets('active included access leads and rolls up the personal panel',
      (tester) async {
    final service = _FakeAiSettingsService(_included());
    await _pump(tester, service);

    // Included sits above personal so the covered-by-default path reads first.
    expect(
      tester.getTopLeft(find.text('Provided by this server')).dy,
      lessThan(tester.getTopLeft(find.text('Your provider')).dy),
    );

    // Personal is rolled up: no setup controls, an explicit not-needed cue.
    expect(find.text('Optional override'), findsOneWidget);
    expect(find.byType(ChoiceChip), findsNothing);
    expect(find.textContaining('provider is not needed'), findsOneWidget);

    // Tapping the panel expands the full personal setup.
    await tester.tap(find.text('Your provider'));
    await tester.pumpAndSettle();
    expect(find.byType(ChoiceChip), findsWidgets);
  });

  testWidgets('personal panel stays expanded when included AI is not active',
      (tester) async {
    final service = _FakeAiSettingsService(_notGrantedGlobalCodex());
    await _pump(tester, service);

    expect(find.text('Optional override'), findsOneWidget);
    expect(find.byType(ChoiceChip), findsWidgets);
  });

  testWidgets('personal ChatGPT can be connected while included AI is active',
      (tester) async {
    final service = _FakeAiSettingsService(_included());
    await _pump(tester, service, withChatGptRoute: true);

    // Included AI is active, so the personal panel starts rolled up.
    await tester.tap(find.text('Your provider'));
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(ChoiceChip, 'OpenAI (OAuth)'));
    await tester.pumpAndSettle();
    final connect = find.text('Connect personal OpenAI OAuth');
    await tester.ensureVisible(connect);
    await tester.tap(connect);
    await tester.pumpAndSettle();

    expect(find.text('Personal ChatGPT route'), findsOneWidget);
  });

  testWidgets('personal xAI Grok can be connected while included AI is active',
      (tester) async {
    final service = _FakeAiSettingsService(_included());
    await _pump(tester, service, withGrokRoute: true);

    await tester.tap(find.text('Your provider'));
    await tester.pumpAndSettle();

    final grokChip = find.widgetWithText(ChoiceChip, 'xAI Grok (OAuth)');
    await tester.ensureVisible(grokChip);
    await tester.tap(grokChip);
    await tester.pumpAndSettle();
    final connect = find.text('Connect personal xAI Grok');
    await tester.ensureVisible(connect);
    await tester.tap(connect);
    await tester.pumpAndSettle();

    expect(find.text('Personal Grok route'), findsOneWidget);
  });

  testWidgets('server-advertised default preselects OpenAI OAuth with Luna',
      (tester) async {
    final service = _FakeAiSettingsService(_untouchedWithServerDefaults());
    await _pump(tester, service);

    // The provider list leads with OpenAI, but the server default wins: the
    // OAuth chip arrives selected and its connect affordance needs no tap.
    final codexChip = tester.widget<ChoiceChip>(
      find.widgetWithText(ChoiceChip, 'OpenAI (OAuth)'),
    );
    expect(codexChip.selected, isTrue);
    expect(find.text('Connect personal OpenAI OAuth'), findsOneWidget);
    expect(find.text('GPT-5.6 Luna'), findsWidgets);
  });

  testWidgets('selected but unavailable included access is not shown active',
      (tester) async {
    final service = _FakeAiSettingsService(_includedUnavailable());
    await _pump(tester, service);

    expect(find.text('Needs admin setup'), findsOneWidget);
    expect(find.text('Active'), findsNothing);
    expect(find.text('Use included access'), findsNothing);
  });
}

Future<void> _pump(
  WidgetTester tester,
  _FakeAiSettingsService service, {
  bool withChatGptRoute = false,
  bool withGrokRoute = false,
}) async {
  tester.view.physicalSize = const Size(900, 1500);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);

  final router = GoRouter(
    initialLocation: '/settings/ai',
    routes: [
      GoRoute(
        path: '/settings/ai',
        builder: (_, __) => const AiAccessScreen(),
      ),
      if (withChatGptRoute)
        GoRoute(
          path: '/settings/chatgpt',
          builder: (_, __) =>
              const Scaffold(body: Text('Personal ChatGPT route')),
        ),
      if (withGrokRoute)
        GoRoute(
          path: '/settings/grok',
          builder: (_, __) => const Scaffold(body: Text('Personal Grok route')),
        ),
    ],
  );
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        aiSettingsServiceProvider.overrideWithValue(service),
        authProvider.overrideWith(_FakeAuthNotifier.new),
      ],
      child: MaterialApp.router(
        theme: AppTheme.dark,
        routerConfig: router,
      ),
    ),
  );
  await tester.pumpAndSettle();
}

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState();

  @override
  Future<void> refreshConfig() async {}
}

class _FakeAiSettingsService extends AiSettingsService {
  _FakeAiSettingsService(this.current) : super(backendDio: Dio());

  AiSettings current;
  int includedCalls = 0;
  final savedKeys = <(String, String)>[];
  final personalSelections = <(String, String)>[];

  @override
  Future<AiSettings> getSettings() async => current;

  @override
  Future<AiSettings> usePersonal({
    required String provider,
    required String model,
    String? apiKey,
  }) async {
    if (apiKey != null) savedKeys.add((provider, apiKey));
    personalSelections.add((provider, model));
    current = _personal(provider: provider, configured: true, model: model);
    return current;
  }

  @override
  Future<void> useIncluded() async {
    includedCalls++;
    current = _included();
  }
}

const _providers = [
  AiProviderOption(
    id: 'openai',
    label: 'OpenAI',
    credentialKey: 'openai_key',
    models: [
      AiModelOption(
        id: 'gpt-5.4-mini',
        label: 'GPT-5.4 mini',
        description: '',
      ),
    ],
  ),
  AiProviderOption(
    id: 'anthropic',
    label: 'Anthropic',
    credentialKey: 'anthropic_key',
    models: [
      AiModelOption(
        id: 'claude-sonnet-4-6',
        label: 'Claude Sonnet 4.6',
        description: '',
      ),
    ],
  ),
  AiProviderOption(
    id: 'gemini',
    label: 'Google Gemini',
    credentialKey: 'gemini_key',
    models: [
      AiModelOption(
        id: 'gemini-2.5-flash',
        label: 'Gemini 2.5 Flash',
        description: '',
      ),
    ],
  ),
  AiProviderOption(
    id: 'grok',
    label: 'xAI Grok',
    credentialKey: 'grok_key',
    models: [
      AiModelOption(
        id: 'grok-4.6',
        label: 'Grok 4.6',
        description: '',
      ),
    ],
  ),
  AiProviderOption(
    id: 'codex',
    label: 'OpenAI (OAuth)',
    credentialKey: '',
    authType: 'user_oauth',
    models: [
      AiModelOption(
        id: 'default',
        label: 'OpenAI recommended',
        description: '',
      ),
      AiModelOption(
        id: 'gpt-5.6-luna',
        label: 'GPT-5.6 Luna',
        description: '',
      ),
    ],
  ),
  AiProviderOption(
    id: 'grok_oauth',
    label: 'xAI Grok (OAuth)',
    credentialKey: '',
    authType: 'user_oauth',
    models: [
      AiModelOption(
        id: 'grok-4.6',
        label: 'Grok 4.6',
        description: '',
      ),
    ],
  ),
];

AiSettings _untouchedWithServerDefaults() => const AiSettings(
      providers: _providers,
      defaultProvider: 'codex',
      defaultModel: 'gpt-5.6-luna',
      personal: PersonalAiSettings(
        selected: false,
        config: null,
        credentials: {
          'openai': false,
          'anthropic': false,
          'gemini': false,
          'codex': false,
        },
      ),
      shared: SharedAiSettings(
        granted: true,
        configured: false,
        config: AiProviderConfig(provider: 'codex', model: 'gpt-5.6-luna'),
      ),
      effective: EffectiveAiSettings(
        available: false,
        source: AiAccessSource.none,
        provider: '',
        model: '',
        reason: '',
      ),
    );

AiSettings _included() => const AiSettings(
      providers: _providers,
      personal: PersonalAiSettings(
        selected: false,
        config: null,
        credentials: {
          'openai': false,
          'anthropic': false,
          'gemini': false,
          'codex': false,
        },
      ),
      shared: SharedAiSettings(
        granted: true,
        configured: true,
        config: AiProviderConfig(provider: 'codex', model: 'default'),
      ),
      effective: EffectiveAiSettings(
        available: true,
        source: AiAccessSource.shared,
        provider: 'codex',
        model: 'default',
        reason: '',
      ),
    );

AiSettings _notGrantedGlobalCodex() => const AiSettings(
      providers: _providers,
      personal: PersonalAiSettings(
        selected: false,
        config: null,
        credentials: {
          'openai': false,
          'anthropic': false,
          'gemini': false,
          'codex': false,
        },
      ),
      shared: SharedAiSettings(
        granted: false,
        configured: true,
        config: AiProviderConfig(provider: 'codex', model: 'default'),
      ),
      effective: EffectiveAiSettings(
        available: false,
        source: AiAccessSource.none,
        provider: '',
        model: '',
        reason: 'shared_access_disabled',
      ),
    );

AiSettings _includedUnavailable() => const AiSettings(
      providers: _providers,
      personal: PersonalAiSettings(
        selected: false,
        config: null,
        credentials: {},
      ),
      shared: SharedAiSettings(
        granted: true,
        configured: false,
        config: AiProviderConfig(provider: 'codex', model: 'default'),
      ),
      effective: EffectiveAiSettings(
        available: false,
        source: AiAccessSource.shared,
        provider: 'codex',
        model: 'default',
        reason: 'shared_codex_disconnected',
      ),
    );

AiSettings _personalBroken() => const AiSettings(
      providers: _providers,
      personal: PersonalAiSettings(
        selected: true,
        config: AiProviderConfig(provider: 'openai', model: 'gpt-5.4-mini'),
        credentials: {
          'openai': false,
          'anthropic': false,
          'gemini': false,
          'codex': false,
        },
      ),
      shared: SharedAiSettings(
        granted: true,
        configured: true,
        config: AiProviderConfig(provider: 'codex', model: 'default'),
      ),
      effective: EffectiveAiSettings(
        available: false,
        source: AiAccessSource.personal,
        provider: 'openai',
        model: 'gpt-5.4-mini',
        reason: 'personal_credential_missing',
      ),
    );

AiSettings _personal({
  required String provider,
  required bool configured,
  String model = 'gpt-5.4-mini',
}) =>
    AiSettings(
      providers: _providers,
      personal: PersonalAiSettings(
        selected: true,
        config: AiProviderConfig(provider: provider, model: model),
        credentials: {
          'openai': provider == 'openai' && configured,
          'anthropic': provider == 'anthropic' && configured,
          'gemini': provider == 'gemini' && configured,
          'codex': provider == 'codex' && configured,
        },
      ),
      shared: const SharedAiSettings(
        granted: true,
        configured: true,
        config: AiProviderConfig(provider: 'codex', model: 'default'),
      ),
      effective: EffectiveAiSettings(
        available: configured,
        source: AiAccessSource.personal,
        provider: provider,
        model: model,
        reason: configured ? '' : 'personal_credential_missing',
      ),
    );
