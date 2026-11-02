import { createRequire } from 'node:module';
import { readFileSync, writeFileSync } from 'node:fs';

// openapi-typescript 7.x still requires Redocly 1.x. Its HTTP header matcher
// expects minimatch 5's callable CommonJS export; minimatch 11 uses a named
// export. Adapt only that import while using the patched upstream matcher.
// Remove this patch when the OpenAPI toolchain supports minimatch 11 directly.
const require = createRequire(import.meta.url);
const file = require.resolve('@redocly/openapi-core/lib/utils.js');
const before = 'const minimatch = require("minimatch");';
const after = 'const minimatch = require("minimatch").minimatch;';
const source = readFileSync(file, 'utf8');

if (!source.includes(after)) {
  if (!source.includes(before)) {
    throw new Error('Redocly minimatch import changed; review the compatibility patch before installing.');
  }
  writeFileSync(file, source.replace(before, after));
}
