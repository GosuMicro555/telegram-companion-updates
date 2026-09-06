from pathlib import Path


def clean(path: Path) -> None:
    original = path.read_text(encoding="utf-8")
    lines = original.splitlines()
    cleaned = "\n".join(line.rstrip() for line in lines).rstrip() + "\n"
    if cleaned != original:
        path.write_text(cleaned, encoding="utf-8")
    path.chmod(0o644)


def main() -> None:
    for path in Path("frontend/wailsjs").rglob("*"):
        if path.suffix in {".ts", ".js"}:
            clean(path)


if __name__ == "__main__":
    main()
