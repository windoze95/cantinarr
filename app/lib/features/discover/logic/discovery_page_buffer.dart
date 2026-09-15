/// One speculative provider page. It never advances the visible cursor or
/// follows another page by itself. A foreground load shares the same future.
class DiscoveryPageBuffer<T> {
  final Future<T> Function(int page) fetch;
  int? _page;
  Future<T>? _pending;
  T? value;

  DiscoveryPageBuffer(this.fetch);

  Future<Object?> prefetch(int? page) async {
    if (page == null || _page == page) return null;
    clear();
    _page = page;
    final pending = _pending = Future.sync(() => fetch(page));
    try {
      final result = await pending;
      if (identical(_pending, pending)) value = result;
    } catch (error) {
      // Observe speculative failures now, but surface them only when this
      // page is needed. Access failures are handled immediately by callers.
      if (identical(_pending, pending)) return error;
    }
    return null;
  }

  Future<T> take(int page) {
    final pending = _page == page ? _pending : null;
    clear();
    return pending ?? Future.sync(() => fetch(page));
  }

  void clear() {
    _page = null;
    _pending = null;
    value = null;
  }
}
