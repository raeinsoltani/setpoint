// Defense slides (Persian, RTL). Generates docs/defense/defense-slides.pptx.
//
//   cd docs/defense && ./build.sh    (installs pptxgenjs from package.json on first run)
//
// Content mirrors the thesis: every number here appears in docs/thesis/latex or
// experiments/results/metrics.csv. Figures come from the thesis's own figure
// pipeline (experiments/thesis_figures.py), rasterised to PNG.
//
// A glossary follows the title. The talk is ~15 slides, then the live demo's set-up
// slide (demo/README.md), the
// conclusion, a thanks-and-questions page, and four backup slides for the questions
// the write-up says are coming:
// the dead-band asymmetry, the load-delivery defect, the cold burst, and where the
// finding sits in the literature.

const path = require("path");
const pptxgen = require("pptxgenjs");

const IMG = process.env.SLIDE_IMG_DIR || path.join(__dirname, "img");

// Blue is the correct policy and red the broken one, the same colours the thesis
// figures use, so a slide and the figure beside it never disagree.
const DARK = "1E2A38";
const LIGHT = "F4F6F8";
const INK = "1B2733";
const MUTED = "5A6B7B";
const GOOD = "2A6099";
const BAD = "C0392B";
// The demo's third controller: broken, but held still by the stabilizer. Amber because
// the ticker and the demo dashboard draw the raw recommendation in amber.
const AMBER = "B9770E";
const RULE = "D7DEE5";

// Not the thesis's B Nazanin: PowerPoint mis-places its ezafe kasra, so final ن renders
// as ذ and final ل picks up a stray alef, on more than half the slides. The font is a
// legacy build with 75 Arabic-block glyphs against Vazirmatn's 142, and no Office
// setting repairs it. XeLaTeX shapes B Nazanin correctly, which is why the thesis is
// unaffected and only the deck has to move.
const FA = "Vazirmatn";
// Vazirmatn is a sans face, so Latin runs pair with Arial rather than the thesis's
// Times New Roman. Its own Latin coverage is 58 glyphs, short of the full set, so
// splitScriptFonts() still has to hand Latin to a face that has it.
const EN = "Arial";
// Symbols in Persian prose. Arial's lambda reads as the Persian digit eight, and Arial
// does not place a combining hat over it; Cambria Math does both, and ships with Office.
const MATH = "Cambria Math";
// Formulas are typeset by XeLaTeX (build.sh) and placed as images: PowerPoint's fonts
// substitute the ceiling brackets with square ones, and these have to match the thesis.

const pres = new pptxgen();
pres.layout = "LAYOUT_WIDE"; // 13.3 x 7.5 in
pres.rtlMode = true;

const W = 13.3;
const H = 7.5;
const M = 0.6; // page margin
// Content sits below the title. Y() pushes it down so the slack is shared between the
// top and the bottom of the slide instead of piling up under the last element.
const SHIFT = 0.5;
const Y = (v) => v + SHIFT;

// Persian text: right-aligned, RTL paragraph direction.
const fa = (o = {}) => ({
  fontFace: FA, rtlMode: true, align: "right", isTextBox: true, margin: 0,
  color: INK, ...o,
});
// A Latin identifier (arm name, tool) inside Persian material.
const en = (text, o = {}) => ({ text, options: { fontFace: EN, ...o } });

// pptxgenjs reads paragraph properties (direction, alignment) from each run, not from
// the parent call, so a multi-run Persian paragraph loses its RTL direction and renders
// back to front. Every run in an array has to carry them.
// A run that holds no Persian at all -- a bare identifier or a Greek symbol -- is an
// edge inside the paragraph, and the space beside it is dropped exactly as it is at a
// string edge. iso() cannot see this: it works per string, and this string is all
// Latin. Fencing the run itself keeps the space. Greek counts, which is why the
// glossary's lambda sat flush against the word after it.
const fence = (t) => (typeof t === "string" && t.trim() && !/[\u0600-\u06FF]/.test(t)
  ? RLM + t + RLM : t);

const rtlRuns = (items) => items.map((t, i) => {
  const o = typeof t === "string" ? {} : (t.options || {});
  return {
    text: fence(typeof t === "string" ? t : t.text),
    options: { fontFace: FA, rtlMode: true, align: "right", breakLine: i < items.length - 1, ...o },
  };
});

// Same per-run properties, but one line: a run array that is a sentence rather than a
// list. Without them the runs lay out back to front, which is how the symbol glossary
// came to read right to left with its parentheses mirrored.
const rtlInline = (items) => items.map((t) => {
  const o = typeof t === "string" ? {} : (t.options || {});
  return {
    text: fence(typeof t === "string" ? t : t.text),
    options: { fontFace: FA, rtlMode: true, align: "center", ...o },
  };
});

// Speaker notes are personal delivery cues, not part of the deliverable, so they live
// in speaker-notes.json, which is untracked. Keyed by slide number in build order; a
// missing file just means a deck with no notes, which is what a clone gets.
const NOTES = (() => {
  try {
    return JSON.parse(require("fs").readFileSync(path.join(__dirname, "speaker-notes.json"), "utf8"));
  } catch (e) {
    return {};
  }
})();
let slideNo = 0;

// PowerPoint drops the space between a Persian word and a Latin one when the Latin run
// lands at the edge of a string: "\u0648\u0627\u0633\u0637 Kubernetes" renders as one word. A
// RIGHT-TO-LEFT MARK on each side of the Latin token keeps it off the edge and the
// space survives. Measured, not guessed: a no-break space does not help, and Persian
// digits never had the problem. Applied to every string the deck emits, below.
const RLM = "\u200F";
const iso = (t) => (typeof t === "string" && /[\u0600-\u06FF]/.test(t)
  // The token must not end on a dot: a sentence-final period swallowed into the fence
  // becomes LTR and renders to the right of the identifier instead of closing the line.
  ? t.replace(/[A-Za-z][A-Za-z0-9._-]*[A-Za-z0-9]|[A-Za-z]/g, (m) => RLM + m + RLM)
      // A number that opens the string is an edge too, and eats the space after it:
      // "\u06F8 \u0627\u062C\u0631\u0627" came out as one word. One mark after the digits restores it.
      // The Persian comma counts as part of the number: without it the mark landed
      // inside "\u06F1\u060C\u06F0\u06F7\u06F0" and split it, so slide 10 read 0701 for 1,070.
      .replace(/^([\u06F0-\u06F9\d]+(?:[/.,\u060C\u066C][\u06F0-\u06F9\d]+)*\.?)/, (m) => m + RLM)
  : t);
const isoAny = (v) => {
  if (typeof v === "string") return iso(v);
  if (Array.isArray(v)) return v.map(isoAny);
  if (v && typeof v === "object" && "text" in v) return { ...v, text: isoAny(v.text) };
  return v;
};

function slide(dark = false) {
  const s = pres.addSlide();
  s.background = { color: dark ? DARK : LIGHT };
  // One chokepoint, so no call site can forget: every string reaching the slide goes
  // through iso() first.
  const addText = s.addText.bind(s);
  s.addText = (t, o) => addText(isoAny(t), o);
  const addTable = s.addTable.bind(s);
  s.addTable = (rows, o) => addTable(isoAny(rows), o);
  const note = NOTES[String(++slideNo)];
  if (note) s.addNotes(note);
  return s;
}

function title(s, text, dark = false) {
  s.addText(text, fa({
    x: M, y: 0.45, w: W - 2 * M, h: 0.9, fontSize: 32, bold: true,
    color: dark ? "FFFFFF" : INK,
  }));
}

// A content card: tinted panel, heading, body lines. No edge stripes.
function card(s, { x, y, w, h, head, headColor = INK, body = [], fill = "FFFFFF", size = 17 }) {
  s.addShape(pres.ShapeType.roundRect, {
    x, y, w, h, fill: { color: fill }, rectRadius: 0.08,
    line: { color: RULE, width: 0.75 },
    shadow: { type: "outer", angle: 90, blur: 6, offset: 1, opacity: 0.08, color: "000000" },
  });
  if (head) {
    s.addText(head, fa({ x: x + 0.25, y: y + 0.2, w: w - 0.5, h: 0.45, fontSize: 20, bold: true, color: headColor }));
  }
  if (body.length) {
    s.addText(
      rtlRuns(body),
      fa({ x: x + 0.25, y: y + (head ? 0.72 : 0.22), w: w - 0.5, h: h - (head ? 0.92 : 0.44), fontSize: size, lineSpacingMultiple: 1.35, color: INK, valign: "top" })
    );
  }
}

// A LaTeX-typeset formula, centred in a box of width `w`. Ratios are the rendered
// PNGs' own aspect ratios, so nothing is stretched.
const FORMULA = {
  "f-threshold": 428 / 133, "f-total": 442 / 200, "f-perreplica": 552 / 133,
  "f-cancel": 1008 / 133, "f-broken": 1200 / 133,
};
function formula(s, name, { x, y, w, align = "center", boxW }) {
  const h = w / FORMULA[name];
  const left = align === "center" ? x + (boxW - w) / 2 : (align === "right" ? x + boxW - w : x);
  s.addImage({ path: path.join(IMG, name + ".png"), x: left, y, w, h });
  return h;
}

// The workloads come from the functions k6 runs, not from a drawing of them, so the
// thumbnail on the method slide cannot drift from the load the runs were given — the
// same reason patterns.js exists. It reads k6's __ENV when it loads; an empty one is
// the unscaled timeline. It is an ES module, and require() of one needs Node 22.12+.
globalThis.__ENV = globalThis.__ENV || {};
const PATTERNS = require(path.join(__dirname, "../../test/load/lib/patterns.js"));
// Shared by all four thumbnails, so a taller curve is more load, not a different axis.
const LOAD_MAX = 1300;

// A workload as a filled curve: time left to right across the run, as in the thesis
// figures. Sampled per second, keeping the corners of every step so a jump stays
// vertical instead of becoming a slope, and every 15th second of the smooth stretches.
function loadCurve(s, fn, { x, y, w, h }) {
  const T = PATTERNS.DURATION;
  const v = (t) => fn(Math.min(t, T));
  const curve = [];
  for (let t = 0; t <= T; t += 1) {
    const jump = Math.abs(v(t) - v(t - 1)) > 50 || Math.abs(v(t + 1) - v(t)) > 50;
    if (t === 0 || t === T || jump || t % 15 === 0) {
      curve.push({ x: (t / T) * w, y: h - (v(t) / LOAD_MAX) * h });
    }
  }
  s.addShape(pres.ShapeType.custGeom, {
    x, y, w, h, fill: { color: INK, transparency: 88 }, line: { type: "none" },
    points: [{ x: 0, y: h }, ...curve, { x: w, y: h }, { close: true }],
  });
  s.addShape(pres.ShapeType.custGeom, {
    x, y, w, h, line: { color: INK, width: 1.75 }, points: curve,
  });
  s.addShape(pres.ShapeType.line, { x, y: y + h, w, h: 0, line: { color: MUTED, width: 0.75 } });
}

// A big number with a label under it.
function stat(s, { x, y, w, value, label, color = GOOD, size = 44 }) {
  s.addText(value, fa({ x, y, w, h: 0.8, fontSize: size, bold: true, color, align: "center", fontFace: FA }));
  s.addText(label, fa({ x, y: y + 0.78, w, h: 0.6, fontSize: 15, color: MUTED, align: "center" }));
}

/* ------------------------------------------------------------------ 1. title */
{
  const s = slide(true);
  // University, then faculty, reading from the right edge the title is aligned to. The
  // emblems carry no lettering: the names are already in the block below. The CEIT mark
  // is solid strokes and reads heavier than AUT's line work, so it sits a little smaller.
  const aut = { h: 1.0, w: 1.0 * (855 / 900) };
  const ceit = { h: 0.88, w: 0.88 * (878 / 900) };
  const ly = 0.6, gap = 0.35;
  s.addImage({ path: path.join(__dirname, "logos", "aut.png"), x: W - M - aut.w, y: ly, ...aut });
  const rule = W - M - aut.w - gap;
  s.addShape(pres.ShapeType.line, { x: rule, y: ly + 0.15, w: 0, h: aut.h - 0.3, line: { color: "4A5D72", width: 1 } });
  s.addImage({ path: path.join(__dirname, "logos", "ceit.png"), x: rule - gap - ceit.w, y: ly + (aut.h - ceit.h) / 2, ...ceit });

  // Everything else moves down by the emblems' row.
  const dy = 0.5;
  s.addText("طراحی و پیاده‌سازی یک مقیاس‌گذار خودکار سفارشی", fa({
    x: M, y: 1.5 + dy, w: W - 2 * M, h: 0.8, fontSize: 36, bold: true, color: "FFFFFF",
  }));
  s.addText("مبتنی بر معیارهای Prometheus برای ریزخدمت‌های ابری",
    fa({ x: M, y: 2.3 + dy, w: W - 2 * M, h: 0.7, fontSize: 30, bold: true, color: "FFFFFF" }));

  s.addText("پایداری حلقه‌ی بسته در مقیاس‌گذاری پیش‌بینانه", fa({
    x: M, y: 3.15 + dy, w: W - 2 * M, h: 0.5, fontSize: 20, color: "9FB3C8", italic: true,
  }));

  s.addText(rtlRuns([
    "ارائه‌دهنده — رائین سلطانی",
    "استاد راهنما — دکتر سید احمد جوادی",
    "استاد داور — دکتر حامد فربه",
  ]), fa({ x: M, y: 4.3 + dy, w: 6, h: 1.4, fontSize: 18, color: "E4EAF0", lineSpacingMultiple: 1.4 }));

  s.addText(rtlRuns(["دانشکده مهندسی کامپیوتر", "دانشگاه صنعتی امیرکبیر", "مهر ۱۴۰۵"]), fa({ x: W - M - 5, y: 4.3 + dy, w: 5, h: 1.4, fontSize: 18, color: "9FB3C8", lineSpacingMultiple: 1.4 }));
}

/* ------------------------------------------------- 2. glossary */
// The terms the later slides use without stopping to define them, in the thesis's own
// wording (its glossary and the metric definitions in chapter 4), so the room can
// come back here instead of guessing.
{
  const s = slide();
  title(s, "واژه‌نامه");
  const terms = [
    ["HPA", "Horizontal Pod Autoscaler", "مقیاس‌گذار خودکار پیش‌فرض Kubernetes"],
    ["نمونه", "Replica", "یک نسخه‌ی در حال اجرای سرویس"],
    ["ناوگان", "Fleet", "همه‌ی نمونه‌های یک سرویس"],
    ["بازو", "Arm", "یکی از پیکربندی‌های مقایسه‌شده، مانند hpa-cpu"],
    ["میراگر", "Damper", "هر سازوکاری که پاسخ کنترل‌گر را کند می‌کند تا نوسان کم شود"],
    ["پنجره‌ی پایدارسازی", "Stabilization Window", "بازه‌ای که میراگر پیشنهادهای اخیر را در آن نگه می‌دارد"],
    ["ناحیه‌ی مرده", "Dead-band", "انحراف کمتر از ۱۰٪ از هدف نادیده گرفته می‌شود"],
    ["تغییر جهت", "Direction Reversal", "تغییر تعداد نمونه‌ها در خلاف جهت تغییر پیشین"],
    ["نقض کیفیت خدمت", "SLA Violation", "درصد زمانی که بار هر نمونه از ۱۱۰ درخواست بر ثانیه فراتر رود"],
    ["تخصیص ناکافی منابع", "Under-provisioning", "کمبود نمونه نسبت به نیاز، بر حسب نمونه-ثانیه (در پایان‌نامه: کم‌تأمین منابع)"],
    ["زمان واکنش", "Reaction Time", "میانه‌ی فاصله‌ی هر پله‌ی بار تا رسیدن ناوگان به نیاز"],
    ["آزمون حذفی", "Ablation", "اجرای همان سیاست با میراگر خاموش"],
  ];
  const cell = (t, o = {}) => ({ text: t, options: { fontFace: FA, align: "right", rtlMode: true, ...o } });
  // Columns read right to left: term, English, meaning. The arrays are written in that
  // order and reversed, as on the results tables.
  const head = ["واژه", "معادل انگلیسی", "معنا"];
  const table = [head.slice().reverse().map((h) => cell(h, { bold: true, color: "FFFFFF", fill: { color: DARK }, fontSize: 14 }))]
    .concat(terms.map(([fa_, en_, def]) => [
      cell(def, { fontSize: 14 }),
      cell(en_, { fontFace: EN, fontSize: 13, color: MUTED, align: "center", rtlMode: false }),
      cell(fa_, { fontSize: 15, bold: true, color: GOOD }),
    ]));
  s.addTable(table, {
    x: M, y: 1.5, w: W - 2 * M, colW: [6.6, 2.9, 2.6],
    rowH: 0.41, border: { type: "solid", color: RULE, pt: 0.75 }, fill: { color: "FFFFFF" }, valign: "middle",
  });
}

/* ------------------------------------------------- 3. problem */
{
  const s = slide();
  title(s, "مقیاس‌گذار خودکار پیش‌فرض دو ضعف شناخته‌شده دارد");
  const w = (W - 2 * M - 0.4) / 2;
  card(s, {
    x: W - M - w, y: Y(1.55), w, h: 2.1, head: "۱. سیگنال", headColor: GOOD,
    body: [
      "تصمیم عمدتاً بر مصرف پردازنده تکیه می‌کند،",
      "که تقریبی غیرمستقیم از بار واقعی برنامه است.",
    ],
  });
  card(s, {
    x: M, y: Y(1.55), w, h: 2.1, head: "۲. زمان", headColor: GOOD,
    body: [
      "کاملاً واکنشی است: تنها پس از بالا رفتن بار عمل می‌کند،",
      "در حالی که آماده‌شدن یک نمونه در این بستر ~۳۰ ثانیه طول می‌کشد.",
    ],
  });
  card(s, {
    x: M, y: Y(3.95), w: W - 2 * M, h: 1.35, fill: "FFFFFF",
    body: [{ text: "راه حل متعارف برای ضعف دوم: افزودن پیش‌بینی به حلقه‌ی تصمیم.", options: { bold: true } },
      "این پروژه همین راه حل را پیاده کرد — و دقیقاً همین‌جا به یافته‌ی اصلی رسید."],
    size: 19,
  });
}

/* ------------------------------------------------- 4. research questions */
{
  const s = slide();
  title(s, "پرسش‌های پژوهش");
  const qs = [
    ["۱", "کدام سیگنالِ پیش‌بینی، حلقه را ناپایدار می‌کند؟"],
    ["۲", "سازوکار پایدارسازی چه مقدار ناپایداری را می‌پوشاند، و به چه بهایی؟"],
    ["۳", "پیش‌بینی چه می‌خرد، و بهای آن چیست؟"],
  ];
  qs.forEach(([n, q], i) => {
    const y = Y(1.6) + i * 1.5;
    s.addShape(pres.ShapeType.ellipse, { x: W - M - 0.85, y, w: 0.85, h: 0.85, fill: { color: GOOD } });
    s.addText(n, fa({ x: W - M - 0.85, y: y + 0.12, w: 0.85, h: 0.6, fontSize: 26, bold: true, color: "FFFFFF", align: "center" }));
    s.addText(q, fa({ x: M, y: y + 0.12, w: W - 2 * M - 1.2, h: 0.7, fontSize: 22 }));
  });
}

/* ------------------------------------------------- 5. architecture */
{
  const s = slide();
  title(s, "معماری: یک حلقه‌ی کنترل بسته");
  const boxes = [
    ["Prometheus", "سیگنال با یک عبارت PromQL"],
    ["موتور سیاست", "آستانه‌ای یا پیش‌بینانه"],
    ["واسط Kubernetes", "زیرمنبع scale"],
    ["ناوگان نمونه‌ها", "بار را سرویس می‌دهد"],
  ];
  const bw = 2.75, gap = 0.42;
  boxes.forEach(([head, sub], i) => {
    const x = W - M - bw - i * (bw + gap);
    s.addShape(pres.ShapeType.roundRect, {
      x, y: Y(2.1), w: bw, h: 1.35, fill: { color: "FFFFFF" }, rectRadius: 0.08,
      line: { color: i === 1 ? GOOD : RULE, width: i === 1 ? 2 : 0.75 },
    });
    s.addText(head, fa({ x: x + 0.15, y: Y(2.28), w: bw - 0.3, h: 0.45, fontSize: 18, bold: true, align: "center", color: i === 1 ? GOOD : INK }));
    s.addText(sub, fa({ x: x + 0.15, y: Y(2.75), w: bw - 0.3, h: 0.5, fontSize: 14, align: "center", color: MUTED }));
    if (i < boxes.length - 1) {
      s.addShape(pres.ShapeType.line, {
        x: x - gap, y: Y(2.775), w: gap, h: 0,
        // Flow runs right to left, so the arrowhead belongs at the line's start.
        line: { color: MUTED, width: 1.5, beginArrowType: "triangle" },
      });
    }
  });
  // feedback edge: fleet load returns to Prometheus
  s.addShape(pres.ShapeType.line, { x: M + 0.2, y: Y(3.95), w: W - 2 * M - 0.4, h: 0, line: { color: BAD, width: 1.75, endArrowType: "triangle" } });
  s.addText("بارِ اندازه‌گیری‌شده به همان سیگنال بازمی‌گردد — اینجا حلقه بسته می‌شود",
    fa({ x: M, y: Y(4.05), w: W - 2 * M, h: 0.5, fontSize: 16, color: BAD, align: "center" }));
  card(s, {
    x: M, y: Y(4.85), w: W - 2 * M, h: 1.1,
    body: ["هر مرحله یک واسط با دست‌کم دو پیاده‌سازی است: سیگنال، سیاست، و اعمال. سیاست از فایل پیکربندی می‌آید و کد تغییر نمی‌کند."],
    size: 17,
  });
}

/* ------------------------------------------------- 6. three policies */
{
  const s = slide();
  title(s, "سه سیاست پیاده‌سازی شد");
  const w = (W - 2 * M - 0.8) / 3;
  const items = [
    { head: "آستانه‌ای", img: "f-threshold", fw: 1.5, note: "واکنشی؛ بار کل مشاهده‌شده", color: INK },
    { head: "پیش‌بینانه بر بار کل", img: "f-total", fw: 1.25, note: "برون‌یابی نرخ ورود درخواست‌ها", color: GOOD },
    { head: "پیش‌بینانه بر بار هر نمونه", img: "f-perreplica", fw: 1.95, note: "برون‌یابی بار هر نمونه", color: BAD },
  ];
  items.forEach((it, i) => {
    const x = W - M - w - i * (w + 0.4);
    card(s, { x, y: Y(1.6), w, h: 2.9, head: it.head, headColor: it.color, body: [] });
    formula(s, it.img, { x, y: Y(2.5), w: it.fw, boxW: w });
    s.addText(it.note, fa({ x: x + 0.25, y: Y(3.25), w: w - 0.5, h: 1.0, fontSize: 16, color: MUTED, align: "center" }));
  });
  s.addText(rtlInline([
    { text: "λ", options: { fontFace: MATH } },
    { text: " نرخ ورود درخواست‌ها، ", options: {} },
    { text: "m", options: { fontFace: MATH } },
    { text: " بار هر نمونه، ", options: {} },
    { text: "r", options: { fontFace: MATH } },
    { text: " تعداد نمونه‌ها، ", options: {} },
    { text: "T", options: { fontFace: MATH } },
    { text: " نرخ هدف هر نمونه، ۱۰۰ درخواست بر ثانیه", options: {} },
  ]), fa({ x: M, y: Y(4.75), w: W - 2 * M, h: 0.5, fontSize: 16, color: MUTED, align: "center" }));
  // The middle and right formulas differ only by the hat, so the room has to be told
  // what it means or the two read as the same policy.
  // No punctuation touches the symbols: a comma beside a Latin run lands on the
  // wrong side of it.
  s.addText(rtlInline([
    { text: "کلاه روی نماد یعنی مقدارِ پیش‌بینی‌شده‌ی آن برای ۴۵ ثانیه‌ی بعد، مثل ", options: {} },
    { text: "λ\u0302", options: { fontFace: MATH } },
    { text: " و ", options: {} },
    { text: "m\u0302", options: { fontFace: MATH } },
    { text: " در فرمول‌های بالا.", options: {} },
  ]), fa({ x: M, y: Y(5.2), w: W - 2 * M, h: 0.5, fontSize: 16, color: MUTED, align: "center" }));
}

/* ------------------------------------------------- 7. the finding */
{
  const s = slide(true);
  title(s, "یافته‌ی اصلی: حذف‌شدنِ r به هم می‌خورد", true);
  s.addText("سیاست واکنشی — پایدار", fa({ x: W - M - 5.6, y: Y(1.5), w: 5.6, h: 0.45, fontSize: 19, bold: true, color: "9FB3C8" }));
  formula(s, "f-cancel", { x: W - M - 5.6, y: Y(2.0), w: 4.5, boxW: 5.6, align: "right" });
  s.addText("شمار نمونه‌ها دقیقاً حذف می‌شود، پس تصمیم به آن وابسته نیست.",
    fa({ x: W - M - 5.6, y: Y(2.6), w: 5.6, h: 0.6, fontSize: 17, color: "C7D4E0" }));

  s.addText("با پیش‌بینیِ بار هر نمونه — ناپایدار", fa({ x: M, y: Y(1.5), w: 5.6, h: 0.45, fontSize: 19, bold: true, color: "F0A9A2" }));
  formula(s, "f-broken", { x: M, y: Y(2.05), w: 5.1, boxW: 5.6, align: "right" });
  // k and f appear only in this formula, so they are defined here rather than on slide 6.
  s.addText(rtlInline([
    { text: "k", options: { fontFace: MATH, align: "right" } },
    { text: " شماره‌ی چرخه‌ی کنترل است و ", options: { align: "right" } },
    { text: "f", options: { fontFace: MATH, align: "right" } },
    { text: " پیش‌بینی‌کننده: یک عملگر حافظه‌دار میان تقسیم و ضرب. شمارِ نمونه‌های گذشته با شمارِ جاری حذف نمی‌شود.", options: { align: "right" } },
  ]), fa({ x: M, y: Y(2.6), w: 5.6, h: 0.9, fontSize: 17, color: "F3C9C4" }));

  card(s, {
    x: M, y: Y(3.75), w: W - 2 * M, h: 1.9, fill: "27374A",
    body: [
      { text: "زیر بارِ ثابت، تنها محرکِ شیبِ برآوردشده، تغییرِ خودِ شمارِ نمونه‌هاست.", options: { bold: true, color: "FFFFFF" } },
      { text: "افق پیش‌بینی، آن شیب را ضرب می‌کند — یعنی افق همان بهره‌ی حلقه است، و افق بلندتر سریع‌تر ناپایدار می‌کند.", options: { color: "D9E2EC" } },
      { text: "مقیاس به بالا ← افت بار هر نمونه ← پیش‌بینیِ افت ← مقیاس به پایین ← و از نو.", options: { color: "F0A9A2" } },
    ],
    size: 18,
  });
}

/* ------------------------------------------------- 8. why HPA is not unstable */
{
  const s = slide();
  // Written back to front on purpose: PowerPoint mirrors a guillemet pair that opens
  // and closes the string, though not one inside a sentence (slide 16 is fine). Swapped
  // here, it displays the right way round.
  title(s, "»پس چرا خودِ HPA ناپایدار نیست؟«");
  card(s, {
    x: M, y: Y(1.55), w: W - 2 * M, h: 1.8, head: "پرسش درست، و پاسخش همان جبر است", headColor: GOOD,
    body: ["HPA هم روی سنجه‌ی هر نمونه کار می‌کند، اما هیچ عملگر حافظه‌داری میان تقسیم و ضرب ندارد: نسبت را در همان لحظه می‌خواند، پس r حذف می‌شود و بهره‌ی مسیر صفر می‌ماند."],
    size: 18,
  });
  const w = (W - 2 * M - 0.4) / 2;
  card(s, { x: W - M - w, y: Y(3.55), w, h: 2.4, head: "دو محافظ دیگر HPA", headColor: INK,
    body: ["ناحیه‌ی مرده‌ی ۱۰ درصدی: تغییرات کوچک نادیده گرفته می‌شوند.", "پنجره‌ی پایدارسازی: پیش‌فرض ۳۰۰ ثانیه برای مقیاس به پایین و صفر برای بالا."], size: 16 });
  card(s, { x: M, y: Y(3.55), w, h: 2.4, head: "و نکته‌ی ظریف", headColor: BAD,
    body: ["همین محافظ‌ها هستند که در ارزیابی، یک قانون کنترل معیوب را هم سالم نشان می‌دهند — پرسش دوم همین است."], size: 16 });
}

/* ------------------------------------------------- 9. method */
{
  const s = slide();
  title(s, "روش ارزیابی");
  const stats = [
    { v: "۵۳", l: "اجرای معتبر" },
    { v: "۴", l: "الگوی بار" },
    { v: "۹", l: "بازوی مقایسه" },
    { v: "۱۸۰۰ ث", l: "هر اجرا" },
  ];
  const sw = (W - 2 * M - 1.2) / 4;
  stats.forEach((st, i) => stat(s, { x: W - M - sw - i * (sw + 0.4), y: 1.45, w: sw, value: st.v, label: st.l }));
  card(s, {
    x: M, y: 2.95, w: W - 2 * M, h: 1.62, head: "چارچوب آزمایش، نه فقط اجرای بار", headColor: GOOD,
    body: [
      "هر اجرا: برچیدن کنترل‌گرها ← اعمال دقیقاً یک بازو ← بازنشانی ناوگان ← گرم‌کردن ← اندازه‌گیری ← نشست ← ثبت سری‌ها ← بررسی اعتبار.",
      "اثرانگشت تصویر پیش از هر اجرا مقابله می‌شود؛ اجرای معیوب با ثبت دلیل کنار گذاشته می‌شود.",
    ],
    size: 15,
  });

  // The four workloads, each as its shape over the run. Same order as the table on the
  // next slide, reading right to left.
  const py = 4.75, ph = 2.3;
  card(s, { x: M, y: py, w: W - 2 * M, h: ph, head: "چهار الگوی بار", headColor: GOOD });
  s.addText("افقی: زمان، ۳۰ دقیقه — عمودی: درخواست بر ثانیه، با مقیاس یکسان", fa({
    x: M + 0.25, y: py + 0.24, w: 6.5, h: 0.4, fontSize: 12.5, color: MUTED, align: "left",
  }));
  const pats = [
    { fn: PATTERNS.diurnal, name: "روزانه", desc: "اوج و افت هموار، مثل ترافیک یک روز" },
    { fn: PATTERNS.ramp, name: "شیب", desc: "رشد پیوسته از ۲۰۰ تا ۱۲۰۰" },
    { fn: PATTERNS.spike, name: "جهشی", desc: "پرش ناگهانی به بیش از ۴ برابر" },
    { fn: PATTERNS.bursty, name: "انفجاری", desc: "سه انفجار، هر یک با دره‌ای پس از آن" },
  ];
  const gut = 0.45, cw = (W - 2 * M - 0.5 - 3 * gut) / 4;
  pats.forEach((p, i) => {
    const x = W - M - 0.25 - cw - i * (cw + gut);
    loadCurve(s, p.fn, { x, y: py + 0.78, w: cw, h: 0.82 });
    s.addText(p.name, fa({ x, y: py + 1.66, w: cw, h: 0.34, fontSize: 16, bold: true, align: "center" }));
    s.addText(p.desc, fa({ x, y: py + 1.96, w: cw, h: 0.28, fontSize: 12, color: MUTED, align: "center" }));
  });
}

/* ------------------------------------------------- 10. comparative results */
{
  const s = slide();
  title(s, "نتایج مقایسه‌ای: سیاست پیش‌بینانه‌ی درست");
  // The comparison as bars rather than a table: one pair per workload, ours beside
  // hpa-custom, so "lower on all four" shows before a number is read. Each chart has its
  // own scale and every bar carries its value, so no axis is drawn. The baseline is
  // grey, not red: red is the broken policy on every other slide.
  const BASE = "A9B5C1";
  const rows = [ // violation %, under-allocation in instance-seconds: [ours, hpa-custom]
    { fn: PATTERNS.diurnal, name: "روزانه", v: [0.0, 4.7], u: [245, 1070], cap: "۵/۷٪" },
    { fn: PATTERNS.ramp, name: "شیب", v: [0.0, 4.7], u: [270, 1230], cap: "۱۲/۳٪" },
    { fn: PATTERNS.spike, name: "جهشی", v: [0.6, 2.2], u: [270, 475], cap: "۱۶/۵٪" },
    { fn: PATTERNS.bursty, name: "انفجاری", v: [1.1, 4.4], u: [960, 1260], cap: "۲۰/۲٪" },
  ];
  // Persian digits, "/" as the decimal mark and "،" between thousands, as in the thesis.
  const faNum = (n, d) => n.toFixed(d).replace(".", "/").replace(/\B(?=(\d{3})+(?!\d))/g, "،")
    .replace(/\d/g, (c) => "۰۱۲۳۴۵۶۷۸۹"[c]);

  // Right to left: violation first, as the talk reads them.
  const top = 1.45, ch = 3.3, cw = (W - 2 * M - 0.4) / 2;
  const charts = [
    { x: W - M - cw, head: "نقض (درصد زمان)", key: "v", fmt: (n) => faNum(n, 1) + "٪" },
    { x: M, head: "تخصیص ناکافی (نمونه-ثانیه)", key: "u", fmt: (n) => faNum(n, 0) },
  ];
  const barTop = top + 1.0, base = top + 2.4;
  charts.forEach((c) => {
    card(s, { x: c.x, y: top, w: cw, h: ch, head: c.head });
    s.addShape(pres.ShapeType.line, { x: c.x + 0.25, y: base, w: cw - 0.5, h: 0, line: { color: MUTED, width: 0.75 } });
    const max = Math.max(...rows.flatMap((r) => r[c.key]));
    const gw = (cw - 0.5) / 4, bw = 0.44;
    rows.forEach((r, i) => {
      const gx = c.x + cw - 0.25 - (i + 1) * gw;
      r[c.key].forEach((val, j) => {
        // Ours on the right of the pair, so it is read first.
        const bx = gx + gw / 2 + (j === 0 ? 0.05 : -0.05 - bw);
        const h = (val / max) * (base - barTop);
        const color = j === 0 ? GOOD : BASE;
        if (h > 0) {
          s.addShape(pres.ShapeType.rect, { x: bx, y: base - h, w: bw, h, fill: { color }, line: { type: "none" } });
        } else {
          // A zero is still a result: a flat bar on the axis, not a gap.
          s.addShape(pres.ShapeType.line, { x: bx, y: base, w: bw, h: 0, line: { color, width: 3 } });
        }
        s.addText(c.fmt(val), fa({
          x: bx - 0.15, y: base - h - 0.32, w: bw + 0.3, h: 0.3, fontSize: 12.5, align: "center",
          bold: j === 0, color: j === 0 ? GOOD : MUTED,
        }));
      });
      loadCurve(s, r.fn, { x: gx + (gw - 0.95) / 2, y: base + 0.12, w: 0.95, h: 0.36 });
      s.addText(r.name, fa({ x: gx, y: base + 0.55, w: gw, h: 0.3, fontSize: 14, bold: true, align: "center" }));
    });
  });

  // Legend, right to left: ours, then the baseline. The baseline is named, not called
  // «خط مبنا»: a Persian comma or colon beside a Latin word lands on the wrong side of
  // its space in PowerPoint, whichever word comes first (slide 20 shows it too).
  const ly = top + ch + 0.1;
  [[GOOD, "پیش‌بینانه بر بار کل"], [BASE, "hpa-custom"]].forEach(([color, label], i) => {
    const x = W - M - 0.22 - i * 3.4;
    s.addShape(pres.ShapeType.rect, { x, y: ly + 0.09, w: 0.22, h: 0.22, fill: { color }, line: { type: "none" } });
    s.addText(label, fa({ x: x - 3.0, y: ly, w: 2.9, h: 0.4, fontSize: 15 }));
  });

  // The cost, per workload, in the same order as the charts.
  const cy = ly + 0.48, chh = 0.55;
  card(s, { x: M, y: cy, w: W - 2 * M, h: chh });
  s.addText("بها: ظرفیت بیشتر از hpa-custom", fa({ x: W - M - 4.45, y: cy, w: 4.2, h: chh, fontSize: 15, bold: true, valign: "middle" }));
  const iw = (W - 2 * M - 4.7) / 4;
  rows.forEach((r, i) => {
    s.addText([
      { text: r.name + "  ", options: { fontFace: FA, rtlMode: true, align: "center", color: INK } },
      { text: r.cap, options: { fontFace: FA, rtlMode: true, align: "center", color: GOOD, bold: true } },
    ], fa({ x: W - M - 4.7 - (i + 1) * iw, y: cy, w: iw, h: chh, fontSize: 15, align: "center", valign: "middle" }));
  });

  card(s, { x: M, y: cy + chh + 0.13, w: W - 2 * M, h: 1.0, fill: "FFFFFF",
    body: [{ text: "در میان بازوهای خودکار، سیاست پیش‌بینانه روی هر چهار الگو کم‌ترین تخصیص ناکافی و کم‌ترین یا هم‌تراز کم‌ترین نقض را دارد. زمان واکنش آن روی الگوی روزانه ۲۷/۵ ثانیه است، در برابر ۱۱۲/۵ ثانیه.", options: { bold: true } }], size: 17 });
}

/* ------------------------------------------------- 11. comparison figure */
{
  const s = slide();
  title(s, "رفتار بازوها روی بار جهشی");
  const cmpH = 5.9, cmpW = cmpH * (1260 / 1480);
  const cmpX = (W - cmpW) / 2 + 1.7, cmpY = 1.35;
  s.addImage({ path: path.join(IMG, "cmp-spike-1.png"), x: cmpX, y: cmpY, w: cmpW, h: cmpH });
  // Nine arms share the figure, so an arrow marks the one the talk is about: our
  // policy's blue line in the middle panel, at the overshoot just after the jump,
  // where it rises before the other arms and a little past the need. Positions are
  // pixels of the 1260 x 1480 image, so the arrow moves with it; they have to be
  // re-read if the thesis figure is regenerated.
  const px = (u, v) => [cmpX + (u / 1260) * cmpW, cmpY + (v / 1480) * cmpH];
  const [tipX, tipY] = px(652, 546); // right corner of the overshoot
  const tailX = W - M - 1.15;
  const tailY = px(0, 488)[1]; // level with the gap between the top and middle panels
  s.addShape(pres.ShapeType.line, {
    x: tipX, y: tailY, w: tailX - tipX, h: tipY - tailY, flipV: true,
    line: { color: GOOD, width: 2.25, beginArrowType: "triangle" },
  });
  card(s, { x: M, y: Y(1.5), w: 3.05, h: 3.2, head: "سه بخش", headColor: GOOD,
    body: ["بالا: بار عرضه‌شده در برابر اندازه‌گیری‌شده.", "میانه: نمونه‌های آماده در برابر نیاز مرجع.", "پایین: بار هر نمونه در برابر هدف و حد کیفیت خدمت."], size: 15 });
  card(s, { x: M, y: Y(4.9), w: 3.05, h: 1.6, fill: "FFFFFF",
    body: ["نوار خاکستری دو سو، گرم‌کردن و نشست است و در سنجه‌ها وارد نمی‌شود."], size: 15 });
}

/* ------------------------------------------------- 12. ablation */
{
  const s = slide();
  title(s, "آزمون حذفی: میراگر را خاموش کن");
  const abW = 6.4, abH = abW * (1120 / 1260);
  s.addImage({ path: path.join(IMG, "ablation-1.png"), x: W - M - abW, y: 1.5, w: abW, h: abH });
  stat(s, { x: M, y: Y(1.6), w: 3.6, value: "۱۱۹", label: "تغییر جهت از ۱۲۰ چرخه‌ی کنترل", color: BAD, size: 52 });
  stat(s, { x: M, y: Y(3.1), w: 3.6, value: "۵۰/۸٪", label: "نقض کیفیت خدمت", color: BAD, size: 52 });
  card(s, { x: M, y: Y(4.6), w: 3.6, h: 1.5, fill: "FFFFFF",
    body: ["۶۰ بالا و ۶۰ پایین: نوسان دیگر یک گرایش نیست، نقطه‌ی ثابتِ سامانه است."], size: 16 });
}

/* ------------------------------------------------- 13. the masking claim */
{
  const s = slide(true);
  title(s, "نتیجه‌ی اصلی", true);
  card(s, { x: M, y: Y(1.7), w: W - 2 * M, h: 1.5, fill: "27374A",
    body: [{ text: "یک پنجره‌ی پایدارسازی ۹۰ ثانیه‌ای می‌تواند یک قانون کنترل بنیاداً معیوب را چنان بپوشاند که از همه‌ی آزمون‌های کیفیت خدمت و هزینه سربلند بیرون بیاید.", options: { bold: true, color: "FFFFFF" } }],
    size: 21 });
  const w = (W - 2 * M - 0.4) / 2;
  card(s, { x: W - M - w, y: Y(3.45), w, h: 2.2, head: "با میراگر ۹۰ ثانیه‌ای", headColor: "9FB3C8", fill: "27374A",
    body: [{ text: "نقض روی شیب: ۰/۰٪", options: { color: "FFFFFF" } }, { text: "نقض روی جهشی: ۰/۶٪", options: { color: "FFFFFF" } }, { text: "روی الگوی روزانه حتی ارزان‌تر از سیاست درست", options: { color: "C7D4E0" } }], size: 18 });
  card(s, { x: M, y: Y(3.45), w, h: 2.2, head: "بدون میراگر", headColor: "F0A9A2", fill: "27374A",
    body: [{ text: "نقض: ۳۳ تا ۵۱٪", options: { color: "FFFFFF" } }, { text: "تغییر جهت: ۹۸ تا ۱۱۹ از ۱۲۰", options: { color: "FFFFFF" } }, { text: "۱۹ تا ۴۱٪ درخواست‌ها ناموفق", options: { color: "F3C9C4" } }], size: 18 });
}

/* ------------------------------------------------- 14. repeats */
{
  const s = slide();
  title(s, "تکرارپذیری: چه چیزی بازتولید شد و چه چیزی نشد");
  const cell = (t, o = {}) => ({ text: t, options: { fontFace: FA, align: "center", rtlMode: true, ...o } });
  const head = ["اجرا", "تغییر جهت", "نقض", "میانگین نمونه‌ی آماده", "درخواست ناموفق"];
  const rows = [
    ["اجرای اصلی", "۱۱۹", "۵۰/۸٪", "۱/۰۰", "۴۱/۰٪"],
    ["تکرار ۱", "۱۱۹", "۵۰/۴٪", "۴/۳۷", "۲۴/۵٪"],
    ["تکرار ۲", "۱۱۵", "۳۲/۹٪", "۴/۹۳", "۱۸/۶٪"],
    ["بازوی سالم بی‌میراگر", "۴۳ و ۴۲ و ۳۹", "۴/۱ تا ۴/۷٪", "۶/۸", "حداکثر ۰/۰۵٪"],
  ];
  const table = [head.slice().reverse().map((h) => cell(h, { bold: true, color: "FFFFFF", fill: { color: DARK }, fontSize: 15 }))]
    .concat(rows.map((r, ri) => r.slice().reverse().map((t) => cell(t, {
      fontSize: 16, color: ri === 3 ? GOOD : INK, bold: ri === 3,
    }))));
  s.addTable(table, { x: M, y: Y(1.35), w: W - 2 * M, colW: Array(5).fill((W - 2 * M) / 5), rowH: 0.55,
    border: { type: "solid", color: RULE, pt: 0.75 }, fill: { color: "FFFFFF" }, valign: "middle" });
  const w = (W - 2 * M - 0.4) / 2;
  card(s, { x: W - M - w, y: Y(4.35), w, h: 2.0, head: "بازتولید شد", headColor: GOOD,
    body: ["امضای کنترل: تقریباً در هر چرخه یک تغییر جهت، زیر هر دو مدل تحویل بار.", "آسیب کاربر، با اختلاف چند صد برابری."], size: 16 });
  card(s, { x: M, y: Y(4.35), w, h: 2.0, head: "بازتولید نشد", headColor: BAD,
    body: ["فروپاشی کامل به یک نمونه، ویژگی همان اجرا بود.", "رقم نقض پراکنده است: ۳۳ تا ۵۱ درصد."], size: 16 });
}

/* ------------------------------------------------- 15. user harm */
{
  const s = slide();
  title(s, "آسیب کاربر: آنچه هیستوگرام برنامه نمی‌دید");
  const sw = (W - 2 * M - 0.8) / 3;
  stat(s, { x: W - M - sw, y: Y(1.7), w: sw, value: "۴۱٪", label: "درخواست ناموفق — بازوی معیوب", color: BAD, size: 50 });
  stat(s, { x: W - M - 2 * sw - 0.4, y: Y(1.7), w: sw, value: "۲ ثانیه", label: "صدک ۹۵ سمت کاربر، حدوداً", color: BAD, size: 50 });
  stat(s, { x: M, y: Y(1.7), w: sw, value: "۰/۰٪", label: "درخواست ناموفق — بازوهای سالم", color: GOOD, size: 50 });
  card(s, { x: M, y: Y(3.5), w: W - 2 * M, h: 2.2, head: "چرا دیده نمی‌شد", headColor: INK,
    body: [
      "هیستوگرام برنامه تنها زمانی را می‌سنجد که درخواست درون گرداننده گذرانده است: درخواستی که شکست می‌خورد یا پیش از برنامه در صف می‌ماند، هرگز به آن نمی‌رسد.",
      { text: "درس روش‌شناختی: تأخیر را باید سمت کاربر سنجید. داده‌اش از ابتدا در خروجی k6 بود و خوانده نشده بود.", options: { bold: true } },
    ], size: 17 });
}

/* ------------------------------------------------- 16. trade-off */
{
  const s = slide();
  title(s, "مبادله، نه برتری مطلق");
  const w = (W - 2 * M - 0.4) / 2;
  card(s, { x: W - M - w, y: Y(1.6), w, h: 2.3, head: "پیش‌بینی چه می‌خرد", headColor: GOOD,
    body: ["کم‌ترین تخصیص ناکافی منابع در میان بازوهای خودکار، روی هر چهار الگو.", "زمان واکنش ۲۰ تا ۳۵ ثانیه در برابر ۳۲/۵ تا ۱۲۷/۵ ثانیه."], size: 17 });
  card(s, { x: M, y: Y(1.6), w, h: 2.3, head: "بهایش چیست", headColor: BAD,
    body: ["۵/۷ تا ۲۰/۲ درصد ظرفیت بیشتر از hpa-custom.", "تأمین ایستا در سطح اوج کیفیت خدمت بهتری می‌دهد — با ظرفیتی به‌مراتب بیشتر."], size: 17 });
  card(s, { x: M, y: Y(4.2), w: W - 2 * M, h: 1.6, fill: "FFFFFF",
    body: [
      { text: "ادعای قابل دفاع: یک مبادله.", options: { bold: true } },
      "و یک نتیجه‌ی منفی نسبت به تصور رایج: پیش‌بینی به «جهش ناگهانی» کمک نمی‌کند. هیچ پیش‌بینی‌کننده‌ای پله‌ی آنی را از پیش نمی‌بیند؛ سود آن روی بارهای روندار و دوره‌ای است.",
    ], size: 17 });
}

/* ------------------------------------------------- 17. live demo */
// The contract for the demo (docs/defense/demo/), stated before it runs: the room is
// told what each controller will do, then watches it. 550 and 6 are the demo's setup,
// not results; the outcomes are the thesis's, in words, with no new numbers.
{
  const s = slide();
  title(s, "نمایش زنده: یک بار، سه کنترل‌گر، یک اختلال");
  s.addText("بار ثابتِ ۵۵۰ درخواست بر ثانیه در سراسر نمایش؛ پاسخ درست ۶ نمونه است. تنها کنترل‌گر عوض می‌شود.",
    fa({ x: M, y: Y(1.25), w: W - 2 * M, h: 0.5, fontSize: 19, color: MUTED }));
  const w = (W - 2 * M - 0.8) / 3;
  const acts = [
    { head: "۱. پیش‌بینی بار کل", color: GOOD, body: ["بدون میراگر", { text: "در یک چرخه به ۶ بازمی‌گردد", options: { bold: true } }] },
    { head: "۲. پیش‌بینی بار هر نمونه", color: BAD, body: ["بدون میراگر", { text: "در هر چرخه تغییر جهت می‌دهد", options: { bold: true } }] },
    { head: "۳. همان سیاست معیوب", color: AMBER, body: ["با میراگر ۹۰ ثانیه‌ای", { text: "ناوگان آرام به نظر می‌رسد؛ توصیه‌ی خام همچنان معیوب است", options: { bold: true } }] },
  ];
  acts.forEach((a, i) => card(s, {
    x: W - M - w - i * (w + 0.4), y: Y(2.0), w, h: 2.3, head: a.head, headColor: a.color, body: a.body, size: 17,
  }));
  card(s, {
    x: M, y: Y(4.6), w: W - 2 * M, h: 1.15, fill: "FFFFFF",
    body: [{ text: "اختلال: در هر پرده چهار نمونه را دستی حذف می‌کنم، همان اتفاقی که با از کار افتادن یک گره می‌افتد. ترافیک تغییری نمی‌کند.", options: { bold: true } }],
    size: 18,
  });
  s.addText("همان تصاویری که پایان‌نامه با آن‌ها سنجیده شد؛ چیزی از نو ساخته نشده است.",
    fa({ x: M, y: Y(5.95), w: W - 2 * M, h: 0.4, fontSize: 14, color: MUTED }));
}

/* ------------------------------------------------- 18. conclusion */
{
  const s = slide(true);
  title(s, "جمع‌بندی", true);
  const lines = [
    ["۱", "پیش‌بینی باید روی سیگنالی انجام شود که کنش کنترل‌گر آن را جابه‌جا نکند؛ شرط دقیق‌تر، حفظ‌شدن حذفِ r در سراسر مسیر تصمیم است."],
    ["۲", "میراگر یک اصلاح نیست: یک قانون کنترل معیوب را می‌پوشاند و از همه‌ی آزمون‌های متعارف عبور می‌دهد."],
    ["۳", "ارزیابی پایداری باید میراگر را خاموش کند و بار پله‌ای بدهد — وگرنه چیزی را می‌سنجد که پیشاپیش پنهان شده است."],
  ];
  lines.forEach(([n, t], i) => {
    const y = Y(1.7) + i * 1.35;
    s.addShape(pres.ShapeType.ellipse, { x: W - M - 0.8, y, w: 0.8, h: 0.8, fill: { color: GOOD } });
    s.addText(n, fa({ x: W - M - 0.8, y: y + 0.1, w: 0.8, h: 0.55, fontSize: 24, bold: true, color: "FFFFFF", align: "center" }));
    s.addText(t, fa({ x: M, y: y + 0.05, w: W - 2 * M - 1.1, h: 1.1, fontSize: 19, color: "E4EAF0" }));
  });
}

/* ------------------------------------------------- 19. thanks and questions */
// Its own page, so the conclusion is off screen once the questions start and the
// backup slides come after it.
{
  const s = slide(true);
  s.addText("با تشکر از توجه شما", fa({
    x: M, y: 2.55, w: W - 2 * M, h: 1.0, fontSize: 44, bold: true, color: "FFFFFF", align: "center",
  }));
  s.addText("پرسش‌ها", fa({
    x: M, y: 3.75, w: W - 2 * M, h: 0.7, fontSize: 28, color: "9FB3C8", align: "center",
  }));
}

/* ------------------------------------------- backup slides */
// `h` is the main card's height: a body that wraps to five lines needs more than the
// default, or its last line falls out of the card.
function backup(head, body, extra, h = 2.0) {
  const s = slide();
  s.addText("اسلاید پشتیبان", fa({ x: M, y: 0.3, w: W - 2 * M, h: 0.35, fontSize: 14, color: MUTED }));
  title(s, head);
  card(s, { x: M, y: Y(1.7), w: W - 2 * M, h, body, size: 18 });
  if (extra) card(s, { x: M, y: Y(1.7) + h + 0.25, w: W - 2 * M, h: 1.45, fill: "FFFFFF", body: extra, size: 17 });
  return s;
}

backup("ناحیه‌ی مرده: یک نامتقارنی افشاشده", [
  { text: "بازوی پیش‌بینانه‌ی ما ناحیه‌ی مرده ندارد؛ هر سه رقیب (آستانه‌ای، hpa-cpu و hpa-custom) ناحیه‌ی ۱۰ درصدی دارند.", options: { bold: true } },
  "کنترل‌گری بدون ناحیه‌ی مرده زودتر حرکت می‌کند، و زمان واکنش سنجه‌ی اصلی این مقایسه است. پس بخشی از تفاوت چهاربرابری ممکن است از نبود این ناحیه بیاید و نه از پیش‌بینی.",
], ["بستن این پرسش نیازمند اجرای دوباره با ناحیه‌ی مرده‌ی فعال است. تصویر اندازه‌گیری‌شده تثبیت شده و تغییر آن، آزمایش را عوض می‌کند."]);

backup("نقص تحویل بار، و دامنه‌ی آن", [
  "تا ۲۲ مرداد، اتصال‌های مولد بار به چند نمونه سنجاق می‌شدند و بار به کل ناوگان نمی‌رسید.",
  { text: "۸ اجرا از ۵۳ اجرا زیر مدل اصلاح‌شده‌اند: چهار اندازه‌گیری مجدد hpa-custom و چهار تکرار بازوهای حذفی.", options: { bold: true } },
], ["گستره‌ی آسیب بررسی شد: بار تحویل‌شده در ۴۲ اجرا از ۴۵ اجرا میان ۰/۹۹۳ تا ۱/۰۱۰ الگو بود. سه استثنا، خودِ بازوی فروپاشیده‌اند — یعنی نتیجه، نه مصنوع."]);

backup("ظرفیت گرم در برابر رگبار سرد", [
  "یک نمونه‌ی گرم بیش از ۲۲۰۰ درخواست بر ثانیه را با صدک ۹۵ برابر ۳/۳۴ میلی‌ثانیه سرویس می‌دهد — بیش از بیست برابر نرخ هدف.",
  { text: "اما نخستین رگبار روی نمونه‌ای که بی‌کار بوده، صدک ۹۵ را به حدود ۲/۵ ثانیه می‌برد؛ همان نمونه در حالت گرم ۸۰۰ درخواست بر ثانیه را با ۲/۶۸ میلی‌ثانیه می‌دهد.", options: { bold: true } },
], ["سازوکارش روشن نیست و به‌عنوان کار آینده ثبت شده. این پدیده می‌تواند سود پیش‌بینی را از راهی توضیح دهد که سنجه‌های فعلی نمی‌بینند: نمونه‌ای که پیش از بار افزوده شود، گرم است."]);

backup("جایگاه در کارهای پیشین", [
  { text: "لیم و همکاران (۲۰۰۹): همان خانواده‌ی مسئله — سیگنالی که کنشِ کنترل‌گر آن را جابه‌جا می‌کند.", options: { bold: true } },
  "در کار آنان علت، درشتی گام کنشگر است (۱ به ۲ ماشین یعنی دو برابر ظرفیت) و درمان، پهن‌تر کردن بازه‌ی هدف در خوشه‌های کوچک: یعنی میراسازی.",
  "در این پروژه علت، حافظه‌ی پیش‌بینی‌کننده است و درمان، عوض کردن سیگنال — چون میراسازی همان چیزی است که مسئله را پنهان می‌کند.",
], ["پادالا و همکاران (۲۰۰۷) و علی‌الدین و همکاران (۲۰۱۲) نیز مقیاس‌گذاری را مسئله‌ی کنترل می‌بینند، اما پایداری سیگنالِ پیش‌بینی را بررسی نمی‌کنند."], 2.4);

// pptxgenjs writes one typeface into all three script slots of every run, so a Latin
// word inside Persian text is asked for from B Nazanin — which has no Latin glyphs at
// all (docs/thesis/latex/README.md). PowerPoint on macOS substitutes silently and it
// looks fine; Windows leaves blanks. OOXML already separates the cases: `latin` covers
// Latin characters and `cs` covers complex script, so keep B Nazanin on `cs` and give
// `latin` the thesis's Latin face. This is \lr{} by another name, applied everywhere.
const JSZip = require("jszip");
const fs = require("fs");

// pptxgenjs writes a note as one left-to-right paragraph with raw line breaks inside it.
// In Presenter View that left-aligns every Persian line and puts its full stop at the
// wrong end, so each line becomes its own right-to-left paragraph instead.
const NOTE_BODY = /<a:p><a:r><a:rPr lang="en-US" dirty="0"\/><a:t>([^<]*)<\/a:t><\/a:r><a:endParaRPr lang="en-US" dirty="0"\/><\/a:p>/;
function rtlNoteParagraphs(xml) {
  return xml.replace(NOTE_BODY, (_, text) => text.split(/\r?\n/).map((line) => (line
    ? `<a:p><a:pPr algn="r" rtl="1"/><a:r><a:rPr lang="fa-IR"><a:latin typeface="${EN}"/><a:cs typeface="${FA}"/></a:rPr><a:t>${line}</a:t></a:r></a:p>`
    : `<a:p><a:pPr algn="r" rtl="1"/><a:endParaRPr lang="fa-IR"/></a:p>`)).join(""));
}

async function splitScriptFonts(file) {
  const zip = await JSZip.loadAsync(fs.readFileSync(file));
  const parts = Object.keys(zip.files)
    .filter((n) => /^ppt\/(slides|notesSlides|slideLayouts|slideMasters)\/[^/]+\.xml$/.test(n));
  let runs = 0;
  for (const name of parts) {
    let xml = await zip.file(name).async("string");
    if (name.startsWith("ppt/notesSlides/")) {
      const rtl = rtlNoteParagraphs(xml);
      if (rtl !== xml) zip.file(name, rtl);
      xml = rtl;
    }
    const next = xml.replace(new RegExp(`<a:latin typeface="${FA}"`, "g"), () => {
      runs += 1;
      return `<a:latin typeface="${EN}"`;
    });
    if (next !== xml) zip.file(name, next);
  }
  fs.writeFileSync(file, await zip.generateAsync({ type: "nodebuffer", compression: "DEFLATE" }));
  return runs;
}

const out = path.join(__dirname, "defense-slides.pptx");
pres.writeFile({ fileName: out })
  .then(() => splitScriptFonts(out))
  .then((n) => console.log(`wrote ${out} — Latin face split from Persian in ${n} run(s)`));
