import '../models/library_sort.dart';

/// Label maps belong to one instance and are refreshed with its library.
class LibrarySortLabels {
  final Map<LibrarySortLookup, Map<int, String>> values;
  const LibrarySortLabels([this.values = const {}]);

  String? name(LibrarySortLookup lookup, int? id) => values[lookup]?[id];

  LibraryTagNames? tags(List<int> ids) {
    if (ids.isEmpty) return null;
    final names = <String>[];
    for (final id in ids) {
      final label = name(LibrarySortLookup.tags, id)?.trim().toLowerCase();
      // A deleted or unreadable tag is unknown, not an untagged record.
      if (label == null || label.isEmpty) return null;
      names.add(label);
    }
    return LibraryTagNames(names..sort());
  }
}

class LibraryTagNames implements Comparable<LibraryTagNames> {
  const LibraryTagNames(this.names);
  final List<String> names;
  @override
  int compareTo(LibraryTagNames other) {
    for (var i = 0; i < names.length && i < other.names.length; i++) {
      final order = names[i].compareTo(other.names[i]);
      if (order != 0) return order;
    }
    return names.length.compareTo(other.names.length);
  }
}

/// Provider progress uses the obtainable count; a known zero is complete.
/// Use a tuple rather than adding a count fraction to the percentage.
class LibraryCompletion implements Comparable<LibraryCompletion> {
  const LibraryCompletion(this.have, this.total);
  final int have;
  final int total;
  double get progress => total == 0 ? 1 : have / total;
  @override
  int compareTo(LibraryCompletion other) {
    final order = progress.compareTo(other.progress);
    return order != 0 ? order : total.compareTo(other.total);
  }
}

String librarySortName(String? providerName, String displayName) =>
    providerName == null || providerName.trim().isEmpty ? displayName : providerName;

Comparable? _normalized(Comparable? value) {
  if (value is String) {
    final text = value.trim().toLowerCase();
    return text.isEmpty ? null : text;
  }
  if (value is num && !value.isFinite) return null;
  return value;
}

/// Sort a copy and retain distinct records. Unknown values remain last in both
/// directions; equal values have a deterministic name/id tie-breaker.
List<T> sortLibrary<T>(Iterable<T> items, LibrarySortSelection selection, {
  required Comparable? Function(T, LibrarySortField) value,
  required String Function(T) name,
  required int Function(T) id,
}) {
  final decorated = [for (final item in items) (
    item: item, value: _normalized(value(item, selection.field)),
    name: name(item).trim().toLowerCase(), id: id(item),
  )];
  decorated.sort((a, b) {
    final av = a.value, bv = b.value;
    if (av == null && bv != null) return 1;
    if (av != null && bv == null) return -1;
    if (av != null && bv != null) {
      final order = av.compareTo(bv);
      if (order != 0) return selection.ascending ? order : -order;
    }
    final order = a.name.compareTo(b.name);
    return order != 0 ? order : a.id.compareTo(b.id);
  });
  return decorated.map((entry) => entry.item).toList();
}
