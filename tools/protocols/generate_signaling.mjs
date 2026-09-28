import { readdir, readFile, unlink } from 'node:fs/promises';
import { join } from 'node:path';
import { GoFileGenerator, GO_COMMON_PRESET } from '@asyncapi/modelina';

const directory = '../../pkg/generatedsignaling';
const source = await readFile('../../api/asyncapi.yaml', 'utf8');
for (const file of await readdir(directory)) {
  if (file.endsWith('.go')) await unlink(join(directory, file));
}
const generator = new GoFileGenerator({
  presets: [{ preset: GO_COMMON_PRESET, options: { addJsonTag: true } }],
});
await generator.generateToFiles(source, directory, { packageName: 'generatedsignaling' });
