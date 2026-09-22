"""Lightweight structure checks for HowlInstinct GitHub Pages docs."""
from pathlib import Path
import re

DOCS = Path(__file__).resolve().parents[2] / "docs"


def test_required_assets_exist():
    for name in ("index.html", "style.css", "script.js", "favicon.svg", "robots.txt", "sitemap.xml"):
        assert (DOCS / name).is_file(), name


def test_index_has_core_landmarks():
    html = (DOCS / "index.html").read_text()
    for needle in (
        'id="main-content"',
        'id="eco-drawer"',
        "HowlInstinct",
        "Confidence is not permission",
        "howlcipher.github.io/howl/",
        "howlcipher.github.io/howlforge/",
        "howlcipher.github.io/howlplane/",
        "howlcipher.github.io/howlframe/",
        'rel="canonical"',
        'property="og:title"',
        "Skip to main content",
    ):
        assert needle in html, needle


def test_internal_asset_refs_are_relative():
    html = (DOCS / "index.html").read_text()
    assert 'href="style.css"' in html
    assert 'src="script.js"' in html
    assert 'href="favicon.svg"' in html


def test_no_hardcoded_localhost():
    html = (DOCS / "index.html").read_text()
    assert "localhost" not in html
    assert "127.0.0.1" not in html


def test_ecosystem_node_count_matches_footer():
    html = (DOCS / "index.html").read_text()
    m = re.search(r"●\s+(\d+)\s+ECOSYSTEM NODES", html)
    assert m, "drawer footer node count missing"
    assert int(m.group(1)) >= 14
