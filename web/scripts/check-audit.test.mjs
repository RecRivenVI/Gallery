import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  analyzeAuditReport,
  parseAuditProcessResult,
  validateAuditReports,
  validateLockState,
  validateMinimatchCompatibility,
  validateOpenApiPremise,
  validateOpenApiToolchain,
  validatePolicy,
  validateWebPremise
} from './check-audit.mjs';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(scriptDir, '..');
const repoRoot = path.resolve(webRoot, '..');

const readJson = async (filePath) => JSON.parse(await readFile(filePath, 'utf8'));
const clone = (value) => JSON.parse(JSON.stringify(value));

const [policyFixture, lockFixture, packageFixture, adapterFixture, fullFixture, productionFixture] =
  await Promise.all([
    readJson(path.join(repoRoot, '.github', 'dependency-audit-exceptions.json')),
    readJson(path.join(webRoot, 'package-lock.json')),
    readJson(path.join(webRoot, 'package.json')),
    readJson(path.join(webRoot, 'vendor', 'minimatch-legacy-adapter', 'package.json')),
    readJson(path.join(scriptDir, 'fixtures', 'npm-audit-full.json')),
    readJson(path.join(scriptDir, 'fixtures', 'npm-audit-production.json'))
  ]);

describe('dependency audit policy', () => {
  it('accepts only the zero-exception reports and locked safe OpenAPI toolchain', () => {
    const policy = validatePolicy(clone(policyFixture), new Date('2030-01-01T00:00:00Z'));
    expect(() => validateLockState(policy, clone(lockFixture))).not.toThrow();
    expect(
      validateOpenApiToolchain(clone(packageFixture), clone(adapterFixture), clone(lockFixture))
    ).toMatchObject({ modernVersion: '10.2.5', braceExpansionVersion: '5.0.8' });
    expect(validateAuditReports(policy, clone(fullFixture), clone(productionFixture))).toEqual([]);
  });

  it('fails when a locked dependency version changes', () => {
    const lock = clone(lockFixture);
    lock.packages['node_modules/minimatch-modern'].version = '10.2.4';
    expect(() => validateOpenApiToolchain(packageFixture, adapterFixture, lock)).toThrow(
      /minimatch-modern 版本已漂移/
    );
  });

  it('fails on an unknown advisory', () => {
    const report = clone(fullFixture);
    report.vulnerabilities['unknown-package'] = {
      name: 'unknown-package',
      severity: 'high',
      isDirect: false,
      via: [
        {
          source: 1,
          name: 'unknown-package',
          url: 'https://github.com/advisories/GHSA-aaaa-bbbb-cccc',
          severity: 'high'
        }
      ],
      effects: [],
      range: '<1.0.0',
      nodes: ['node_modules/unknown-package']
    };
    report.metadata.vulnerabilities.high += 1;
    report.metadata.vulnerabilities.total += 1;
    expect(() => validateAuditReports(policyFixture, report, productionFixture)).toThrow(
      /full npm audit 根 advisory 集合已变化/
    );
  });

  it('never permits critical findings', () => {
    const report = clone(fullFixture);
    report.vulnerabilities['critical-package'] = {
      name: 'critical-package',
      severity: 'critical',
      isDirect: false,
      via: [
        {
          source: 2,
          name: 'critical-package',
          url: 'https://github.com/advisories/GHSA-dddd-eeee-ffff',
          severity: 'critical'
        }
      ],
      effects: [],
      range: '<1.0.0',
      nodes: ['node_modules/critical-package']
    };
    report.metadata.vulnerabilities.critical = 1;
    report.metadata.vulnerabilities.total = 1;
    expect(() => analyzeAuditReport(report, 'fixture')).toThrow(/critical/);
  });

  it('fails when an exception is added back without code authorization', () => {
    const policy = clone(policyFixture);
    policy.exceptions.push({ advisory: 'GHSA-aaaa-bbbb-cccc' });
    expect(() => validatePolicy(policy)).toThrow(/允许的 advisory 集合已变化/);
  });
});

describe('npm audit process boundary', () => {
  it('accepts exit code 0 for a structurally valid zero-finding report', () => {
    expect(
      parseAuditProcessResult(
        { status: 0, signal: null, error: null, stdout: JSON.stringify(fullFixture), stderr: '' },
        'fixture'
      )
    ).toEqual(fullFixture);
  });

  it.each([
    [{ status: 2, signal: null, error: null, stdout: '{}', stderr: '' }, /非漏洞型退出码/],
    [{ status: 1, signal: null, error: null, stdout: 'not-json', stderr: '' }, /不是有效 JSON/],
    [
      {
        status: 1,
        signal: null,
        error: null,
        stdout: JSON.stringify({ error: { code: 'ENOAUDIT', summary: 'network unavailable' } }),
        stderr: ''
      },
      /返回错误对象/
    ]
  ])('fails closed for npm/network/non-JSON errors', (result, expected) => {
    expect(() => parseAuditProcessResult(result, 'fixture')).toThrow(expected);
  });
});

describe('minimatch legacy adapter', () => {
  it('preserves the callable v5 surface over the audited modern implementation', () => {
    const require = createRequire(import.meta.url);
    const minimatch = require('../vendor/minimatch-legacy-adapter');
    expect(validateMinimatchCompatibility(minimatch)).toBe(8);
  });
});

describe('local reachability premises', () => {
  const packageJson = {
    engines: { node: '>=22.22.0' },
    scripts: {
      'generate:api': 'openapi-typescript ../internal/contract/api/openapi.yaml -o src/api/schema.gen.ts'
    },
    dependencies: { 'react-router': '8.3.0' },
    devDependencies: {}
  };

  it('accepts internal OpenAPI refs and rejects external refs', () => {
    expect(
      validateOpenApiPremise(
        packageJson,
        'schema:\n  $ref: "#/components/schemas/Work"\nitems:\n  - $ref: \'#/components/schemas/Media\''
      )
    ).toBe(2);
    expect(() =>
      validateOpenApiPremise(packageJson, 'schema:\n  $ref: "https://example.invalid/schema.yaml"')
    ).toThrow(/外部 \$ref/);
  });

  it('accepts the SPA entrypoint and rejects RSC or SSR APIs', () => {
    const spaEntry =
      "import { BrowserRouter } from 'react-router';\ncreateRoot(root).render(<BrowserRouter />);";
    const valid = {
      packageJson,
      // 双入口：画廊与管理各是一个独立 SPA，前提必须对每一个都成立。
      entrySources: [
        { path: 'src/gallery/main.tsx', content: spaEntry },
        { path: 'src/manage/main.tsx', content: spaEntry }
      ],
      viteConfig: 'export default { build: {} };',
      sourceFiles: [{ path: 'src/gallery/main.tsx', content: "import { Link } from 'react-router';" }]
    };
    expect(() => validateWebPremise(valid)).not.toThrow();
    expect(() =>
      validateWebPremise({
        ...valid,
        packageJson: {
          ...packageJson,
          dependencies: { ...packageJson.dependencies, 'react-router-dom': '7.18.2' }
        }
      })
    ).toThrow(/RSC\/SSR 依赖/);
    expect(() =>
      validateWebPremise({
        ...valid,
        packageJson: { ...packageJson, engines: { node: '>=22.12.0' } }
      })
    ).toThrow(/Node baseline/);
    expect(() =>
      validateWebPremise({
        ...valid,
        entrySources: [
          { path: 'src/gallery/main.tsx', content: spaEntry },
          { path: 'src/manage/main.tsx', content: 'hydrateRoot(root, <App />);' }
        ]
      })
    ).toThrow(/src\/manage\/main\.tsx/);
    expect(() =>
      validateWebPremise({
        ...valid,
        sourceFiles: [
          { path: 'src/server.tsx', content: "import { createRequestHandler } from '@react-router/node';" }
        ]
      })
    ).toThrow(/RSC\/SSR/);
    expect(() =>
      validateWebPremise({
        ...valid,
        sourceFiles: [{ path: 'src/rsc.tsx', content: 'const router = unstable_RSCStaticRouter;' }]
      })
    ).toThrow(/RSC\/Server Action/);
  });
});
