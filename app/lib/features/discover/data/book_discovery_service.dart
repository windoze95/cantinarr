final _workId = RegExp(r'^ol:OL[1-9][0-9]{0,11}W$');

/// Open Library metadata has no Chaptarr identity or availability snapshot.
class DiscoveryBook {
  final String foreignId;
  final String title;
  final List<String> authors;
  final int? year;
  final String description;
  final int? coverId;

  const DiscoveryBook(
      {required this.foreignId,
      required this.title,
      this.authors = const [],
      this.year,
      this.description = '',
      this.coverId});

  static bool validId(String id) => _workId.hasMatch(id);
  String get workId => foreignId.substring(3);
  String get author => authors.join(', ');
  String get searchTerm => [title, author].where((s) => s.isNotEmpty).join(' ');
  String get openLibraryUrl => 'https://openlibrary.org/works/$workId';
  String? get coverUrl =>
      coverId != null && coverId! > 0 && coverId! <= 9007199254740991
          ? 'https://covers.openlibrary.org/b/id/$coverId-M.jpg?default=false'
          : null;
  String detailLocation(String? instanceId) =>
      Uri(path: '/detail/book/$foreignId', queryParameters: {
        if (instanceId != null) 'instance_id': instanceId,
        'source': 'openlibrary'
      }).toString();

  factory DiscoveryBook.fromJson(Map<String, dynamic> json) {
    final id = json['foreign_id'] as String? ?? '';
    final title = json['title'] as String? ?? '';
    if (!validId(id) || title.trim().isEmpty || json['authors'] is! List) {
      throw const FormatException('Invalid book discovery response');
    }
    return DiscoveryBook(
        foreignId: id,
        title: title,
        authors: (json['authors'] as List).cast<String>(),
        year: json['year'] as int?,
        description: json['description'] as String? ?? '',
        coverId: json['cover_id'] as int?);
  }
}
