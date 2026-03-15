import 'package:flutter/foundation.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';

enum PersonCreditFilter { all, movies, tvShows }

enum PersonCreditSort { date, title, rating }

class PersonDetailState {
  final bool isLoading;
  final String? error;
  final PersonDetail? person;
  final List<PersonCredit> allCredits;
  final PersonCreditFilter filter;
  final PersonCreditSort sort;
  final bool sortAscending;

  const PersonDetailState({
    this.isLoading = false,
    this.error,
    this.person,
    this.allCredits = const [],
    this.filter = PersonCreditFilter.all,
    this.sort = PersonCreditSort.date,
    this.sortAscending = false,
  });

  List<PersonCredit> get filteredCredits {
    var list = allCredits;
    switch (filter) {
      case PersonCreditFilter.movies:
        list = list.where((c) => c.mediaType == 'movie').toList();
      case PersonCreditFilter.tvShows:
        list = list.where((c) => c.mediaType == 'tv').toList();
      case PersonCreditFilter.all:
        break;
    }

    list = List.of(list);
    switch (sort) {
      case PersonCreditSort.date:
        list.sort((a, b) {
          final ad = a.releaseDate ?? '';
          final bd = b.releaseDate ?? '';
          return sortAscending ? ad.compareTo(bd) : bd.compareTo(ad);
        });
      case PersonCreditSort.title:
        list.sort((a, b) => sortAscending
            ? a.title.compareTo(b.title)
            : b.title.compareTo(a.title));
      case PersonCreditSort.rating:
        list.sort((a, b) {
          final ar = a.voteAverage ?? 0;
          final br = b.voteAverage ?? 0;
          return sortAscending ? ar.compareTo(br) : br.compareTo(ar);
        });
    }
    return list;
  }

  Map<String, List<PersonCredit>> get creditsByYear {
    final map = <String, List<PersonCredit>>{};
    for (final c in filteredCredits) {
      final y = c.year ?? 'TBA';
      map.putIfAbsent(y, () => []).add(c);
    }
    return map;
  }

  PersonDetailState copyWith({
    bool? isLoading,
    String? error,
    PersonDetail? person,
    List<PersonCredit>? allCredits,
    PersonCreditFilter? filter,
    PersonCreditSort? sort,
    bool? sortAscending,
  }) =>
      PersonDetailState(
        isLoading: isLoading ?? this.isLoading,
        error: error,
        person: person ?? this.person,
        allCredits: allCredits ?? this.allCredits,
        filter: filter ?? this.filter,
        sort: sort ?? this.sort,
        sortAscending: sortAscending ?? this.sortAscending,
      );
}

class PersonDetailNotifier extends ChangeNotifier {
  final DiscoverApiService _api;
  final int _id;

  PersonDetailState _state = const PersonDetailState();
  PersonDetailState get state => _state;
  set state(PersonDetailState value) {
    _state = value;
    notifyListeners();
  }

  PersonDetailNotifier({
    required DiscoverApiService api,
    required int id,
  })  : _api = api,
        _id = id;

  Future<void> load() async {
    state = state.copyWith(isLoading: true);
    try {
      final results = await Future.wait([
        _api.personDetail(_id),
        _api.personCredits(_id),
      ]);
      state = state.copyWith(
        isLoading: false,
        person: results[0] as PersonDetail,
        allCredits: results[1] as List<PersonCredit>,
      );
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        error: 'Failed to load person: $e',
      );
    }
  }

  void setFilter(PersonCreditFilter filter) {
    state = state.copyWith(filter: filter);
  }

  void setSort(PersonCreditSort sort) {
    if (state.sort == sort) {
      state = state.copyWith(sortAscending: !state.sortAscending);
    } else {
      state = state.copyWith(sort: sort, sortAscending: false);
    }
  }
}
