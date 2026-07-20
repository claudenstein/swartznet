#!/bin/bash
# build-whitepaper.sh — regenerate whitepaper.tex from the AUTHORITATIVE
# whitepaper.md and (optionally) compile whitepaper.pdf.
#
#   ./build-whitepaper.sh            # regenerate whitepaper.tex + compile the PDF
#   ./build-whitepaper.sh --tex-only # regenerate whitepaper.tex only
#
# The prose lives in whitepaper.md; this script derives the LaTeX body from it
# with pandoc (faithful text + correct escaping) and wraps it in the preamble
# below. Requires: pandoc, and (unless --tex-only) a TeX distribution (latexmk).
set -euo pipefail
cd "$(dirname "$0")"

command -v pandoc >/dev/null || { echo "error: pandoc not found" >&2; exit 1; }
SRC=whitepaper.md
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Split the source: the abstract paragraph (after the H1 title, before the first
# '## ' heading) and the body (from the first '## ' onward).
awk 'NR>1 && !/^# / { if (/^## /) exit; print }' "$SRC" > "$TMP/abstract.md"
awk 'f || /^## /{ f=1; print }' "$SRC" > "$TMP/body.md"

pandoc -f markdown+smart -t latex "$TMP/abstract.md" -o "$TMP/abstract.tex"
pandoc -f markdown+smart -t latex --shift-heading-level-by=-1 "$TMP/body.md" -o "$TMP/body.tex"

# Long monospace file paths in the references overrun the margin as \texttt
# (unbreakable). Convert every \texttt{docs/...} to \path{...} (xurl breaks it
# anywhere) and undo pandoc's underscore escaping (\path takes _ verbatim).
python3 - "$TMP/body.tex" <<'PY'
import re, sys
path = sys.argv[1]
s = open(path).read()
s = re.sub(r'\\texttt\{(docs/[^}]*)\}',
           lambda m: r'\path{' + m.group(1).replace(r'\_', '_') + '}', s)
open(path, 'w').write(s)
PY

# ---- assemble the self-contained whitepaper.tex ----------------------------
cat > whitepaper.tex <<'PREAMBLE'
% SwartzNet whitepaper — LaTeX source.
% GENERATED from docs/whitepaper.md by docs/build-whitepaper.sh (prose is
% authoritative in the .md). Self-contained: compiles with a standard TeX Live
% via `latexmk -pdf whitepaper` or `make -C docs whitepaper`.
\documentclass[11pt]{article}

% ---- fonts & typography ---------------------------------------------------
\usepackage[T1]{fontenc}
\usepackage[utf8]{inputenc}
\usepackage{newtxtext}          % Times-like professional serif text
\usepackage{newtxmath}
\usepackage{microtype}          % refined justification & character protrusion

% ---- page geometry --------------------------------------------------------
\usepackage[letterpaper,margin=1in]{geometry}
\linespread{1.04}
\setlength{\parindent}{1.4em}

% ---- lists & section headings ---------------------------------------------
\usepackage{enumitem}
\setlist{topsep=4pt,itemsep=2pt,parsep=0pt}
\usepackage{titlesec}
\setcounter{secnumdepth}{0}     % headings carry their own manual numbers (1., 2., ...)
\titleformat{\section}{\normalfont\large\bfseries}{}{0pt}{}
\titlespacing*{\section}{0pt}{1.5em}{0.55em}

% ---- links (clean: no colored boxes, working PDF bookmarks) ---------------
\usepackage[hidelinks]{hyperref}
\usepackage{xurl}               % breakable file paths in the references (\path)
\hypersetup{
  pdftitle={SwartzNet: Full-Text Search over the Mainline BitTorrent Network},
  pdfauthor={The SwartzNet Project},
  pdfsubject={A mainline-compatible distributed full-text search layer for BitTorrent},
  pdfkeywords={BitTorrent, DHT, full-text search, BEP-44, peer-to-peer, set reconciliation, RIBLT}
}

% pandoc helper (tight lists)
\providecommand{\tightlist}{\setlength{\itemsep}{0pt}\setlength{\parskip}{0pt}}

\begin{document}

% ---- title block ----------------------------------------------------------
\begin{center}
  {\LARGE\bfseries SwartzNet\par}
  \vspace{0.35em}
  {\large\bfseries Full-Text Search over the Mainline BitTorrent Network\par}
  \vspace{0.9em}
  {\normalsize The SwartzNet Project\par}
  \vspace{0.25em}
  {\normalsize July 2026\par}
\end{center}
\vspace{0.4em}

% ---- abstract -------------------------------------------------------------
\begin{center}
\begin{minipage}{0.86\textwidth}
\small\setlength{\parindent}{0pt}
PREAMBLE
cat "$TMP/abstract.tex" >> whitepaper.tex
cat >> whitepaper.tex <<'MID'
\end{minipage}
\end{center}
\vspace{1.1em}

MID
cat "$TMP/body.tex" >> whitepaper.tex
printf '\n\\end{document}\n' >> whitepaper.tex

echo "wrote whitepaper.tex ($(wc -l < whitepaper.tex) lines)"

if [ "${1:-}" != "--tex-only" ]; then
  command -v latexmk >/dev/null || { echo "error: latexmk not found (use --tex-only)" >&2; exit 1; }
  latexmk -pdf -interaction=nonstopmode -halt-on-error whitepaper.tex
  echo "wrote whitepaper.pdf"
fi
