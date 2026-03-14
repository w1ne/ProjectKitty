def normalize_name(name: str) -> str:
    return name.strip().title()


def compute_total(items):
    total = 0
    for item in items:
        total += item
    return item
