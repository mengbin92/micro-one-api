import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import ts from 'typescript';

const webRoot = path.resolve(import.meta.dirname, '..');
const sourceRoot = path.join(webRoot, 'src');
const generatedLocale = path.join(sourceRoot, 'locales', 'en-US.generated.ts');
const mode = process.argv[2];

function walk(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const target = path.join(directory, entry.name);
    return entry.isDirectory() ? walk(target) : [target];
  });
}

function sourceFiles() {
  return walk(sourceRoot).filter((file) => {
    return /\.(ts|tsx)$/.test(file)
      && !file.endsWith('.test.ts')
      && !file.endsWith('.test.tsx')
      && !file.endsWith('types/api.ts')
      && !file.startsWith(`${path.join(sourceRoot, 'locales')}${path.sep}`)
      && !file.endsWith('lib/i18n.ts');
  });
}

function hasHan(value) {
  return /[\u3400-\u9fff]/.test(value);
}

function normalize(value) {
  return value.replace(/\s+/g, ' ').trim();
}

function parse(file) {
  const text = fs.readFileSync(file, 'utf8');
  const kind = file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  return { text, source: ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, kind) };
}

function collectMessages() {
  const messages = new Set();
  for (const file of sourceFiles()) {
    const { source } = parse(file);
    const visit = (node) => {
      if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isJsxText(node)) {
        const value = normalize(node.text);
        if (hasHan(value)) messages.add(value);
      } else if (ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) {
        const value = normalize(node.text);
        if (hasHan(value)) messages.add(value);
      }
      ts.forEachChild(node, visit);
    };
    visit(source);
  }
  return [...messages].sort((left, right) => left.localeCompare(right, 'zh-CN'));
}

function batches(messages, maxCharacters = 2400) {
  const result = [];
  let current = [];
  let size = 0;
  for (const message of messages) {
    if (current.length > 0 && size + message.length + 1 > maxCharacters) {
      result.push(current);
      current = [];
      size = 0;
    }
    current.push(message);
    size += message.length + 1;
  }
  if (current.length > 0) result.push(current);
  return result;
}

function translateBatch(messages) {
  const response = execFileSync('curl', [
    '-fsS', '--retry', '4', '--retry-all-errors',
    'https://translate.googleapis.com/translate_a/single',
    '--data-urlencode', 'client=gtx',
    '--data-urlencode', 'sl=zh-CN',
    '--data-urlencode', 'tl=en',
    '--data-urlencode', 'dt=t',
    '--data-urlencode', `q=${messages.join('\n')}`,
  ], { encoding: 'utf8', maxBuffer: 1024 * 1024 * 4 });
  const payload = JSON.parse(response);
  const translated = payload[0].map((part) => part[0]).join('').trimEnd().split('\n');
  if (translated.length !== messages.length) {
    throw new Error(`translation line mismatch: expected ${messages.length}, received ${translated.length}`);
  }
  return translated;
}

function generateLocale() {
  const messages = collectMessages();
  const entries = [];
  const groups = batches(messages);
  for (let index = 0; index < groups.length; index += 1) {
    process.stderr.write(`translating ${index + 1}/${groups.length}\n`);
    const translated = translateBatch(groups[index]);
    entries.push(...groups[index].map((message, itemIndex) => [message, translated[itemIndex]]));
  }
  const lines = entries.map(([zh, en]) => `  ${JSON.stringify(zh)}: ${JSON.stringify(en)},`);
  fs.writeFileSync(generatedLocale, [
    '// Generated from user-visible zh-CN source strings. Do not edit by hand.',
    'export const EN_US_MESSAGES: Record<string, string> = {',
    ...lines,
    '};',
    '',
  ].join('\n'));
}

function insideFunction(node) {
  for (let parent = node.parent; parent; parent = parent.parent) {
    if (ts.isFunctionLike(parent)) return true;
  }
  return false;
}

function alreadyTranslated(node) {
  const parent = node.parent;
  return ts.isCallExpression(parent) && ts.isIdentifier(parent.expression) && parent.expression.text === 't';
}

function codemodFile(file) {
  const { text, source } = parse(file);
  const replacements = [];

  const replace = (start, end, value) => replacements.push({ start, end, value });
  const visit = (node) => {
    if (ts.isJsxText(node)) {
      const value = normalize(node.text);
      if (value && hasHan(value)) replace(node.pos, node.end, `{t(${JSON.stringify(value)})}`);
      return;
    }

    if (!insideFunction(node) || alreadyTranslated(node)) {
      ts.forEachChild(node, visit);
      return;
    }

    if (ts.isTemplateExpression(node)) {
      const containsHan = [node.head, ...node.templateSpans.map((span) => span.literal)].some((part) => hasHan(part.text));
      if (containsHan) {
        replace(node.getStart(source), node.end, `t(${text.slice(node.getStart(source), node.end)})`);
        return;
      }
    }

    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
      if (hasHan(node.text)) {
        const call = `t(${JSON.stringify(node.text)})`;
        if (ts.isJsxAttribute(node.parent)) replace(node.getStart(source), node.end, `{${call}}`);
        else replace(node.getStart(source), node.end, call);
        return;
      }
    }

    ts.forEachChild(node, visit);
  };
  visit(source);
  if (replacements.length === 0) return;

  const hasImport = source.statements.some((statement) => {
    return ts.isImportDeclaration(statement) && statement.moduleSpecifier.text === '@/lib/i18n';
  });
  if (!hasImport) {
    const imports = source.statements.filter(ts.isImportDeclaration);
    const position = imports.length > 0 ? imports.at(-1).end : 0;
    replace(position, position, `${position > 0 ? '\n' : ''}import { t } from '@/lib/i18n';`);
  }

  let output = text;
  for (const item of replacements.sort((left, right) => right.start - left.start)) {
    output = output.slice(0, item.start) + item.value + output.slice(item.end);
  }
  fs.writeFileSync(file, output);
}

function codemod() {
  for (const file of sourceFiles()) codemodFile(file);
}

if (mode === 'generate') generateLocale();
else if (mode === 'codemod') codemod();
else throw new Error('usage: node scripts/i18n-tools.mjs <generate|codemod>');
