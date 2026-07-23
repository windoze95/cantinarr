import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/attention_menu_visibility_switch.dart';
import '../data/request_settings_service.dart';
import '../../request/data/request_service.dart';
import '../../request/logic/pending_approvals_provider.dart';

/// Sentinel season-scope value used in the approve dialog meaning "keep the
/// exact seasons the user requested" (no coarse-scope override).
const _keepRequestedScope = '__keep_requested__';

List<String> _bookSelectionSummaryLines(PendingRequestItem item) {
  final selection = item.bookSelection;
  if (!item.isBook || selection == null) return const [];

  final lines = <String>[];
  final authorName = selection.authorName?.trim() ?? '';
  if (authorName.isNotEmpty) {
    lines.add('Author: $authorName');
  } else if (selection.foreignAuthorId?.isNotEmpty ?? false) {
    lines.add('Author: Exact author selected');
  }

  void addPublication(
    String label,
    BookPublicationSelection? publication,
  ) {
    if (publication == null) return;
    final facts = <String>[];
    final editionTitle = publication.editionTitle?.trim() ?? '';
    if (editionTitle.isNotEmpty &&
        _normalizedBookFact(editionTitle) !=
            _normalizedBookFact(item.title)) {
      facts.add(editionTitle);
    }
    final publisher = publication.publisher?.trim() ?? '';
    if (publisher.isNotEmpty) facts.add(publisher);
    final language = publication.language?.trim() ?? '';
    if (language.isNotEmpty) facts.add(language);
    if (publication.year != null) facts.add('${publication.year}');
    if (publication.pageCount != null) {
      facts.add('${publication.pageCount} pages');
    }
    final isbn = publication.isbn13?.trim() ?? '';
    if (isbn.isNotEmpty) facts.add('ISBN $isbn');
    final asin = publication.asin?.trim() ?? '';
    if (asin.isNotEmpty) facts.add('ASIN $asin');

    final visibleFacts = <String>[];
    final seen = <String>{};
    for (final fact in facts) {
      if (seen.add(_normalizedBookFact(fact))) visibleFacts.add(fact);
    }
    final details = visibleFacts.isEmpty
        ? 'Exact edition selected'
        : visibleFacts.join(' · ');
    lines.add('$label: $details');
  }

  final requestedFormat = item.requestedBookFormat;
  if (requestedFormat != BookRequestFormat.audiobook) {
    addPublication('eBook', selection.ebook);
  }
  if (requestedFormat != BookRequestFormat.ebook) {
    addPublication('Audiobook', selection.audiobook);
  }
  return lines;
}

String _normalizedBookFact(String value) =>
    value.trim().toLowerCase().replaceAll(RegExp(r'\s+'), ' ');

/// Admin approval queue: approve (optionally modifying options) or deny
/// pending media requests.
class PendingRequestsScreen extends ConsumerStatefulWidget {
  const PendingRequestsScreen({super.key});

  @override
  ConsumerState<PendingRequestsScreen> createState() =>
      _PendingRequestsScreenState();
}

class _PendingRequestsScreenState extends ConsumerState<PendingRequestsScreen> {
  static const _approvalRecheckDelays = [
    Duration.zero,
    Duration(milliseconds: 500),
    Duration(milliseconds: 1500),
  ];

  late final RequestSettingsService _service;
  List<PendingRequestItem>? _pending;
  AdminRequestSettings? _admin;
  bool _isLoading = true;
  String? _error;
  final Set<int> _decidingIds = {};

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _service = RequestSettingsService(
        backendDio: ref.read(backendClientProvider),
      );
      _load();
    });
  }

  String _friendlyError(Object e) {
    String? raw;
    String? code;
    if (e is DioException) {
      final data = e.response?.data;
      if (data is Map) {
        final responseCode = data['code'];
        if (responseCode is String && responseCode.isNotEmpty) {
          code = responseCode;
        }
        final message = data['error'] ?? data['message'];
        if (message is String && message.isNotEmpty) raw = message;
      } else if (data is String && data.isNotEmpty) {
        raw = data;
      }
    }
    if (code == 'book_match_not_found') {
      return 'Cantinarr couldn’t verify this book match. Ask the requester to try again.';
    }
    if (code == 'book_selection_invalid') {
      return 'This version choice is no longer valid. Deny this request and ask the requester to search for the book again.';
    }
    if (code == 'book_edition_unavailable') {
      return 'The selected edition is no longer available. Deny this request and ask the requester to choose another version.';
    }
    if (code == 'book_format_unresolved') {
      return 'The selected version is not identified as an eBook or audiobook. Deny this request and ask the requester to choose another version.';
    }
    if (code == 'book_multi_work_unsupported') {
      return 'This result contains multiple books. Deny this request and ask the requester to choose one individual title.';
    }
    if (code == 'book_configuration_invalid') {
      return 'Check this book library’s paths and profiles, then try again.';
    }
    if (code == 'book_connection_invalid') {
      return 'Check this book library’s connection, then try again.';
    }
    if (code == 'book_request_rejected' || code == 'book_search_rejected') {
      return raw ?? 'The book library rejected this request. Check its settings and try again.';
    }
    if (code == 'book_request_unverified') {
      return 'Cantinarr could not verify the selected edition, so no download search started. Try approval again; if it keeps failing, check the book library.';
    }
    if (code == 'book_catalog_pending' ||
        code == 'book_outcome_pending' ||
        code == 'book_search_unconfirmed') {
      return 'The book library is still confirming this request. The approval was not sent again; wait a moment and refresh.';
    }
    final lower = raw?.toLowerCase() ?? '';
    final missingBookInstance = lower.contains('book') &&
        lower.contains('instance') &&
        (lower.contains('missing') ||
            lower.contains('does not identify') ||
            lower.contains('no library') ||
            lower.contains('no pinned'));
    if (missingBookInstance) {
      return 'This older request doesn’t identify a book library; deny it and ask the requester to submit it again.';
    }
    if (lower.contains('root folder') ||
        lower.contains('quality profile') ||
        lower.contains('metadata profile') ||
        lower.contains('book configuration')) {
      return 'Check this book library’s paths and profiles, then try again.';
    }
    return 'Something went wrong. Try again.';
  }

  String _approvalMessage(
    PendingRequestItem item,
    BookApprovalResult result,
  ) {
    if (!item.isBook) return 'Approved ${item.title}';
    if (!result.isKnown || result.formats.isEmpty) {
      return 'Approval saved. The remaining queue was refreshed.';
    }
    final approved = <String>[];
    final attention = <String>[];
    for (final format in [
      BookRequestFormat.ebook,
      BookRequestFormat.audiobook,
    ]) {
      final status = result.formats[format];
      if (status == null) continue;
      switch (status) {
        case RequestStatus.available:
        case RequestStatus.downloading:
        case RequestStatus.requested:
        case RequestStatus.partial:
          approved.add('${format.label} approved.');
        case RequestStatus.pending:
          attention.add('${format.label} still needs attention.');
        case RequestStatus.denied:
        case RequestStatus.unavailable:
          attention.add(result.status == RequestStatus.partial
              ? '${format.label} still needs attention.'
              : '${format.label} could not be approved.');
      }
    }
    final message = [...approved, ...attention].join(' ');
    return message.isEmpty
        ? 'Approval saved. The remaining queue was refreshed.'
        : message;
  }

  Future<void> _load() async {
    setState(() {
      _isLoading = _pending == null;
      _error = null;
    });
    try {
      final pending = await _service.listPending();
      final admin = await _service.getAdminSettings();
      if (!mounted) return;
      setState(() {
        _pending = pending;
        _admin = admin;
        _isLoading = false;
      });
      // Keep the drawer + app-icon badges in sync with the queue we just loaded
      // (covers opening the screen and the reload after an approve/deny).
      ref.read(pendingApprovalsProvider.notifier).setCount(pending.length);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _approve(PendingRequestItem item) async {
    if (_decidingIds.contains(item.id)) return;
    final admin = _admin;
    if (admin == null) return;
    final requestedBookFormat = item.requestedBookFormat;
    if (item.isBook && requestedBookFormat == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'This request uses an unsupported book format and cannot be approved.',
          ),
        ),
      );
      return;
    }
    final profiles = item.isBook
        ? const <QualityProfile>[]
        : (item.isTv ? admin.sonarrProfiles : admin.radarrProfiles);

    // An explicit per-season request stores a JSON list in seasonScope, which
    // isn't one of the coarse dropdown values — represent it as a "keep
    // requested" option so the dropdown doesn't break and the admin can leave
    // the chosen seasons untouched.
    final isExplicit = SeasonScope.isExplicitList(item.seasonScope);
    String chosenScope = isExplicit
        ? _keepRequestedScope
        : (item.seasonScope.isNotEmpty ? item.seasonScope : SeasonScope.all);
    int? chosenProfile;

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) {
        return StatefulBuilder(
          builder: (dialogContext, setDialogState) {
            return AlertDialog(
              backgroundColor: AppTheme.surface,
              title: Text(
                'Approve ${item.title}',
                style: const TextStyle(color: AppTheme.textPrimary),
              ),
              content: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    item.requestedByLabel,
                    style: const TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 13,
                    ),
                  ),
                  const SizedBox(height: 16),
                  if (item.isTv) ...[
                    const Text(
                      'Season scope',
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13),
                    ),
                    const SizedBox(height: 4),
                    DropdownButtonFormField<String>(
                      initialValue: chosenScope,
                      dropdownColor: AppTheme.surfaceVariant,
                      style: const TextStyle(color: AppTheme.textPrimary),
                      decoration: const InputDecoration(
                        border: OutlineInputBorder(),
                        isDense: true,
                      ),
                      items: [
                        if (isExplicit)
                          DropdownMenuItem<String>(
                            value: _keepRequestedScope,
                            child: Text(
                                'Keep requested (${SeasonScope.describe(item.seasonScope)})'),
                          ),
                        ...SeasonScope.choices.map((c) =>
                            DropdownMenuItem<String>(
                                value: c.value, child: Text(c.label))),
                      ],
                      onChanged: (v) {
                        if (v != null) {
                          setDialogState(() => chosenScope = v);
                        }
                      },
                    ),
                    const SizedBox(height: 16),
                  ],
                  if (item.isBook) ...[
                    const Text(
                      'Requested format',
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      requestedBookFormat!.label,
                      style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    if (item.instanceName.isNotEmpty) ...[
                      const SizedBox(height: 14),
                      Text(
                        'Library: ${item.instanceName}',
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 14,
                        ),
                      ),
                    ],
                    if (_bookSelectionSummaryLines(item).isNotEmpty) ...[
                      const SizedBox(height: 14),
                      _BookSelectionSummary(item: item, framed: true),
                    ],
                  ] else ...[
                    const Text(
                      'Quality profile',
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13),
                    ),
                    const SizedBox(height: 4),
                    DropdownButtonFormField<int?>(
                      initialValue: chosenProfile,
                      dropdownColor: AppTheme.surfaceVariant,
                      style: const TextStyle(color: AppTheme.textPrimary),
                      decoration: const InputDecoration(
                        border: OutlineInputBorder(),
                        isDense: true,
                      ),
                      items: [
                        const DropdownMenuItem<int?>(
                          value: null,
                          child: Text('Default'),
                        ),
                        ...profiles.map((p) => DropdownMenuItem<int?>(
                              value: p.id,
                              child: Text(p.name),
                            )),
                      ],
                      onChanged: (v) => setDialogState(() => chosenProfile = v),
                    ),
                  ],
                ],
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.of(dialogContext).pop(false),
                  child: const Text('Cancel'),
                ),
                ElevatedButton(
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppTheme.available,
                    foregroundColor: AppTheme.background,
                  ),
                  onPressed: () => Navigator.of(dialogContext).pop(true),
                  child: const Text('Approve'),
                ),
              ],
            );
          },
        );
      },
    );

    if (confirmed != true) return;
    if (!mounted) return;
    setState(() => _decidingIds.add(item.id));
    try {
      final result = await _service.approve(
        item.id,
        // The "keep requested" sentinel sends no override, so the server keeps
        // the explicit season list the user picked.
        seasonScope: item.isTv
            ? (chosenScope == _keepRequestedScope ? null : chosenScope)
            : null,
        qualityProfileId: item.isBook ? null : chosenProfile,
      );
      if (!mounted) return;
      await _load();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_approvalMessage(item, result))),
      );
    } catch (e) {
      if (!mounted) return;
      final completed = item.isBook
          ? await _reconcileBookApprovalOutcome(item.id)
          : false;
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(completed
              ? 'Approval completed. The remaining queue was refreshed.'
              : _friendlyError(e)),
        ),
      );
    } finally {
      if (mounted) setState(() => _decidingIds.remove(item.id));
    }
  }

  Future<bool> _reconcileBookApprovalOutcome(int requestId) async {
    for (final delay in _approvalRecheckDelays) {
      if (delay != Duration.zero) await Future<void>.delayed(delay);
      if (!mounted) return false;
      try {
        final pending = await _service.listPending();
        if (!mounted) return false;
        setState(() {
          _pending = pending;
          _isLoading = false;
          _error = null;
        });
        ref.read(pendingApprovalsProvider.notifier).setCount(pending.length);
        if (!pending.any((request) => request.id == requestId)) return true;
      } catch (_) {
        // The original approval must never be replayed. A later read attempt
        // may still establish the outcome; otherwise retain the honest error.
      }
    }
    return false;
  }

  Future<void> _deny(PendingRequestItem item) async {
    final controller = TextEditingController();
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) {
        return AlertDialog(
          backgroundColor: AppTheme.surface,
          title: Text(
            'Deny ${item.title}',
            style: const TextStyle(color: AppTheme.textPrimary),
          ),
          content: TextField(
            controller: controller,
            autofocus: true,
            style: const TextStyle(color: AppTheme.textPrimary),
            decoration: const InputDecoration(
              labelText: 'Reason (optional)',
              labelStyle: TextStyle(color: AppTheme.textSecondary),
              border: OutlineInputBorder(),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(false),
              child: const Text('Cancel'),
            ),
            ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: AppTheme.error,
                foregroundColor: AppTheme.background,
              ),
              onPressed: () => Navigator.of(dialogContext).pop(true),
              child: const Text('Deny'),
            ),
          ],
        );
      },
    );

    final reason = controller.text.trim();
    controller.dispose();
    if (confirmed != true) return;
    if (!mounted) return;
    try {
      await _service.deny(item.id, reason: reason.isEmpty ? null : reason);
      if (!mounted) return;
      await _load();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Denied ${item.title}')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Approvals')),
      body: CenteredContent(
        child: Column(
          children: [
            Expanded(
              child: _isLoading
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : _error != null && _pending == null
                      ? Center(
                          child: Padding(
                            padding: const EdgeInsets.all(24),
                            child: Column(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Text(_error!,
                                    style:
                                        const TextStyle(color: AppTheme.error),
                                    textAlign: TextAlign.center),
                                const SizedBox(height: 12),
                                ElevatedButton(
                                    onPressed: _load,
                                    child: const Text('Retry')),
                              ],
                            ),
                          ),
                        )
                      : RefreshIndicator(
                          color: AppTheme.accent,
                          onRefresh: _load,
                          child: (_pending?.isEmpty ?? true)
                              ? ListView(
                                  physics:
                                      const AlwaysScrollableScrollPhysics(),
                                  children: const [
                                    SizedBox(height: 120),
                                    Center(
                                      child: Text(
                                        'No pending requests.',
                                        style: TextStyle(
                                            color: AppTheme.textSecondary),
                                      ),
                                    ),
                                  ],
                                )
                              : ListView.separated(
                                  physics:
                                      const AlwaysScrollableScrollPhysics(),
                                  padding:
                                      const EdgeInsets.symmetric(vertical: 8),
                                  itemCount: _pending!.length,
                                  separatorBuilder: (_, __) => const Divider(
                                      color: AppTheme.border, height: 1),
                                  itemBuilder: (context, index) {
                                    final item = _pending![index];
                                    return _PendingTile(
                                      item: item,
                                      deciding: _decidingIds.contains(item.id),
                                      onApprove: () => _approve(item),
                                      onDeny: () => _deny(item),
                                    );
                                  },
                                ),
                        ),
            ),
            const Divider(color: AppTheme.border, height: 1),
            const SafeArea(
              top: false,
              child: AttentionMenuVisibilitySwitch(
                item: AttentionMenuItem.approvals,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _PendingTile extends StatelessWidget {
  final PendingRequestItem item;
  final bool deciding;
  final VoidCallback onApprove;
  final VoidCallback onDeny;

  const _PendingTile({
    required this.item,
    this.deciding = false,
    required this.onApprove,
    required this.onDeny,
  });

  @override
  Widget build(BuildContext context) {
    final showScope = item.isTv && item.seasonScope.isNotEmpty;
    final showBookFormat = item.isBook && item.bookFormat.isNotEmpty;
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      title: Text(
        item.title,
        style: const TextStyle(
          color: AppTheme.textPrimary,
          fontWeight: FontWeight.bold,
        ),
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 2),
          Text(
            item.requestedByLabel,
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
          const SizedBox(height: 6),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              _chip(item.mediaLabel),
              if (showScope) _chip(SeasonScope.describe(item.seasonScope)),
              if (showBookFormat)
                _chip(item.requestedBookFormat?.label ?? 'Unsupported format'),
              if (item.isBook && item.instanceName.isNotEmpty)
                _chip('Library: ${item.instanceName}'),
            ],
          ),
          if (_bookSelectionSummaryLines(item).isNotEmpty) ...[
            const SizedBox(height: 8),
            _BookSelectionSummary(item: item),
          ],
        ],
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (deciding)
            const Padding(
              padding: EdgeInsets.all(12),
              child: SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
            )
          else
            IconButton(
              icon: const Icon(Icons.check_circle_outline),
              color: AppTheme.available,
              tooltip: item.isBook && item.requestedBookFormat == null
                  ? 'Unsupported book format'
                  : 'Approve',
              onPressed: item.isBook && item.requestedBookFormat == null
                  ? null
                  : onApprove,
            ),
          IconButton(
            icon: const Icon(Icons.cancel_outlined),
            color: AppTheme.error,
            tooltip: 'Deny',
            onPressed: deciding ? null : onDeny,
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

class _BookSelectionSummary extends StatelessWidget {
  final PendingRequestItem item;
  final bool framed;

  const _BookSelectionSummary({
    required this.item,
    this.framed = false,
  });

  @override
  Widget build(BuildContext context) {
    final lines = _bookSelectionSummaryLines(item);
    final summary = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (framed) ...[
          const Text(
            'Selected publication',
            style: TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 12,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 6),
        ],
        for (var index = 0; index < lines.length; index++) ...[
          if (index > 0) const SizedBox(height: 3),
          Text(
            lines[index],
            style: TextStyle(
              color: framed ? AppTheme.textPrimary : AppTheme.textSecondary,
              fontSize: framed ? 13 : 12,
              height: 1.25,
            ),
          ),
        ],
      ],
    );
    if (!framed) return summary;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: AppTheme.border),
      ),
      child: summary,
    );
  }
}
