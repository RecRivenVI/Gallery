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

// 生成文件进入 Git，必须与仓库统一为 LF；AJV standalone 本身不承担工作树换行策略。
await writeFile(outputPath, generated.replace(/\r\n?/g, '\n'), 'utf8');
await writeFile(
  declarationPath,
  "import type { ValidatorFunctions } from '@rjsf/validator-ajv8';\n\ndeclare const validatorFunctions: ValidatorFunctions;\nexport = validatorFunctions;\n",
  'utf8'
);
