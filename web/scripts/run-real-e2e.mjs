import { spawnSync } from 'node:child_process';
import { Console } from 'node:console';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const output = new Console({ stdout: process.stdout, stderr: process.stderr });
const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

const go = process.env.GALLERY_GO ?? (process.platform === 'win32' ? '' : 'go');
if (go === '') {
  output.error('Windows 必须先按仓库工具链规则设置 GALLERY_GO。');
  process.exit(1);
}

const result = spawnSync(
  go,
  ['run', '../tools/testlab/cmd/web-e2e', '-repo-root', '..', ...process.argv.slice(2)],
  {
    cwd: webRoot,
    env: { ...process.env, GOTOOLCHAIN: 'local' },
    stdio: 'inherit'
  }
);
if (result.error) {
  output.error(result.error.message);
  process.exit(1);
}
process.exit(result.status ?? 1);
