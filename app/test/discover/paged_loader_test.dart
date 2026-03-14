import 'package:flutter_test/flutter_test.dart';
import 'package:cantinarr/features/discover/logic/paged_loader.dart';

void main() {
  group('PagedLoader', () {
    late PagedLoader loader;

    setUp(() {
      loader = PagedLoader();
    });

    test('initial state', () {
      expect(loader.page, 1);
      expect(loader.totalPages, 1);
      expect(loader.isLoading, false);
      expect(loader.hasMore, true);
    });

    test('beginLoading returns true on first call', () {
      expect(loader.beginLoading(), true);
      expect(loader.isLoading, true);
    });

    test('beginLoading returns false when already loading', () {
      loader.beginLoading();
      expect(loader.beginLoading(), false);
    });

    test('endLoading increments page', () {
      loader.beginLoading();
      loader.endLoading(5);
      expect(loader.page, 2);
      expect(loader.totalPages, 5);
      expect(loader.isLoading, false);
    });

    test('hasMore is false when page exceeds totalPages', () {
      loader.beginLoading();
      loader.endLoading(1); // totalPages=1, page is now 2
      expect(loader.hasMore, false);
    });

    test('cancelLoading allows retry', () {
      loader.beginLoading();
      loader.cancelLoading();
      expect(loader.isLoading, false);
      expect(loader.beginLoading(), true);
    });

    test('reset returns to initial state', () {
      loader.beginLoading();
      loader.endLoading(5);
      loader.reset();
      expect(loader.page, 1);
      expect(loader.totalPages, 1);
      expect(loader.isLoading, false);
    });
  });
}
