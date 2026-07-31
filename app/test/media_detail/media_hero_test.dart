import 'package:cantinarr/features/media_detail/ui/media_hero.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  Widget heroPage({
    required String title,
    String? backdropPath,
    String? posterPath,
    ScrollController? controller,
    bool disableAnimations = false,
  }) {
    return MaterialApp(
      home: Scaffold(
        body: CustomScrollView(
          controller: controller,
          slivers: [
            SliverPersistentHeader(
              pinned: true,
              delegate: MediaHeroDelegate(
                title: title,
                posterPath: posterPath,
                backdropPath: backdropPath,
                expandedExtent: 480,
                collapsedExtent: 64,
                topPadding: 0,
                disableAnimations: disableAnimations,
                onBack: () {},
              ),
            ),
            const SliverToBoxAdapter(child: SizedBox(height: 2000)),
          ],
        ),
      ),
    );
  }

  testWidgets('hero shows the title with its semantics contract intact',
      (tester) async {
    final semantics = tester.ensureSemantics();
    await tester.pumpWidget(heroPage(title: 'Project Hail Mary'));
    await tester.pumpAndSettle();

    expect(find.text('Project Hail Mary'), findsOneWidget);
    expect(find.bySemanticsIdentifier('media-detail-title'), findsOneWidget);
    expect(find.text('NOW IN FOCUS'), findsNothing);
    semantics.dispose();
  });

  testWidgets('hero renders a finished composition without any artwork',
      (tester) async {
    await tester.pumpWidget(heroPage(title: 'No Art Title'));
    await tester.pumpAndSettle();

    expect(find.text('No Art Title'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
      'collapsing keeps one semantic title and surfaces the marquee copy',
      (tester) async {
    final semantics = tester.ensureSemantics();
    final controller = ScrollController();
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      heroPage(title: 'The Long Voyage', controller: controller),
    );
    await tester.pumpAndSettle();

    expect(find.text('The Long Voyage'), findsOneWidget);

    // Fully collapsed: the bar title mounts alongside the (faded, but still
    // semantically present) hero title.
    controller.jumpTo(480 - 64);
    await tester.pumpAndSettle();

    expect(find.text('The Long Voyage'), findsNWidgets(2));
    expect(find.bySemanticsIdentifier('media-detail-title'), findsOneWidget);
    expect(find.byTooltip('Back'), findsOneWidget);
    semantics.dispose();
  });

  testWidgets(
      'backdrop hero collapses and restores cleanly (image falls back to the '
      'ambient stage in tests)', (tester) async {
    final controller = ScrollController();
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      heroPage(
        title: 'Artful Title',
        backdropPath: '/backdrop.jpg',
        posterPath: '/poster.jpg',
        controller: controller,
      ),
    );
    await tester.pumpAndSettle();

    controller.jumpTo(480 - 64);
    await tester.pumpAndSettle();
    controller.jumpTo(0);
    await tester.pumpAndSettle();

    expect(find.text('Artful Title'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('reduced motion renders the backdrop hero without animating',
      (tester) async {
    final controller = ScrollController();
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      heroPage(
        title: 'Calm Title',
        backdropPath: '/backdrop.jpg',
        posterPath: '/poster.jpg',
        controller: controller,
        disableAnimations: true,
      ),
    );
    // With every hero flourish disabled the first frame must already be
    // settled — nothing left animating for pumpAndSettle to drain.
    await tester.pumpAndSettle();

    controller.jumpTo(480 - 64);
    await tester.pumpAndSettle();

    expect(find.text('Calm Title'), findsNWidgets(2));
    expect(tester.takeException(), isNull);
  });

  testWidgets('back affordance fires the callback at rest and collapsed',
      (tester) async {
    var popped = 0;
    final controller = ScrollController();
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: CustomScrollView(
            controller: controller,
            slivers: [
              SliverPersistentHeader(
                pinned: true,
                delegate: MediaHeroDelegate(
                  title: 'Poppable',
                  posterPath: null,
                  backdropPath: null,
                  expandedExtent: 480,
                  collapsedExtent: 64,
                  topPadding: 0,
                  disableAnimations: false,
                  onBack: () => popped++,
                ),
              ),
              const SliverToBoxAdapter(child: SizedBox(height: 2000)),
            ],
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.arrow_back));
    expect(popped, 1);

    controller.jumpTo(480 - 64);
    await tester.pumpAndSettle();
    await tester.tap(find.byIcon(Icons.arrow_back));
    expect(popped, 2);
  });
}
