import unittest

from check_language_onboarding import fixtures


class OnboardingFixtureTests(unittest.TestCase):
    def test_existing_translations_are_never_replaced(self):
        existing = {"en", "fr", "ar", "fr_CA"}
        probes = list(fixtures(existing))
        self.assertEqual(len(probes), 2)
        for locale, name, tag in probes:
            self.assertNotIn(locale, existing)
            self.assertTrue(name)
            self.assertEqual(tag, locale.replace("_", "-"))
            if "_" in locale:
                self.assertIn(locale.split("_")[0], existing)

    def test_contributions_can_fill_every_probe_slot_without_changing_code(self):
        existing = {"en", "fr"}
        for _ in range(64):
            probes = list(fixtures(existing))
            if not probes:
                break
            for locale, _, _ in probes:
                self.assertNotIn(locale, existing)
                existing.add(locale)
        else:
            self.fail("Fixture selection did not exhaust its finite probe pool")
        self.assertGreater(len(existing), 8)
        self.assertEqual(list(fixtures(existing)), [])


if __name__ == "__main__":
    unittest.main()
