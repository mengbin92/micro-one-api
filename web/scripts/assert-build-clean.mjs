import { spawnSync } from 'node:child_process';

const result = spawnSync('npm', ['run', 'build'], {
  cwd: new URL('..', import.meta.url),
  encoding: 'utf8',
  shell: false,
});

const output = `${result.stdout || ''}\n${result.stderr || ''}`;
process.stdout.write(result.stdout || '');
process.stderr.write(result.stderr || '');

if (result.status !== 0) {
  process.exit(result.status ?? 1);
}

if (/Unknown at rule/i.test(output)) {
  console.error('Build emitted unexpected CSS at-rule warnings.');
  process.exit(1);
}
