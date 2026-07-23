import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/dashboard/ui/book_request_wizard.dart';
import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/ui/book_request_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('match step fits a narrow phone at 200 percent text',
      (tester) async {
    tester.view.physicalSize = const Size(320, 700);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
      tester.platformDispatcher.clearTextScaleFactorTestValue();
    });

    final original = _book(
      'The Hobbit',
      'hobbit-original',
      1937,
      'George Allen & Unwin',
    );
    final anniversary = _book(
      'The Hobbit',
      'hobbit-anniversary',
      2012,
      'HarperCollins',
      editionTitle: '75th Anniversary Edition',
    );
    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark,
        home: Scaffold(
          body: Builder(
            builder: (context) => Center(
              child: TextButton(
                onPressed: () => showBookRequestWizard(
                  context,
                  request: const BookRequestPickerContext(
                    foreignId: 'hobbit-original',
                    title: 'The Hobbit',
                    instanceId: 'books',
                    detail: BookRequestStatusDetail(),
                    requestableFormats: [BookRequestFormat.audiobook],
                  ),
                  selectedBook: original,
                  candidates: [
                    BookRequestWizardCandidate(
                      book: original,
                      foreignId: 'hobbit-original',
                      rank: 0,
                      matchEvidence: 'Exact title match',
                    ),
                    BookRequestWizardCandidate(
                      book: anniversary,
                      foreignId: 'hobbit-anniversary',
                      rank: 1,
                      matchEvidence: 'Exact title match',
                    ),
                  ],
                ),
                child: const Text('Open request'),
              ),
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('Open request'));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    await tester.scrollUntilVisible(
      find.text('Which match looks right?'),
      140,
      scrollable: find.byType(Scrollable).last,
    );
    expect(find.text('Which match looks right?'), findsOneWidget);
    await tester.scrollUntilVisible(
      find.textContaining('re-check this exact author and Audiobook version'),
      120,
      scrollable: find.byType(Scrollable).last,
    );
    expect(find.textContaining('re-check this exact author and Audiobook version'),
        findsOneWidget);
    expect(tester.takeException(), isNull);

    await tester.scrollUntilVisible(
      find.textContaining('75th Anniversary Edition'),
      160,
      scrollable: find.byType(Scrollable).last,
    );
    expect(find.textContaining('75th Anniversary Edition'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
      'a sparse first record cannot collapse conflicting publications',
      (tester) async {
    final sparse = ChaptarrBook.fromJson({
      'title': 'The Hobbit',
      'foreignBookId': 'hobbit-sparse',
      'author': {'authorName': 'J. R. R. Tolkien'},
    });
    final original = _book(
      'The Hobbit: Original Edition',
      'hobbit-original',
      1937,
      'George Allen & Unwin',
    );
    final anniversary = _book(
      'The Hobbit: Anniversary Edition',
      'hobbit-anniversary',
      2012,
      'HarperCollins',
    );
    BookRequestTarget? selected;
    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark,
        home: Scaffold(
          body: Builder(
            builder: (context) => Center(
              child: TextButton(
                onPressed: () async {
                  selected = await showBookRequestWizard(
                    context,
                    request: const BookRequestPickerContext(
                      foreignId: 'hobbit-sparse',
                      title: 'The Hobbit',
                      instanceId: 'books',
                      detail: BookRequestStatusDetail(),
                      requestableFormats: [BookRequestFormat.audiobook],
                    ),
                    selectedBook: sparse,
                    candidates: [
                      BookRequestWizardCandidate(
                        book: sparse,
                        foreignId: 'hobbit-sparse',
                        rank: 0,
                        matchEvidence: 'Exact title match',
                      ),
                      BookRequestWizardCandidate(
                        book: original,
                        foreignId: 'hobbit-original',
                        rank: 1,
                        matchEvidence: 'Strong title match',
                      ),
                      BookRequestWizardCandidate(
                        book: anniversary,
                        foreignId: 'hobbit-anniversary',
                        rank: 2,
                        matchEvidence: 'Strong title match',
                      ),
                    ],
                  );
                },
                child: const Text('Open request'),
              ),
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('Open request'));
    await tester.pumpAndSettle();

    expect(selected, isNull);
    expect(find.text('Which match looks right?'), findsOneWidget);
    await tester.scrollUntilVisible(
      find.textContaining('HarperCollins'),
      160,
      scrollable: find.byType(Scrollable).last,
    );
    await tester.drag(
      find.byType(Scrollable).last,
      const Offset(0, -140),
    );
    await tester.pumpAndSettle();
    expect(find.textContaining('HarperCollins'), findsOneWidget);

    await tester.tap(
      find.ancestor(
        of: find.textContaining('HarperCollins'),
        matching: find.byType(InkWell),
      ),
    );
    await tester.pumpAndSettle();

    expect(selected?.foreignId, 'hobbit-anniversary');
    expect(selected?.format, BookRequestFormat.audiobook);
    expect(
      selected?.selection?.audiobook?.foreignEditionId,
      'hobbit-anniversary-2012',
    );
  });

  testWidgets('same work publications return the exact selected edition',
      (tester) async {
    final book = ChaptarrBook.fromJson({
      'title': 'The Hobbit',
      'foreignBookId': 'hobbit',
      'year': 1937,
      'author': {
        'authorName': 'J. R. R. Tolkien',
        'foreignAuthorId': 'tolkien',
      },
      'editions': [
        {
          'foreignEditionId': 'hobbit-audio-original',
          'title': 'Original narration',
          'publisher': 'Recorded Books',
          'format': 'Audiobook',
          'isEbook': false,
          'asin': 'AUDIO-ONE',
        },
        {
          'foreignEditionId': 'hobbit-audio-anniversary',
          'title': 'Anniversary narration',
          'publisher': 'HarperAudio',
          'format': 'Audiobook',
          'isEbook': false,
          'asin': 'AUDIO-TWO',
        },
      ],
    });
    BookRequestTarget? selected;
    await tester.pumpWidget(MaterialApp(
      theme: AppTheme.dark,
      home: Scaffold(
        body: Builder(
          builder: (context) => TextButton(
            onPressed: () async {
              selected = await showBookRequestWizard(
                context,
                request: const BookRequestPickerContext(
                  foreignId: 'hobbit',
                  title: 'The Hobbit',
                  instanceId: 'books',
                  detail: BookRequestStatusDetail(),
                  requestableFormats: [BookRequestFormat.audiobook],
                ),
                selectedBook: book,
                candidates: [
                  BookRequestWizardCandidate(
                    book: book,
                    foreignId: 'hobbit',
                    rank: 0,
                    matchEvidence: 'Exact title match',
                  ),
                ],
              );
            },
            child: const Text('Open request'),
          ),
        ),
      ),
    ));

    await tester.tap(find.text('Open request'));
    await tester.pumpAndSettle();
    expect(find.text('Which match looks right?'), findsOneWidget);
    expect(find.textContaining('Original narration'), findsOneWidget);
    await tester.scrollUntilVisible(
      find.textContaining('Anniversary narration'),
      140,
      scrollable: find.byType(Scrollable).last,
    );
    expect(find.textContaining('Anniversary narration'), findsOneWidget);

    final anniversaryCard = find.ancestor(
      of: find.textContaining('Anniversary narration'),
      matching: find.byType(InkWell),
    );
    await tester.ensureVisible(anniversaryCard);
    await tester.pumpAndSettle();
    await tester.tap(anniversaryCard);
    await tester.pumpAndSettle();

    expect(selected?.foreignId, 'hobbit');
    expect(selected?.selection?.foreignAuthorId, 'tolkien');
    expect(
      selected?.selection?.audiobook?.foreignEditionId,
      'hobbit-audio-anniversary',
    );
    expect(selected?.selection?.audiobook?.asin, 'AUDIO-TWO');
  });

  testWidgets(
      'equivalent hidden ids keep the clicked library row as representative',
      (tester) async {
    final lookup = ChaptarrBook.fromJson({
      'title': 'Flock',
      'foreignBookId': 'lookup-flock',
      'author': {
        'authorName': 'Kate Stewart',
        'foreignAuthorId': 'author-flock',
      },
    });
    final library = ChaptarrBook.fromJson({
      'title': 'Flock',
      'foreignBookId': 'library-flock',
      'author': {'authorName': 'Kate Stewart'},
    });
    BookRequestTarget? selected;
    await tester.pumpWidget(MaterialApp(
      theme: AppTheme.dark,
      home: Scaffold(
        body: Builder(
          builder: (context) => TextButton(
            onPressed: () async {
              selected = await showBookRequestWizard(
                context,
                request: const BookRequestPickerContext(
                  foreignId: 'library-flock',
                  title: 'Flock',
                  instanceId: 'books',
                  detail: BookRequestStatusDetail(),
                  requestableFormats: [BookRequestFormat.ebook],
                ),
                selectedBook: library,
                candidates: [
                  BookRequestWizardCandidate(
                    book: lookup,
                    foreignId: 'lookup-flock',
                    catalogForeignBookId: 'lookup-flock',
                    rank: 0,
                    matchEvidence: 'Exact title and author',
                  ),
                  BookRequestWizardCandidate(
                    book: library,
                    foreignId: 'library-flock',
                    rank: 1,
                    matchEvidence: 'In your library',
                  ),
                ],
              );
            },
            child: const Text('Open request'),
          ),
        ),
      ),
    ));

    await tester.tap(find.text('Open request'));
    await tester.pumpAndSettle();

    expect(find.text('Which match looks right?'), findsNothing);
    expect(selected?.foreignId, 'library-flock');
    expect(selected?.selection?.catalogForeignBookId, isNull);
    expect(selected?.selection?.foreignAuthorId, 'author-flock');
    expect(selected?.selection?.authorName, 'Kate Stewart');
  });

  testWidgets('hidden edition ids never create indistinguishable choices',
      (tester) async {
    final book = ChaptarrBook.fromJson({
      'title': 'The Hobbit',
      'foreignBookId': 'hobbit',
      'author': {'authorName': 'J. R. R. Tolkien'},
      'editions': [
        {
          'foreignEditionId': 'audio-edition-one',
          'format': 'Audiobook',
          'isEbook': false,
        },
        {
          'foreignEditionId': 'audio-edition-two',
          'format': 'Audiobook',
          'isEbook': false,
        },
      ],
    });
    BookRequestTarget? selected;
    await tester.pumpWidget(MaterialApp(
      theme: AppTheme.dark,
      home: Scaffold(
        body: Builder(
          builder: (context) => TextButton(
            onPressed: () async {
              selected = await showBookRequestWizard(
                context,
                request: const BookRequestPickerContext(
                  foreignId: 'hobbit',
                  title: 'The Hobbit',
                  instanceId: 'books',
                  detail: BookRequestStatusDetail(),
                  requestableFormats: [BookRequestFormat.audiobook],
                ),
                selectedBook: book,
                candidates: [
                  BookRequestWizardCandidate(
                    book: book,
                    foreignId: 'hobbit',
                    rank: 0,
                    matchEvidence: 'Exact title and author',
                  ),
                ],
              );
            },
            child: const Text('Open request'),
          ),
        ),
      ),
    ));

    await tester.tap(find.text('Open request'));
    await tester.pumpAndSettle();

    expect(find.text('Which match looks right?'), findsNothing);
    expect(selected?.foreignId, 'hobbit');
    expect(
      selected?.selection?.audiobook?.foreignEditionId,
      'audio-edition-one',
    );
  });

  testWidgets(
      'discovery-only format hints stay requestable without a false exact selection',
      (tester) async {
    final book = ChaptarrBook.fromJson({
      'title': 'Haunting Adeline (Cat and Mouse, #1)',
      'foreignBookId': 'catalog-haunting-adeline',
      'author': {
        'authorName': 'H.D. Carlton',
        'foreignAuthorId': 'author-hd-carlton',
      },
      'editions': [
        {
          'foreignEditionId': 'lookup-only-audio',
          'title': 'Haunting Adeline',
          'isEbook': false,
          'format': null,
        },
      ],
    });
    BookRequestTarget? selected;
    await tester.pumpWidget(MaterialApp(
      theme: AppTheme.dark,
      home: Scaffold(
        body: Builder(
          builder: (context) => TextButton(
            onPressed: () async {
              selected = await showBookRequestWizard(
                context,
                request: const BookRequestPickerContext(
                  foreignId: 'library-haunting-adeline',
                  title: 'Haunting Adeline (Cat and Mouse, #1)',
                  instanceId: 'books',
                  detail: BookRequestStatusDetail(),
                  requestableFormats: [BookRequestFormat.audiobook],
                ),
                selectedBook: book,
                candidates: [
                  BookRequestWizardCandidate(
                    book: book,
                    foreignId: 'library-haunting-adeline',
                    lookupTerm: 'haunting Adelin',
                    catalogForeignBookId: 'catalog-haunting-adeline',
                    rank: 0,
                    matchEvidence: 'Title starts with your search',
                  ),
                ],
              );
            },
            child: const Text('Open request'),
          ),
        ),
      ),
    ));

    await tester.tap(find.text('Open request'));
    await tester.pumpAndSettle();

    expect(find.text('Which match looks right?'), findsNothing);
    expect(selected?.foreignId, 'library-haunting-adeline');
    expect(selected?.selection?.lookupTerm, 'haunting Adelin');
    expect(
      selected?.selection?.catalogForeignBookId,
      'catalog-haunting-adeline',
    );
    expect(selected?.selection?.foreignAuthorId, 'author-hd-carlton');
    // `isEbook: false` is useful discovery metadata, but the server cannot
    // safely validate it as Chaptarr's authoritative publication format.
    expect(selected?.selection?.audiobook, isNull);
  });

  testWidgets('an authorless selected row keeps the query that found it',
      (tester) async {
    final book = ChaptarrBook.fromJson({
      'title': 'The Lost Chronicle',
      'foreignBookId': 'lost-chronicle',
      'mediaType': 'Audiobook',
    });
    BookRequestTarget? selected;
    await tester.pumpWidget(MaterialApp(
      theme: AppTheme.dark,
      home: Scaffold(
        body: Builder(
          builder: (context) => TextButton(
            onPressed: () async {
              selected = await showBookRequestWizard(
                context,
                request: const BookRequestPickerContext(
                  foreignId: 'lost-chronicle',
                  title: 'The Lost Chronicle',
                  instanceId: 'books',
                  detail: BookRequestStatusDetail(),
                  requestableFormats: [BookRequestFormat.audiobook],
                ),
                selectedBook: book,
                candidates: [
                  BookRequestWizardCandidate(
                    book: book,
                    foreignId: 'lost-chronicle',
                    lookupTerm: 'lost chronicl',
                    catalogForeignBookId: 'lost-chronicle',
                    rank: 0,
                    matchEvidence: 'Title starts with your search',
                  ),
                ],
              );
            },
            child: const Text('Open request'),
          ),
        ),
      ),
    ));

    await tester.tap(find.text('Open request'));
    await tester.pumpAndSettle();

    expect(selected?.foreignId, 'lost-chronicle');
    expect(selected?.selection?.lookupTerm, 'lost chronicl');
    expect(selected?.selection?.catalogForeignBookId, 'lost-chronicle');
    expect(selected?.selection?.foreignAuthorId, isNull);
    expect(selected?.selection?.authorName, isNull);
  });
}

ChaptarrBook _book(
  String title,
  String foreignId,
  int year,
  String publisher, {
  String? editionTitle,
}) =>
    ChaptarrBook.fromJson({
      'title': title,
      'foreignBookId': foreignId,
      'year': year,
      'author': {'authorName': 'J. R. R. Tolkien'},
      'editions': [
        {
          'id': year,
          'foreignEditionId': '$foreignId-$year',
          'publisher': publisher,
          'format': 'Audiobook',
          'isEbook': false,
          if (editionTitle != null) 'title': editionTitle,
        },
      ],
    });
