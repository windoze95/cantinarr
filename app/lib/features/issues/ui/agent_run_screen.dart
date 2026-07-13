import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../data/agent_action_models.dart';
import '../logic/issues_provider.dart';
import 'issue_refresh_banner.dart';

/// Read-only audit timeline for one agent run (`GET /api/admin/agent-runs/{id}`).
///
/// Renders the run's summary and its ordered steps (each model turn
/// and tool call) as a passive timeline. Every step's text — tool input/output,
/// assistant reasoning — is UNTRUSTED and rendered as selectable, truncated
/// text only. Nothing here is a control.
class AgentRunScreen extends ConsumerStatefulWidget {
  final int runId;

  const AgentRunScreen({super.key, required this.runId});

  @override
  ConsumerState<AgentRunScreen> createState() => _AgentRunScreenState();
}

class _AgentRunScreenState extends ConsumerState<AgentRunScreen>
    with WidgetsBindingObserver {
  AgentRunDetail? _detail;
  bool _isLoading = true;
  String? _error;
  int _loadEpoch = 0;
  Timer? _poll;

  static const _pollInterval = Duration(seconds: 10);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _load();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _poll?.cancel();
    super.dispose();
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load() async {
    if (!mounted) return;
    final epoch = ++_loadEpoch;
    setState(() {
      _isLoading = _detail == null;
      if (_detail == null) _error = null;
    });
    try {
      final detail = await ref.read(issuesServiceProvider).getRun(widget.runId);
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _detail = detail;
        _isLoading = false;
        _error = null;
      });
      _syncPolling(detail.run.status);
    } catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  void _syncPolling(String status) {
    final live = const {
      'running',
      'waiting_user',
      'waiting_approval',
      'resume_pending',
    }.contains(status);
    if (live) {
      _poll ??= Timer.periodic(_pollInterval, (_) => _load());
    } else {
      _poll?.cancel();
      _poll = null;
    }
  }

  @override
  Widget build(BuildContext context) {
    final detail = _detail;
    return Scaffold(
      appBar: AppBar(title: const Text('Agent activity')),
      body: CenteredContent(
          child: _isLoading
              ? const Center(
                  child: CircularProgressIndicator(color: AppTheme.accent))
              : detail == null
                  ? Center(
                      child: Padding(
                        padding: const EdgeInsets.all(24),
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text(_friendlyError(_error ?? 'Could not load run'),
                                style: const TextStyle(color: AppTheme.error),
                                textAlign: TextAlign.center),
                            const SizedBox(height: 12),
                            ElevatedButton(
                                onPressed: _load, child: const Text('Retry')),
                          ],
                        ),
                      ),
                    )
                  : RefreshIndicator(
                      color: AppTheme.accent,
                      onRefresh: _load,
                      child: ListView(
                        physics: const AlwaysScrollableScrollPhysics(),
                        padding: const EdgeInsets.all(16),
                        children: [
                          if (_error != null) ...[
                            IssueRefreshBanner(
                              message:
                                  "Couldn't refresh agent activity. Showing the last update.",
                              onRetry: _load,
                            ),
                            const SizedBox(height: 12),
                          ],
                          _RunSummary(run: detail.run),
                          const SizedBox(height: 16),
                          if (detail.steps.isEmpty)
                            const Padding(
                              padding: EdgeInsets.only(top: 24),
                              child: Center(
                                child: Text('No steps recorded.',
                                    style: TextStyle(
                                        color: AppTheme.textSecondary)),
                              ),
                            )
                          else
                            for (final step in detail.steps)
                              _StepTile(step: step),
                        ],
                      ),
                    )),
    );
  }
}

/// The run summary, shown above the step timeline.
class _RunSummary extends StatelessWidget {
  final AgentRun run;
  const _RunSummary({required this.run});

  @override
  Widget build(BuildContext context) {
    final chips = <String>[
      if (run.model.isNotEmpty) run.model,
      '${run.stepCount} step${run.stepCount == 1 ? '' : 's'}',
      if (run.stopReasonLabel != null) run.stopReasonLabel!,
    ];
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border, width: 0.5),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Run #${run.id} · ${run.statusLabel}',
            style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 14,
                fontWeight: FontWeight.w700),
          ),
          if (run.startedAt != null) ...[
            const SizedBox(height: 4),
            Text(
              'Started ${DateFormat('MMM d, h:mm a').format(run.startedAt!)}',
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
          ],
          const SizedBox(height: 10),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: chips.map(_chip).toList(),
          ),
        ],
      ),
    );
  }

  Widget _chip(String label) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: AppTheme.border),
      ),
      child: Text(
        label,
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

/// One step of the audit ledger. Tool calls show the tool name + a quoted,
/// truncated input/output; assistant turns show the reasoning text; give-ups
/// and errors are tinted. All text is passive.
class _StepTile extends StatelessWidget {
  final AgentStep step;
  const _StepTile({required this.step});

  @override
  Widget build(BuildContext context) {
    final (icon, color, heading) = _describe(step);
    final details = <Widget>[];

    if (step.text != null && step.text!.trim().isNotEmpty) {
      details.add(_text(step.text!.trim()));
    }
    if (step.toolInput != null && step.toolInput!.trim().isNotEmpty) {
      details.add(_quoted('input', step.toolInput!.trim()));
    }
    if (step.toolOutput != null && step.toolOutput!.trim().isNotEmpty) {
      details.add(_quoted('result', step.toolOutput!.trim()));
    }

    return Padding(
      padding: const EdgeInsets.only(bottom: 14),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 2),
            child: Icon(icon, size: 16, color: color),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  heading,
                  style: TextStyle(
                    color: step.isError ? AppTheme.error : AppTheme.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                ...details,
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _text(String t) => Padding(
        padding: const EdgeInsets.only(top: 4),
        child: SelectableText(
          t,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontSize: 13, height: 1.35),
        ),
      );

  Widget _quoted(String label, String value) => Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Container(
          width: double.infinity,
          padding: const EdgeInsets.all(8),
          decoration: BoxDecoration(
            color: AppTheme.surfaceVariant,
            borderRadius: BorderRadius.circular(6),
            border: const Border(
              left: BorderSide(color: AppTheme.border, width: 2),
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                label[0].toUpperCase() + label.substring(1),
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 10,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(height: 4),
              SelectableText(
                value,
                maxLines: 8,
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 12,
                  height: 1.35,
                  fontFamily: 'monospace',
                ),
              ),
            ],
          ),
        ),
      );

  (IconData, Color, String) _describe(AgentStep s) {
    if (s.isError) {
      return (Icons.error_outline, AppTheme.error, _toolHeading(s, 'Error'));
    }
    switch (s.kind) {
      case 'assistant':
        return (Icons.smart_toy, AppTheme.accent, 'Agent');
      case 'tool_call':
        return (
          Icons.build_outlined,
          AppTheme.downloading,
          _toolHeading(s, 'Tool call')
        );
      case 'tool_result':
        return (Icons.south, AppTheme.textSecondary, _toolHeading(s, 'Result'));
      case 'giveup':
        return (Icons.flag_outlined, AppTheme.error, 'Gave up');
      case 'system':
        return (Icons.info_outline, AppTheme.textSecondary, 'System');
      default:
        return (Icons.circle, AppTheme.textSecondary, 'Activity');
    }
  }

  String _toolHeading(AgentStep s, String fallback) =>
      (s.toolName != null && s.toolName!.isNotEmpty) ? s.toolName! : fallback;
}
