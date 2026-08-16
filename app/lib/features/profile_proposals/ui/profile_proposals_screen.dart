import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/attention_menu_visibility_switch.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/profile_proposal_models.dart';
import '../data/profile_proposals_service.dart';
import '../logic/profile_proposals_provider.dart';

/// Admin approval queue for quality-profile changes parked by external MCP
/// agents. The server holds the plan and re-validates live settings on
/// approval; this screen only shows the server-rendered diff and sends the
/// decision.
class ProfileProposalsScreen extends ConsumerStatefulWidget {
  const ProfileProposalsScreen({super.key});

  @override
  ConsumerState<ProfileProposalsScreen> createState() =>
      _ProfileProposalsScreenState();
}

class _ProfileProposalsScreenState extends ConsumerState<ProfileProposalsScreen>
    with WidgetsBindingObserver {
  List<ProfileChangeProposal>? _proposals;
  bool _loading = true;
  String? _error;
  int _loadEpoch = 0;
  final Set<int> _busyIds = {};
  Timer? _poll;

  static const _pollInterval = Duration(seconds: 30);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
    _poll = Timer.periodic(_pollInterval, (_) => _load());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _poll?.cancel();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _load();
  }

  Future<void> _load() async {
    final epoch = ++_loadEpoch;
    try {
      final proposals = await ref
          .read(profileProposalsServiceProvider)
          .listProposals(status: 'all');
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _proposals = proposals;
        _loading = false;
        _error = null;
      });
      // Seed the attention-menu badge from the list this screen already
      // holds, so a decision made here drains the drawer immediately.
      ref
          .read(pendingProfileProposalsProvider.notifier)
          .setCount(proposals.where((p) => p.isPending).length);
    } catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _loading = false;
        _error = _friendlyError(e);
      });
    }
  }

  String _friendlyError(Object error) {
    final match = RegExp(r'"error":"([^"]+)"').firstMatch(error.toString());
    return match?.group(1) ?? 'Something went wrong.';
  }

  Future<void> _decide(
    ProfileChangeProposal proposal, {
    required bool approve,
  }) async {
    if (_busyIds.contains(proposal.id)) return;
    String? note;
    if (!approve) {
      note = await _askRejectNote();
      if (note == null || !mounted) return; // dialog dismissed
    } else {
      final confirmed = await _confirmApproval(proposal);
      if (confirmed != true || !mounted) return;
    }
    setState(() => _busyIds.add(proposal.id));
    final service = ref.read(profileProposalsServiceProvider);
    String? message;
    try {
      if (approve) {
        final updated = await service.approveProposal(proposal.id);
        message = updated.status == 'applied'
            ? 'Applied and verified on ${proposal.instanceName.isEmpty ? proposal.service : proposal.instanceName}.'
            : updated.statusLabel;
      } else {
        await service.rejectProposal(proposal.id, note: note);
        message = 'Proposal rejected. Nothing was changed.';
      }
    } catch (e) {
      message = _friendlyError(e);
    }
    if (!mounted) return;
    setState(() => _busyIds.remove(proposal.id));
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(message)));
    await _load();
  }

  Future<bool?> _confirmApproval(ProfileChangeProposal proposal) {
    return showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Apply this profile change?'),
        content: Text(
          'Cantinarr will re-check that "${proposal.profileName}" still matches '
          'what the assistant saw, apply the change, verify the stored result, '
          'and record it in Configuration history.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Approve'),
          ),
        ],
      ),
    );
  }

  Future<String?> _askRejectNote() async {
    final controller = TextEditingController();
    final note = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Reject this proposal?'),
        content: TextField(
          controller: controller,
          decoration: const InputDecoration(
            labelText: 'Reason (optional)',
          ),
          maxLines: 2,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(backgroundColor: AppTheme.danger),
            onPressed: () => Navigator.of(context).pop(controller.text),
            child: const Text('Reject'),
          ),
        ],
      ),
    );
    // Dispose after the route animation finishes so the exiting dialog never
    // touches a disposed controller.
    WidgetsBinding.instance.addPostFrameCallback((_) => controller.dispose());
    return note;
  }

  @override
  Widget build(BuildContext context) {
    final proposals = _proposals;
    final pending =
        proposals?.where((p) => p.isPending).toList(growable: false) ?? const [];
    final decided =
        proposals?.where((p) => !p.isPending).toList(growable: false) ??
            const [];
    // The visibility switch sits outside the scroll region so it survives
    // the loading, error, and empty states — a hidden drawer entry's only
    // in-place recovery is this control (Settings has the other copy).
    return Scaffold(
      appBar: AppBar(title: const Text('Profile Change Approvals')),
      body: Column(
        children: [
          Expanded(child: _buildBody(proposals, pending, decided)),
          const Divider(color: AppTheme.border, height: 1),
          const SafeArea(
            top: false,
            child: AttentionMenuVisibilitySwitch(
              item: AttentionMenuItem.profileApprovals,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildBody(
    List<ProfileChangeProposal>? proposals,
    List<ProfileChangeProposal> pending,
    List<ProfileChangeProposal> decided,
  ) {
    return _loading
        ? const Center(
            child: CircularProgressIndicator(color: AppTheme.accent),
          )
        : proposals == null
            ? FullScreenError(
                title: 'Approvals unavailable',
                message: _error ?? 'Could not load profile change proposals.',
                onRetry: _load,
              )
            : RefreshIndicator(
                  color: AppTheme.accent,
                  onRefresh: _load,
                  child: LayoutBuilder(builder: (context, constraints) {
                    final hPad = AppBreakpoints.centeredContentPadding(
                      constraints.maxWidth,
                    );
                    return ListView(
                      physics: const AlwaysScrollableScrollPhysics(),
                      padding: EdgeInsets.fromLTRB(hPad, 12, hPad, 32),
                      children: [
                        if (_error != null)
                          Padding(
                            padding: const EdgeInsets.only(bottom: 12),
                            child: ErrorBanner(message: _error!),
                          ),
                        if (pending.isEmpty)
                          const _EmptyState()
                        else
                          for (final proposal in pending)
                            _ProposalCard(
                              key: ValueKey(
                                  '${proposal.id}-${proposal.status}'),
                              proposal: proposal,
                              busy: _busyIds.contains(proposal.id),
                              onApprove: () =>
                                  _decide(proposal, approve: true),
                              onReject: () =>
                                  _decide(proposal, approve: false),
                            ),
                        if (decided.isNotEmpty) ...[
                          const Padding(
                            padding: EdgeInsets.fromLTRB(4, 20, 4, 8),
                            child: Text(
                              'Recent',
                              style: TextStyle(
                                color: AppTheme.textSecondary,
                                fontSize: 13,
                                fontWeight: FontWeight.w700,
                              ),
                            ),
                          ),
                          for (final proposal in decided)
                            _DecidedTile(proposal: proposal),
                        ],
                      ],
                    );
                  }),
                );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState();

  @override
  Widget build(BuildContext context) {
    return const Padding(
      padding: EdgeInsets.symmetric(vertical: 40),
      child: Column(
        children: [
          Icon(Icons.fact_check_outlined, size: 40, color: AppTheme.textMuted),
          SizedBox(height: 12),
          Text(
            'Nothing awaiting approval',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 15),
          ),
          SizedBox(height: 6),
          Text(
            'When an external assistant proposes a quality-profile change, '
            'it appears here for your review before anything is written.',
            textAlign: TextAlign.center,
            style: TextStyle(color: AppTheme.textMuted, fontSize: 12.5),
          ),
        ],
      ),
    );
  }
}

class _ProposalCard extends StatelessWidget {
  final ProfileChangeProposal proposal;
  final bool busy;
  final VoidCallback onApprove;
  final VoidCallback onReject;

  const _ProposalCard({
    super.key,
    required this.proposal,
    required this.busy,
    required this.onApprove,
    required this.onReject,
  });

  String get _target {
    final instance = proposal.instanceName.isEmpty
        ? proposal.service
        : proposal.instanceName;
    return '$instance · profile ${proposal.profileId}';
  }

  String get _meta {
    final parts = [
      if (proposal.proposedByName.isNotEmpty)
        'Proposed by ${proposal.proposedByName}',
      if (proposal.sourceClient.isNotEmpty) proposal.sourceClient,
    ];
    return parts.join(' · ');
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant.withValues(alpha: 0.82),
        borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            proposal.profileName.isEmpty
                ? 'Quality profile'
                : proposal.profileName,
            style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w700),
          ),
          const SizedBox(height: 2),
          Text(
            _target,
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12.5),
          ),
          if (_meta.isNotEmpty) ...[
            const SizedBox(height: 2),
            Text(
              _meta,
              style: const TextStyle(color: AppTheme.textMuted, fontSize: 12),
            ),
          ],
          const SizedBox(height: 12),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: AppTheme.surface,
              borderRadius: BorderRadius.circular(10),
              border: Border.all(color: AppTheme.border),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                for (final line in proposal.diff)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 1.5),
                    child: SelectableText(
                      line,
                      style: const TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 12,
                      ),
                    ),
                  ),
              ],
            ),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              const Spacer(),
              OutlinedButton(
                onPressed: busy ? null : onReject,
                child: const Text('Reject'),
              ),
              const SizedBox(width: 10),
              ElevatedButton.icon(
                onPressed: busy ? null : onApprove,
                icon: busy
                    ? const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.check, size: 16),
                label: const Text('Approve'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _DecidedTile extends StatelessWidget {
  final ProfileChangeProposal proposal;

  const _DecidedTile({required this.proposal});

  @override
  Widget build(BuildContext context) {
    final detail = [
      proposal.statusLabel,
      if (proposal.decidedByName.isNotEmpty) 'by ${proposal.decidedByName}',
      if (proposal.rejectNote.isNotEmpty) '— ${proposal.rejectNote}',
      if (proposal.resultText.isNotEmpty && proposal.rejectNote.isEmpty)
        '— ${proposal.resultText}',
    ].join(' ');
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant.withValues(alpha: 0.5),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '${proposal.profileName} · ${proposal.instanceName.isEmpty ? proposal.service : proposal.instanceName}',
            style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 2),
          Text(
            detail,
            style: const TextStyle(color: AppTheme.textMuted, fontSize: 12),
          ),
        ],
      ),
    );
  }
}
