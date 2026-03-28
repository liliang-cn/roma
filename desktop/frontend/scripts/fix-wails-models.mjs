import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const modelsPath = resolve(process.cwd(), 'wailsjs/go/models.ts');

try {
  const source = readFileSync(modelsPath, 'utf8');
  const fixed = source.replaceAll('payload?: Record<string, any>;', 'payload?: { [key: string]: any };');
  if (fixed !== source) {
    writeFileSync(modelsPath, fixed, 'utf8');
  }
} catch (error) {
  if (error && typeof error === 'object' && 'code' in error && error.code === 'ENOENT') {
    process.exit(0);
  }
  throw error;
}
