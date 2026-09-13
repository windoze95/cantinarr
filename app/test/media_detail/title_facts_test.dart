import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/logic/title_facts.dart';
import 'package:flutter_test/flutter_test.dart';

/// The Details lines of a title page: which facts earn a line, in what
/// order, and how money, runtime, and crew names read.
void main() {
  group('movieFacts', () {
    test('lists the known lines in reading order', () {
      final facts = movieFacts(const MovieDetail(
        id: 603,
        title: 'The Matrix',
        status: 'Released',
        runtime: 136,
        budget: 63000000,
        revenue: 463517383,
        countries: ['United States of America'],
        credits: TitleCredits(crew: [
          CrewMember(
              id: 1,
              name: 'Lana Wachowski',
              job: 'Director',
              department: 'Directing'),
          CrewMember(
              id: 2,
              name: 'Lilly Wachowski',
              job: 'Director',
              department: 'Directing'),
          CrewMember(
              id: 1,
              name: 'Lana Wachowski',
              job: 'Screenplay',
              department: 'Writing'),
          CrewMember(
              id: 2,
              name: 'Lilly Wachowski',
              job: 'Screenplay',
              department: 'Writing'),
          CrewMember(
              id: 3,
              name: 'Joel Silver',
              job: 'Producer',
              department: 'Production'),
        ]),
      ));

      expect(facts, const [
        TitleFact('Directed by', 'Lana Wachowski, Lilly Wachowski'),
        TitleFact('Written by', 'Lana Wachowski, Lilly Wachowski'),
        TitleFact('Country', 'United States of America'),
        TitleFact('Runtime', '2h 16m'),
        TitleFact('Budget', '\$63M'),
        TitleFact('Revenue', '\$464M'),
      ]);
    });

    test('an unreleased status leads, and Released is not a line', () {
      final facts = movieFacts(const MovieDetail(
        id: 1,
        title: 'Soon',
        status: 'Post Production',
        runtime: 0,
        budget: 0,
        revenue: 12,
      ));
      expect(facts, const [TitleFact('Status', 'Post Production')]);
    });

    test('writers are unique, capped at three, and only writing jobs', () {
      final crew = [
        for (var i = 0; i < 5; i++)
          CrewMember(
            id: i,
            name: 'Writer $i',
            job: i.isEven ? 'Writer' : 'Story',
            department: 'Writing',
          ),
        const CrewMember(
            id: 0, name: 'Writer 0', job: 'Screenplay', department: 'Writing'),
        const CrewMember(
            id: 9, name: 'Novelist', job: 'Novel', department: 'Writing'),
      ];
      final facts = movieFacts(
          MovieDetail(id: 1, title: 'x', credits: TitleCredits(crew: crew)));
      expect(facts, const [
        TitleFact('Written by', 'Writer 0, Writer 1, Writer 2'),
      ]);
    });

    test('nothing known means no lines', () {
      expect(movieFacts(const MovieDetail(id: 1, title: 'x')), isEmpty);
    });
  });

  group('tvFacts', () {
    test('status, creators, network, and country', () {
      final facts = tvFacts(const TVDetail(
        id: 1396,
        name: 'Breaking Bad',
        status: 'Ended',
        createdBy: [
          CrewMember(
              id: 1,
              name: 'Vince Gilligan',
              job: 'Creator',
              department: 'Creators'),
        ],
        networks: [TaggedId(id: 174, name: 'AMC')],
        countries: ['United States of America'],
      ));
      expect(facts, const [
        TitleFact('Status', 'Ended'),
        TitleFact('Created by', 'Vince Gilligan'),
        TitleFact('Network', 'AMC'),
        TitleFact('Country', 'United States of America'),
      ]);
    });

    test('a show TMDB knows nothing about has no lines', () {
      expect(tvFacts(const TVDetail(id: 1, name: 'x', status: ' ')), isEmpty);
    });

    test('premiere retains the original full date before and on the day', () {
      const detail = TVDetail(id: 1, name: 'x', firstAirDate: '2026-10-07');
      for (final now in [DateTime(2026, 9, 12), DateTime(2026, 10, 7, 23, 59)]) {
        expect(tvFacts(detail, now: now),
            const [TitleFact('Premiere', 'Oct 7, 2026')]);
      }
      expect(tvFacts(detail, now: DateTime(2026, 10, 8)),
          const [TitleFact('First aired', 'Oct 7, 2026')]);
    });

    test('the original first-air date remains when a returning season is due', () {
      const detail = TVDetail(id: 1, name: 'x', firstAirDate: '2024-10-02',
        seasons: [Season(id: 2, seasonNumber: 2, airDate: '2026-10-07')],
      );
      expect(tvFacts(detail, now: DateTime(2026, 9, 12)),
          const [TitleFact('First aired', 'Oct 2, 2024')]);
    });

    test('Season 1 supplies the original date when TMDB omits first_air_date', () {
      const detail = TVDetail(id: 1, name: 'x', seasons: [
        Season(id: 0, seasonNumber: 0, airDate: '2024-01-01'),
        Season(id: 1, seasonNumber: 1, airDate: '2026-10-07'),
      ]);
      expect(tvFacts(detail, now: DateTime(2026, 9, 12)),
          const [TitleFact('Premiere', 'Oct 7, 2026')]);
    });

    test('invalid and incomplete premiere dates never become facts', () {
      for (final date in [null, '', '2026', '2026-02-30']) {
        expect(tvFacts(TVDetail(id: 1, name: 'x', firstAirDate: date)), isEmpty);
      }
    });
  });

  group('formatMoney', () {
    test('compacts to thousands, millions, and billions', () {
      expect(formatMoney(63000000), '\$63M');
      expect(formatMoney(463517383), '\$464M');
      expect(formatMoney(9500000), '\$9.5M');
      expect(formatMoney(1234000000), '\$1.2B');
      expect(formatMoney(1000000000), '\$1B');
      expect(formatMoney(850000), '\$850K');
      expect(formatMoney(999700000), '\$1B');
    });

    test('unknown and junk values are not facts', () {
      expect(formatMoney(null), isNull);
      expect(formatMoney(0), isNull);
      expect(formatMoney(12), isNull);
    });
  });

  group('formatRuntime', () {
    test('hours and minutes', () {
      expect(formatRuntime(136), '2h 16m');
      expect(formatRuntime(120), '2h');
      expect(formatRuntime(58), '58m');
      expect(formatRuntime(0), isNull);
      expect(formatRuntime(null), isNull);
    });
  });

  group('crewSections', () {
    test(
        'orders departments for reading, keeps TMDB order within one, and '
        'merges a person\'s jobs there', () {
      final sections = crewSections(const [
        CrewMember(id: 3, name: 'Editor', job: 'Editor', department: 'Editing'),
        CrewMember(
            id: 4, name: 'Someone', job: 'Thanks', department: 'Zebra Unit'),
        CrewMember(
            id: 2, name: 'Joel Silver', job: 'Producer', department: 'Production'),
        CrewMember(
            id: 1,
            name: 'Lana Wachowski',
            job: 'Director',
            department: 'Directing'),
        CrewMember(
            id: 2,
            name: 'Joel Silver',
            job: 'Executive Producer',
            department: 'Production'),
        CrewMember(
            id: 5, name: 'Vince Gilligan', job: 'Creator', department: 'Creators'),
        CrewMember(id: 6, name: 'Nobody', job: 'Helper', department: ''),
      ]);

      expect(sections.map((s) => s.department), [
        'Creators',
        'Directing',
        'Production',
        'Editing',
        'Crew',
        'Zebra Unit',
      ]);
      expect(sections[2].members.single.job, 'Producer, Executive Producer');
      expect(sections.last.members.single.name, 'Someone');
    });
  });
}
