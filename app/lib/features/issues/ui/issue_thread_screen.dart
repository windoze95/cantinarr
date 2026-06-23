import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';

import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/issue_models.dart';
import '../logic/issues_provider.dart';

/// The issue conversation: a read-mostly transcript rendered with the AI-chat
/// bubble grammar, plus a reply field.
///
/// Provenance drives layout — a reporter/admin message is a right-aligned
/// bubble; an agent/system message is left/centered. Every message body is
/// rendered as PASSIVE, selectable text only (never a control or label),
/// because a `user` body is untrusted. Wave 1 only ever shows user/admin and
/// agent/system messages; proposed-action/decision arms are stubbed for a
/// later wave and intentionally render as a plain notice so unused cases can't
/// fail `flutter analyze`.
class IssueThreadScreen extends ConsumerStatefulWidget {
  final int issueId;

  const IssueThreadScreen({super.key, required this.issueId});

  @override
  ConsumerState<IssueThreadScreen> createState() => _IssueThreadScreenState();
}

class _IssueThreadScreenState extends ConsumerState<IssueThreadScreen> {
  IssueThread? _thread;
  bool _isLoading = true;
  String? _error;

  final _replyController = TextEditingController();
  final _scrollController = ScrollController();
  bool _sending = false;

  /// A short REST re-poll while the issue is still being worked, so steps that
  /// arrive without a WS ping (socket down) still surface. Best-effort.
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load(initial: true));
  }

  @override
  void dispose() {
    _poll?.cancel();
    _replyController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load({bool initial = false}) async {
    if (initial) setState(() => _isLoading = _thread == null);
    try {
      final thread =
          await ref.read(issuesServiceProvider).getThread(widget.issueId);
      if (!mounted) return;
      setState(() {
        _thread = thread;
        _isLoading = false;
        _error = null;
      });
      _syncPolling(thread.issue.status);
      if (initial) _scrollToBottomSoon();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        if (_thread == null) _error = e.toString();
      });
    }
  }

  /// Keep a low-frequency poll running only while the issue is actively being
  /// worked; stop it once the issue parks or terminates.
  void _syncPolling(IssueStatus status) {
    if (status.isActive) {
      _poll ??= Timer.periodic(
        const Duration(seconds: 10),
        (_) => _load(),
      );
    } else {
      _poll?.cancel();
      _poll = null;
    }
  }

  void _scrollToBottomSoon() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.jumpTo(_scrollController.position.maxScrollExtent);
      }
    });
  }

  Future<void> _sendReply() async {
    final text = _replyController.text.trim();
    if (text.isEmpty || _sending) return;
    setState(() => _sending = true);
    try {
      await ref.read(issuesServiceProvider).reply(widget.issueId, text);
      _replyController.clear();
      await _load();
      if (mounted) _scrollToBottomSoon();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    // Refetch the thread over REST when this issue pings (the server emits a
    // thin `issue_updated` per persisted step, not full bodies).
    ref.listen(issueEventsProvider(widget.issueId), (_, __) => _load());

    final thread = _thread;
    return Scaffold(
      appBar: AppBar(
        title: Text(thread?.issue.title.isNotEmpty == true
            ? thread!.issue.title
            : 'Issue'),
      ),
      body: _isLoading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent))
          : thread == null
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(_friendlyError(_error ?? 'Could not load issue'),
                            style: const TextStyle(color: AppTheme.error),
                            textAlign: TextAlign.center),
                        const SizedBox(height: 12),
                        ElevatedButton(
                          onPressed: () => _load(initial: true),
                          child: const Text('Retry'),
                        ),
                      ],
                    ),
                  ),
                )
              : _buildBody(thread),
    );
  }

  Widget _buildBody(IssueThread thread) {
    final issue = thread.issue;
    return Column(
      children: [
        Expanded(
          child: RefreshIndicator(
            color: AppTheme.accent,
            onRefresh: _load,
            child: ListView(
              controller: _scrollController,
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.all(16),
              children: [
                _IssueSummaryCard(issue: issue),
                const SizedBox(height: 16),
                for (final msg in thread.messages) _MessageRow(message: msg),
                if (issue.status.isActive) const _WorkingIndicator(),
              ],
            ),
          ),
        ),
        _ReplyBar(
          controller: _replyController,
          sending: _sending,
          enabled: !issue.status.isTerminal,
          hintText: issue.status == IssueStatus.awaitingUser
              ? 'Answer the question…'
              : 'Add a reply…',
          onSend: _sendReply,
        ),
      ],
    );
  }
}

/// A compact header describing the issue scope/category/status, with the
/// reporter's free-text detail rendered as passive (untrusted) text.
class _IssueSummaryCard extends StatelessWidget {
  final Issue issue;
  const _IssueSummaryCard({required this.issue});

  @override
  Widget build(BuildContext context) {
    final bits = <String>[
      issue.scopeLabel,
      if (issue.category != null) issue.category!.label,
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
            bits.join(' · '),
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 12),
          ),
          const SizedBox(height: 6),
          Text(
            'Status: ${issue.status.label}',
            style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w600),
          ),
          if (issue.detail.isNotEmpty) ...[
            const SizedBox(height: 10),
            // UNTRUSTED reporter/diagnosis text — passive, selectable only.
            SelectableText(
              issue.detail,
              style: const TextStyle(
                  color: AppTheme.textPrimary, fontSize: 14, height: 1.4),
            ),
          ],
        ],
      ),
    );
  }
}

/// One thread message rendered in the AI-chat grammar. Human (reporter/admin)
/// messages align right; agent/system align left or centered.
class _MessageRow extends StatelessWidget {
  final IssueMessage message;
  const _MessageRow({required this.message});

  @override
  Widget build(BuildContext context) {
    switch (message.authorKind) {
      case IssueAuthorKind.user:
      case IssueAuthorKind.admin:
        return _Bubble(message: message, isFromHuman: true);
      case IssueAuthorKind.agent:
        return _Bubble(message: message, isFromHuman: false);
      case IssueAuthorKind.system:
      case IssueAuthorKind.unknown:
        // Centered, low-emphasis notice. Also the safe fallback for any
        // not-yet-modeled message kind a later wave introduces.
        return _SystemNotice(message: message);
    }
  }
}

/// A left/right chat bubble. The body is always passive selectable text.
class _Bubble extends StatelessWidget {
  final IssueMessage message;
  final bool isFromHuman;

  const _Bubble({required this.message, required this.isFromHuman});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        mainAxisAlignment:
            isFromHuman ? MainAxisAlignment.end : MainAxisAlignment.start,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (!isFromHuman) ...[
            Container(
              width: 32,
              height: 32,
              decoration: const BoxDecoration(
                color: AppTheme.accent,
                shape: BoxShape.circle,
              ),
              child:
                  const Icon(Icons.smart_toy, size: 18, color: Colors.white),
            ),
            const SizedBox(width: 8),
          ],
          Flexible(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (message.authorName.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(left: 2, bottom: 4),
                    child: Text(
                      message.authorName,
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 11),
                    ),
                  ),
                Container(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 10),
                  decoration: BoxDecoration(
                    color: isFromHuman
                        ? AppTheme.accent.withValues(alpha: 0.15)
                        : AppTheme.surfaceVariant,
                    borderRadius: BorderRadius.only(
                      topLeft: const Radius.circular(16),
                      topRight: const Radius.circular(16),
                      bottomLeft: Radius.circular(isFromHuman ? 16 : 4),
                      bottomRight: Radius.circular(isFromHuman ? 4 : 16),
                    ),
                    border: Border.all(
                      color: isFromHuman
                          ? AppTheme.accent.withValues(alpha: 0.3)
                          : AppTheme.border,
                    ),
                  ),
                  // Passive, selectable text — never a control/label.
                  child: SelectableText(
                    message.body,
                    style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 15,
                      height: 1.4,
                    ),
                  ),
                ),
                if (message.createdAt != null)
                  Padding(
                    padding: const EdgeInsets.only(left: 2, top: 4),
                    child: Text(
                      DateFormat('MMM d, h:mm a').format(message.createdAt!),
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 10),
                    ),
                  ),
              ],
            ),
          ),
          if (isFromHuman) const SizedBox(width: 8),
        ],
      ),
    );
  }
}

/// A centered system notice (e.g. "Admin denied: …"). Passive text only.
class _SystemNotice extends StatelessWidget {
  final IssueMessage message;
  const _SystemNotice({required this.message});

  @override
  Widget build(BuildContext context) {
    if (message.body.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Center(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          decoration: BoxDecoration(
            color: AppTheme.surfaceVariant,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: AppTheme.border),
          ),
          child: SelectableText(
            message.body,
            textAlign: TextAlign.center,
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 12),
          ),
        ),
      ),
    );
  }
}

/// A small "working" hint shown while the issue is open/investigating.
class _WorkingIndicator extends StatelessWidget {
  const _WorkingIndicator();

  @override
  Widget build(BuildContext context) {
    return const Padding(
      padding: EdgeInsets.only(top: 4, left: 2),
      child: Row(
        children: [
          SizedBox(
            width: 12,
            height: 12,
            child: CircularProgressIndicator(
                strokeWidth: 1.5, color: AppTheme.accent),
          ),
          SizedBox(width: 8),
          Text(
            'Working on it…',
            style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                fontStyle: FontStyle.italic),
          ),
        ],
      ),
    );
  }
}

/// The bottom reply input. Disabled once the issue is terminal.
class _ReplyBar extends StatelessWidget {
  final TextEditingController controller;
  final bool sending;
  final bool enabled;
  final String hintText;
  final VoidCallback onSend;

  const _ReplyBar({
    required this.controller,
    required this.sending,
    required this.enabled,
    required this.hintText,
    required this.onSend,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        border: Border(top: BorderSide(color: AppTheme.border)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: SafeArea(
        top: false,
        child: enabled
            ? Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: controller,
                      keyboardType: TextInputType.multiline,
                      textInputAction: TextInputAction.send,
                      onSubmitted: (_) => onSend(),
                      minLines: 1,
                      maxLines: 4,
                      style: const TextStyle(color: AppTheme.textPrimary),
                      decoration: InputDecoration(
                        hintText: hintText,
                        hintStyle:
                            const TextStyle(color: AppTheme.textSecondary),
                        border: InputBorder.none,
                        contentPadding: const EdgeInsets.symmetric(
                            horizontal: 12, vertical: 10),
                      ),
                    ),
                  ),
                  IconButton(
                    onPressed: sending ? null : onSend,
                    icon: Icon(
                      Icons.send_rounded,
                      color:
                          sending ? AppTheme.textSecondary : AppTheme.accent,
                    ),
                  ),
                ],
              )
            : const Padding(
                padding: EdgeInsets.symmetric(vertical: 10, horizontal: 8),
                child: Text(
                  'This issue is closed.',
                  style:
                      TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                ),
              ),
      ),
    );
  }
}
