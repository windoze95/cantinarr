import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../../chaptarr/data/chaptarr_models.dart';
import '../../request/data/book_ownership.dart';
import '../../request/data/request_service.dart';
import '../../request/ui/book_request_button.dart';
import '../logic/book_search_ranking.dart';

/// One actionable search record available to the requester wizard.
class BookRequestWizardCandidate {
  final ChaptarrBook book;
  final String foreignId;
  final BookOwnership? ownership;
  final bool ownershipStatusKnown;
  final BookRequestStatusDetail? statusDetail;
  final int rank;
  final String matchEvidence;
  final bool recommendationEligible;

  const BookRequestWizardCandidate({
    required this.book,
    required this.foreignId,
    this.ownership,
    this.ownershipStatusKnown = true,
    this.statusDetail,
    required this.rank,
    required this.matchEvidence,
    this.recommendationEligible = true,
  });
}

/// Runs a format-first request flow. A second step appears only when Chaptarr
/// exposes meaningfully different editions of the selected work. Equivalent
/// backend records are resolved deterministically and never presented as a
/// choice made up of internal record numbers.
Future<BookRequestTarget?> showBookRequestWizard(
  BuildContext context, {
  required BookRequestPickerContext request,
  required ChaptarrBook selectedBook,
  required List<BookRequestWizardCandidate> candidates,
}) async {
  final related = candidates
      .where((candidate) =>
          candidate.foreignId.trim().isNotEmpty &&
          sameBookWork(selectedBook, candidate.book))
      .toList()
    ..sort((a, b) => a.rank.compareTo(b.rank));
  final usable = related.isEmpty
      ? [
          BookRequestWizardCandidate(
            book: selectedBook,
            foreignId: request.foreignId,
            statusDetail: request.detail,
            rank: 0,
            matchEvidence: 'Selected title',
          ),
        ]
      : related;
  final formats = _formatChoices(request.requestableFormats);
  if (formats.isEmpty) return null;

  if (formats.length == 1) {
    final choices = _editionChoices(usable, formats.single, request);
    if (choices.length == 1) {
      return _targetFor(
        choices.single.candidate,
        formats.single,
        choices.single.selection,
      );
    }
  }

  return showModalBottomSheet<BookRequestTarget>(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (_) => _BookRequestWizardSheet(
      request: request,
      selectedBook: selectedBook,
      candidates: usable,
      formats: formats,
    ),
  );
}

List<BookRequestFormat> _formatChoices(
  List<BookRequestFormat> requestable,
) {
  final choices = <BookRequestFormat>[
    if (requestable.contains(BookRequestFormat.ebook))
      BookRequestFormat.ebook,
    if (requestable.contains(BookRequestFormat.audiobook))
      BookRequestFormat.audiobook,
  ];
  if (choices.length == 2) choices.add(BookRequestFormat.both);
  return choices;
}

class _EditionChoice {
  final BookRequestWizardCandidate candidate;
  final BookRequestSelection selection;
  final List<String> publicationFacts;
  final int equivalentMatchCount;

  const _EditionChoice({
    required this.candidate,
    required this.selection,
    required this.publicationFacts,
    required this.equivalentMatchCount,
  });

  bool get hasEquivalentMatches => equivalentMatchCount > 1;
}

class _PublicationVariant {
  final BookPublicationSelection? ebook;
  final BookPublicationSelection? audiobook;
  final List<String> facts;

  const _PublicationVariant({
    this.ebook,
    this.audiobook,
    this.facts = const [],
  });
}

List<_EditionChoice> _editionChoices(
  List<BookRequestWizardCandidate> candidates,
  BookRequestFormat format,
  BookRequestPickerContext request,
) {
  final requestable = candidates
      .where((candidate) => _candidateSupports(candidate, format, request))
      .toList();
  if (requestable.isEmpty) {
    final selected = candidates
        .where((candidate) => candidate.foreignId == request.foreignId)
        .toList();
    if (selected.isNotEmpty) requestable.add(selected.first);
  }

  final expanded = <_EditionChoice>[];
  for (final candidate in requestable) {
    final author = candidate.book.author;
    for (final variant in _publicationVariants(candidate.book, format)) {
      expanded.add(_EditionChoice(
        candidate: candidate,
        selection: BookRequestSelection(
          foreignAuthorId: author?.foreignAuthorId?.trim(),
          authorName: author?.authorName.trim(),
          ebook: variant.ebook,
          audiobook: variant.audiobook,
        ),
        publicationFacts: variant.facts,
        equivalentMatchCount: 1,
      ));
    }
  }

  final groups = <List<_EditionChoice>>[];
  for (final choice in expanded) {
    List<_EditionChoice>? matchingGroup;
    for (final group in groups) {
      // Never expose two clickable cards that serialize to the same server
      // selection or whose user-visible publication facts are identical.
      // Search rows stay distinct, but the request sheet must not ask someone
      // to choose between hidden catalog ids. The whole-group check keeps the
      // presentation grouping transitive and stable.
      if (group.every((existing) =>
          sameBookWork(existing.candidate.book, choice.candidate.book) &&
          _sameSelection(
            existing.selection,
            choice.selection,
            existing.publicationFacts,
            choice.publicationFacts,
          ))) {
        matchingGroup = group;
        break;
      }
    }
    if (matchingGroup == null) {
      groups.add([choice]);
    } else {
      matchingGroup.add(choice);
    }
  }
  return [for (final group in groups) _groupedChoice(group, request)];
}

_EditionChoice _groupedChoice(
  List<_EditionChoice> group,
  BookRequestPickerContext request,
) {
  final representative = group.firstWhere(
    (choice) => choice.candidate.foreignId == request.foreignId,
    orElse: () => group.first,
  );
  final strongestAuthor = group.firstWhere(
    (choice) =>
        _normalizedIdentity(choice.selection.foreignAuthorId).isNotEmpty,
    orElse: () => representative,
  );
  return _EditionChoice(
    // Keep the exact result the requester clicked, including that result's
    // publication selector. Equivalent rows may only enrich a missing author
    // id; they never substitute another row's edition id.
    candidate: representative.candidate,
    selection: BookRequestSelection(
      foreignAuthorId: strongestAuthor.selection.foreignAuthorId,
      authorName: representative.selection.authorName ??
          strongestAuthor.selection.authorName,
      ebook: representative.selection.ebook,
      audiobook: representative.selection.audiobook,
    ),
    publicationFacts: representative.publicationFacts,
    equivalentMatchCount: group.length,
  );
}

List<_PublicationVariant> _publicationVariants(
  ChaptarrBook book,
  BookRequestFormat format,
) {
  final ebooks = _publicationChoices(book, BookFormat.ebook);
  final audiobooks = _publicationChoices(book, BookFormat.audiobook);
  if (format == BookRequestFormat.ebook) {
    return ebooks
        .map((publication) => _PublicationVariant(
              ebook: publication.$1,
              facts: publication.$2,
            ))
        .toList();
  }
  if (format == BookRequestFormat.audiobook) {
    return audiobooks
        .map((publication) => _PublicationVariant(
              audiobook: publication.$1,
              facts: publication.$2,
            ))
        .toList();
  }

  final variants = <_PublicationVariant>[];
  for (final ebook in ebooks) {
    for (final audiobook in audiobooks) {
      variants.add(_PublicationVariant(
        ebook: ebook.$1,
        audiobook: audiobook.$1,
        facts: [
          if (ebook.$2.isNotEmpty) 'eBook: ${ebook.$2.join(' · ')}',
          if (audiobook.$2.isNotEmpty)
            'Audiobook: ${audiobook.$2.join(' · ')}',
        ],
      ));
    }
  }
  return variants;
}

List<(BookPublicationSelection?, List<String>)> _publicationChoices(
  ChaptarrBook book,
  BookFormat format,
) {
  final editions = book.editions
      .where((edition) => edition.bookFormat == format)
      .toList();
  final choices = <(BookPublicationSelection?, List<String>)>[];
  final seen = <String>{};
  for (final edition in editions) {
    final selection = _publicationSelection(book, edition);
    final key = _publicationKey(selection);
    if (key.isEmpty || !seen.add(key)) continue;
    choices.add((selection, _publicationFacts(book, edition)));
  }
  if (choices.isNotEmpty) return choices;

  final bookFormat = book.format;
  final foreignEditionId = book.foreignEditionId?.trim() ?? '';
  if (bookFormat == format && foreignEditionId.isNotEmpty) {
    final selection = BookPublicationSelection(
      foreignEditionId: foreignEditionId,
      editionTitle: book.title,
      year: book.releaseDate?.year,
      pageCount: book.displayPageCount,
    );
    return [(selection, _publicationFactsFromSelection(book, selection))];
  }
  // The lookup exposed no stable publication identity for this format. Keep
  // one work-level option; the server will still revalidate the author/work
  // and choose deterministically instead of accepting a made-up client ID.
  return const [(null, <String>[])];
}

BookPublicationSelection _publicationSelection(
  ChaptarrBook book,
  ChaptarrEdition edition,
) =>
    BookPublicationSelection(
      foreignEditionId: edition.foreignEditionId?.trim(),
      isbn13: edition.isbn13?.trim(),
      asin: edition.asin?.trim(),
      editionTitle: edition.title?.trim(),
      publisher: edition.publisher?.trim(),
      language: edition.language?.trim(),
      year: book.releaseDate?.year,
      pageCount:
          edition.pageCount > 0 ? edition.pageCount : book.displayPageCount,
    );

List<String> _publicationFacts(
  ChaptarrBook book,
  ChaptarrEdition edition,
) =>
    _publicationFactsFromSelection(book, _publicationSelection(book, edition));

List<String> _publicationFactsFromSelection(
  ChaptarrBook book,
  BookPublicationSelection selection,
) =>
    [
      if ((selection.publisher ?? '').isNotEmpty) selection.publisher!,
      if ((selection.editionTitle ?? '').isNotEmpty &&
          selection.editionTitle!.toLowerCase() != book.title.toLowerCase())
        selection.editionTitle!,
      if ((selection.language ?? '').isNotEmpty) selection.language!,
      if (selection.year != null && selection.year! > 0) '${selection.year}',
      if (selection.pageCount != null && selection.pageCount! > 0)
        '${selection.pageCount} pages',
      if ((selection.isbn13 ?? '').isNotEmpty) 'ISBN ${selection.isbn13}',
      if ((selection.asin ?? '').isNotEmpty) 'ASIN ${selection.asin}',
    ];

bool _sameSelection(
  BookRequestSelection a,
  BookRequestSelection b,
  List<String> aVisibleFacts,
  List<String> bVisibleFacts,
) =>
    _sameAuthorSelection(a, b) &&
    ((_publicationKey(a.ebook) == _publicationKey(b.ebook) &&
            _publicationKey(a.audiobook) ==
                _publicationKey(b.audiobook)) ||
        _sameVisibleFacts(aVisibleFacts, bVisibleFacts));

bool _sameVisibleFacts(List<String> a, List<String> b) {
  if (a.length != b.length) return false;
  for (var index = 0; index < a.length; index++) {
    if (_normalizedIdentity(a[index]) != _normalizedIdentity(b[index])) {
      return false;
    }
  }
  return true;
}

bool _sameAuthorSelection(BookRequestSelection a, BookRequestSelection b) {
  final aId = _normalizedIdentity(a.foreignAuthorId);
  final bId = _normalizedIdentity(b.foreignAuthorId);
  if (aId.isNotEmpty && bId.isNotEmpty) return aId == bId;

  final aName = _normalizedIdentity(a.authorName);
  final bName = _normalizedIdentity(b.authorName);
  if (aName.isNotEmpty && bName.isNotEmpty) return aName == bName;

  // Preserve the old work-level grouping for two genuinely authorless rows,
  // but never equate a known author with a blank one.
  return aId.isEmpty && bId.isEmpty && aName.isEmpty && bName.isEmpty;
}

String _publicationKey(BookPublicationSelection? selection) {
  if (selection == null) return '';
  final foreign = _normalizedIdentity(selection.foreignEditionId);
  if (foreign.isNotEmpty) return 'foreign:$foreign';
  final isbn = (selection.isbn13 ?? '')
      .replaceAll(RegExp(r'[^0-9Xx]'), '')
      .toLowerCase();
  if (isbn.isNotEmpty) return 'isbn:$isbn';
  final asin = _normalizedIdentity(selection.asin);
  if (asin.isNotEmpty) return 'asin:$asin';
  final description = [
    _normalizedIdentity(selection.editionTitle),
    _normalizedIdentity(selection.publisher),
    _normalizedIdentity(selection.language),
    selection.year ?? 0,
    selection.pageCount ?? 0,
  ];
  if (description.take(3).every((value) => value == '') &&
      description.skip(3).every((value) => value == 0)) {
    return '';
  }
  return 'description:${description.join('|')}';
}

String _normalizedIdentity(String? value) =>
    (value ?? '').trim().toLowerCase().replaceAll(RegExp(r'\s+'), ' ');

bool _candidateSupports(
  BookRequestWizardCandidate candidate,
  BookRequestFormat format,
  BookRequestPickerContext request,
) {
  final detail = candidate.foreignId == request.foreignId
      ? request.detail
      : candidate.statusDetail;
  if (detail != null) {
    return detail
        .withOwnership(
          candidate.ownership,
          ownershipStatusKnown: candidate.ownershipStatusKnown,
        )
        .isRequestable(format);
  }
  if (!candidate.ownershipStatusKnown) return false;
  final ownership = candidate.ownership;
  if (ownership == null) return true;
  final ebookRequestable = !ownership.ebook.owned;
  final audiobookRequestable = !ownership.audiobook.owned;
  return switch (format) {
    BookRequestFormat.ebook => ebookRequestable,
    BookRequestFormat.audiobook => audiobookRequestable,
    BookRequestFormat.both => ebookRequestable && audiobookRequestable,
  };
}

BookRequestTarget _targetFor(
  BookRequestWizardCandidate candidate,
  BookRequestFormat format,
  BookRequestSelection? selection,
) =>
    BookRequestTarget(
      foreignId: candidate.foreignId,
      title: candidate.book.title,
      format: format,
      selection: selection,
    );

class _BookRequestWizardSheet extends StatefulWidget {
  final BookRequestPickerContext request;
  final ChaptarrBook selectedBook;
  final List<BookRequestWizardCandidate> candidates;
  final List<BookRequestFormat> formats;

  const _BookRequestWizardSheet({
    required this.request,
    required this.selectedBook,
    required this.candidates,
    required this.formats,
  });

  @override
  State<_BookRequestWizardSheet> createState() =>
      _BookRequestWizardSheetState();
}

class _BookRequestWizardSheetState extends State<_BookRequestWizardSheet> {
  BookRequestFormat? _selectedFormat;
  ScrollController? _scrollController;

  @override
  void initState() {
    super.initState();
    if (widget.formats.length == 1) _selectedFormat = widget.formats.single;
  }

  List<_EditionChoice> get _choices => _selectedFormat == null
      ? const []
      : _editionChoices(
          widget.candidates,
          _selectedFormat!,
          widget.request,
        );

  void _selectFormat(BookRequestFormat format) {
    final choices = _editionChoices(
      widget.candidates,
      format,
      widget.request,
    );
    if (choices.length == 1) {
      Navigator.of(context).pop(
        _targetFor(
          choices.single.candidate,
          format,
          choices.single.selection,
        ),
      );
      return;
    }
    setState(() => _selectedFormat = format);
    _returnToTop();
  }

  void _backToFormat() {
    setState(() => _selectedFormat = null);
    _returnToTop();
  }

  void _returnToTop() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final controller = _scrollController;
      if (!mounted || controller == null || !controller.hasClients) return;
      controller.jumpTo(controller.position.minScrollExtent);
    });
  }

  @override
  Widget build(BuildContext context) {
    final choosingEdition = _selectedFormat != null;
    return DraggableScrollableSheet(
      initialChildSize: 0.72,
      minChildSize: 0.45,
      maxChildSize: 0.94,
      expand: false,
      builder: (context, scrollController) {
        _scrollController = scrollController;
        return Align(
          alignment: Alignment.bottomCenter,
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 640),
            child: Material(
              color: AppTheme.surface,
              borderRadius:
                  const BorderRadius.vertical(top: Radius.circular(20)),
              clipBehavior: Clip.antiAlias,
              child: ListView(
                controller: scrollController,
                padding: EdgeInsets.fromLTRB(
                  20,
                  12,
                  20,
                  MediaQuery.paddingOf(context).bottom + 20,
                ),
                children: [
                  _SheetHeader(
                    title: widget.selectedBook.title,
                    choosingEdition: choosingEdition,
                    selectedFormat: _selectedFormat,
                    onBack: choosingEdition && widget.formats.length > 1
                        ? _backToFormat
                        : null,
                  ),
                  const SizedBox(height: 20),
                  if (!choosingEdition)
                    ...widget.formats.map((format) => Padding(
                          padding: const EdgeInsets.only(bottom: 10),
                          child: _FormatCard(
                            format: format,
                            onTap: () => _selectFormat(format),
                          ),
                        ))
                  else ...[
                    Text(
                      'Which match looks right?',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                    const SizedBox(height: 6),
                    Text(
                      'Choose the closest catalog match. Cantinarr will re-check this exact author and ${_selectedFormat!.label} version before it starts the search.',
                      style: Theme.of(context).textTheme.bodyMedium,
                    ),
                    const SizedBox(height: 16),
                    for (var index = 0; index < _choices.length; index++)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 12),
                        child: _EditionCard(
                          choice: _choices[index],
                          recommended: index == 0 &&
                              _choices[index]
                                  .candidate
                                  .recommendationEligible,
                          onTap: () => Navigator.of(context).pop(
                            _targetFor(
                              _choices[index].candidate,
                              _selectedFormat!,
                              _choices[index].selection,
                            ),
                          ),
                        ),
                      ),
                  ],
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}

class _SheetHeader extends StatelessWidget {
  final String title;
  final bool choosingEdition;
  final BookRequestFormat? selectedFormat;
  final VoidCallback? onBack;

  const _SheetHeader({
    required this.title,
    required this.choosingEdition,
    required this.selectedFormat,
    this.onBack,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Center(
          child: Container(
            width: 40,
            height: 4,
            decoration: BoxDecoration(
              color: AppTheme.textSecondary,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            if (onBack != null)
              IconButton(
                tooltip: 'Back to format',
                onPressed: onBack,
                icon: const Icon(Icons.arrow_back),
              )
            else
              const Icon(Icons.auto_stories, color: AppTheme.accent),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                title,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.titleLarge,
              ),
            ),
            IconButton(
              tooltip: 'Close',
              onPressed: () => Navigator.of(context).pop(),
              icon: const Icon(Icons.close),
            ),
          ],
        ),
        const SizedBox(height: 10),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            _StepPill(
              label: selectedFormat?.label ?? 'Choose format',
              active: !choosingEdition,
              complete: choosingEdition,
            ),
            _StepPill(
              label: 'Confirm match',
              active: choosingEdition,
              complete: false,
            ),
          ],
        ),
        const SizedBox(height: 12),
        Text(
          choosingEdition
              ? 'Choose between catalog matches only when their publication details show a real difference.'
              : 'Choose how you want to enjoy this book.',
          style: Theme.of(context).textTheme.bodyMedium,
        ),
      ],
    );
  }
}

class _StepPill extends StatelessWidget {
  final String label;
  final bool active;
  final bool complete;

  const _StepPill({
    required this.label,
    required this.active,
    required this.complete,
  });

  @override
  Widget build(BuildContext context) {
    final color = active || complete ? AppTheme.accent : AppTheme.textMuted;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: active
            ? AppTheme.accent.withValues(alpha: 0.14)
            : AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(AppTheme.radiusPill),
        border: Border.all(
          color: active ? AppTheme.accent : AppTheme.border,
        ),
      ),
      child: Text.rich(
        TextSpan(
          children: [
            WidgetSpan(
              alignment: PlaceholderAlignment.middle,
              child: Icon(
                complete ? Icons.check : Icons.circle,
                size: complete ? 14 : 8,
                color: color,
              ),
            ),
            const TextSpan(text: '  '),
            TextSpan(text: label),
          ],
        ),
        softWrap: true,
        style: TextStyle(
          color: color,
          fontSize: 12,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

class _FormatCard extends StatelessWidget {
  final BookRequestFormat format;
  final VoidCallback onTap;

  const _FormatCard({required this.format, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final (icon, description) = switch (format) {
      BookRequestFormat.ebook => (
          Icons.menu_book,
          'Read on an e-reader, tablet, or phone',
        ),
      BookRequestFormat.audiobook => (
          Icons.headphones,
          'Listen to the narrated edition',
        ),
      BookRequestFormat.both => (
          Icons.library_books,
          'Request reading and listening editions',
        ),
    };
    return Material(
      color: AppTheme.surfaceVariant,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppTheme.radiusMd),
        side: const BorderSide(color: AppTheme.border),
      ),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 42,
                height: 42,
                decoration: BoxDecoration(
                  color: AppTheme.accent.withValues(alpha: 0.14),
                  borderRadius: BorderRadius.circular(AppTheme.radiusSm),
                ),
                child: Icon(icon, color: AppTheme.accent),
              ),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      format.label,
                      style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(height: 3),
                    Text(
                      description,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 10),
              const Padding(
                padding: EdgeInsets.only(top: 9),
                child: Icon(
                  Icons.arrow_forward,
                  color: AppTheme.textSecondary,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _EditionCard extends StatelessWidget {
  final _EditionChoice choice;
  final bool recommended;
  final VoidCallback onTap;

  const _EditionCard({
    required this.choice,
    required this.recommended,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final candidate = choice.candidate;
    final author = candidate.book.author?.authorName.trim() ?? '';
    final facts = choice.publicationFacts.isNotEmpty
        ? choice.publicationFacts
        : bookEditionFacts(candidate.book);
    final metadata = <String>[
      if (author.isNotEmpty) author,
      ...facts,
    ].join(' · ');
    return Semantics(
      button: true,
      label: recommended
          ? 'Recommended book match, ${candidate.book.title}'
          : 'Book match, ${candidate.book.title}',
      child: Material(
        color: AppTheme.surfaceVariant,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(AppTheme.radiusMd),
          side: BorderSide(
            color: recommended ? AppTheme.accent : AppTheme.border,
            width: recommended ? 1.5 : 1,
          ),
        ),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          child: Container(
            padding: const EdgeInsets.fromLTRB(16, 14, 14, 14),
            decoration: BoxDecoration(
              border: Border(
                left: BorderSide(
                  color: recommended
                      ? AppTheme.accent
                      : AppTheme.borderStrong,
                  width: 4,
                ),
              ),
            ),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      if (recommended) ...[
                        const _RecommendedBadge(),
                        const SizedBox(height: 8),
                      ],
                      Text(
                        candidate.book.title,
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      if (metadata.isNotEmpty) ...[
                        const SizedBox(height: 5),
                        Text(
                          metadata,
                          style: Theme.of(context).textTheme.bodySmall,
                        ),
                      ],
                      const SizedBox(height: 8),
                      Text(
                        candidate.matchEvidence,
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      if (choice.hasEquivalentMatches) ...[
                        const SizedBox(height: 6),
                        Text(
                          '${choice.equivalentMatchCount} equivalent catalog matches grouped',
                          style: const TextStyle(
                            color: AppTheme.warning,
                            fontSize: 12,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
                const SizedBox(width: 12),
                const Padding(
                  padding: EdgeInsets.only(top: 4),
                  child: Icon(
                    Icons.arrow_forward,
                    color: AppTheme.textSecondary,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _RecommendedBadge extends StatelessWidget {
  const _RecommendedBadge();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: AppTheme.accent.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(AppTheme.radiusPill),
      ),
      child: const Text(
        'Recommended',
        style: TextStyle(
          color: AppTheme.accent,
          fontSize: 11,
          fontWeight: FontWeight.w800,
          letterSpacing: 0.2,
        ),
      ),
    );
  }
}
