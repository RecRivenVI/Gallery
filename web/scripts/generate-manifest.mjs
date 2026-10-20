import { readFile, writeFile } from 'node:fs/promises';
import { buildVersion } from './build-version.mjs';
import path from 'node:path';

const root = path.resolve(import.meta.dirname, '../..');
const contract = await readFile(path.join(root, 'api/openapi.yaml'), 'utf8');
const contractVersion = /^ {2}version:\s*["']?([^"'\s]+)/m.exec(contract)[1];
const apiVersion = /^ {2}\/api\/(v[0-9]+)\//m.exec(contract)[1];
const webVersion = buildVersion(root);
await writeFile(
  path.join(root, 'web/public/gallery-web.json'),
  JSON.stringify({ webVersion, contractVersion, apiVersion }, null, 2) + '\n'
);
