#!/usr/bin/env python3
"""Unattended driver for the study-book pipeline (docs/PROJECT_GUIDE.md).

For each chapter N it runs, in order, with automatic verification after every step:
  research  Haiku 5.5 (headless `claude -p`)      -> notes/chNN_research.md
  code      DeepSeek V4.1 Flash (opencode wrapper) -> notes/chNN_code.md
  write     DeepSeek V4.1 Flash (opencode wrapper) -> chapter block in the book
  review    Haiku 5.5                              -> reviews/chNN_review.md
  fix       Haiku 5.5                              -> chapter block in the book

Rules enforced by this script (not by trust):
  * the model that ran every step is read back from its transcript and must be the expected one;
  * only one step edits the book at a time; after every step the checksum of every OTHER chapter
    (and of the front matter) must be unchanged, and the book must still have 50 chapter markers;
  * no step may change any repo file except the book;
  * research/code notes for up to LOOKAHEAD chapters ahead are prepared in parallel.
State is saved in state.json so a halted run resumes where it stopped.
Usage: drive.py --from 6 --to 25 [--mark-done 6:research,code,write] [--dry-run]
"""
import argparse
import concurrent.futures as cf
import datetime
import hashlib
import json
import os
import re
import subprocess
import sys
import threading
import time
from pathlib import Path

sys.dont_write_bytecode = True
os.environ["PYTHONDONTWRITEBYTECODE"] = "1"
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from chapters import SPECS  # noqa: E402

REPO = HERE.parents[1]
BOOK = REPO / "docs" / "PROJECT_GUIDE.md"
SCRIPTS = REPO / ".claude" / "scripts"
WORK = Path(os.environ.get(
    "PIPELINE_WORKDIR",
    "/private/tmp/claude-504/-Users-darshan-indihood-Documents-projects-l7LoadBalancer/"
    "b3318e0b-2ecf-4ff1-8412-af5224048be8/scratchpad"))
NOTES, REVIEWS, PROMPTS, LOGS = WORK / "notes", WORK / "reviews", WORK / "prompts", WORK / "pipeline-logs"
for d in (NOTES, REVIEWS, PROMPTS, LOGS):
    d.mkdir(parents=True, exist_ok=True)
STATE = WORK / "pipeline-state.json"
STATUS = WORK / "pipeline-status.txt"

HAIKU = "claude-haiku-5-5"
LOOKAHEAD = 3
TOOLS = ",".join([
    "Read", "Grep", "Glob", "Edit", "Write", "WebSearch", "WebFetch",
    "Bash(grep:*)", "Bash(sed:*)", "Bash(awk:*)", "Bash(cat:*)", "Bash(ls:*)", "Bash(wc:*)",
    "Bash(head:*)", "Bash(tail:*)", "Bash(find:*)", "Bash(mkdir:*)", "Bash(shasum:*)",
    "Bash(go vet:*)", "Bash(go mod init:*)", "Bash(go run:*)", "Bash(git status:*)", "Bash(git diff:*)",
])
BANNED = re.compile(r"\b(the notes|fact sheet|research file|code notes|research notes|the writer agent|the reviewer agent)\b", re.I)

TITLES = {1: "What this project is", 2: 'What "L7" means', 3: "Types of load balancers",
          4: "What an L7 balancer does with a request", 5: "Go and the tech stack"}
TITLES.update({n: s["title"] for n, s in SPECS.items()})

_lock = threading.Lock()
_state_lock = threading.Lock()


# ----------------------------------------------------------------------------- logging / state
def log(msg):
    line = f"{datetime.datetime.now():%H:%M:%S} {msg}"
    with _lock:
        print(line, flush=True)
        with open(LOGS / "driver.log", "a") as f:
            f.write(line + "\n")


def set_status(msg):
    STATUS.write_text(f"{datetime.datetime.now():%Y-%m-%d %H:%M:%S} {msg}\n")


def load_state():
    return json.loads(STATE.read_text()) if STATE.exists() else {}


def mark(n, step, value="done"):
    with _state_lock:
        st = load_state()
        st.setdefault(str(n), {})[step] = value
        STATE.write_text(json.dumps(st, indent=1))


def is_done(n, step):
    return load_state().get(str(n), {}).get(step, "").startswith("done")


class Halt(Exception):
    pass


# ----------------------------------------------------------------------------- book helpers
def book_text():
    return BOOK.read_text()


def block_re(n):
    return re.compile(rf"<!-- CHAPTER-{n}-START -->.*?<!-- CHAPTER-{n}-END -->", re.S)


def chapter_block(n):
    m = block_re(n).search(book_text())
    return m.group(0) if m else ""


def checksums():
    t = book_text()
    out = {"head": hashlib.sha1(t.split("<!-- CHAPTER-1-START -->")[0].encode()).hexdigest()}
    for n in range(1, 26):
        m = block_re(n).search(t)
        out[str(n)] = hashlib.sha1((m.group(0) if m else "MISSING").encode()).hexdigest()
    return out


def marker_count():
    return len(re.findall(r"<!-- CHAPTER-\d+-(?:START|END) -->", book_text()))


def words(n):
    return len(chapter_block(n).split())


def reset_chapter(n):
    t = book_text()
    t = block_re(n).sub(f"<!-- CHAPTER-{n}-START -->\n<!-- CHAPTER-{n}-END -->", t, count=1)
    BOOK.write_text(t)


def check_book(before, allowed_changed, what):
    """Only chapters in allowed_changed may differ from the `before` checksums."""
    after = checksums()
    bad = [k for k in after if after[k] != before[k] and k not in {str(a) for a in allowed_changed}]
    if bad:
        raise Halt(f"{what}: changed parts of the book it may not touch: {bad}")
    if marker_count() != 50:
        raise Halt(f"{what}: book has {marker_count()} chapter markers (expected 50)")


# ----------------------------------------------------------------------------- repo guard
def repo_snapshot():
    out = subprocess.run(["git", "-C", str(REPO), "status", "--porcelain", "-uall"],
                         capture_output=True, text=True, check=True).stdout
    snap = {}
    for line in out.splitlines():
        f = line[3:]
        if f == "docs/PROJECT_GUIDE.md" or "__pycache__" in f:
            continue
        p = REPO / f
        snap[f] = hashlib.sha1(p.read_bytes()).hexdigest() if p.is_file() else "DELETED"
    return snap


def check_repo(before, what, allow=()):
    after = repo_snapshot()
    changed = [f for f in set(before) | set(after) if before.get(f) != after.get(f) and f not in allow]
    if changed:
        raise Halt(f"{what}: changed repo files it may not touch: {changed}")


# ----------------------------------------------------------------------------- runners
def run_claude(role, prompt, timeout=3000):
    """Headless Haiku 5.5 run. Returns the result text. Raises Halt on wrong model."""
    ts = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    cmd = ["claude", "-p", "--model", HAIKU, "--output-format", "json",
           "--permission-mode", "acceptEdits", "--allowedTools", TOOLS, "--add-dir", str(WORK)]
    p = subprocess.run(cmd, input=prompt, cwd=REPO, capture_output=True, text=True, timeout=timeout)
    raw = p.stdout.strip()
    (LOGS / f"{role}-{ts}.json").write_text(raw + "\n--- stderr ---\n" + p.stderr)
    try:
        data = json.loads(raw[raw.index("{"):])
    except Exception as e:  # noqa: BLE001
        raise RuntimeError(f"{role}: unparsable claude output ({e}); rc={p.returncode}")
    models = set(data.get("modelUsage", {}))
    if models != {HAIKU}:
        raise Halt(f"{role}: WRONG MODEL {sorted(models)} (expected only {HAIKU})")
    if data.get("is_error") or p.returncode != 0:
        raise RuntimeError(f"{role}: claude reported an error: {str(data.get('result'))[:300]}")
    return data.get("result", "")


def run_opencode(role, prompt_file, allow=(), timeout=2400):
    cmd = [str(SCRIPTS / "opencode-agent.sh"), "--role", role, "--prompt-file", str(prompt_file),
           "--log-dir", str(LOGS), "--timeout", str(timeout)]
    for a in allow:
        cmd += ["--allow", a]
    # concurrent steps may be editing the book: the driver verifies the book itself.
    cmd += ["--ignore", "docs/PROJECT_GUIDE.md"]
    p = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, timeout=timeout + 300)
    out = (p.stdout + p.stderr).strip()
    (LOGS / f"{role}-wrapper-{datetime.datetime.now():%H%M%S}.txt").write_text(out)
    if p.returncode in (3, 4):
        raise Halt(f"{role}: opencode wrapper hard failure (exit {p.returncode}): {out[-400:]}")
    if p.returncode != 0:
        raise RuntimeError(f"{role}: opencode wrapper failed (exit {p.returncode}): {out[-300:]}")


def with_retries(label, fn, attempts=2):
    last = None
    for i in range(1, attempts + 1):
        try:
            return fn()
        except Halt:
            raise
        except Exception as e:  # noqa: BLE001
            last = e
            log(f"{label}: attempt {i}/{attempts} failed: {e}")
    raise RuntimeError(f"{label}: failed after {attempts} attempts: {last}")


# ----------------------------------------------------------------------------- prompts
def p_research(n):
    s = SPECS[n]
    topics = "\n".join(f"{i}. {t}" for i, t in enumerate(s["research"], 1))
    return f"""You are the RESEARCH agent for chapter {n} ("{s['title']}") of a student study book about a Go HTTP load balancer. Use WebSearch/WebFetch (standard mode; extended only if results are thin). Do NOT read or modify the repo.

Research with high-trust primary sources (official documentation, RFCs, papers, vendor docs; Go docs at go.dev / pkg.go.dev). Topics:
{topics}

Rules: write for a college student: short, plain English, each point with its source URL. TAG every point with exactly one of VERIFIED (you fetched and read the page and it says this), SECONDARY (only a summary/snippet/blog), OWN WORDING (your explanation or analogy), UNVERIFIED (could not confirm). Never present a guess as VERIFIED. If a page cannot be fetched say so. Keep each point short.
Write the notes to {NOTES / f'ch{n:02d}_research.md'} (the only file you may write). Reply with a 3-line confirmation."""


def p_code(n):
    s = SPECS[n]
    pre = (PROMPTS / "_preamble.md").read_text()
    foot = (PROMPTS / "_footer.md").read_text()
    out = NOTES / f"ch{n:02d}_code.md"
    return (f"# TASK: code-search fact sheet for Chapter {n}, \"{s['title']}\"\n\n{pre}\n"
            f"## What to find{s['code']}\n{foot}\n\nWrite it to this exact path (outside the repo, so allowed):\n{out}\n\n"
            "When finished, reply with exactly 3 short lines: the output path, how many path:line references the sheet contains, and anything you could not verify.\n")


def p_write(n):
    s = SPECS[n]
    nxt = TITLES.get(n + 1)
    nxt_line = (f'a **Next chapter** pointer: Chapter {n + 1}, "{nxt}"' if nxt else
                "a closing paragraph instead of a next-chapter pointer (this is the last chapter)")
    return f"""# TASK: write Chapter {n}, "{s['title']}", of a study book

## Who you are and what this is for
You are the WRITER agent in a pipeline that produces a long study book for COLLEGE STUDENTS about a Go project called l7LoadBalancer (an HTTP Layer-7 load balancer). The book is the single markdown file:

    {BOOK}

Other agents already wrote earlier chapters and a reviewer will check EVERY `path:line` reference you write against the code, so accuracy matters more than length. The repo is at {REPO} (git commit 53504fc); your working directory is the repo root. The student reads the book with the code open next to it.

## HARD RULES (read twice)
1. The ONLY repo file you may change is `docs/PROJECT_GUIDE.md`, and inside it ONLY the Chapter {n} block, which is currently two empty marker lines:
   ```
   <!-- CHAPTER-{n}-START -->
   <!-- CHAPTER-{n}-END -->
   ```
   Do NOT change, reformat, re-wrap, re-number or "improve" anything else in the file: not the title, the table of contents, or any other chapter. Do NOT create any other file inside the repo. Do not run `go build`, `go test`, `make`, `gofmt` or anything that writes into the repo.
2. HOW to edit: first compose the whole chapter in a DRAFT file OUTSIDE the repo at `{WORK / 'drafts' / f'ch{n:02d}.md'}` (write it in several parts if long; append with shell `cat >> file <<'EOF'`). The draft starts with `<!-- CHAPTER-{n}-START -->` on the first line and ends with `<!-- CHAPTER-{n}-END -->` on the last line. When complete and re-read, do ONE edit of the book with your file-editing tool: replace the exact two-line text `<!-- CHAPTER-{n}-START -->\\n<!-- CHAPTER-{n}-END -->` with the full draft text. Never overwrite the whole book. Never use a script that rewrites the book. Afterwards check `grep -c 'CHAPTER-[0-9]*-\\(START\\|END\\)' docs/PROJECT_GUIDE.md` prints 50.
3. Facts come ONLY from these two files (read both completely before writing):
   - `{NOTES / f'ch{n:02d}_code.md'}` (verified code facts with path:line)
   - `{NOTES / f'ch{n:02d}_research.md'}` (general knowledge with source URLs; each point tagged VERIFIED / SECONDARY / OWN WORDING / UNVERIFIED; if it says research is unavailable, rely on the code facts and leave out general-knowledge claims you cannot support)
   You may also open any repo file to double-check a line number before you write it. NEVER invent a `path:line`. If a line number from the notes is wrong when you check, use the corrected one or drop the reference.
4. Claims tagged SECONDARY, OWN WORDING or UNVERIFIED must be worded carefully ("commonly described as...") or left out; they never go into the Sources list. Only VERIFIED URLs go into a short "Sources" list at the end.
5. The book must read as a book. NEVER write "the notes", "the fact sheet", "the research file", "the code notes", "the writer", "the reviewer", "the agent", or mention these instructions.
6. Do not cite Go standard-library source line numbers. Describe standard-library behaviour in words. Do not repeat what earlier chapters explained at length: point back ("see Chapter 4") instead.
7. Honesty: if the code facts show something surprising or imperfect, say so plainly. Illustrative numbers must be labelled illustrative; numbers derived from code constants must show the arithmetic.

## Read first (tone and format)
Read the title, "How to read this book", the table of contents, and two or three earlier chapters of `docs/PROJECT_GUIDE.md` (blocks between `<!-- CHAPTER-k-START -->` and `<!-- CHAPTER-k-END -->`; Chapters 4 and 5 are good models). Copy: the friendly plain-English tone; one `## Chapter {n}: {s['title']}` heading with `###` sub-headings; references in backticks like `internal/balancer/selector.go:19` right next to the thing they explain; for each important picture one valid Mermaid diagram AND an aligned ASCII version; a "What you learned" recap; a quiz with answers inside `<details>`.

## Audience and style
A college student who has read the earlier chapters. Short sentences; explain every technical term at first use (do not assume words like backend, upstream, round trip, terminate, socket, hop-by-hop header, CAS, EWMA are known unless an earlier chapter defined them: a one-sentence reminder is fine). Friendly, concrete, never condescending. Analogies are welcome when labelled as analogies. Prefer tables and diagrams over long paragraphs.

## Content to write, in this order
Start with the heading `## Chapter {n}: {s['title']}`.
{s['outline']}
- {nxt_line}.

Length: about 4,000-6,500 words including tables and diagrams (shorter only if the topic truly needs it).

## Quality checklist before you finish
- Every `path:line` came from the code facts or you verified it yourself in the file.
- Every Mermaid block is valid (labels with special characters in quotes) and every ASCII box is aligned and closed.
- No leftover words like "the notes". No TODOs, no placeholders.
- The book still has exactly 50 chapter marker lines and every other chapter is byte-for-byte unchanged.

When finished, reply with exactly 3 short lines: the word count of Chapter {n} (`awk '/CHAPTER-{n}-START/,/CHAPTER-{n}-END/' docs/PROJECT_GUIDE.md | wc -w`), the number of path:line references you used, and anything you were unsure about.
"""


def p_review(n):
    s = SPECS[n]
    nxt = TITLES.get(n + 1)
    return f"""You are the REVIEWER agent for chapter {n} of a student study book. The book file is {BOOK}. Review ONLY the text between `<!-- CHAPTER-{n}-START -->` and `<!-- CHAPTER-{n}-END -->`. The repo (Go project, commit 53504fc) is at {REPO}. Do NOT modify the book or any repo file. Your only output file is the review report (plus optional scratch Go files under {WORK / 'review-snippets'}/ outside the repo).

Reader: a college student who has read the earlier chapters. Chapter {n} is "{s['title']}": it must explain the topic in simple human English, with diagrams and file:line references, nothing missing, with "what it does", "why we need it", and what goes IN and comes OUT in order (when a client request arrives; when the selector/algorithm is changed) where relevant.
Also read the notes the writer used: {NOTES / f'ch{n:02d}_code.md'} and {NOTES / f'ch{n:02d}_research.md'} (tags VERIFIED/SECONDARY/OWN WORDING/UNVERIFIED). Also skim the neighbouring chapters of the book for consistency.

CHECK 1 - CODE REFERENCES (most important). For EVERY `path:line` reference to project code, open the real file and confirm the line shows what the sentence says (+/-2 lines only if clearly the same statement; otherwise WRONG). Verify the ORDER of any step-by-step walk against the real execution order, every constant and value, every status code, every message string. Table: reference | claim | actual code | OK / WRONG / UNVERIFIABLE, with the correct line when wrong. Flag any standard-library line numbers (not allowed).
CHECK 2 - ACCURACY OF GENERAL KNOWLEDGE AND CLAIMS OF ABSENCE. Check technical statements; flag anything tagged SECONDARY/OWN WORDING/UNVERIFIED in the research notes but presented as established fact; check the Sources list contains only VERIFIED URLs; verify every "not used / does not exist / never" claim with grep across the whole repo (internal, cmd, test, demo, deployments, bench). Verify every worked numeric example's arithmetic. Compile or `go vet` any generic Go snippet in a throwaway module under the scratch directory (stdlib only).
CHECK 3 - COMPLETENESS against the intended content. {s['review_extra']}
Also: the required in/out tables (client request path; selector change) where relevant; recap; 6-question quiz with answers in <details>; Next-chapter pointer{(' to Chapter %d "%s"' % (n + 1, nxt)) if nxt else ' (none: last chapter)'}; cross-chapter pointers that match the book's table of contents.
CHECK 4 - CLARITY AND DIAGRAMS. Jargon unexplained at first use, confusing sentences, repetition, places a student gets lost. Every Mermaid block syntactically valid; every ASCII diagram aligned with closed borders; quiz answers correct. The book must read as a book: flag any "the notes", "fact sheet", "research file", "the writer". Say if any section is bloated and could be cut without loss.

Write the report to {REVIEWS / f'ch{n:02d}_review.md'}: FIRST LINE exactly `VERDICT: PASS` or `VERDICT: NEEDS FIXES`; then the tables/lists; then a final numbered FIX LIST of exact minimal edits (quote the old text, give the replacement) for the fixer agent, including every unexplained term with a suggested one-line explanation. Be strict and specific. Reply with a 3-line summary: verdict, number of wrong references, number of other fixes."""


def p_fix(n):
    return f"""You are the FIXER agent for chapter {n} of a study book. The book is {BOOK}. The repo (Go, commit 53504fc) is at {REPO}.

Apply the reviewer's FIX LIST (all items) from {REVIEWS / f'ch{n:02d}_review.md'} to the text between `<!-- CHAPTER-{n}-START -->` and `<!-- CHAPTER-{n}-END -->` ONLY. Use ONLY the Edit tool to change the book: do NOT write Python/sed/awk scripts that rewrite the file. Keep both marker lines. Do not touch any other chapter, the table of contents, or any other file. You are the only agent editing the book right now. Find each passage by its quoted text, not by line numbers in the report.

Rules:
- Apply every item. Added glosses must be simple, friendly, one or two sentences, at first use.
- Any NEW file:line reference or code fact you add must be verified against the real file first. Never invent line numbers. Never cite standard-library line numbers. Check any new Go snippet compiles (throwaway module under {WORK / 'fix-snippets'}/ outside the repo, stdlib only).
- Keep the book reading as a book: no "the notes", "fact sheet", "research file".
- Optional "cut" suggestions in the report are NOT approved unless the report says they are required.
- If you disagree with an item because the code shows otherwise, do not apply it; list it in your reply with the reason.
When done reply with: number of fixes applied, any skipped (with reason), and the chapter word count (`awk '/CHAPTER-{n}-START/,/CHAPTER-{n}-END/' docs/PROJECT_GUIDE.md | wc -w`)."""


# ----------------------------------------------------------------------------- steps
def notes_ok(path, minbytes, pattern, minhits):
    return path.exists() and path.stat().st_size >= minbytes and len(re.findall(pattern, path.read_text())) >= minhits


def step_research(n):
    out = NOTES / f"ch{n:02d}_research.md"
    if is_done(n, "research") and out.exists():
        return
    if not SPECS[n].get("research"):
        out.write_text("Research is not needed for this chapter: rely on the code facts only.\n")
        mark(n, "research")
        return
    before = repo_snapshot()

    def go():
        run_claude(f"ch{n:02d}-research", p_research(n))
        if not notes_ok(out, 1500, r"https?://", 3):
            raise RuntimeError("research notes missing or too thin")
    try:
        with_retries(f"ch{n:02d} research", go)
        check_repo(before, f"ch{n:02d} research")
        mark(n, "research")
    except Halt:
        raise
    except RuntimeError as e:
        log(f"WARNING ch{n:02d} research unavailable ({e}); continuing with code facts only")
        out.write_text("Research is unavailable for this chapter: rely on the code facts only and leave out general-knowledge claims you cannot support.\n")
        mark(n, "research", "done-unavailable")


def step_code(n):
    out = NOTES / f"ch{n:02d}_code.md"
    if is_done(n, "code") and out.exists():
        return
    pf = PROMPTS / f"ch{n:02d}_code.md"
    if SPECS[n].get("code"):
        pf.write_text(p_code(n))
    elif not pf.exists():
        raise Halt(f"ch{n:02d}: no code-search prompt")
    before = repo_snapshot()

    def go():
        run_opencode(f"ch{n:02d}-code", pf)
        if not notes_ok(out, 4000, r"\w+\.(?:go|md|yaml|yml|sh):\d+", 5):
            raise RuntimeError("code notes missing or too thin")
    with_retries(f"ch{n:02d} code", go)
    check_repo(before, f"ch{n:02d} code")
    mark(n, "code")


def step_write(n):
    if is_done(n, "write"):
        return
    spec = SPECS[n]
    if not spec.get("outline"):
        raise Halt(f"ch{n:02d}: no writer outline")
    pf = PROMPTS / f"ch{n:02d}_writer.md"
    pf.write_text(p_write(n))

    def go():
        reset_chapter(n)
        before_book, before_repo = checksums(), repo_snapshot()
        run_opencode(f"ch{n:02d}-writer", pf, allow=["docs/PROJECT_GUIDE.md"])
        check_book(before_book, [n], f"ch{n:02d} write")
        check_repo(before_repo, f"ch{n:02d} write")
        w = words(n)
        if w < 1500:
            raise RuntimeError(f"chapter has only {w} words")
        if chapter_block(n).count("```") % 2:
            raise RuntimeError("unbalanced code fences")
    with_retries(f"ch{n:02d} write", go)
    if BANNED.search(chapter_block(n)):
        log(f"WARNING ch{n:02d}: writer used banned wording; the reviewer will flag it")
    log(f"ch{n:02d} written: {words(n)} words")
    mark(n, "write")


def verdict(n):
    p = REVIEWS / f"ch{n:02d}_review.md"
    head = p.read_text()[:600] if p.exists() else ""
    if "NEEDS FIXES" in head:
        return "NEEDS FIXES"
    if "VERDICT: PASS" in head or re.search(r"\bPASS\b", head):
        return "PASS"
    return None


def step_review(n):
    if is_done(n, "review"):
        return

    def go():
        before_book, before_repo = checksums(), repo_snapshot()
        (REVIEWS / f"ch{n:02d}_review.md").unlink(missing_ok=True)
        run_claude(f"ch{n:02d}-review", p_review(n))
        check_book(before_book, [], f"ch{n:02d} review")
        check_repo(before_repo, f"ch{n:02d} review")
        if verdict(n) is None:
            raise RuntimeError("review report has no verdict line")
    try:
        with_retries(f"ch{n:02d} review", go)
        mark(n, "review", "done:" + verdict(n))
    except Halt:
        raise
    except RuntimeError as e:
        log(f"WARNING ch{n:02d}: review failed ({e}); chapter stays unreviewed")
        mark(n, "review", "done-failed")


def step_fix(n):
    if is_done(n, "fix"):
        return
    v = load_state().get(str(n), {}).get("review", "")
    if v.endswith("PASS") or v == "done-failed":
        mark(n, "fix", "done-skipped")
        return

    def go():
        before_book, before_repo = checksums(), repo_snapshot()
        run_claude(f"ch{n:02d}-fix", p_fix(n))
        check_book(before_book, [n], f"ch{n:02d} fix")
        check_repo(before_repo, f"ch{n:02d} fix")
        if words(n) < 1500:
            raise RuntimeError("chapter shrank below 1500 words after fixing")
    try:
        with_retries(f"ch{n:02d} fix", go)
        mark(n, "fix")
    except Halt:
        raise
    except RuntimeError as e:
        log(f"WARNING ch{n:02d}: fix step failed ({e}); chapter keeps its pre-fix text")
        mark(n, "fix", "done-failed")
    if BANNED.search(chapter_block(n)):
        log(f"WARNING ch{n:02d}: banned wording still present after fix")
    log(f"ch{n:02d} fixed: {words(n)} words")


# ----------------------------------------------------------------------------- main loop
def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--from", dest="first", type=int, default=6)
    ap.add_argument("--to", dest="last", type=int, default=25)
    ap.add_argument("--mark-done", action="append", default=[], help="N:step,step")
    ap.add_argument("--dry-run", action="store_true")
    a = ap.parse_args()

    for md in a.mark_done:
        n, steps = md.split(":")
        for s in steps.split(","):
            mark(int(n), s)
    for n in range(a.first, a.last + 1):
        if SPECS[n].get("notes_ready"):
            for step, f in (("research", f"ch{n:02d}_research.md"), ("code", f"ch{n:02d}_code.md")):
                if (NOTES / f).exists():
                    mark(n, step)
    if a.dry_run:
        for n in range(a.first, a.last + 1):
            s = SPECS[n]
            print(f"--- ch{n}: {s['title']} | notes_ready={s.get('notes_ready', False)} | outline={'yes' if s.get('outline') else 'NO'} | code={'yes' if s.get('code') else 'no'}")
        return

    if not (PROMPTS / "_preamble.md").exists():
        raise SystemExit("missing prompts/_preamble.md")
    log(f"pipeline start: chapters {a.first}-{a.last}")
    pool = cf.ThreadPoolExecutor(max_workers=4)
    futs = {}

    def submit_notes(n):
        for step, fn in (("research", step_research), ("code", step_code)):
            if (n, step) not in futs and not is_done(n, step):
                futs[(n, step)] = pool.submit(fn, n)

    def wait_notes(n):
        for step, fn in (("research", step_research), ("code", step_code)):
            f = futs.get((n, step))
            if f is not None:
                f.result()
            elif not is_done(n, step):
                fn(n)

    try:
        for n in range(a.first, a.last + 1):
            for k in range(n, min(n + LOOKAHEAD, a.last) + 1):
                submit_notes(k)
            set_status(f"chapter {n} ({TITLES[n]}): waiting for notes")
            wait_notes(n)
            for name, fn in (("write", step_write), ("review", step_review), ("fix", step_fix)):
                set_status(f"chapter {n} ({TITLES[n]}): {name}")
                log(f"ch{n:02d} {name} ...")
                fn(n)
            log(f"=== chapter {n} complete ({words(n)} words) ===")
        set_status("ALL CHAPTERS COMPLETE")
        log("pipeline finished")
    except Halt as e:
        set_status(f"HALTED: {e}")
        log(f"HALTED: {e}")
        pool.shutdown(wait=False, cancel_futures=True)
        sys.exit(2)
    except Exception as e:  # noqa: BLE001
        set_status(f"FAILED: {e}")
        log(f"FAILED: {e}")
        pool.shutdown(wait=False, cancel_futures=True)
        sys.exit(1)
    pool.shutdown(wait=True)


if __name__ == "__main__":
    main()
