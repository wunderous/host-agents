#!/usr/bin/env python3
"""Validate generated routes, metadata, links, search, OpenAPI, and basic a11y."""
from __future__ import annotations

import json
import struct
import sys
import xml.etree.ElementTree as ET
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
SITE = ROOT / "site" / "public"
ORIGIN = "https://www.opute.io"


def fail(message: str) -> None:
    print(f"generated site validation failed: {message}", file=sys.stderr)
    raise SystemExit(1)


class PageParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.title_parts: list[str] = []
        self.title_open = False
        self.h1_count = 0
        self.main_count = 0
        self.skip_count = 0
        self.lang: str | None = None
        self.meta: dict[str, str] = {}
        self.canonical: str | None = None
        self.og_image: str | None = None
        self.og_image_type: str | None = None
        self.twitter_image: str | None = None
        self.ids: set[str] = set()
        self.links: list[str] = []
        self.mermaid_count = 0
        self.diagram_alt_count = 0
        self.summary_count = 0
        self.details_count = 0
        self.inputs: list[dict[str, str]] = []
        self.images_missing_alt = 0
        self._capture: str | None = None
        self._capture_parts: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = {key: value or "" for key, value in attrs}
        if tag == "html":
            self.lang = values.get("lang")
        if tag == "title":
            self.title_open = True
            self._capture = "title"
        if tag == "h1":
            self.h1_count += 1
        if tag == "main":
            self.main_count += 1
        if tag == "a" and "skip-link" in values.get("class", "").split():
            self.skip_count += 1
            if values.get("href") != "#main-content":
                fail("skip link does not target #main-content")
        if "id" in values:
            if values["id"] in self.ids:
                fail(f"duplicate id in rendered page: {values['id']}")
            self.ids.add(values["id"])
        for name in ("href", "src"):
            if values.get(name):
                self.links.append(values[name])
        if tag == "meta":
            key = values.get("name") or values.get("property")
            if key:
                self.meta[key.lower()] = values.get("content", "")
                if key.lower() == "og:image":
                    self.og_image = values.get("content")
                if key.lower() == "og:image:type":
                    self.og_image_type = values.get("content")
                if key.lower() == "twitter:image":
                    self.twitter_image = values.get("content")
        if tag == "link" and "canonical" in values.get("rel", "").split():
            self.canonical = values.get("href")
        if tag == "pre" and "mermaid" in values.get("class", "").split():
            self.mermaid_count += 1
        if tag == "p" and "diagram-alt" in values.get("class", "").split():
            self.diagram_alt_count += 1
        if tag == "details":
            self.details_count += 1
        if tag == "summary":
            self.summary_count += 1
        if tag == "input":
            self.inputs.append(values)
        if tag == "img" and "alt" not in values:
            self.images_missing_alt += 1

    def handle_endtag(self, tag: str) -> None:
        if tag == "title":
            self.title_open = False
            self._capture = None

    def handle_data(self, data: str) -> None:
        if self.title_open:
            self.title_parts.append(data)


def route_for_file(path: Path) -> str:
    relative = path.relative_to(SITE)
    if relative.as_posix() == "index.html":
        return "/"
    if relative.name == "index.html":
        return "/" + relative.parent.as_posix().rstrip("/") + "/"
    return "/" + relative.as_posix()


def html_parser(path: Path) -> PageParser:
    parser = PageParser()
    parser.feed(path.read_text(encoding="utf-8"))
    parser.close()
    return parser


def resolve_local_target(page: Path, raw: str) -> tuple[Path | None, str]:
    parsed = urlsplit(raw)
    if parsed.scheme or parsed.netloc:
        return None, ""
    path = unquote(parsed.path)
    if not path:
        target = page
    elif path.startswith("/"):
        target = SITE / path.lstrip("/")
    else:
        target = page.parent / path
    if path.endswith("/") or not target.suffix:
        target = target / "index.html"
    resolved = target.resolve()
    try:
        resolved.relative_to(SITE.resolve())
    except ValueError:
        fail(f"local link resolves outside the published site: {raw}")
    return resolved, unquote(parsed.fragment)


def main() -> None:
    if not SITE.is_dir():
        fail("site/public is missing")
    html_files = sorted(SITE.rglob("*.html"))
    if not html_files:
        fail("no rendered HTML pages found")

    parsers: dict[Path, PageParser] = {}
    titles: dict[str, Path] = {}
    canonicals: dict[str, Path] = {}
    expected_routes: set[str] = set()
    link_errors: list[str] = []
    for page in html_files:
        parser = html_parser(page)
        parsers[page] = parser
        route = route_for_file(page)
        expected_routes.add(route)
        title = "".join(parser.title_parts).strip()
        if not title:
            fail(f"{route} has no title")
        if title in titles:
            fail(f"title is duplicated on {route} and {route_for_file(titles[title])}")
        titles[title] = page
        if parser.h1_count != 1:
            fail(f"{route} has {parser.h1_count} h1 elements; expected one")
        if parser.main_count != 1:
            fail(f"{route} has {parser.main_count} main landmarks; expected one")
        if parser.skip_count != 1:
            fail(f"{route} has no unique skip link")
        if parser.lang != "en":
            fail(f"{route} must declare English document language")
        description = parser.meta.get("description", "").strip()
        if not description:
            fail(f"{route} has no meta description")
        canonical = parser.canonical or ""
        expected_canonical = ORIGIN + route
        if canonical != expected_canonical:
            fail(f"{route} canonical is {canonical!r}, expected {expected_canonical!r}")
        if canonical in canonicals:
            fail(f"canonical URL is duplicated: {canonical}")
        canonicals[canonical] = page
        if parser.images_missing_alt:
            fail(f"{route} has an image without an alt attribute")
        if parser.mermaid_count != parser.diagram_alt_count:
            fail(f"{route} has {parser.mermaid_count} Mermaid diagrams and {parser.diagram_alt_count} text alternatives")
        if parser.details_count != parser.summary_count:
            fail(f"{route} has details disclosures without matching summaries")
        for item in parser.inputs:
            if not item.get("aria-label") and not item.get("aria-labelledby"):
                fail(f"{route} has an input without an accessible name")
        if not parser.og_image or parser.og_image_type != "image/png":
            fail(f"{route} has no PNG Open Graph preview image")
        image_url = urlsplit(parser.og_image)
        if image_url.scheme or image_url.netloc:
            if image_url.scheme != "https" or image_url.netloc != "www.opute.io":
                fail(f"{route} Open Graph image is outside the canonical site origin")
            image_path, _ = resolve_local_target(page, image_url.path)
        else:
            image_path, _ = resolve_local_target(page, parser.og_image)
        if image_path is None or not image_path.is_file():
            fail(f"{route} Open Graph image is not a local published asset")
        if parser.twitter_image != parser.og_image:
            fail(f"{route} Twitter and Open Graph preview images differ")
        for raw in parser.links:
            target, fragment = resolve_local_target(page, raw)
            if target is None:
                continue
            if not target.is_file():
                link_errors.append(f"{route}: {raw}")
                continue
            if fragment and target.suffix.lower() == ".html":
                target_parser = parsers.get(target)
                if target_parser is None:
                    target_parser = html_parser(target)
                if fragment not in target_parser.ids:
                    link_errors.append(f"{route}: {raw} (missing fragment)")
    if link_errors:
        fail("broken local links:\n  " + "\n  ".join(link_errors[:30]))

    preview_image = SITE / "og-image.png"
    try:
        image_header = preview_image.read_bytes()
    except OSError:
        fail("Open Graph PNG is missing")
    if (
        len(image_header) < 24
        or image_header[:8] != b"\x89PNG\r\n\x1a\n"
        or struct.unpack(">II", image_header[16:24]) != (1200, 630)
    ):
        fail("Open Graph PNG must be a valid 1200x630 image")

    search_path = SITE / "search-index.json"
    try:
        search = json.loads(search_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(f"search index is invalid ({type(error).__name__})")
    search_pages = search.get("pages") if isinstance(search, dict) else None
    if not isinstance(search_pages, list):
        fail("search index has no pages array")
    search_routes = set()
    for item in search_pages:
        if not isinstance(item, dict) or any(not isinstance(item.get(field), str) for field in ("url", "title", "description", "body")):
            fail("search index contains a malformed page")
        search_routes.add(item["url"])
    if len(search_routes) != len(search_pages):
        fail("search index contains duplicate route entries")
    if search_routes != expected_routes:
        missing = sorted(expected_routes - search_routes)
        extra = sorted(search_routes - expected_routes)
        fail(f"search routes differ from rendered pages (missing={missing}, extra={extra})")

    try:
        sitemap_root = ET.parse(SITE / "sitemap.xml").getroot()
    except (OSError, ET.ParseError) as error:
        fail(f"sitemap is invalid ({type(error).__name__})")
    sitemap_routes = set()
    for element in sitemap_root.findall("{*}url/{*}loc"):
        location = element.text or ""
        parsed = urlsplit(location)
        if parsed.scheme != "https" or parsed.netloc != "www.opute.io" or parsed.query or parsed.fragment:
            fail(f"sitemap contains a noncanonical location: {location}")
        sitemap_routes.add(parsed.path or "/")
    if sitemap_routes != expected_routes:
        fail("sitemap routes do not match rendered HTML pages")

    try:
        openapi_json = json.loads((SITE / "openapi.json").read_text(encoding="utf-8"))
        openapi_yaml = json.loads((SITE / "openapi.yaml").read_text(encoding="utf-8"))
        catalog = json.loads((ROOT / "site" / "context" / "release-catalog.json").read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(f"OpenAPI or catalog JSON is invalid ({type(error).__name__})")
    if openapi_json != openapi_yaml:
        fail("OpenAPI JSON and YAML describe different objects")
    if openapi_json.get("info", {}).get("version") != catalog.get("packageVersion"):
        fail("OpenAPI version does not match the documented package")

    css = (SITE / "styles.css").read_text(encoding="utf-8")
    for required in (":focus-visible", "max-width: 879px", "max-width: 520px", ".search-error", ".diagram-alt"):
        if required not in css:
            fail(f"responsive/accessibility CSS is missing {required}")
    search_script = (SITE / "search.js").read_text(encoding="utf-8")
    for required in ("search-loading", "search-error", "search-retry", "search-empty", "loadPromise = null", "malformed pages"):
        if required not in search_script:
            fail(f"search state implementation is missing {required}")
    print(f"Validated {len(html_files)} HTML pages, {len(search_routes)} search routes, sitemap, links, OpenAPI parity, and basic accessibility structure.")


if __name__ == "__main__":
    main()
