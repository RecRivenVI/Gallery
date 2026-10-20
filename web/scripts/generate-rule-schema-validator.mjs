import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath, URL } from 'node:url';
import Ajv2020 from 'ajv/dist/2020.js';
import { compileSchemaValidatorsCode } from '@rjsf/validator-ajv8/compileSchemaValidators';

const schemaPath = fileURLToPath(new URL('../../internal/rules/rule-package.schema.json', import.meta.url));
const outputPath = fileURLToPath(new URL('../src/manage/ruleSchemaValidator.gen.cjs', import.meta.url));
const declarationPath = fileURLToPath(
  new URL('../src/manage/ruleSchemaValidator.gen.d.cts', import.meta.url)
);

const schema = JSON.parse(await readFile(schemaPath, 'utf8'));
const generated = compileSchemaValidatorsCode(schema, {
  AjvClass: Ajv2020,
  ajvFormatOptions: false
});

// 构建时生成并忽略；写入来源标记与 LF。
await writeFile(
  outputPath,
  '// Code generated from internal/rules/rule-package.schema.json. DO NOT EDIT.\n' +
    generated.replace(/\r\n?/g, '\n'),
  'utf8'
);
await writeFile(
  declarationPath,
  "// Code generated from internal/rules/rule-package.schema.json. DO NOT EDIT.\nimport type { ValidatorFunctions } from '@rjsf/validator-ajv8';\n\ndeclare const validatorFunctions: ValidatorFunctions;\nexport = validatorFunctions;\n",
  'utf8'
);
