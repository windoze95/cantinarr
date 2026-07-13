import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/codex_oauth_service.dart';
import '../data/ai_settings_service.dart';

typedef CodexExternalUrlLauncher = Future<bool> Function(Uri uri);

/// Kept injectable so the browser hand-off is deterministic in widget tests.
final codexExternalUrlLauncherProvider = Provider<CodexExternalUrlLauncher>(
  (_) => (uri) => launchUrl(uri, mode: LaunchMode.externalApplication),
);

/// Self-service OpenAI OAuth connection for the current Cantinarr user.
class CodexConnectionScreen extends ConsumerStatefulWidget {
  final CodexOAuthScope scope;

  const CodexConnectionScreen({
    super.key,
    this.scope = CodexOAuthScope.personal,
  });

  @override
  ConsumerState<CodexConnectionScreen> createState() =>
      _CodexConnectionScreenState();
}

class _CodexConnectionScreenState extends ConsumerState<CodexConnectionScreen> {
  late final CodexOAuthService _service;
  CodexDeviceAuthorization? _flow;
  DateTime? _flowExpiresAt;
  Timer? _pollTimer;
  Timer? _expiryTimer;
  bool _starting = false;
  bool _checking = false;
  bool _cancelling = false;
  bool _unlinking = false;
  String? _flowError;

  bool get _isShared => widget.scope == CodexOAuthScope.adminShared;

  @override
  void initState() {
    super.initState();
    _service = ref.read(
      _isShared ? adminCodexOAuthServiceProvider : codexOAuthServiceProvider,
    );
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    _expiryTimer?.cancel();
    final flow = _flow;
    if (flow != null) {
      unawaited(_cancelFlowBestEffort(flow.flowId));
    }
    super.dispose();
  }

  Future<void> _beginConnection() async {
    setState(() {
      _starting = true;
      _flowError = null;
    });
    try {
      final flow = await _service.beginDeviceAuthorization();
      if (!mounted) {
        unawaited(_cancelFlowBestEffort(flow.flowId));
        return;
      }
      setState(() {
        _flow = flow;
        _flowExpiresAt = DateTime.now().add(flow.expiresIn);
      });
      _startPolling(flow);
      await _openVerificationPage(flow.verificationUri);
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _flowError = 'Could not start OpenAI OAuth sign-in. Try again.';
      });
    } finally {
      if (mounted) setState(() => _starting = false);
    }
  }

  void _startPolling(CodexDeviceAuthorization flow) {
    _pollTimer?.cancel();
    _expiryTimer?.cancel();
    _pollTimer = Timer.periodic(
      flow.pollInterval,
      (_) => unawaited(_checkConnection(silent: true)),
    );
    _expiryTimer = Timer(flow.expiresIn, () {
      if (_flow?.flowId != flow.flowId) return;
      _finishFlowWithError(
        'That one-time code expired. Start again.',
        cancelFlow: flow,
      );
    });
  }

  Future<void> _openVerificationPage(Uri uri) async {
    var opened = false;
    try {
      opened = await ref.read(codexExternalUrlLauncherProvider)(uri);
    } catch (_) {
      opened = false;
    }
    if (!opened && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'Could not open ChatGPT sign-in. Copy the code and try Reopen.',
          ),
        ),
      );
    }
  }

  Future<void> _checkConnection({bool silent = false}) async {
    final flow = _flow;
    if (flow == null || _checking) return;
    if (_flowExpiresAt?.isBefore(DateTime.now()) == true) {
      _finishFlowWithError(
        'That one-time code expired. Start again.',
        cancelFlow: flow,
      );
      return;
    }

    setState(() => _checking = true);
    try {
      final result = await _service.checkDeviceAuthorization(flow.flowId);
      if (!mounted || _flow?.flowId != flow.flowId) return;
      switch (result.status) {
        case CodexDeviceFlowStatus.pending:
          if (!silent) {
            ScaffoldMessenger.of(context).showSnackBar(
              const SnackBar(
                content: Text('Still waiting for approval in ChatGPT.'),
              ),
            );
          }
          return;
        case CodexDeviceFlowStatus.connected:
          _pollTimer?.cancel();
          _expiryTimer?.cancel();
          setState(() {
            _flow = null;
            _flowExpiresAt = null;
            _flowError = null;
          });
          if (_isShared) {
            ref.invalidate(adminCodexConnectionStatusProvider);
            ref.invalidate(aiSettingsProvider);
          } else {
            ref.invalidate(codexConnectionStatusProvider);
            ref.invalidate(aiSettingsProvider);
          }
          await _refreshAppAvailability();
          if (mounted) {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content: Text(_isShared
                    ? 'Shared OpenAI OAuth connected'
                    : 'Personal OpenAI OAuth connected'),
              ),
            );
          }
          return;
        case CodexDeviceFlowStatus.expired:
          _finishFlowWithError('That one-time code expired. Start again.');
          return;
        case CodexDeviceFlowStatus.failed:
          _finishFlowWithError(
            result.error.isEmpty
                ? 'ChatGPT did not approve the connection. Start again.'
                : result.error,
          );
          ref.invalidate(
            _isShared
                ? adminCodexConnectionStatusProvider
                : codexConnectionStatusProvider,
          );
          await _refreshAppAvailability();
          return;
      }
    } catch (_) {
      if (!silent && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Could not check the connection. Try again.'),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _checking = false);
    }
  }

  void _finishFlowWithError(
    String message, {
    CodexDeviceAuthorization? cancelFlow,
  }) {
    _pollTimer?.cancel();
    _expiryTimer?.cancel();
    if (cancelFlow != null) {
      unawaited(_cancelFlowBestEffort(cancelFlow.flowId));
    }
    if (!mounted) return;
    setState(() {
      _flow = null;
      _flowExpiresAt = null;
      _flowError = message;
    });
  }

  Future<void> _cancelConnection() async {
    final flow = _flow;
    if (flow == null || _cancelling) return;
    _pollTimer?.cancel();
    _expiryTimer?.cancel();
    setState(() => _cancelling = true);
    try {
      await _service.cancelDeviceAuthorization(flow.flowId);
    } catch (_) {
      // The server expires abandoned flows. Cancelling locally is still safe.
    } finally {
      if (mounted) {
        setState(() {
          _flow = null;
          _flowExpiresAt = null;
          _cancelling = false;
          _flowError = null;
        });
      }
    }
  }

  Future<void> _cancelFlowBestEffort(String flowId) async {
    try {
      await _service.cancelDeviceAuthorization(flowId);
    } catch (_) {
      // The server also expires abandoned flows; cleanup must never surface as
      // an unhandled error while the route is being torn down.
    }
  }

  Future<void> _unlink() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(_isShared
            ? 'Disconnect shared OpenAI OAuth?'
            : 'Disconnect personal OpenAI OAuth?'),
        content: Text(
          _isShared
              ? 'Included AI will stop working for every user who relies on '
                  'this shared account. Cantinarr conversations are not deleted.'
              : 'Cantinarr will forget this personal account connection. If it '
                  'is your selected provider, AI stays unavailable until you '
                  'choose another source. Cantinarr conversations are not deleted.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: const Text('Disconnect'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    setState(() => _unlinking = true);
    try {
      await _service.unlink();
      if (_isShared) {
        ref.invalidate(adminCodexConnectionStatusProvider);
        ref.invalidate(aiSettingsProvider);
      } else {
        ref.invalidate(codexConnectionStatusProvider);
        ref.invalidate(aiSettingsProvider);
      }
      await _refreshAppAvailability();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(_isShared
                ? 'Shared OpenAI OAuth disconnected'
                : 'Personal OpenAI OAuth disconnected'),
          ),
        );
      }
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Could not disconnect OpenAI OAuth. Try again.'),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _unlinking = false);
    }
  }

  Future<void> _refreshAppAvailability() async {
    try {
      await ref.read(authProvider.notifier).refreshConfig();
    } catch (_) {
      // The status tile still refreshes immediately; config retries on resume.
    }
  }

  Future<void> _copyCode(String code) async {
    await Clipboard.setData(ClipboardData(text: code));
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('One-time code copied')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final status = ref.watch(
      _isShared
          ? adminCodexConnectionStatusProvider
          : codexConnectionStatusProvider,
    );
    return Scaffold(
      appBar: AppBar(
        title: Text(
          _isShared ? 'Shared OpenAI (OAuth)' : 'Personal OpenAI (OAuth)',
        ),
        actions: [
          IconButton(
            onPressed: () => ref.invalidate(
              _isShared
                  ? adminCodexConnectionStatusProvider
                  : codexConnectionStatusProvider,
            ),
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh connection',
          ),
        ],
      ),
      body: CenteredContent(
        child: status.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppTheme.accent),
          ),
          error: (_, __) => _StatusError(
            onRetry: () => ref.invalidate(
              _isShared
                  ? adminCodexConnectionStatusProvider
                  : codexConnectionStatusProvider,
            ),
          ),
          data: _buildStatus,
        ),
      ),
    );
  }

  Widget _buildStatus(CodexConnectionStatus status) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        _ConnectionIntro(connected: status.connected, shared: _isShared),
        if (_isShared) ...[
          const SizedBox(height: 14),
          const _SharedAccountWarning(),
        ],
        if (_flowError != null) ...[
          const SizedBox(height: 14),
          _InlineMessage(message: _flowError!, isError: true),
        ],
        const SizedBox(height: 20),
        if (status.connected)
          _buildConnected(status)
        else if (status.selected && !status.available)
          _UnavailablePanel(selected: true, shared: _isShared)
        else if (_flow != null)
          _buildPending(_flow!)
        else
          _buildDisconnected(),
      ],
    );
  }

  Widget _buildDisconnected() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          _isShared ? 'Connect the server account' : 'Connect your own account',
          style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 18,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 6),
        Text(
          _isShared
              ? 'ChatGPT opens in your browser. The resulting authorization '
                  'stays encrypted on this server and can power included AI '
                  'for users you explicitly enable.'
              : 'ChatGPT opens in your browser and gives your Cantinarr '
                  'account a private, revocable connection. Your password '
                  'never passes through Cantinarr.',
          style: const TextStyle(color: AppTheme.textSecondary, height: 1.45),
        ),
        const SizedBox(height: 18),
        ElevatedButton.icon(
          onPressed: _starting ? null : _beginConnection,
          icon: _starting
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Icon(Icons.open_in_browser, size: 19),
          label: Text(
            _isShared ? 'Connect shared OpenAI OAuth' : 'Connect OpenAI OAuth',
          ),
        ),
      ],
    );
  }

  Widget _buildPending(CodexDeviceAuthorization flow) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        AppPanel(
          accentColor: AppTheme.accent,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const Row(
                children: [
                  SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: AppTheme.signal,
                    ),
                  ),
                  SizedBox(width: 10),
                  Text(
                    'Waiting for ChatGPT approval',
                    style: TextStyle(
                      color: AppTheme.textPrimary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              Container(
                padding: const EdgeInsets.fromLTRB(16, 14, 8, 14),
                decoration: BoxDecoration(
                  color: AppTheme.background.withValues(alpha: 0.72),
                  borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
                  border: Border.all(
                    color: AppTheme.accent.withValues(alpha: 0.42),
                  ),
                ),
                child: Row(
                  children: [
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const Text(
                            'ONE-TIME CODE',
                            style: TextStyle(
                              color: AppTheme.textMuted,
                              fontSize: 10,
                              fontWeight: FontWeight.w700,
                              letterSpacing: 1.4,
                            ),
                          ),
                          const SizedBox(height: 5),
                          SelectableText(
                            flow.userCode,
                            key: const ValueKey('codex-user-code'),
                            style: const TextStyle(
                              color: AppTheme.accent,
                              fontSize: 26,
                              fontWeight: FontWeight.w700,
                              letterSpacing: 2.8,
                            ),
                          ),
                        ],
                      ),
                    ),
                    IconButton(
                      onPressed: () => _copyCode(flow.userCode),
                      icon: const Icon(Icons.copy_rounded),
                      tooltip: 'Copy one-time code',
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 14),
              const Text(
                'Sign in on the page that opened, enter this code, then '
                'approve Cantinarr. This screen checks automatically.',
                style: TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 13,
                  height: 1.45,
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: 14),
        Wrap(
          spacing: 10,
          runSpacing: 8,
          children: [
            OutlinedButton(
              onPressed: _checking ? null : () => _checkConnection(),
              child: Text(_checking ? 'Checking…' : 'Check now'),
            ),
            OutlinedButton.icon(
              onPressed: () => _openVerificationPage(flow.verificationUri),
              icon: const Icon(Icons.open_in_new, size: 17),
              label: const Text('Reopen ChatGPT sign-in'),
            ),
            TextButton(
              onPressed: _cancelling ? null : _cancelConnection,
              child: Text(_cancelling ? 'Cancelling…' : 'Cancel'),
            ),
          ],
        ),
      ],
    );
  }

  Widget _buildConnected(CodexConnectionStatus status) {
    final limits = status.rateLimits;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        AppPanel(
          accentColor: AppTheme.available,
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 42,
                height: 42,
                decoration: BoxDecoration(
                  color: AppTheme.available.withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
                ),
                child: const Icon(
                  Icons.check_rounded,
                  color: AppTheme.available,
                ),
              ),
              const SizedBox(width: 13),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Text(
                      'Connected',
                      style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 17,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    if (status.accountEmail.isNotEmpty) ...[
                      const SizedBox(height: 3),
                      Text(
                        status.accountEmail,
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                        ),
                      ),
                    ],
                    if (status.planType.isNotEmpty) ...[
                      const SizedBox(height: 3),
                      Text(
                        _planLabel(status.planType),
                        style: const TextStyle(
                          color: AppTheme.textMuted,
                          fontSize: 12,
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            ],
          ),
        ),
        if (!status.available) ...[
          const SizedBox(height: 14),
          _UnavailablePanel(
            selected: status.selected,
            connected: true,
            shared: _isShared,
          ),
        ],
        if (limits != null && !limits.isEmpty) ...[
          const SizedBox(height: 22),
          const Text(
            'Codex usage',
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 18,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            status.stale
                ? 'This cached snapshot may be out of date. Refresh to try again.'
                : status.updatedAt == null
                    ? 'Shown from the server’s recent ChatGPT usage snapshot.'
                    : 'Updated ${DateFormat('MMM d, h:mm a').format(status.updatedAt!.toLocal())}',
            style: TextStyle(
              color: status.stale ? AppTheme.warning : AppTheme.textSecondary,
              fontSize: 13,
            ),
          ),
          const SizedBox(height: 12),
          if (limits.primary != null)
            _UsageMeter(
              label: 'Short-term allowance',
              window: limits.primary!,
            ),
          if (limits.primary != null && limits.secondary != null)
            const SizedBox(height: 12),
          if (limits.secondary != null)
            _UsageMeter(
              label: 'Long-term allowance',
              window: limits.secondary!,
            ),
        ],
        const SizedBox(height: 24),
        OutlinedButton.icon(
          onPressed: _unlinking ? null : _unlink,
          icon: const Icon(Icons.link_off, size: 18),
          label: Text(_unlinking
              ? 'Disconnecting…'
              : _isShared
                  ? 'Disconnect shared OpenAI OAuth'
                  : 'Disconnect OpenAI OAuth'),
          style: OutlinedButton.styleFrom(foregroundColor: AppTheme.error),
        ),
      ],
    );
  }
}

class _ConnectionIntro extends StatelessWidget {
  final bool connected;
  final bool shared;

  const _ConnectionIntro({required this.connected, required this.shared});

  @override
  Widget build(BuildContext context) {
    return AppPanel(
      accentColor: AppTheme.signal,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(
            connected ? Icons.bolt_rounded : Icons.auto_awesome_rounded,
            color: AppTheme.signal,
            size: 28,
          ),
          const SizedBox(height: 12),
          Text(
            shared ? 'Included AI, one allowance' : 'Use your ChatGPT plan',
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 22,
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 7),
          Text(
            shared
                ? 'This server account can power the assistant for every user '
                    'you grant included access. They share its Codex meter.'
                : 'Cantinarr can run its assistant against your own Codex '
                    'allowance. This personal connection takes priority over '
                    'included AI when selected.',
            style: const TextStyle(color: AppTheme.textSecondary, height: 1.45),
          ),
        ],
      ),
    );
  }
}

class _UnavailablePanel extends StatelessWidget {
  final bool selected;
  final bool connected;
  final bool shared;

  const _UnavailablePanel({
    required this.selected,
    this.connected = false,
    this.shared = false,
  });

  @override
  Widget build(BuildContext context) {
    return _InlineMessage(
      message: shared
          ? connected
              ? selected
                  ? 'The shared account remains connected, but Codex is '
                      'currently unavailable on this server.'
                  : 'The shared account remains connected. Select OpenAI '
                      '(OAuth) as the included provider to use it.'
              : selected
                  ? 'OpenAI OAuth is selected for included AI, but the Codex '
                      'runtime is currently unavailable.'
                  : 'Select OpenAI (OAuth) as the included provider before '
                      'connecting the shared account.'
          : connected
              ? selected
                  ? 'Your personal OpenAI OAuth remains connected, but Codex '
                      'is currently unavailable on this server.'
                  : 'Your personal OpenAI OAuth is saved while you use a '
                      'different AI source.'
              : selected
                  ? 'Personal OpenAI OAuth is selected, but Codex is currently '
                      'unavailable on this server.'
                  : 'This personal connection is saved even while you use a '
                      'different AI source.',
    );
  }
}

class _SharedAccountWarning extends StatelessWidget {
  const _SharedAccountWarning();

  @override
  Widget build(BuildContext context) {
    return const AppPanel(
      accentColor: AppTheme.warning,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.warning_amber_rounded, color: AppTheme.warning),
          SizedBox(width: 12),
          Expanded(
            child: Text(
              'Sharing this connection means enabled users send prompts and '
              'tool context through the same OpenAI OAuth account and consume one '
              'Codex allowance. Activity is attributable to that account, and '
              'any subscription or usage costs remain with it. ChatGPT '
              'accounts are intended for one person; only enable this for '
              'people or devices you control.',
              style: TextStyle(
                color: AppTheme.textSecondary,
                height: 1.45,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _InlineMessage extends StatelessWidget {
  final String message;
  final bool isError;

  const _InlineMessage({required this.message, this.isError = false});

  @override
  Widget build(BuildContext context) {
    final color = isError ? AppTheme.error : AppTheme.textSecondary;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
        border: Border.all(color: color.withValues(alpha: 0.24)),
      ),
      child: Text(message, style: TextStyle(color: color, height: 1.4)),
    );
  }
}

class _UsageMeter extends StatelessWidget {
  final String label;
  final CodexRateLimitWindow window;

  const _UsageMeter({required this.label, required this.window});

  @override
  Widget build(BuildContext context) {
    final used = window.usedPercent.clamp(0, 100).toDouble();
    final color = used >= 90
        ? AppTheme.error
        : used >= 75
            ? AppTheme.warning
            : AppTheme.accent;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant.withValues(alpha: 0.72),
        borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  label,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Text(
                '${_percentLabel(used)}% used',
                style: TextStyle(
                  color: color,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          ClipRRect(
            borderRadius: BorderRadius.circular(AppTheme.radiusPill),
            child: LinearProgressIndicator(
              value: used / 100,
              minHeight: 7,
              color: color,
              backgroundColor: AppTheme.border,
            ),
          ),
          if (window.resetsAt != null) ...[
            const SizedBox(height: 8),
            Text(
              'Resets ${DateFormat('MMM d, h:mm a').format(window.resetsAt!.toLocal())}',
              style: const TextStyle(
                color: AppTheme.textMuted,
                fontSize: 12,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _StatusError extends StatelessWidget {
  final VoidCallback onRetry;

  const _StatusError({required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.cloud_off_outlined,
                color: AppTheme.textSecondary, size: 42),
            const SizedBox(height: 12),
            const Text(
              'Could not load the OpenAI OAuth connection.',
              style: TextStyle(color: AppTheme.textSecondary),
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 12),
            OutlinedButton(onPressed: onRetry, child: const Text('Retry')),
          ],
        ),
      ),
    );
  }
}

String _percentLabel(double value) => value == value.roundToDouble()
    ? value.toInt().toString()
    : value.toStringAsFixed(1);

String _planLabel(String raw) {
  final words = raw
      .replaceAll('_', ' ')
      .split(' ')
      .where((word) => word.isNotEmpty)
      .map((word) => '${word[0].toUpperCase()}${word.substring(1)}')
      .join(' ');
  return words.toLowerCase().startsWith('chatgpt') ? words : 'ChatGPT $words';
}
