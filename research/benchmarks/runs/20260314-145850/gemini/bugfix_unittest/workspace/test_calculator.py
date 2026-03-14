import unittest

from calculator import compute_total, normalize_name


class CalculatorTests(unittest.TestCase):
    def test_compute_total_returns_sum(self):
        self.assertEqual(compute_total([2, 3, 5]), 10)

    def test_normalize_name(self):
        self.assertEqual(normalize_name("  aNdRiI "), "Andrii")


if __name__ == "__main__":
    unittest.main()
