import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';

/// Step-by-step setup wizard for new users.
class SetupWizardScreen extends StatefulWidget {
  const SetupWizardScreen({super.key});

  @override
  State<SetupWizardScreen> createState() => _SetupWizardScreenState();
}

class _SetupWizardScreenState extends State<SetupWizardScreen> {
  int _currentStep = 0;

  final _steps = const [
    _SetupStep(
      title: 'Welcome to Cantinarr',
      icon: Icons.movie_filter,
      description:
          'Your personal media companion. Discover movies and TV shows, request them with one tap, and manage your media server — all in one beautiful app.',
    ),
    _SetupStep(
      title: 'Connect TMDB',
      icon: Icons.search,
      description:
          'TMDB powers our discovery engine. Get a free API key at themoviedb.org to unlock trending content, search, and recommendations.',
      hasTextField: true,
      textFieldHint: 'TMDB API Key',
    ),
    _SetupStep(
      title: 'Add Radarr (Movies)',
      icon: Icons.movie_outlined,
      description:
          'Connect your Radarr instance to request movies with one tap. They\'ll be automatically downloaded and added to your library.',
      hasServerConfig: true,
    ),
    _SetupStep(
      title: 'Add Sonarr (TV)',
      icon: Icons.tv,
      description:
          'Connect your Sonarr instance to request TV shows. New episodes will be automatically downloaded as they air.',
      hasServerConfig: true,
    ),
    _SetupStep(
      title: 'AI Assistant (Optional)',
      icon: Icons.smart_toy_outlined,
      description:
          'Add an Anthropic API key to unlock the AI assistant. It can help you discover content, get personalized recommendations, and guide you through setup.',
      hasTextField: true,
      textFieldHint: 'Anthropic API Key (optional)',
    ),
    _SetupStep(
      title: 'You\'re All Set!',
      icon: Icons.check_circle_outline,
      description:
          'You can always change these settings later. Start discovering and requesting movies and TV shows!',
    ),
  ];

  @override
  Widget build(BuildContext context) {
    final step = _steps[_currentStep];
    final isLast = _currentStep == _steps.length - 1;
    final isFirst = _currentStep == 0;

    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            // Progress
            Padding(
              padding: const EdgeInsets.all(16),
              child: Row(
                children: List.generate(_steps.length, (i) {
                  return Expanded(
                    child: Container(
                      height: 3,
                      margin: const EdgeInsets.symmetric(horizontal: 2),
                      decoration: BoxDecoration(
                        color: i <= _currentStep
                            ? AppTheme.accent
                            : AppTheme.border,
                        borderRadius: BorderRadius.circular(2),
                      ),
                    ),
                  );
                }),
              ),
            ),

            // Skip
            if (!isLast)
              Align(
                alignment: Alignment.centerRight,
                child: TextButton(
                  onPressed: () => Navigator.pop(context),
                  child: const Text('Skip',
                      style: TextStyle(color: AppTheme.textSecondary)),
                ),
              ),

            // Content
            Expanded(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 32),
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    // Icon
                    Container(
                      width: 80,
                      height: 80,
                      decoration: BoxDecoration(
                        color: AppTheme.accent.withValues(alpha: 0.12),
                        shape: BoxShape.circle,
                      ),
                      child: Icon(step.icon, size: 40, color: AppTheme.accent),
                    ),
                    const SizedBox(height: 24),

                    // Title
                    Text(
                      step.title,
                      style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 26,
                        fontWeight: FontWeight.bold,
                      ),
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 12),

                    // Description
                    Text(
                      step.description,
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 16, height: 1.5),
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 32),

                    // Text field
                    if (step.hasTextField)
                      TextField(
                        decoration: InputDecoration(
                          hintText: step.textFieldHint,
                          prefixIcon: const Icon(Icons.key),
                        ),
                        obscureText: true,
                      ),

                    // Server config
                    if (step.hasServerConfig) ...[
                      const TextField(
                        decoration: InputDecoration(
                          hintText: 'Host (e.g. 192.168.1.100)',
                          prefixIcon: Icon(Icons.dns),
                        ),
                      ),
                      const SizedBox(height: 12),
                      const TextField(
                        decoration: InputDecoration(
                          hintText: 'Port (e.g. 7878)',
                          prefixIcon: Icon(Icons.numbers),
                        ),
                        keyboardType: TextInputType.number,
                      ),
                      const SizedBox(height: 12),
                      const TextField(
                        decoration: InputDecoration(
                          hintText: 'API Key',
                          prefixIcon: Icon(Icons.key),
                        ),
                        obscureText: true,
                      ),
                    ],
                  ],
                ),
              ),
            ),

            // Navigation buttons
            Padding(
              padding: const EdgeInsets.all(24),
              child: Row(
                children: [
                  if (!isFirst)
                    Expanded(
                      child: OutlinedButton(
                        onPressed: () =>
                            setState(() => _currentStep--),
                        style: OutlinedButton.styleFrom(
                          foregroundColor: AppTheme.textPrimary,
                          side: const BorderSide(color: AppTheme.border),
                          padding: const EdgeInsets.symmetric(vertical: 14),
                          shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(12),
                          ),
                        ),
                        child: const Text('Back'),
                      ),
                    ),
                  if (!isFirst) const SizedBox(width: 12),
                  Expanded(
                    flex: isFirst ? 1 : 1,
                    child: ElevatedButton(
                      onPressed: () {
                        if (isLast) {
                          Navigator.pop(context);
                        } else {
                          setState(() => _currentStep++);
                        }
                      },
                      child: Text(isLast ? 'Get Started' : 'Continue'),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _SetupStep {
  final String title;
  final IconData icon;
  final String description;
  final bool hasTextField;
  final String? textFieldHint;
  final bool hasServerConfig;

  const _SetupStep({
    required this.title,
    required this.icon,
    required this.description,
    this.hasTextField = false,
    this.textFieldHint,
    this.hasServerConfig = false,
  });
}
