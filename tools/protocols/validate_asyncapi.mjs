import { readFile } from 'node:fs/promises';
import { Parser } from '@asyncapi/parser';

const path = process.argv[2];
if (!path) {
  console.error('usage: node validate_asyncapi.mjs <asyncapi-document>');
  process.exit(2);
}

const parser = new Parser();
const diagnostics = await parser.validate(await readFile(path, 'utf8'));
const errors = diagnostics.filter((diagnostic) => diagnostic.severity === 0);
if (errors.length > 0) {
  for (const diagnostic of errors) {
    console.error(`${diagnostic.severity}: ${diagnostic.message} (${diagnostic.path.join('.')})`);
  }
  process.exit(1);
}
