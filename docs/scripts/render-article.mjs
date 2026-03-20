#!/usr/bin/env node
/**
 * render-article.mjs
 *
 * Converts a Markdown article into a LinkedIn-ready version:
 *   1. Renders every ```mermaid block to a PNG via @mermaid-js/mermaid-cli
 *   2. Renders every markdown table to a PNG via puppeteer (bundled with mermaid-cli)
 *   3. Replaces both with  ![label](images/<article>/label.png)
 *   4. Writes the result to <article>-linkedin.md
 *
 * Usage:
 *   node docs/scripts/render-article.mjs docs/articles/ARTICLE_3_TAKING_ACTION.md
 */

import { execSync, spawnSync } from "child_process";
import { readFileSync, writeFileSync, mkdirSync } from "fs";
import { dirname, basename, join, resolve } from "path";
import { fileURLToPath } from "url";
import { tmpdir } from "os";
import { randomBytes } from "crypto";

const __dirname = dirname(fileURLToPath(import.meta.url));

// ── args ──────────────────────────────────────────────────────────────────────
const [, , inputPath] = process.argv;
if (!inputPath) {
  console.error("Usage: node render-article.mjs <path/to/article.md>");
  process.exit(1);
}

const articlePath = resolve(inputPath);
const articleDir  = dirname(articlePath);
const articleBase = basename(articlePath, ".md");
const imagesDir   = join(articleDir, "images", articleBase);
const outputPath  = join(articleDir, `${articleBase}-linkedin.md`);

mkdirSync(imagesDir, { recursive: true });

// ── ensure mermaid-cli is available and locate its node_modules ───────────────
console.log("Checking @mermaid-js/mermaid-cli…");
try {
  execSync("npx --yes @mermaid-js/mermaid-cli --version", { stdio: "ignore" });
} catch {
  console.error("Could not initialise mermaid-cli. Check your npm/npx setup.");
  process.exit(1);
}

// Find puppeteer's CJS entry point inside the npx cache where mermaid-cli was installed.
function findPuppeteerPath() {
  const npxCache = join(process.env.HOME, ".npm", "_npx");
  try {
    // Find the directory first, then resolve its main entry via package.json.
    const dir = execSync(
      `find "${npxCache}" -maxdepth 3 -name "puppeteer" -type d 2>/dev/null | grep "node_modules/puppeteer$" | head -1`,
      { encoding: "utf8" }
    ).trim();
    if (!dir) return null;
    const pkg = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
    // Prefer the CJS export; fall back to "main".
    const cjsExport = pkg?.exports?.["."]?.require ?? pkg?.exports?.["."]?.default;
    const entry = cjsExport
      ? join(dir, typeof cjsExport === "object" ? cjsExport.default ?? cjsExport : cjsExport)
      : join(dir, pkg.main ?? "index.js");
    return entry;
  } catch {
    return null;
  }
}

// ── helpers ───────────────────────────────────────────────────────────────────
function tmpFile(ext) {
  return join(tmpdir(), `render-${randomBytes(4).toString("hex")}${ext}`);
}

function renderMermaid(body, pngFile) {
  const mmdFile = tmpFile(".mmd");
  writeFileSync(mmdFile, body.trim());
  const r = spawnSync(
    "npx", ["--yes", "@mermaid-js/mermaid-cli",
            "-i", mmdFile, "-o", pngFile,
            "-b", "white", "-w", "1200"],
    { encoding: "utf8", stdio: "inherit" }
  );
  return r.status === 0;
}

async function renderTable(tableMarkdown, pngFile, puppeteerPath) {
  // Convert markdown table rows to styled HTML.
  const lines = tableMarkdown.trim().split("\n").filter(l => l.trim());
  const rows  = lines.filter(l => !/^\s*\|[-: |]+\|\s*$/.test(l));
  const html  = `<!DOCTYPE html><html><head><meta charset="utf-8">
<style>
  body { margin: 32px; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; font-size: 15px; background: #fff; }
  table { border-collapse: collapse; min-width: 760px; width: 100%; }
  th { background: #1a1a2e; color: #fff; padding: 13px 20px; text-align: left; font-weight: 600; font-size: 14px; letter-spacing: 0.03em; white-space: nowrap; }
  td { padding: 11px 20px; border-bottom: 1px solid #e5e7eb; vertical-align: top; line-height: 1.5; }
  tr:last-child td { border-bottom: none; }
  tr:nth-child(even) td { background: #f9fafb; }
  tr:hover td { background: #f0f4ff; }
  code { background: #e8eaf6; color: #1a1a2e; padding: 2px 7px; border-radius: 4px; font-size: 0.88em; font-family: "SFMono-Regular", Consolas, monospace; font-weight: 600; }
  th code { background: rgba(255,255,255,0.18); color: #fff; }
</style></head><body><table>
${rows.map((row, i) => {
  const tag  = i === 0 ? "th" : "td";
  const cols = row.split("|").slice(1, -1).map(c => c.trim());
  return `<tr>${cols.map(c =>
    `<${tag}>${c.replace(/`([^`]+)`/g, "<code>$1</code>").replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>")}</${tag}>`
  ).join("")}</tr>`;
}).join("\n")}
</table></body></html>`;

  const htmlFile = tmpFile(".html");
  writeFileSync(htmlFile, html);

  // Dynamically import puppeteer from its resolved location.
  const { default: puppeteer } = await import(puppeteerPath);
  const browser = await puppeteer.launch({
    args: ["--no-sandbox", "--disable-setuid-sandbox"],
  });
  try {
    const page = await browser.newPage();
    await page.goto(`file://${htmlFile}`, { waitUntil: "networkidle0" });
    const el = await page.$("table");
    await el.screenshot({ path: pngFile, omitBackground: false });
  } finally {
    await browser.close();
  }
  return true;
}

// ── process source ────────────────────────────────────────────────────────────
let source = readFileSync(articlePath, "utf8");

// 1. Mermaid blocks
let diagramCount = 0;
source = source.replace(/^```mermaid\n([\s\S]*?)^```/gm, (match, body) => {
  diagramCount++;
  const label   = `diagram-${diagramCount}`;
  const pngFile = join(imagesDir, `${label}.png`);
  console.log(`Rendering mermaid ${label}…`);
  if (!renderMermaid(body, pngFile)) {
    console.warn(`  Warning: rendering failed — keeping mermaid block.`);
    return match;
  }
  return `![${label}](images/${articleBase}/${label}.png)`;
});

// 2. Markdown tables
const puppeteerPath = findPuppeteerPath();
if (!puppeteerPath) {
  console.warn("Could not locate puppeteer in npx cache — tables will not be rendered as images.");
}

let tableCount = 0;
const tableMatches = [];
source = source.replace(
  /^(\|.+\|\n\|[-: |]+\|\n(?:\|.+\|\n?)+)/gm,
  (match) => {
    tableCount++;
    const label   = `table-${tableCount}`;
    tableMatches.push({ label, match });
    return `__TABLE_PLACEHOLDER_${label}__`;
  }
);

if (puppeteerPath && tableCount > 0) {
  for (const { label, match } of tableMatches) {
    const pngFile = join(imagesDir, `${label}.png`);
    console.log(`Rendering ${label}…`);
    try {
      await renderTable(match, pngFile, puppeteerPath);
      source = source.replace(
        `__TABLE_PLACEHOLDER_${label}__`,
        `![${label}](images/${articleBase}/${label}.png)`
      );
    } catch (e) {
      console.warn(`  Warning: table render failed (${e.message}) — keeping markdown table.`);
      source = source.replace(`__TABLE_PLACEHOLDER_${label}__`, match);
    }
  }
} else {
  // Restore placeholders back to original markdown
  for (const { label, match } of tableMatches) {
    source = source.replace(`__TABLE_PLACEHOLDER_${label}__`, match);
  }
}

writeFileSync(outputPath, source);
console.log(`\nDone — ${diagramCount} diagrams, ${tableCount} tables rendered.`);
console.log(`  Images  → ${imagesDir}/`);
console.log(`  Article → ${outputPath}`);
console.log(`\nNext steps for LinkedIn:`);
console.log(`  1. Open linkedin.com → Write article (Pulse)`);
console.log(`  2. Paste content from ${outputPath} via https://markdowntolinkedin.com`);
console.log(`     (handles bold, headings, code spans)`);
console.log(`  3. Where image placeholders appear, upload the PNG from ${imagesDir}/`);
