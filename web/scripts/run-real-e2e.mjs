import { spawnSync } from 'node:child_process';
import { Console } from 'node:console';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const output = new Console({ stdout: process.stdout, stderr: process.stderr });
const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

const args = process.argv.slice(2);
const goIndex = args.indexOf('-go-executable');
const go = goIndex >= 0 ? args[goIndex + 1] : (process.env.GALLERY_GO ?? 'go');

const result = spawnSync(go, ['run', '../tools/testlab/cmd/web-e2e', '-repo-root', '..', ...args], {
  cwd: webRoot,
  env: { ...process.env, GOTOOLCHAIN: 'local' },
  stdio: 'inherit'
});
if (result.error) {
  output.error(result.error.message);
  process.exit(1);
}
process.exit(result.status ?? 1);
