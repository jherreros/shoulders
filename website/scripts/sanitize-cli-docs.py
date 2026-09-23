#!/usr/bin/env python3
"""Post-process cobra-generated markdown for Docusaurus.

1. Restructures flat `shoulders_group_sub.md` files into a grouped tree:
     shoulders.md                 -> index.md
     shoulders_<cmd>.md           -> <cmd>.md
     shoulders_<cmd>_<sub>.md     -> <cmd>/<sub>.md
   and rewrites the SEE ALSO cross-links to match.
2. Adds front matter (title + sidebar label) so pages show
   "shoulders app apply" instead of the filename.
3. Sanitizes MDX hazards: converts tab/4-space indented code blocks to
   fenced blocks (MDX has no indented code) and escapes bare '<' as
   '&lt;' outside fenced blocks and `code spans`.

Stdlib only — runs on any system Python 3.
"""
import pathlib
import re
import sys

OUT = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else None
if OUT is None:
    raise SystemExit("usage: sanitize-cli-docs.py <generated-dir>")

LT = re.compile(r"<(?=[A-Za-z(/!])")
INDENTED = re.compile(r"^(?:\t| {4})")
LINK = re.compile(r"\]\((shoulders[a-z0-9_-]*\.md)\)")


def new_rel(old_name: str, has_children: dict[str, bool]) -> str:
    """Map a cobra filename to its new relative path inside OUT.

    Groups with subcommands become the index page of their folder so the
    sidebar shows a single entry (category linked to the doc):
      shoulders.md                 -> index.md
      shoulders_<cmd>.md           -> <cmd>/index.md  (if cmd has children)
                                      <cmd>.md        (otherwise)
      shoulders_<cmd>_<sub>.md     -> <cmd>/<sub>.md
    """
    stem = old_name[:-3]  # strip .md
    parts = stem.split("_")
    assert parts[0] == "shoulders", old_name
    rest = parts[1:]
    if not rest:
        return "index.md"
    if len(rest) == 1:
        if has_children.get(rest[0]):
            return f"{rest[0]}/index.md"
        return f"{rest[0]}.md"
    return f"{rest[0]}/{'_'.join(rest[1:])}.md"


def doc_title(new_path: str) -> tuple[str, str]:
    """Return (title, sidebar_label) for a restructured path."""
    if new_path == "index.md":
        return "shoulders", "All commands"
    stem = new_path[:-3]
    parts = stem.split("/")
    if parts[-1] == "index":
        # Group page: label is the group name.
        title = "shoulders " + " ".join(parts[:-1])
        return title, parts[-2]
    words = [p.replace("_", " ") for p in parts]
    title = "shoulders " + " ".join(words)
    # Leaf label keeps the group prefix so `app init` can't be confused
    # with the top-level `init` command.
    return title, " ".join(words)


def escape_outside_spans(line: str) -> str:
    parts = line.split("`")
    for i in range(0, len(parts), 2):
        parts[i] = LT.sub("&lt;", parts[i])
    return "`".join(parts)


def sanitize(text: str) -> str:
    lines = text.splitlines(keepends=True)
    out_lines: list[str] = []
    in_fence = False
    in_indented = False
    for line in lines:
        if line.lstrip().startswith("```"):
            if in_indented:
                out_lines.append("```\n")
                in_indented = False
            in_fence = not in_fence
            out_lines.append(line)
            continue
        if in_fence:
            out_lines.append(line)
            continue
        if INDENTED.match(line):
            if not in_indented:
                out_lines.append("```text\n")
                in_indented = True
            out_lines.append(INDENTED.sub("", line, count=1))
            continue
        if line.strip() == "" and in_indented:
            out_lines.append(line)
            continue
        if in_indented:
            out_lines.append("```\n")
            in_indented = False
        out_lines.append(escape_outside_spans(line))
    if in_indented:
        out_lines.append("```\n")
    return "".join(out_lines)


def main() -> None:
    old_files = sorted(OUT.glob("shoulders*.md"))
    groups = {p.name[len("shoulders_"):-len(".md")] for p in old_files if p.name != "shoulders.md"}
    # A group "has children" if some other file starts with "<group>_".
    has_children = {
        g: any(
            o.name.startswith(f"shoulders_{g}_")
            for o in old_files
        )
        for g in groups
    }
    mapping = {p.name: new_rel(p.name, has_children) for p in old_files}

    for old in old_files:
        new_path = OUT / mapping[old.name]
        new_path.parent.mkdir(parents=True, exist_ok=True)
        text = old.read_text()

        def rewrite_link(m: re.Match) -> str:
            target = mapping.get(m.group(1), m.group(1))
            rel = pathlib.PurePosixPath(target)
            here = pathlib.PurePosixPath(mapping[old.name]).parent
            # relpath from the new file's directory to the target
            import os.path
            rel_str = os.path.relpath(str(rel), str(here))
            return "](" + rel_str + ")"

        text = LINK.sub(rewrite_link, text)
        text = sanitize(text)
        title, label = doc_title(mapping[old.name])
        text = f"---\ntitle: \"{title}\"\nsidebar_label: \"{label}\"\n---\n\n" + text
        new_path.write_text(text)
        old.unlink()

    # Category labels for the grouped folders, linked to the group page so the
    # sidebar renders a single entry per group.
    for folder in sorted({p.parent for p in (OUT / m for m in mapping.values()) if p.parent != OUT}):
        doc_id = f"cli/generated/{folder.name}/index"
        (folder / "_category_.json").write_text(
            f'{{"label": "{folder.name}", "link": {{"type": "doc", "id": "{doc_id}"}}}}\n'
        )

    count = len(list(OUT.rglob("*.md")))
    print(f"Restructured + sanitized {count} CLI docs")


main()
