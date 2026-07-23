import { mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import sharp from 'sharp';

const publicDir = path.resolve(import.meta.dirname, '../public');
const iconDir = path.join(publicDir, 'icons');
const source = await readFile(path.join(publicDir, 'favicon.svg'));
await mkdir(iconDir, { recursive: true });
await Promise.all(
  [192, 512].map((size) =>
    sharp(source)
      .resize(size, size)
      .png({ compressionLevel: 9 })
      .toFile(path.join(iconDir, `gallery-${size}.png`))
  )
);
