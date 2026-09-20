// Defense slides (Persian, RTL). Generates docs/defense/defense-slides.pptx.
//
//   cd docs/defense && NODE_PATH=<dir with pptxgenjs> node slides.js
//
// Content mirrors the thesis: every number here appears in docs/thesis/latex or
// experiments/results/metrics.csv. Figures come from the thesis's own figure
// pipeline (experiments/thesis_figures.py), rasterised to PNG.
//
// The talk is ~15 slides plus four backup slides for the questions the write-up
// says are coming: the dead-band asymmetry, the load-delivery defect, the cold
// burst, and where the finding sits in the literature.

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
const RULE = "D7DEE5";

const FA = "B Nazanin";
const EN = "Times New Roman";
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
const rtlRuns = (items) => items.map((t, i) => {
  const o = typeof t === "string" ? {} : (t.options || {});
  return {
    text: typeof t === "string" ? t : t.text,
    options: { fontFace: FA, rtlMode: true, align: "right", breakLine: i < items.length - 1, ...o },
  };
});

function slide(dark = false) {
  const s = pres.addSlide();
  s.background = { color: dark ? DARK : LIGHT };
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

// A big number with a label under it.
function stat(s, { x, y, w, value, label, color = GOOD, size = 44 }) {
  s.addText(value, fa({ x, y, w, h: 0.8, fontSize: size, bold: true, color, align: "center", fontFace: FA }));
  s.addText(label, fa({ x, y: y + 0.78, w, h: 0.6, fontSize: 15, color: MUTED, align: "center" }));
}

/* ------------------------------------------------------------------ 1. title */
{
  const s = slide(true);
  s.addText("طراحی و پیاده‌سازی یک مقیاس‌گذار خودکار سفارشی", fa({
    x: M, y: 1.5, w: W - 2 * M, h: 0.8, fontSize: 36, bold: true, color: "FFFFFF",
  }));
  s.addText("مبتنی بر معیارهای Prometheus برای ریزخدمت‌های ابری",
    fa({ x: M, y: 2.3, w: W - 2 * M, h: 0.7, fontSize: 30, bold: true, color: "FFFFFF" }));

  s.addText("پایداری حلقه‌ی بسته در مقیاس‌گذاری پیش‌بینانه", fa({
    x: M, y: 3.15, w: W - 2 * M, h: 0.5, fontSize: 20, color: "9FB3C8", italic: true,
  }));

  s.addText(rtlRuns([
    "ارائه‌دهنده — رائین سلطانی",
    "استاد راهنما — دکتر سید احمد جوادی",
    "استاد داور — دکتر حامد فربه",
  ]), fa({ x: M, y: 4.3, w: 6, h: 1.4, fontSize: 18, color: "E4EAF0", lineSpacingMultiple: 1.4 }));

  s.addText(rtlRuns(["دانشکده مهندسی کامپیوتر", "دانشگاه صنعتی امیرکبیر", "مهر ۱۴۰۵"]), fa({ x: W - M - 5, y: 4.3, w: 5, h: 1.4, fontSize: 18, color: "9FB3C8", lineSpacingMultiple: 1.4 }));
  s.addNotes("سلام و معرفی. عنوان پروژه، و اینکه یافته‌ی اصلی درباره‌ی پایداری حلقه است نه خود مقیاس‌گذار.");
}

/* ------------------------------------------------- 2. problem */
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
  s.addNotes("دو ضعف HPA. راه حل متعارف پیش‌بینی است. پروژه همین را ساخت و در ارزیابی به مسئله‌ی پایداری رسید.");
}

/* ------------------------------------------------- 3. research questions */
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
  s.addNotes("سه پرسش. پرسش دوم به نتیجه‌ی اصلی تبدیل شد.");
}

/* ------------------------------------------------- 4. architecture */
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
  s.addNotes("معماری سه دغدغه‌ی مستقل: خواندن سیگنال، تصمیم، اعمال. نکته‌ی مهم: خروجی روی ناوگان، سیگنال ورودی را جابه‌جا می‌کند.");
}

/* ------------------------------------------------- 5. three policies */
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
  s.addText([
    { text: "λ ", options: { fontFace: EN } },
    { text: "نرخ ورود درخواست‌ها، ", options: {} },
    { text: "m ", options: { fontFace: EN } },
    { text: "بار هر نمونه، ", options: {} },
    { text: "r ", options: { fontFace: EN } },
    { text: "تعداد نمونه‌ها، ", options: {} },
    { text: "T ", options: { fontFace: EN } },
    { text: "نرخ هدف هر نمونه (۱۰۰ درخواست بر ثانیه)", options: {} },
  ], fa({ x: M, y: Y(4.75), w: W - 2 * M, h: 0.5, fontSize: 16, color: MUTED, align: "center" }));
  s.addNotes("سیاست سوم آن چیزی است که شهود می‌گوید درست است: بار هر نمونه را پیش‌بینی کن. همین معیوب است.");
}

/* ------------------------------------------------- 6. the finding */
{
  const s = slide(true);
  title(s, "یافته‌ی اصلی: حذف‌شدنِ r به هم می‌خورد", true);
  s.addText("سیاست واکنشی — پایدار", fa({ x: W - M - 5.6, y: Y(1.5), w: 5.6, h: 0.45, fontSize: 19, bold: true, color: "9FB3C8" }));
  formula(s, "f-cancel", { x: W - M - 5.6, y: Y(2.0), w: 4.5, boxW: 5.6, align: "right" });
  s.addText("شمار نمونه‌ها دقیقاً حذف می‌شود، پس تصمیم به آن وابسته نیست.",
    fa({ x: W - M - 5.6, y: Y(2.6), w: 5.6, h: 0.6, fontSize: 17, color: "C7D4E0" }));

  s.addText("با پیش‌بینیِ بار هر نمونه — ناپایدار", fa({ x: M, y: Y(1.5), w: 5.6, h: 0.45, fontSize: 19, bold: true, color: "F0A9A2" }));
  formula(s, "f-broken", { x: M, y: Y(2.05), w: 5.1, boxW: 5.6, align: "right" });
  s.addText("پیش‌بینی‌کننده یک عملگر حافظه‌دار میان تقسیم و ضرب است؛ شمارِ نمونه‌های گذشته با شمارِ جاری حذف نمی‌شود.",
    fa({ x: M, y: Y(2.6), w: 5.6, h: 0.9, fontSize: 17, color: "F3C9C4" }));

  card(s, {
    x: M, y: Y(3.75), w: W - 2 * M, h: 1.9, fill: "27374A",
    body: [
      { text: "زیر بارِ ثابت، تنها محرکِ شیبِ برآوردشده، تغییرِ خودِ شمارِ نمونه‌هاست.", options: { bold: true, color: "FFFFFF" } },
      { text: "افق پیش‌بینی، آن شیب را ضرب می‌کند — یعنی افق همان بهره‌ی حلقه است، و افق بلندتر سریع‌تر ناپایدار می‌کند.", options: { color: "D9E2EC" } },
      { text: "مقیاس به بالا ← افت بار هر نمونه ← پیش‌بینیِ افت ← مقیاس به پایین ← و از نو.", options: { color: "F0A9A2" } },
    ],
    size: 18,
  });
  s.addNotes("این اسلاید قلب دفاع است. جبر را روی تخته هم می‌توان نوشت: r در فرمول پایه حذف می‌شود؛ حافظه‌ی پیش‌بینی‌کننده این حذف را می‌شکند.");
}

/* ------------------------------------------------- 7. why HPA is not unstable */
{
  const s = slide();
  title(s, "«پس چرا خودِ HPA ناپایدار نیست؟»");
  card(s, {
    x: M, y: Y(1.55), w: W - 2 * M, h: 1.5, head: "پرسش درست، و پاسخش همان جبر است", headColor: GOOD,
    body: ["HPA هم روی سنجه‌ی هر نمونه کار می‌کند، اما هیچ عملگر حافظه‌داری میان تقسیم و ضرب ندارد: نسبت را در همان لحظه می‌خواند، پس r حذف می‌شود و بهره‌ی مسیر صفر می‌ماند."],
    size: 18,
  });
  const w = (W - 2 * M - 0.4) / 2;
  card(s, { x: W - M - w, y: Y(3.25), w, h: 1.9, head: "دو محافظ دیگر HPA", headColor: INK,
    body: ["ناحیه‌ی مرده‌ی ۱۰ درصدی: تغییرات کوچک نادیده گرفته می‌شوند.", "پنجره‌ی پایدارسازی: پیش‌فرض ۳۰۰ ثانیه برای مقیاس به پایین و صفر برای بالا."], size: 16 });
  card(s, { x: M, y: Y(3.25), w, h: 1.9, head: "و نکته‌ی ظریف", headColor: BAD,
    body: ["همین محافظ‌ها هستند که در ارزیابی، یک قانون کنترل معیوب را هم سالم نشان می‌دهند — پرسش دوم همین است."], size: 16 });
  s.addNotes("این پرسشی است که داور می‌پرسد. پاسخ: عملگر حافظه‌دار. بعد پل بزن به آزمون حذفی.");
}

/* ------------------------------------------------- 8. method */
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
  stats.forEach((st, i) => stat(s, { x: W - M - sw - i * (sw + 0.4), y: Y(1.6), w: sw, value: st.v, label: st.l }));
  card(s, {
    x: M, y: Y(3.35), w: W - 2 * M, h: 2.1, head: "چارچوب آزمایش، نه فقط اجرای بار", headColor: GOOD,
    body: [
      "هر اجرا: برچیدن کنترل‌گرها ← اعمال دقیقاً یک بازو ← بازنشانی ناوگان ← گرم‌کردن ← اندازه‌گیری ← نشست ← ثبت سری‌ها ← بررسی اعتبار.",
      "اثرانگشت تصویر پیش از هر اجرا مقابله می‌شود؛ اجرای معیوب با ثبت دلیل کنار گذاشته می‌شود.",
      "الگوها: روزانه (هموار)، شیب، جهشی و انفجاری (پله‌ای).",
    ],
    size: 17,
  });
  s.addNotes("تأکید: بیشترِ کارِ چارچوب، گرفتنِ اجرای بی‌سروصدا خراب است. هر بررسی از یک شکستِ واقعی آمده.");
}

/* ------------------------------------------------- 9. comparative results */
{
  const s = slide();
  title(s, "نتایج مقایسه‌ای: سیاست پیش‌بینانه‌ی درست");
  const head = ["الگوی بار", "نقض (پیش‌بینانه)", "نقض (خط مبنا)", "کم‌تأمین (پیش‌بینانه)", "کم‌تأمین (خط مبنا)", "ظرفیت بیشتر"];
  const rows = [
    ["روزانه", "۰/۰٪", "۴/۷٪", "۲۴۵", "۱،۰۷۰", "۵/۷٪"],
    ["شیب", "۰/۰٪", "۴/۷٪", "۲۷۰", "۱،۲۳۰", "۱۲/۳٪"],
    ["جهشی", "۰/۶٪", "۲/۲٪", "۲۷۰", "۴۷۵", "۱۶/۵٪"],
    ["انفجاری", "۱/۱٪", "۴/۴٪", "۹۶۰", "۱،۲۶۰", "۲۰/۲٪"],
  ];
  const cell = (t, o = {}) => ({ text: t, options: { fontFace: FA, align: "center", rtlMode: true, ...o } });
  const table = [head.slice().reverse().map((h) => cell(h, { bold: true, color: "FFFFFF", fill: { color: DARK }, fontSize: 14 }))]
    .concat(rows.map((r) => r.slice().reverse().map((t, i) => cell(t, {
      fontSize: 15,
      color: i === 5 ? INK : (i === 4 || i === 2 ? GOOD : INK),
      bold: i === 5 || i === 4 || i === 2,
    }))));
  s.addTable(table, {
    x: M, y: Y(1.65), w: W - 2 * M, colW: Array(6).fill((W - 2 * M) / 6),
    rowH: 0.52, border: { type: "solid", color: RULE, pt: 0.75 }, fill: { color: "FFFFFF" }, valign: "middle",
  });
  s.addText("خط مبنا در این جدول hpa-custom است. کم‌تأمین منابع بر حسب نمونه-ثانیه، و «ظرفیت بیشتر» یعنی نمونه-ثانیه‌ی اضافی نسبت به همان خط مبنا.",
    fa({ x: M, y: Y(4.55), w: W - 2 * M, h: 0.4, fontSize: 14, color: MUTED }));
  card(s, { x: M, y: Y(5.05), w: W - 2 * M, h: 1.0, fill: "FFFFFF",
    body: [{ text: "در میان بازوهای خودکار، کم‌ترین کم‌تأمین روی هر چهار الگو و کم‌ترین یا هم‌تراز کم‌ترین نقض — و زمان واکنش ۲۷/۵ ثانیه در برابر ۱۱۲/۵ ثانیه روی الگوی روزانه.", options: { bold: true } }], size: 17 });
  s.addNotes("خط اصلی: در میان بازوهای خودکار برنده‌ایم، اما این برتری مطلق نیست — اسلاید مبادله.");
}

/* ------------------------------------------------- 10. comparison figure */
{
  const s = slide();
  title(s, "رفتار بازوها روی بار جهشی");
  const cmpH = 5.9, cmpW = cmpH * (1260 / 1480);
  s.addImage({ path: path.join(IMG, "cmp-spike-1.png"), x: (W - cmpW) / 2 + 1.7, y: 1.35, w: cmpW, h: cmpH });
  card(s, { x: M, y: Y(1.5), w: 3.05, h: 3.2, head: "سه بخش", headColor: GOOD,
    body: ["بالا: بار عرضه‌شده در برابر اندازه‌گیری‌شده.", "میانه: نمونه‌های آماده در برابر نیاز مرجع.", "پایین: بار هر نمونه در برابر هدف و حد کیفیت خدمت."], size: 15 });
  card(s, { x: M, y: Y(4.9), w: 3.05, h: 1.6, fill: "FFFFFF",
    body: ["نوار خاکستری دو سو، گرم‌کردن و نشست است و در سنجه‌ها وارد نمی‌شود."], size: 15 });
  s.addNotes("نشان بده که همه‌ی بازوهای سالم پله را دنبال می‌کنند؛ تفاوت در سرعت و در کم‌تأمین است.");
}

/* ------------------------------------------------- 11. ablation */
{
  const s = slide();
  title(s, "آزمون حذفی: میراگر را خاموش کن");
  const abW = 6.4, abH = abW * (1120 / 1260);
  s.addImage({ path: path.join(IMG, "ablation-1.png"), x: W - M - abW, y: 1.5, w: abW, h: abH });
  stat(s, { x: M, y: Y(1.6), w: 3.6, value: "۱۱۹", label: "تغییر جهت از ۱۲۰ چرخه‌ی کنترل", color: BAD, size: 52 });
  stat(s, { x: M, y: Y(3.1), w: 3.6, value: "۵۰/۸٪", label: "نقض کیفیت خدمت", color: BAD, size: 52 });
  card(s, { x: M, y: Y(4.6), w: 3.6, h: 1.5, fill: "FFFFFF",
    body: ["۶۰ بالا و ۶۰ پایین: نوسان دیگر یک گرایش نیست، نقطه‌ی ثابتِ سامانه است."], size: 16 });
  s.addNotes("ستون راست با میراگر، چپ بدون آن. با میراگر هر دو سیاست یکسان به نظر می‌رسند — این کل نکته است.");
}

/* ------------------------------------------------- 12. the masking claim */
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
  s.addNotes("همان سیاست، همان بار، تنها یک پارامتر عوض شده. میراگر اصلاح نیست، پوشاندن است.");
}

/* ------------------------------------------------- 13. repeats */
{
  const s = slide();
  title(s, "تکرارپذیری: چه چیزی بازتولید شد و چه چیزی نشد");
  const cell = (t, o = {}) => ({ text: t, options: { fontFace: FA, align: "center", rtlMode: true, ...o } });
  const head = ["اجرا", "تغییر جهت", "نقض", "میانگین نمونه‌ی آماده", "درخواست ناموفق"];
  const rows = [
    ["اجرای اصلی", "۱۱۹", "۵۰/۸٪", "۱/۰۰", "۴۱/۰٪"],
    ["تکرار ۱", "۱۱۹", "۵۰/۴٪", "۴/۳۷", "۲۴/۵٪"],
    ["تکرار ۲", "۱۱۵", "۳۲/۹٪", "۴/۹۳", "۱۸/۶٪"],
    ["بازوی سالم بی‌میراگر", "۴۳ / ۴۲ / ۳۹", "۴/۱ تا ۴/۷٪", "۶/۸", "حداکثر ۰/۰۵٪"],
  ];
  const table = [head.slice().reverse().map((h) => cell(h, { bold: true, color: "FFFFFF", fill: { color: DARK }, fontSize: 15 }))]
    .concat(rows.map((r, ri) => r.slice().reverse().map((t) => cell(t, {
      fontSize: 16, color: ri === 3 ? GOOD : INK, bold: ri === 3,
    }))));
  s.addTable(table, { x: M, y: Y(1.65), w: W - 2 * M, colW: Array(5).fill((W - 2 * M) / 5), rowH: 0.55,
    border: { type: "solid", color: RULE, pt: 0.75 }, fill: { color: "FFFFFF" }, valign: "middle" });
  const w = (W - 2 * M - 0.4) / 2;
  card(s, { x: W - M - w, y: Y(4.65), w, h: 1.7, head: "بازتولید شد", headColor: GOOD,
    body: ["امضای کنترل: تقریباً در هر چرخه یک تغییر جهت، زیر هر دو مدل تحویل بار.", "آسیب کاربر، با اختلاف چند صد برابری."], size: 16 });
  card(s, { x: M, y: Y(4.65), w, h: 1.7, head: "بازتولید نشد", headColor: BAD,
    body: ["فروپاشی کامل به یک نمونه، ویژگی همان اجرا بود.", "رقم نقض پراکنده است: ۳۳ تا ۵۱ درصد."], size: 16 });
  s.addNotes("اینجا صادق باش: دو ادعا تعدیل شد و در متن هم تعدیل شده است. نتیجه‌ی اصلی دست‌نخورده ماند.");
}

/* ------------------------------------------------- 14. user harm */
{
  const s = slide();
  title(s, "آسیب کاربر: آنچه هیستوگرام برنامه نمی‌دید");
  const sw = (W - 2 * M - 0.8) / 3;
  stat(s, { x: W - M - sw, y: Y(1.7), w: sw, value: "۴۱٪", label: "درخواست ناموفق — بازوی معیوب", color: BAD, size: 50 });
  stat(s, { x: W - M - 2 * sw - 0.4, y: Y(1.7), w: sw, value: "~۲ ث", label: "صدک ۹۵ سمت کاربر", color: BAD, size: 50 });
  stat(s, { x: M, y: Y(1.7), w: sw, value: "۰/۰٪", label: "درخواست ناموفق — بازوهای سالم", color: GOOD, size: 50 });
  card(s, { x: M, y: Y(3.5), w: W - 2 * M, h: 2.2, head: "چرا دیده نمی‌شد", headColor: INK,
    body: [
      "هیستوگرام برنامه تنها زمانی را می‌سنجد که درخواست درون گرداننده گذرانده است: درخواستی که شکست می‌خورد یا پیش از برنامه در صف می‌ماند، هرگز به آن نمی‌رسد.",
      { text: "درس روش‌شناختی: تأخیر را باید سمت کاربر سنجید. داده‌اش از ابتدا در خروجی k6 بود و خوانده نشده بود.", options: { bold: true } },
    ], size: 17 });
  s.addNotes("این تازه‌ترین یافته است. اگر پرسیدند چرا در نسخه‌های قبلی نبود: داده بود، سنجه‌ی اشتباه گزارش می‌شد.");
}

/* ------------------------------------------------- 15. trade-off */
{
  const s = slide();
  title(s, "مبادله، نه برتری مطلق");
  const w = (W - 2 * M - 0.4) / 2;
  card(s, { x: W - M - w, y: Y(1.6), w, h: 2.3, head: "پیش‌بینی چه می‌خرد", headColor: GOOD,
    body: ["کم‌ترین کم‌تأمین منابع در میان بازوهای خودکار، روی هر چهار الگو.", "زمان واکنش ۲۰ تا ۳۵ ثانیه در برابر ۳۲/۵ تا ۱۲۷/۵ ثانیه."], size: 17 });
  card(s, { x: M, y: Y(1.6), w, h: 2.3, head: "بهایش چیست", headColor: BAD,
    body: ["۵/۷ تا ۲۰/۲ درصد ظرفیت بیشتر از hpa-custom.", "تأمین ایستا در سطح اوج کیفیت خدمت بهتری می‌دهد — با ظرفیتی به‌مراتب بیشتر."], size: 17 });
  card(s, { x: M, y: Y(4.2), w: W - 2 * M, h: 1.6, fill: "FFFFFF",
    body: [
      { text: "ادعای قابل دفاع: یک مبادله.", options: { bold: true } },
      "و یک نتیجه‌ی منفی نسبت به تصور رایج: پیش‌بینی به «جهش ناگهانی» کمک نمی‌کند. هیچ پیش‌بینی‌کننده‌ای پله‌ی آنی را از پیش نمی‌بیند؛ سود آن روی بارهای روندار و دوره‌ای است.",
    ], size: 17 });
  s.addNotes("اگر داور بگوید static-peak بهتر است: بله، و در متن هم آمده. بحث بر سر هزینه است.");
}

/* ------------------------------------------------- 16. conclusion */
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
  s.addText("با تشکر — پرسش‌ها", fa({ x: M, y: Y(6.2), w: W - 2 * M, h: 0.6, fontSize: 24, bold: true, color: "FFFFFF", align: "center" }));
  s.addNotes("سه جمله‌ی پایانی. جمله‌ی سوم همان چیزی است که می‌خواهی در ذهن داور بماند.");
}

/* ------------------------------------------- backup slides */
function backup(head, body, extra) {
  const s = slide();
  s.addText("اسلاید پشتیبان", fa({ x: M, y: 0.3, w: W - 2 * M, h: 0.35, fontSize: 14, color: MUTED }));
  title(s, head);
  card(s, { x: M, y: Y(1.7), w: W - 2 * M, h: 2.0, body, size: 18 });
  if (extra) card(s, { x: M, y: Y(3.95), w: W - 2 * M, h: 1.45, fill: "FFFFFF", body: extra, size: 17 });
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
], ["پادالا و همکاران (۲۰۰۷) و علی‌الدین و همکاران (۲۰۱۲) نیز مقیاس‌گذاری را مسئله‌ی کنترل می‌بینند، اما پایداری سیگنالِ پیش‌بینی را بررسی نمی‌کنند."]);

// pptxgenjs writes one typeface into all three script slots of every run, so a Latin
// word inside Persian text is asked for from B Nazanin — which has no Latin glyphs at
// all (docs/thesis/latex/README.md). PowerPoint on macOS substitutes silently and it
// looks fine; Windows leaves blanks. OOXML already separates the cases: `latin` covers
// Latin characters and `cs` covers complex script, so keep B Nazanin on `cs` and give
// `latin` the thesis's Latin face. This is \lr{} by another name, applied everywhere.
const JSZip = require("jszip");
const fs = require("fs");

async function splitScriptFonts(file) {
  const zip = await JSZip.loadAsync(fs.readFileSync(file));
  const parts = Object.keys(zip.files)
    .filter((n) => /^ppt\/(slides|notesSlides|slideLayouts|slideMasters)\/[^/]+\.xml$/.test(n));
  let runs = 0;
  for (const name of parts) {
    const xml = await zip.file(name).async("string");
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
