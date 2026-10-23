import 'package:cantinarr/l10n/l10n.dart';
import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/network/api_error_message.dart';
import '../data/hardcover_connection.dart';
import '../data/instance_api_service.dart';

final hardcoverUrlLauncherProvider = Provider<Future<bool> Function(Uri)>(
    (ref) => (uri) => launchUrl(uri, mode: LaunchMode.externalApplication));

/// The server owns polling intervals and completion. Closing this dialog
/// cancels the initiating admin's flow; a replacement keeps working until the
/// new authorization passes catalog verification.
class HardcoverConnectionDialog extends ConsumerStatefulWidget {
  final InstanceApiService service;
  final String instanceId;
  const HardcoverConnectionDialog(
      {super.key, required this.service, required this.instanceId});

  @override
  ConsumerState<HardcoverConnectionDialog> createState() =>
      _HardcoverConnectionDialogState();
}

class _HardcoverConnectionDialogState
    extends ConsumerState<HardcoverConnectionDialog>
    with WidgetsBindingObserver {
  HardcoverDeviceFlow? _flow;
  Timer? _poll;
  bool _starting = true;
  bool _checking = false;
  bool _cancelling = false;
  bool _closing = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    unawaited(_begin());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _poll?.cancel();
    final flow = _flow;
    if (!_closing && flow?.status == 'pending') {
      unawaited(_cancelAbandoned(flow!.flowId));
    }
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) unawaited(_check());
  }

  Future<void> _cancelAbandoned(String id) async {
    try {
      await widget.service.cancelHardcoverDevice(widget.instanceId, id);
    } catch (_) {}
  }

  Future<void> _begin() async {
    _poll?.cancel();
    setState(() {
      _starting = true;
      _error = null;
      _flow = null;
    });
    try {
      final flow = await widget.service.beginHardcoverDevice(widget.instanceId);
      if (!mounted || _closing) {
        await _cancelAbandoned(flow.flowId);
        return;
      }
      _accept(flow);
    } catch (e) {
      if (mounted) {
        setState(() => _error = context.l10n
            .settingsMessageCouldNotStartHardcoverSignInValue1(
                (apiErrorMessage(e)).toString()));
      }
    } finally {
      if (mounted) setState(() => _starting = false);
    }
  }

  void _accept(HardcoverDeviceFlow flow) {
    if (_closing) return;
    if (flow.status == 'connected') {
      _close(flow);
      return;
    }
    setState(() {
      _flow = flow;
      _error = flow.error.isEmpty ? null : flow.error;
    });
    _schedule();
  }

  void _schedule() {
    _poll?.cancel();
    final flow = _flow;
    if (flow?.status == 'pending' && !_closing && !_cancelling) {
      _poll = Timer(flow!.interval, () => unawaited(_check()));
    }
  }

  Future<void> _check() async {
    final flow = _flow;
    if (flow == null ||
        flow.status != 'pending' ||
        _checking ||
        _cancelling ||
        _closing) {
      return;
    }
    _poll?.cancel();
    _checking = true;
    try {
      final result = await widget.service
          .checkHardcoverDevice(widget.instanceId, flow.flowId);
      if (mounted && !_cancelling) _accept(result);
    } catch (e) {
      if (!mounted || _cancelling || _closing) return;
      final missing = e is DioException && e.response?.statusCode == 404;
      setState(() {
        if (missing) _flow = null;
        _error = missing
            ? context.l10n.settingsThatSignInIsNoLongerAvailableThe
            : context.l10n
                .settingsMessageCouldNotCheckHardcoverYetRetryingAutomaticallyValue1(
                    (apiErrorMessage(e)).toString());
      });
    } finally {
      _checking = false;
      if (mounted) _schedule();
    }
  }

  Future<void> _cancel() async {
    if (_cancelling || _closing) return;
    _poll?.cancel();
    final flow = _flow;
    if (flow == null || flow.status != 'pending') {
      _close(null);
      return;
    }
    setState(() => _cancelling = true);
    try {
      final result = await widget.service
          .cancelHardcoverDevice(widget.instanceId, flow.flowId);
      if (mounted) _close(result.status == 'connected' ? result : null);
    } catch (e) {
      if (!mounted) return;
      if (e is DioException && e.response?.statusCode == 404) {
        _close(null);
        return;
      }
      setState(() => _error = context.l10n
          .settingsMessageCouldNotCancelSignInTryCancelAgain(
              (apiErrorMessage(e)).toString()));
    } finally {
      if (mounted) setState(() => _cancelling = false);
    }
  }

  void _close(HardcoverDeviceFlow? result) {
    if (_closing || !mounted) return;
    _poll?.cancel();
    setState(() => _closing = true);
    Navigator.of(context).pop(result);
  }

  Future<void> _open() async {
    final uri = _flow?.verificationUri;
    if (uri == null) return;
    var opened = false;
    try {
      opened = await ref.read(hardcoverUrlLauncherProvider)(uri);
    } catch (_) {}
    if (!opened && mounted) {
      setState(() => _error =
          context.l10n.settingsCouldNotOpenTheBrowserVisitHardcoverApp);
    }
  }

  Future<void> _copy() async {
    try {
      await Clipboard.setData(ClipboardData(text: _flow!.userCode));
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text(context.l10n.settingsCodeCopied)));
      }
    } catch (_) {
      if (mounted) {
        setState(() =>
            _error = context.l10n.settingsCouldNotCopyTheCodeYouCanSelect);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final flow = _flow;
    final pending = flow?.status == 'pending';
    return PopScope<HardcoverDeviceFlow>(
      canPop: _closing,
      onPopInvokedWithResult: (didPop, result) {
        if (!didPop) unawaited(_cancel());
      },
      child: AlertDialog(
        constraints: const BoxConstraints(maxWidth: 520),
        scrollable: true,
        title: Text(context.l10n.settingsConnectHardcover),
        content: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(context
                  .l10n.settingsApproveThisCodeOnHardcoverToShowTrending),
              if (_starting) ...[
                const SizedBox(height: 20),
                const Center(child: CircularProgressIndicator())
              ],
              if (pending) ...[
                const SizedBox(height: 20),
                SelectableText(flow!.userCode,
                    style: Theme.of(context)
                        .textTheme
                        .headlineMedium
                        ?.copyWith(letterSpacing: 2)),
                const SizedBox(height: 8),
                const SelectableText('hardcover.app/link'),
                const SizedBox(height: 12),
                Text(context.l10n
                    .settingsMessageWaitingForApprovalCodeExpiresAtValue1Cantinarr(
                        (MaterialLocalizations.of(context).formatTimeOfDay(
                                TimeOfDay.fromDateTime(
                                    flow.expiresAt!.toLocal())))
                            .toString())),
                const SizedBox(height: 12),
                Wrap(spacing: 8, runSpacing: 8, children: [
                  FilledButton.icon(
                      onPressed: _cancelling ? null : _open,
                      icon: const Icon(Icons.open_in_new),
                      label: Text(context.l10n.settingsOpenHardcover)),
                  TextButton.icon(
                      onPressed: _cancelling ? null : _copy,
                      icon: const Icon(Icons.copy),
                      label: Text(context.l10n.settingsCopyCode)),
                ]),
              ],
              if (_error != null) ...[
                const SizedBox(height: 16),
                Text(_error!,
                    style:
                        TextStyle(color: Theme.of(context).colorScheme.error))
              ],
            ]),
        actions: [
          if (!_starting && !pending)
            TextButton(
                onPressed: _begin,
                child: Text(context.l10n.settingsStartAgain)),
          TextButton(
              onPressed: _cancelling ? null : _cancel,
              child: Text(_cancelling
                  ? context.l10n.settingsCancelling
                  : context.l10n.settingsCancel)),
        ],
      ),
    );
  }
}
