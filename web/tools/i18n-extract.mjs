// Collects every string passed to t() and plural(), then reports the translations each
// catalog lacks. With --check it fails when any catalog is incomplete or stale.
import fs from 'node:fs';
import path from 'node:path';
import ts from 'typescript';

const root = path.resolve(import.meta.dirname, '..');
const files = [];
const walk = dir => { for (const e of fs.readdirSync(dir, { withFileTypes: true })) { const p = path.join(dir, e.name); if (e.isDirectory()) walk(p); else if (/\.tsx?$/.test(e.name) && !e.name.endsWith('.gen.ts')) files.push(p); } };
walk(path.join(root, 'src'));

const keys = new Set();
const dynamic = [];
for (const file of files) {
  const sf = ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const visit = node => {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && ['t', 'plural'].includes(node.expression.text)) {
      const args = node.expression.text === 't' ? [node.arguments[0]] : [node.arguments[1], node.arguments[2]];
      for (const a of args) {
        if (a && (ts.isStringLiteral(a) || ts.isNoSubstitutionTemplateLiteral(a))) keys.add(a.text);
        else if (!/i18n\.ts$|api\/client\.ts$/.test(file)) dynamic.push(`${path.relative(root, file)}:${sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1}`);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
}
// Server error messages are shown as sent, translated when the catalog knows them.
const goFiles = [];
const walkGo = dir => { for (const e of fs.readdirSync(dir, { withFileTypes: true })) { const p = path.join(dir, e.name); if (e.isDirectory()) walkGo(p); else if (e.name.endsWith('.go') && !e.name.endsWith('_test.go')) goFiles.push(p); } };
walkGo(path.join(root, '..', 'internal'));
for (const file of goFiles) {
  const text = fs.readFileSync(file, 'utf8');
  for (const m of text.matchAll(/fault\.Invalid\("((?:[^"\\]|\\.)+)"\)|Message:\s+"((?:[^"\\]|\\.)+)"/g)) {
    const message = JSON.parse(`"${m[1] ?? m[2]}"`);
    if (/[a-z]{3}/.test(message)) keys.add(message);
  }
}
const sorted = [...keys].sort();
let incomplete = false;
for (const lang of ['de', 'fr', 'nl', 'es']) {
  const file = path.join(root, 'src/locales', `${lang}.json`);
  const current = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : {};
  const next = {};
  for (const k of sorted) if (current[k]) next[k] = current[k];
  const missing = sorted.filter(k => !current[k]);
  const stale = Object.keys(current).filter(k => !keys.has(k));
  const holes = k => (k.match(/\{\w+\}/g) ?? []).sort().join();
  const broken = sorted.filter(k => current[k] && holes(k) !== holes(current[k]));
  if (broken.length) { incomplete = true; console.log(`${lang}: placeholders differ in ${broken.length}: ${broken.slice(0, 5).map(k => JSON.stringify(k)).join(', ')}`); }
  if (!process.argv.includes('--check')) {
    fs.writeFileSync(file, JSON.stringify(next, null, 1) + '\n');
    fs.writeFileSync(path.join(root, 'tools', `i18n-missing-${lang}.json`), JSON.stringify(missing, null, 1) + '\n');
  }
  if (missing.length || stale.length) incomplete = true;
  console.log(`${lang}: ${sorted.length - missing.length}/${sorted.length} translated${stale.length ? `, ${stale.length} stale` : ''}`);
}
if (dynamic.length) console.log(`t() with non-literal text (translate at the call site): ${dynamic.join(', ')}`);
if (process.argv.includes('--check') && incomplete) process.exit(1);
