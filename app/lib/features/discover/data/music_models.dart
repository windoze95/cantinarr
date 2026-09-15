/// Public discovery metadata, keyed by a MusicBrainz release-group ID.
/// This model contains no library or request state.
class MusicAlbum {
  final String foreignId;
  final String title;
  final String artist;
  final List<MusicArtist> artists;
  final String releaseDate;
  final String releaseType;
  final String? artwork;
  final String disambiguation;

  const MusicAlbum({
    required this.foreignId,
    required this.title,
    required this.artist,
    this.artists = const [],
    this.releaseDate = '',
    this.releaseType = 'Album',
    this.artwork,
    this.disambiguation = '',
  });

  factory MusicAlbum.fromJson(Map<String, dynamic> json) {
    final id = json['foreign_id'] as String?;
    final title = json['title'] as String?;
    if (id == null || id.isEmpty || title == null || title.isEmpty) {
      throw const FormatException('Invalid music album');
    }
    return MusicAlbum(
      foreignId: id,
      title: title,
      artist: json['artist'] as String? ?? '',
      artists: (json['artists'] as List? ?? const [])
          .map((a) => MusicArtist.fromJson(Map<String, dynamic>.from(a as Map)))
          .toList(),
      releaseDate: json['release_date'] as String? ?? '',
      releaseType: json['release_type'] as String? ?? 'Album',
      artwork: json['artwork'] as String?,
      disambiguation: json['disambiguation'] as String? ?? '',
    );
  }

  String get subtitle => [artist, if (releaseType != 'Album') releaseType]
      .where((part) => part.isNotEmpty)
      .join(' · ');

  String detailLocation(String? instanceId, {String? query}) => Uri(
        path: '/detail/album/$foreignId',
        queryParameters: {
          if (instanceId != null) 'instance_id': instanceId,
          'title': title,
          if (query != null && query.isNotEmpty) 'q': query,
        },
      ).toString();
}

class MusicPage {
  final List<MusicAlbum> results;
  final int page;
  final int? nextPage;
  final String emptyMessage;

  const MusicPage({
    required this.results,
    required this.page,
    this.nextPage,
    this.emptyMessage = '',
  });

  factory MusicPage.fromJson(Map<String, dynamic> json) {
    final page = json['page'] as int?;
    final next = json['next_page'] as int?;
    if (page == null ||
        page < 1 ||
        json['results'] is! List ||
        (next != null && next <= page)) {
      throw const FormatException('Invalid music page');
    }
    return MusicPage(
      page: page,
      nextPage: next,
      results: (json['results'] as List)
          .map((item) => MusicAlbum.fromJson(item as Map<String, dynamic>))
          .toList(growable: false),
      emptyMessage: json['empty_message'] as String? ?? '',
    );
  }
}

class MusicGenre {
  final String id;
  final String name;
  final String tag;
  const MusicGenre({required this.id, required this.name, required this.tag});
  factory MusicGenre.fromJson(Map<String, dynamic> json) => MusicGenre(
        id: json['id'] as String,
        name: json['name'] as String,
        tag: json['tag'] as String,
      );
}

class MusicArtist {
  final String foreignId;
  final String name;
  final String disambiguation;
  final String type;
  final String country;
  const MusicArtist(
      {required this.foreignId,
      required this.name,
      this.disambiguation = '',
      this.type = '',
      this.country = ''});
  factory MusicArtist.fromJson(Map<String, dynamic> json) {
    final id = json['foreign_id'] as String?;
    final name = json['name'] as String?;
    if (id == null || id.isEmpty || name == null || name.isEmpty) {
      throw const FormatException('Invalid music artist');
    }
    return MusicArtist(
        foreignId: id,
        name: name,
        disambiguation: json['disambiguation'] as String? ?? '',
        type: json['type'] as String? ?? '',
        country: json['country'] as String? ?? '');
  }
  String get subtitle =>
      [disambiguation, type, country].where((s) => s.isNotEmpty).join(' · ');
  String detailLocation(String? instanceId, {String? query}) =>
      Uri(path: '/detail/artist/$foreignId', queryParameters: {
        'name': name,
        if (instanceId != null) 'instance_id': instanceId,
        if (query != null && query.isNotEmpty) 'q': query
      }).toString();
}

class MusicArtistPage {
  final List<MusicArtist> results;
  final int page;
  final int? nextPage;
  final String emptyMessage;
  const MusicArtistPage(
      {required this.results,
      required this.page,
      this.nextPage,
      this.emptyMessage = ''});
  factory MusicArtistPage.fromJson(Map<String, dynamic> json) {
    final page = json['page'] as int?;
    final next = json['next_page'] as int?;
    if (page == null ||
        page < 1 ||
        json['results'] is! List ||
        (next != null && next <= page)) {
      throw const FormatException('Invalid artist page');
    }
    return MusicArtistPage(
        results: (json['results'] as List)
            .map((a) =>
                MusicArtist.fromJson(Map<String, dynamic>.from(a as Map)))
            .toList(),
        page: page,
        nextPage: next,
        emptyMessage: json['empty_message'] as String? ?? '');
  }
}
