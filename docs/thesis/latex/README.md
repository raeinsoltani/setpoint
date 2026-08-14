# Thesis source (Persian, XeLaTeX)

The written deliverable, on the official Amirkabir University of Technology
`AUTthesis` class. Persian throughout; only code listings, figure axis labels and
identifiers are English.

## Building

Requires **XeLaTeX** (TeX Live 2014 or newer) — not pdfLaTeX. The document uses
XePersian for RTL typesetting.

```
xelatex AUTthesis
bibtex  AUTthesis
xelatex AUTthesis
xelatex AUTthesis
```

For the index (`\printindex`), run `makeindex AUTthesis` between passes.

**This has not been compiled.** No TeX distribution is installed on the machine
where these files were written, so the source has been checked by inspection and
by script (cross-references, citation keys, environment nesting) but never run
through XeLaTeX. Expect to fix a small number of layout issues — most likely
table widths and float placement — on the first real build.

## Fonts

- **B Nazanin** — required, and installed on this machine.
- **PGaramond** — optional. The template uses it to render digits inside maths in
  Persian. `commands.tex` leaves `\setdigitfont` commented out, so digits in
  formulas are Latin. Uncomment it if the font is installed and Persian digits in
  formulas are wanted.
- **IranNastaliq** — optional, for the dedication page. `commands.tex` falls back
  to B Nazanin; swap the `\defpersianfont\nastaliq` line if it is installed.

## Files

| File | Contents |
|---|---|
| `AUTthesis.tex` | master file; chapter list |
| `commands.tex` | preamble, fonts, listings/TikZ setup, page styles |
| `AUTthesis.cls` | official AUT class, copied unmodified from `admin/template/latex` |
| `fa_title.tex` | title page metadata **and the Persian abstract** |
| `taid.tex` | defence form placeholder + originality declaration |
| `Chant.tex`, `acknowledgement.tex` | dedication and acknowledgements — **personal, rewrite before submitting** |
| `TOC-TOF-LOT.tex` | contents / figures / tables |
| `list-of-symbols.tex` | symbols and abbreviations |
| `chapter1.tex` | introduction |
| `chapter2.tex` | background and related work |
| `chapter3.tex` | design and implementation |
| `chapter4.tex` | evaluation methodology |
| `chapter5.tex` | results and analysis |
| `chapter6.tex` | conclusion and future work |
| `appendix1.tex` | appendices A (config), B (reproduction), C (full result tables) |
| `references.bib` | bibliography |
| `dicfa2en.tex`, `dicen2fa.tex` | glossaries |
| `en-abstract.tex`, `en_title.tex` | English abstract and title page |
| `figures/` | the four comparison figures from `experiments/results/` |

## Where the numbers come from

Every measured figure in chapters 4–6 traces to `docs/lab-notebook.md` or to
`experiments/results/` (`metrics.csv`, `table_*.md`, `summary.md`). The tables in
appendix C are transcriptions of `experiments/results/table_*.md`.

If the experiments are re-run, `make analyze` regenerates those files and the
appendix tables must be updated to match.

## Known items needing the author's attention

1. **Dedication and acknowledgements** are placeholder text.
2. **`\department`** is set to `مهندسی کامپیوتر`; confirm whether the faculty
   expects a گرایش here.
3. **`taid.tex`** expects the scanned defence form to be inserted.
4. The **defence date** on the title page is `شهریور ۱۴۰۵` — update if it moves.
