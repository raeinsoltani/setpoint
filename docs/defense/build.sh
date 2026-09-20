#!/usr/bin/env bash
# Build the defense deck: figures, formulas, then the .pptx.
#
#   ./build.sh            # needs node with pptxgenjs, xelatex, pdftoppm
#
# Figures come from the thesis's own pipeline, so a slide can never show a figure the
# thesis does not. Formulas are typeset by XeLaTeX because PowerPoint substitutes the
# ceiling brackets.
set -euo pipefail
cd "$(dirname "$0")"
IMG="${SLIDE_IMG_DIR:-img}"
mkdir -p "$IMG" && tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

for f in comparison-spike ablation-spike; do
  pdftoppm -png -r 200 "../thesis/latex/figures/$f.pdf" "$IMG/${f/comparison-/cmp-}"
done

mkf() { # name bg fg body
  cat > "$tmp/$1.tex" <<EOF
\documentclass[preview,border=3pt]{standalone}
\usepackage{amsmath}\usepackage{xcolor}
\definecolor{bgc}{HTML}{$2}\definecolor{fgc}{HTML}{$3}
\pagecolor{bgc}\color{fgc}
\begin{document}\$\displaystyle $4\$\end{document}
EOF
  (cd "$tmp" && xelatex -interaction=nonstopmode -halt-on-error "$1.tex" >/dev/null)
  pdftoppm -png -r 600 "$tmp/$1.pdf" "$tmp/$1" && mv "$tmp/$1-1.png" "$IMG/$1.png"
}
mkf f-threshold  FFFFFF 1B2733 'd = \left\lceil \lambda / T \right\rceil'
mkf f-total      FFFFFF 2A6099 'd = \left\lceil \hat{\lambda} / T \right\rceil'
mkf f-perreplica FFFFFF C0392B 'd = \left\lceil r \cdot \hat{m} / T \right\rceil'
mkf f-cancel     1E2A38 FFFFFF 'd = \left\lceil r \cdot (\lambda / r) / T \right\rceil = \left\lceil \lambda / T \right\rceil'
mkf f-broken     1E2A38 FFFFFF 'd = \left\lceil r_k \cdot f(m_{k-1}, m_{k-2}, \ldots) / T \right\rceil'

SLIDE_IMG_DIR="$IMG" node slides.js
