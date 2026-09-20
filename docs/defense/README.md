# Defense deck (Persian, RTL)

`defense-slides.pptx` — 16 talk slides and 4 backup slides. Build it:

```
./build.sh          # needs node + pptxgenjs, xelatex, pdftoppm
```

`slides.js` is the source; the `.pptx` and `img/` are generated and untracked, the
same arrangement as the thesis PDF.

## Where the content comes from

Every number on a slide is in the thesis or in `experiments/results/metrics.csv`.
The figures are rasterised from `docs/thesis/latex/figures/`, which
`experiments/thesis_figures.py` generates from the runs the thesis tables cite — so a
slide cannot show a figure the thesis does not have. The formulas are typeset by
XeLaTeX rather than typed into PowerPoint, because PowerPoint's fonts substitute square
brackets for the ceiling brackets.

## Two things that bite in RTL PowerPoint

- **Paragraph direction is per-run.** pptxgenjs reads `rtlMode`/`align` from each run in
  a multi-run paragraph, not from the parent call. Miss it on one run and that paragraph
  renders back to front. `rtlRuns()` applies them to every run.
- **Arrowheads.** A left-pointing flow arrow is a line with `beginArrowType`, not
  `endArrowType`.

## Checking a change

There is no LibreOffice here, so QA goes through PowerPoint itself, which is also a
truer preview (real fonts, real bidi):

```
osascript -e 'tell application "Microsoft PowerPoint"
  open POSIX file "<abs path>/defense-slides.pptx"
  save active presentation in POSIX file "/tmp/qa.pdf" as save as PDF
  close active presentation saving no
end tell'
pdftoppm -jpeg -r 100 /tmp/qa.pdf /tmp/qa   # then look at every slide
```

Speaker notes are on each slide; the backup slides cover the dead-band asymmetry, the
load-delivery defect, the cold burst, and the position against Lim et al.
