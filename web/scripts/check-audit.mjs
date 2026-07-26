import { spawnSync } from 'node:child_process';
import { Console } from 'node:console';
import { readdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const output = new Console({ stdout: process.stdout, stderr: process.stderr });
const scriptPath = fileURLToPath(import.meta.url);
const scriptDir = path.dirname(scriptPath);
const webRoot = path.resolve(scriptDir, '..');
const repoRoot = path.resolve(webRoot, '..');

const POLICY_PATH = path.join(repoRoot, '.github', 'dependency-audit-exceptions.json');
const LOCK_PATH = path.join(webRoot, 'package-lock.json');
const PACKAGE_PATH = path.join(webRoot, 'package.json');
const OPENAPI_PATH = path.join(repoRoot, 'internal', 'contract', 'api', 'openapi.yaml');
const SOURCE_ROOT = path.join(webRoot, 'src');
// 双入口：画廊与管理是两个独立 SPA，`web-spa-no-rsc-ssr` 这条静态前提必须对**每一个**入口
// 成立。只检查其中一个等于给另一个开了后门。
const ENTRY_PATHS = [
  path.join(SOURCE_ROOT, 'gallery', 'main.tsx'),
  path.join(SOURCE_ROOT, 'manage', 'main.tsx')
];
const VITE_CONFIG_PATH = path.join(webRoot, 'vite.config.ts');
const MAX_EXCEPTION_EXPIRY = '2026-08-09';

const permittedExceptions = new Map([
  ['GHSA-mh99-v99m-4gvg', { scope: 'dev', premises: ['openapi-internal-refs-only'] }],
  ['GHSA-qwww-vcr4-c8h2', { scope: 'production', premises: ['web-spa-no-rsc-ssr'] }]
]);

const sourceExtensions = new Set(['.cjs', '.js', '.jsx', '.mjs', '.ts', '.tsx']);

export class AuditGateError extends Error {
  constructor(message) {
    super(message);
    this.name = 'AuditGateError';
  }
}

function assertGate(condition, message) {
  if (!condition) throw new AuditGateError(message);
}

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function sortedUnique(values) {
  return [...new Set(values)].sort((left, right) => left.localeCompare(right));
}

function assertString(value, label) {
  assertGate(typeof value === 'string' && value.length > 0, `${label} 必须是非空字符串`);
  return value;
}

function assertStringArray(value, label) {
  assertGate(Array.isArray(value), `${label} 必须是数组`);
  for (const [index, item] of value.entries()) assertString(item, `${label}[${index}]`);
  assertGate(value.length === new Set(value).size, `${label} 不得包含重复项`);
  return value;
}

function assertSameStrings(actual, expected, label) {
  const actualSorted = sortedUnique(actual);
  const expectedSorted = sortedUnique(expected);
  assertGate(
    JSON.stringify(actualSorted) === JSON.stringify(expectedSorted),
    `${label}已变化：期望 ${JSON.stringify(expectedSorted)}，实际 ${JSON.stringify(actualSorted)}`
  );
}

function parseJson(text, label) {
  try {
    return JSON.parse(text);
  } catch (error) {
    throw new AuditGateError(
      `${label} 不是有效 JSON：${error instanceof Error ? error.message : String(error)}`
    );
  }
}

async function readJson(filePath, label) {
  return parseJson(await readFile(filePath, 'utf8'), label);
}

export function validatePolicy(policy, now = new Date()) {
  assertGate(isRecord(policy), '依赖审计例外文件必须是 JSON object');
  assertGate(policy.schemaVersion === 1, `不支持的例外 schemaVersion：${String(policy.schemaVersion)}`);
  assertGate(policy.manifest === 'web/package-lock.json', '例外 manifest 必须锁定为 web/package-lock.json');
  assertGate(Array.isArray(policy.exceptions), '例外文件缺少 exceptions 数组');

  const currentDate = now.toISOString().slice(0, 10);
  const actualIds = policy.exceptions.map((entry, index) =>
    assertString(entry?.advisory, `exceptions[${index}].advisory`)
  );
  assertSameStrings(actualIds, [...permittedExceptions.keys()], '允许的 advisory 集合');

  for (const [index, exception] of policy.exceptions.entries()) {
    assertGate(isRecord(exception), `exceptions[${index}] 必须是 object`);
    const prefix = `exceptions[${index}]`;
    const expected = permittedExceptions.get(exception.advisory);
    assertGate(expected, `${prefix} 包含未获代码级授权的 advisory：${exception.advisory}`);
    assertGate(exception.scope === expected.scope, `${exception.advisory} 的 scope 必须为 ${expected.scope}`);
    assertGate(
      /^\d{4}-\d{2}-\d{2}$/.test(exception.expiresOn),
      `${exception.advisory} 的 expiresOn 必须是 YYYY-MM-DD`
    );
    assertGate(
      exception.expiresOn === MAX_EXCEPTION_EXPIRY,
      `${exception.advisory} 的 expiresOn 不得偏离代码级上限 ${MAX_EXCEPTION_EXPIRY}`
    );
    assertGate(
      currentDate <= exception.expiresOn,
      `${exception.advisory} 的临时例外已于 ${exception.expiresOn} 到期`
    );
    assertString(exception.reason, `${prefix}.reason`);
    assertSameStrings(
      assertStringArray(exception.premises, `${prefix}.premises`),
      expected.premises,
      `${exception.advisory} 的本地静态前提`
    );

    const fullPackages = assertStringArray(exception.fullPackages, `${prefix}.fullPackages`);
    const productionPackages = assertStringArray(
      exception.productionPackages,
      `${prefix}.productionPackages`
    );
    assertGate(fullPackages.length > 0, `${exception.advisory} 必须声明 fullPackages`);
    if (exception.scope === 'dev') {
      assertGate(
        productionPackages.length === 0,
        `${exception.advisory} 是 dev-only，productionPackages 必须为空`
      );
    } else {
      assertGate(
        productionPackages.length > 0,
        `${exception.advisory} 是 production，必须声明 productionPackages`
      );
      assertGate(
        productionPackages.every((name) => fullPackages.includes(name)),
        `${exception.advisory} 的 productionPackages 必须是 fullPackages 子集`
      );
    }

    assertGate(isRecord(exception.rootDependency), `${prefix}.rootDependency 必须是 object`);
    assertString(exception.rootDependency.name, `${prefix}.rootDependency.name`);
    assertGate(
      exception.rootDependency.section === 'dependencies' ||
        exception.rootDependency.section === 'devDependencies',
      `${prefix}.rootDependency.section 无效`
    );
    assertString(exception.rootDependency.specifier, `${prefix}.rootDependency.specifier`);
    assertGate(
      Array.isArray(exception.lockChain) && exception.lockChain.length > 0,
      `${prefix}.lockChain 不能为空`
    );
    for (const [chainIndex, item] of exception.lockChain.entries()) {
      assertGate(isRecord(item), `${prefix}.lockChain[${chainIndex}] 必须是 object`);
      assertString(item.name, `${prefix}.lockChain[${chainIndex}].name`);
      assertString(item.path, `${prefix}.lockChain[${chainIndex}].path`);
      assertString(item.version, `${prefix}.lockChain[${chainIndex}].version`);
    }
    assertGate(
      exception.lockChain[0].name === exception.rootDependency.name,
      `${exception.advisory} 的 lockChain 必须从 rootDependency 开始`
    );
    assertSameStrings(
      exception.lockChain.map((item) => item.name),
      fullPackages,
      `${exception.advisory} 的锁定链 package 集合`
    );
  }

  return policy;
}

export function validateLockState(policy, lock) {
  assertGate(isRecord(lock), 'package-lock.json 必须是 JSON object');
  assertGate(lock.lockfileVersion === 3, `只接受 lockfileVersion 3，实际为 ${String(lock.lockfileVersion)}`);
  assertGate(isRecord(lock.packages), 'package-lock.json 缺少 packages object');
  const root = lock.packages[''];
  assertGate(isRecord(root), 'package-lock.json 缺少根 package entry');

  for (const exception of policy.exceptions) {
    const rootSection = root[exception.rootDependency.section];
    assertGate(
      isRecord(rootSection),
      `${exception.advisory} 的根依赖区 ${exception.rootDependency.section} 不存在`
    );
    assertGate(
      rootSection[exception.rootDependency.name] === exception.rootDependency.specifier,
      `${exception.advisory} 根依赖 ${exception.rootDependency.name} 已变化：期望 ${exception.rootDependency.specifier}，实际 ${String(rootSection[exception.rootDependency.name])}`
    );

    for (const [index, item] of exception.lockChain.entries()) {
      const locked = lock.packages[item.path];
      assertGate(isRecord(locked), `${exception.advisory} 锁定节点不存在：${item.path}`);
      assertGate(
        locked.version === item.version,
        `${exception.advisory} 锁定节点 ${item.name} 已变化：期望 ${item.version}，实际 ${String(locked.version)}`
      );
      const next = exception.lockChain[index + 1];
      if (next) {
        assertGate(
          isRecord(locked.dependencies) && typeof locked.dependencies[next.name] === 'string',
          `${exception.advisory} 锁定链断裂：${item.name} 不再直接依赖 ${next.name}`
        );
      }
    }
  }
}

function validateAuditReportShape(report, label) {
  assertGate(isRecord(report), `${label} npm audit 报告必须是 object`);
  assertGate(!('error' in report), `${label} npm audit 返回错误对象`);
  assertGate(report.auditReportVersion === 2, `${label} auditReportVersion 必须为 2`);
  assertGate(isRecord(report.vulnerabilities), `${label} 缺少 vulnerabilities object`);
  assertGate(isRecord(report.metadata?.vulnerabilities), `${label} 缺少 metadata.vulnerabilities`);
  const packageCount = Object.keys(report.vulnerabilities).length;
  assertGate(
    report.metadata.vulnerabilities.total === packageCount,
    `${label} vulnerabilities.total 与 package 节点数不一致`
  );
  assertGate(
    Number(report.metadata.vulnerabilities.critical ?? 0) === 0,
    `${label} 出现 critical 漏洞，临时例外不得放行`
  );
}

export function parseAuditProcessResult(result, label) {
  assertGate(!result.error, `${label} npm audit 无法启动：${result.error?.message ?? 'unknown error'}`);
  assertGate(!result.signal, `${label} npm audit 被信号 ${result.signal} 终止`);
  assertGate(
    result.status === 0 || result.status === 1,
    `${label} npm audit 非漏洞型退出码：${String(result.status)}`
  );
  const report = parseJson(String(result.stdout ?? '').trim(), `${label} npm audit stdout`);
  validateAuditReportShape(report, label);
  const hasVulnerabilities = Object.keys(report.vulnerabilities).length > 0;
  assertGate(result.status === (hasVulnerabilities ? 1 : 0), `${label} npm audit 退出码与报告漏洞状态不一致`);
  return report;
}

function extractAdvisoryId(via, packageName, label) {
  assertGate(isRecord(via), `${label} ${packageName} 包含无法识别的 via entry`);
  assertGate(via.severity !== 'critical', `${label} ${packageName} 的根 advisory 为 critical`);
  const match = typeof via.url === 'string' ? via.url.match(/\/advisories\/(GHSA-[0-9a-z-]+)/i) : null;
  assertGate(match, `${label} ${packageName} 的 advisory 缺少可审计 GHSA URL`);
  return match[1];
}

export function analyzeAuditReport(report, label) {
  validateAuditReportShape(report, label);
  const vulnerabilities = report.vulnerabilities;
  const memo = new Map();

  const resolve = (packageName, stack = []) => {
    if (memo.has(packageName)) return memo.get(packageName);
    assertGate(
      !stack.includes(packageName),
      `${label} npm audit via 出现循环：${[...stack, packageName].join(' -> ')}`
    );
    const vulnerability = vulnerabilities[packageName];
    assertGate(isRecord(vulnerability), `${label} via 引用了缺失 package：${packageName}`);
    assertGate(vulnerability.severity !== 'critical', `${label} ${packageName} 为 critical，禁止例外`);
    assertGate(
      Array.isArray(vulnerability.via) && vulnerability.via.length > 0,
      `${label} ${packageName} 缺少 via`
    );
    assertGate(
      Array.isArray(vulnerability.nodes) &&
        vulnerability.nodes.length > 0 &&
        vulnerability.nodes.every((node) => typeof node === 'string' && node.length > 0),
      `${label} ${packageName} 缺少有效 nodes`
    );

    const advisoryIds = new Set();
    for (const via of vulnerability.via) {
      if (typeof via === 'string') {
        for (const advisory of resolve(via, [...stack, packageName])) advisoryIds.add(advisory);
      } else {
        advisoryIds.add(extractAdvisoryId(via, packageName, label));
      }
    }
    assertGate(advisoryIds.size > 0, `${label} ${packageName} 无法解析到根 advisory`);
    memo.set(packageName, advisoryIds);
    return advisoryIds;
  };

  const packagesByAdvisory = new Map();
  for (const packageName of Object.keys(vulnerabilities)) {
    for (const advisory of resolve(packageName)) {
      const packages = packagesByAdvisory.get(advisory) ?? new Set();
      packages.add(packageName);
      packagesByAdvisory.set(advisory, packages);
    }
  }
  return { packagesByAdvisory };
}

export function validateAuditReports(policy, fullReport, productionReport) {
  const full = analyzeAuditReport(fullReport, 'full');
  const production = analyzeAuditReport(productionReport, 'production-only');
  const expectedIds = policy.exceptions.map((entry) => entry.advisory);
  const fullIds = [...full.packagesByAdvisory.keys()];
  const productionIds = [...production.packagesByAdvisory.keys()];
  assertSameStrings(fullIds, expectedIds, 'full npm audit 根 advisory 集合');
  assertGate(
    productionIds.every((id) => full.packagesByAdvisory.has(id)),
    'production-only npm audit 出现 full 报告中不存在的 advisory'
  );

  for (const exception of policy.exceptions) {
    const fullPackages = [...(full.packagesByAdvisory.get(exception.advisory) ?? [])];
    const productionPackages = [...(production.packagesByAdvisory.get(exception.advisory) ?? [])];
    assertSameStrings(fullPackages, exception.fullPackages, `${exception.advisory} full package 传播范围`);
    assertSameStrings(
      productionPackages,
      exception.productionPackages,
      `${exception.advisory} production package 传播范围`
    );
    if (exception.scope === 'dev') {
      assertGate(productionPackages.length === 0, `${exception.advisory} 已从 dev-only 漂移到 production`);
    } else {
      assertGate(productionPackages.length > 0, `${exception.advisory} 不再符合锁定的 production scope`);
    }
  }

  return policy.exceptions.map((entry) => ({
    advisory: entry.advisory,
    scope: entry.scope,
    expiresOn: entry.expiresOn
  }));
}

export function validateOpenApiPremise(packageJson, openApiText) {
  assertGate(
    packageJson.scripts?.['generate:api'] ===
      'openapi-typescript ../internal/contract/api/openapi.yaml -o src/api/schema.gen.ts',
    'generate:api 不再只读取锁定的仓库内 OpenAPI 文件'
  );
  let refCount = 0;
  for (const line of openApiText.split(/\r?\n/)) {
    const occurrenceCount = line.match(/\$ref:/g)?.length ?? 0;
    if (occurrenceCount === 0) continue;
    const matches = [...line.matchAll(/\$ref:\s*(?:"([^"]+)"|'([^']+)')/g)];
    assertGate(matches.length === occurrenceCount, `OpenAPI 出现无法静态判定的 $ref：${line.trim()}`);
    for (const match of matches) {
      const reference = match[1] ?? match[2];
      assertGate(reference.startsWith('#/'), `OpenAPI 出现外部 $ref：${reference}`);
      refCount += 1;
    }
  }
  assertGate(refCount > 0, 'OpenAPI 未发现可验证的 $ref，静态前提无法成立');
  return refCount;
}

const forbiddenWebPatterns = [
  {
    pattern:
      /(?:from\s*|import\s*\()\s*['"](?:react-router\/rsc|@react-router\/(?:dev|node|serve)|react-server-dom(?:-[a-z-]+)?|react-dom\/server)['"]/,
    reason: 'RSC/SSR package import'
  },
  {
    pattern:
      /\b(?:HydratedRouter|RSCHydratedRouter|RSCStaticRouter|ServerRouter|StaticRouter|StaticRouterProvider|createCallServer|createRequestHandler|createStaticHandler|createStaticRouter|decodeAction|decodeFormState|decodeReply|renderToPipeableStream|renderToReadableStream)\b/,
    reason: 'RSC/SSR API'
  },
  {
    pattern: /\b[A-Za-z0-9_]*(?:RSC|CallServer|ServerAction)[A-Za-z0-9_]*\b/,
    reason: 'RSC/Server Action identifier'
  },
  { pattern: /^\s*['"]use server['"]\s*;?/m, reason: 'Server Action directive' },
  { pattern: /\bhydrateRoot\s*\(/, reason: 'SSR hydration entrypoint' }
];

export function validateWebPremise({ packageJson, entrySources, viteConfig, sourceFiles }) {
  assertGate(
    packageJson.dependencies?.['react-router-dom'] === '7.18.1',
    'react-router-dom 不再锁定为 7.18.1'
  );
  const allDependencies = {
    ...(packageJson.dependencies ?? {}),
    ...(packageJson.devDependencies ?? {})
  };
  const forbiddenDependencies = Object.keys(allDependencies).filter(
    (name) =>
      name.startsWith('@react-router/') ||
      name === 'react-server-dom-webpack' ||
      name === 'react-server-dom-parcel' ||
      name === 'react-server-dom-turbopack'
  );
  assertGate(
    forbiddenDependencies.length === 0,
    `Web 增加了 RSC/SSR 依赖：${forbiddenDependencies.join(', ')}`
  );
  const scriptCommands = Object.values(packageJson.scripts ?? {}).join('\n');
  assertGate(
    !/(?:react-router|remix)\s+(?:build|dev|serve)\b|(?:^|\s)--ssr(?:\s|$)/m.test(scriptCommands),
    'Web package scripts 出现 RSC/SSR 构建或服务入口'
  );
  assertGate(Array.isArray(entrySources) && entrySources.length > 0, 'Web 入口列表为空');
  for (const entry of entrySources) {
    assertString(entry?.path, 'entrySources[].path');
    assertString(entry?.content, `${entry?.path} 入口内容`);
    assertGate(
      entry.content.includes("import { BrowserRouter } from 'react-router-dom';"),
      `${entry.path} 不再使用 BrowserRouter SPA`
    );
    assertGate(
      entry.content.includes('createRoot(root).render('),
      `${entry.path} 不再使用纯客户端 createRoot`
    );
  }
  assertGate(!/\bssr\s*:/.test(viteConfig), 'Vite 配置出现 SSR build 入口');

  for (const source of sourceFiles) {
    for (const forbidden of forbiddenWebPatterns) {
      assertGate(!forbidden.pattern.test(source.content), `${source.path} 出现 ${forbidden.reason}`);
    }
  }
}

async function collectSourceFiles(rootPath) {
  const result = [];
  const visit = async (currentPath) => {
    const entries = await readdir(currentPath, { withFileTypes: true });
    for (const entry of entries) {
      const entryPath = path.join(currentPath, entry.name);
      assertGate(!entry.isSymbolicLink(), `Web source 不允许符号链接：${path.relative(webRoot, entryPath)}`);
      if (entry.isDirectory()) {
        await visit(entryPath);
      } else if (entry.isFile() && sourceExtensions.has(path.extname(entry.name))) {
        result.push({
          path: path.relative(webRoot, entryPath).replaceAll(path.sep, '/'),
          content: await readFile(entryPath, 'utf8')
        });
      }
    }
  };
  await visit(rootPath);
  return result.sort((left, right) => left.path.localeCompare(right.path));
}

async function validateRepositoryPremises(packageJson) {
  const openApiText = await readFile(OPENAPI_PATH, 'utf8');
  const internalRefCount = validateOpenApiPremise(packageJson, openApiText);
  const sourceFiles = await collectSourceFiles(SOURCE_ROOT);
  const entrySources = await Promise.all(
    ENTRY_PATHS.map(async (entryPath) => ({
      path: path.relative(webRoot, entryPath).replaceAll(path.sep, '/'),
      content: await readFile(entryPath, 'utf8')
    }))
  );
  validateWebPremise({
    packageJson,
    entrySources,
    viteConfig: await readFile(VITE_CONFIG_PATH, 'utf8'),
    sourceFiles
  });
  return { internalRefCount, sourceFileCount: sourceFiles.length };
}

function runNpmAudit(extraArgs) {
  const npmExecPath = process.env.npm_execpath;
  const command = npmExecPath ? process.execPath : process.platform === 'win32' ? 'npm.cmd' : 'npm';
  const args = npmExecPath
    ? [npmExecPath, 'audit', '--json', '--audit-level=low', ...extraArgs]
    : ['audit', '--json', '--audit-level=low', ...extraArgs];
  return spawnSync(command, args, {
    cwd: webRoot,
    encoding: 'utf8',
    maxBuffer: 16 * 1024 * 1024,
    shell: false
  });
}

export async function runAuditGate({ now = new Date(), auditRunner = runNpmAudit } = {}) {
  const policy = validatePolicy(await readJson(POLICY_PATH, '依赖审计例外文件'), now);
  const lock = await readJson(LOCK_PATH, 'web/package-lock.json');
  const packageJson = await readJson(PACKAGE_PATH, 'web/package.json');
  validateLockState(policy, lock);
  const premises = await validateRepositoryPremises(packageJson);
  const fullReport = parseAuditProcessResult(auditRunner([]), 'full');
  const productionReport = parseAuditProcessResult(auditRunner(['--omit=dev']), 'production-only');
  const exceptions = validateAuditReports(policy, fullReport, productionReport);
  return { exceptions, premises };
}

const invokedPath = process.argv[1] ? path.resolve(process.argv[1]) : '';
if (invokedPath === scriptPath) {
  try {
    const result = await runAuditGate();
    output.log(
      `依赖审计通过：${result.exceptions.length} 条有期限例外；OpenAPI 内部引用 ${result.premises.internalRefCount} 个；Web source ${result.premises.sourceFileCount} 个。`
    );
    for (const exception of result.exceptions) {
      output.log(`- ${exception.advisory}: ${exception.scope}, expires ${exception.expiresOn}`);
    }
  } catch (error) {
    output.error(`依赖审计失败：${error instanceof Error ? error.message : String(error)}`);
    process.exitCode = 1;
  }
}
