import 'package:cantinarr/l10n/l10n.dart';
import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/ai_settings_service.dart';
import '../data/grok_oauth_service.dart';
import 'codex_connection_screen.dart' show codexExternalUrlLauncherProvider;

/// Self-service xAI Grok OAuth connection for the current Cantinarr user, or
/// the admin-funded shared account when [scope] is adminShared.
///
/// Signing in uses xAI's device flow: Cantinarr shows a one-time code, the
/// xAI page approves it, and the resulting authorization stays encrypted on
/// the server. Works with a SuperGrok subscription or X Premium+.
class GrokConnectionScreen extends ConsumerStatefulWidget {
  final GrokOAuthScope scope;

  const GrokConnectionScreen({
    super.key,
    this.scope = GrokOAuthScope.personal,
  });

  @override
  ConsumerState<GrokConnectionScreen> createState() =>
      _GrokConnectionScreenState();
}

class _GrokConnectionScreenState extends ConsumerState<GrokConnectionScreen> {
  late final GrokOAuthService _service;
  GrokDeviceAuthorization? _flow;
  DateTime? _flowExpiresAt;
  Timer? _pollTimer;
  Timer? _expiryTimer;
  bool _starting = false;
  bool _checking = false;
  bool _cancelling = false;
  bool _unlinking = false;
  String? _flowError;

  bool get _isShared => widget.scope == GrokOAuthScope.adminShared;

  @override
  void initState() {
    super.initState();
    _service = ref.read(
      _isShared ? adminGrokOAuthServiceProvider : grokOAuthServiceProvider,
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

  void _invalidateStatus() {
    ref.invalidate(
      _isShared
          ? adminGrokConnectionStatusProvider
          : grokConnectionStatusProvider,
    );
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
    } on DioException catch (error) {
      if (!mounted) return;
      // The server's error body explains actionable conflicts ("a sign-in is
      // already in progress", "disconnect the current account first") that a
      // generic retry hint would misdescribe.
      final serverMessage = _responseError(error.response?.data);
      setState(() {
        _flowError = serverMessage.isNotEmpty
            ? serverMessage
            : context.l10n.settingsCouldNotStartXaiSignInTryAgain;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _flowError = context.l10n.settingsCouldNotStartXaiSignInTryAgain;
      });
    } finally {
      if (mounted) setState(() => _starting = false);
    }
  }

  void _startPolling(GrokDeviceAuthorization flow) {
    _pollTimer?.cancel();
    _expiryTimer?.cancel();
    _pollTimer = Timer.periodic(
      flow.pollInterval,
      (_) => unawaited(_checkConnection(silent: true)),
    );
    _expiryTimer = Timer(flow.expiresIn, () {
      if (_flow?.flowId != flow.flowId) return;
      _finishFlowWithError(
        context.l10n.settingsThatOneTimeCodeExpiredStartAgain,
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
        SnackBar(
          content: Text(
            context.l10n.settingsCouldNotOpenTheXaiSignInPage,
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
        context.l10n.settingsThatOneTimeCodeExpiredStartAgain,
        cancelFlow: flow,
      );
      return;
    }

    setState(() => _checking = true);
    try {
      final result = await _service.checkDeviceAuthorization(flow.flowId);
      if (!mounted || _flow?.flowId != flow.flowId) return;
      switch (result.status) {
        case GrokDeviceFlowStatus.pending:
          if (!silent) {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content:
                    Text(context.l10n.settingsStillWaitingForApprovalOnXAi),
              ),
            );
          }
          return;
        case GrokDeviceFlowStatus.connected:
          _pollTimer?.cancel();
          _expiryTimer?.cancel();
          setState(() {
            _flow = null;
            _flowExpiresAt = null;
            _flowError = null;
          });
          _invalidateStatus();
          ref.invalidate(aiSettingsProvider);
          await _refreshAppAvailability();
          if (mounted) {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content: Text(_isShared
                    ? context.l10n.settingsSharedXaiGrokConnected
                    : context.l10n.settingsPersonalXaiGrokConnected),
              ),
            );
          }
          return;
        case GrokDeviceFlowStatus.expired:
          _finishFlowWithError(
              context.l10n.settingsThatOneTimeCodeExpiredStartAgain);
          return;
        case GrokDeviceFlowStatus.failed:
          _finishFlowWithError(
            result.error.isEmpty
                ? context.l10n.settingsXaiDidNotApproveTheConnectionStartAgain
                : result.error,
          );
          _invalidateStatus();
          await _refreshAppAvailability();
          return;
      }
    } catch (_) {
      if (!silent && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content:
                Text(context.l10n.settingsCouldNotCheckTheConnectionTryAgain),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _checking = false);
    }
  }

  void _finishFlowWithError(
    String message, {
    GrokDeviceAuthorization? cancelFlow,
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
            ? context.l10n.settingsDisconnectSharedXaiGrok
            : context.l10n.settingsDisconnectPersonalXaiGrok),
        content: Text(
          _isShared
              ? context.l10n.settingsIncludedAiWillStopWorkingForEveryUser
              : context.l10n
                  .settingsCantinarrWillForgetThisPersonalAccountConnectionIf,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: Text(context.l10n.settingsCancel),
          ),
          ElevatedButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: Text(context.l10n.settingsDisconnect),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    setState(() => _unlinking = true);
    try {
      await _service.unlink();
      _invalidateStatus();
      ref.invalidate(aiSettingsProvider);
      await _refreshAppAvailability();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(_isShared
                ? context.l10n.settingsSharedXaiGrokDisconnected
                : context.l10n.settingsPersonalXaiGrokDisconnected),
          ),
        );
      }
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content:
                Text(context.l10n.settingsCouldNotDisconnectXaiGrokTryAgain),
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
        SnackBar(content: Text(context.l10n.settingsOneTimeCodeCopied)),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final status = ref.watch(
      _isShared
          ? adminGrokConnectionStatusProvider
          : grokConnectionStatusProvider,
    );
    return Scaffold(
      appBar: AppBar(
        title: Text(
          _isShared
              ? context.l10n.settingsSharedXaiGrokOauth
              : context.l10n.settingsPersonalXaiGrokOauth,
        ),
        actions: [
          IconButton(
            onPressed: _invalidateStatus,
            icon: const Icon(Icons.refresh),
            tooltip: context.l10n.settingsRefreshConnection,
          ),
        ],
      ),
      body: CenteredContent(
        child: status.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppTheme.accent),
          ),
          error: (_, __) => _StatusError(onRetry: _invalidateStatus),
          data: _buildStatus,
        ),
      ),
    );
  }

  Widget _buildStatus(GrokConnectionStatus status) {
    final flow = _flow;
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        _ConnectionIntro(connected: status.connected, shared: _isShared),
        const SizedBox(height: 12),
        if (flow != null)
          _buildPending(flow)
        else if (status.connected)
          _buildConnected(status)
        else
          _buildDisconnected(status),
        if (_flowError != null) ...[
          const SizedBox(height: 12),
          Text(
            _flowError!,
            style: const TextStyle(color: AppTheme.error, height: 1.4),
          ),
        ],
      ],
    );
  }

  Widget _buildDisconnected(GrokConnectionStatus status) {
    return AppPanel(
      accentColor: AppTheme.signal,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            _isShared
                ? context.l10n.settingsTheXaiSignInPageOpensInYour
                : context.l10n.settingsTheXaiSignInPageOpensInYour2,
            style: const TextStyle(color: AppTheme.textSecondary, height: 1.45),
          ),
          const SizedBox(height: 8),
          Text(
            context.l10n.settingsWorksWithASupergrokSubscriptionOrXPremium,
            style: TextStyle(
              color: AppTheme.textMuted,
              fontSize: 12.5,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 14),
          ElevatedButton.icon(
            onPressed: _starting || !status.available ? null : _beginConnection,
            icon: _starting
                ? const SizedBox(
                    width: 17,
                    height: 17,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.open_in_browser_rounded, size: 18),
            label: Text(
              _isShared
                  ? context.l10n.settingsConnectSharedXaiGrok
                  : context.l10n.settingsConnectXaiGrok,
            ),
          ),
          if (!status.available) ...[
            const SizedBox(height: 10),
            Text(
              context.l10n.settingsGrokOauthIsUnavailableOnThisServerRight,
              style: TextStyle(color: AppTheme.textMuted, fontSize: 12.5),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildPending(GrokDeviceAuthorization flow) {
    return AppPanel(
      accentColor: AppTheme.accent,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              const SizedBox(
                width: 18,
                height: 18,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  context.l10n.settingsWaitingForXaiApproval,
                  style: TextStyle(fontWeight: FontWeight.w600),
                ),
              ),
              if (_checking)
                const Text(
                  'checking…',
                  style: TextStyle(color: AppTheme.textMuted, fontSize: 12),
                ),
            ],
          ),
          const SizedBox(height: 12),
          Text(
            context.l10n.settingsApproveTheRequestOnTheXaiPageIf,
            style: TextStyle(color: AppTheme.textSecondary, height: 1.4),
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(
                child: SelectableText(
                  flow.userCode,
                  key: const ValueKey('grok-user-code'),
                  style: const TextStyle(
                    fontSize: 22,
                    fontWeight: FontWeight.w700,
                    letterSpacing: 2,
                    fontFeatures: [FontFeature.tabularFigures()],
                  ),
                ),
              ),
              IconButton(
                onPressed: () => _copyCode(flow.userCode),
                icon: const Icon(Icons.copy_rounded),
                tooltip: context.l10n.settingsCopyCode,
              ),
            ],
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: OutlinedButton.icon(
                  onPressed: () => _openVerificationPage(flow.verificationUri),
                  icon: const Icon(Icons.open_in_new_rounded, size: 17),
                  label: Text(context.l10n.settingsReopenXaiSignIn),
                ),
              ),
              const SizedBox(width: 8),
              TextButton(
                onPressed: _checking ? null : () => _checkConnection(),
                child: Text(context.l10n.settingsCheckNow),
              ),
              const SizedBox(width: 8),
              TextButton(
                onPressed: _cancelling ? null : _cancelConnection,
                child: Text(context.l10n.settingsCancel),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildConnected(GrokConnectionStatus status) {
    return AppPanel(
      accentColor: AppTheme.success,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              const Icon(Icons.check_circle_rounded,
                  color: AppTheme.success, size: 20),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  status.accountEmail.isEmpty
                      ? context.l10n.settingsXaiAccountConnected
                      : status.accountEmail,
                  style: const TextStyle(fontWeight: FontWeight.w600),
                ),
              ),
            ],
          ),
          if (status.planType.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              context.l10n
                  .settingsMessagePlanValue1((status.planType).toString()),
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
              ),
            ),
          ],
          const SizedBox(height: 8),
          Text(
            _isShared
                ? context.l10n.settingsThisAccountFundsIncludedAiForEveryGranted
                : context.l10n.settingsChatTurnsUsingThisProviderDrawOnThis,
            style: const TextStyle(
              color: AppTheme.textMuted,
              fontSize: 12.5,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 14),
          OutlinedButton.icon(
            onPressed: _unlinking ? null : _unlink,
            icon: _unlinking
                ? const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.link_off_rounded, size: 18),
            style: OutlinedButton.styleFrom(foregroundColor: AppTheme.error),
            label: Text(
              _isShared
                  ? context.l10n.settingsDisconnectSharedXaiGrok2
                  : context.l10n.settingsDisconnectXaiGrok,
            ),
          ),
        ],
      ),
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
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            shared
                ? context.l10n.settingsIncludedAiOneAllowance
                : context.l10n.settingsUseYourGrokPlan,
            style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700),
          ),
          const SizedBox(height: 6),
          Text(
            connected
                ? context.l10n
                    .settingsMessageGrokAnswersWithThisValue1XaiAccountManage(
                        (shared
                                ? context.l10n.settingsShared
                                : context.l10n.settingsPersonal3)
                            .toString())
                : context.l10n.settingsSignInWithAnXaiAccountInsteadOf,
            style: const TextStyle(color: AppTheme.textSecondary, height: 1.45),
          ),
        ],
      ),
    );
  }
}

String _responseError(Object? data) {
  if (data is Map<String, dynamic>) {
    return data['error'] as String? ?? '';
  }
  return '';
}

class _StatusError extends StatelessWidget {
  final VoidCallback onRetry;

  const _StatusError({required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(context.l10n.settingsCouldNotLoadTheGrokConnectionStatus),
          const SizedBox(height: 10),
          OutlinedButton(
              onPressed: onRetry, child: Text(context.l10n.settingsRetry)),
        ],
      ),
    );
  }
}
