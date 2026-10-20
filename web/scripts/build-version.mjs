import { execFileSync } from 'node:child_process';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

export function buildVersion(root) {
  try {
    return execFileSync(
      'git',
      ['describe', '--tags', '--exact-match', '--match', '[0-9]*', '--match', 'v[0-9]*'],
      { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }
    )
      .trim()
      .replace(/^v/, '');
  } catch {
    return (
      'dev+' + execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim()
    );
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.stdout.write(buildVersion(path.resolve(import.meta.dirname, '../..')) + '\n');
}
