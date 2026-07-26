import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  analyzeAuditReport,
  parseAuditProcessResult,
  validateAuditReports,
  validateLockState,
  validateOpenApiPremise,
  validatePolicy,
  validateWebPremise
} from './check-audit.mjs';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(scriptDir, '..');
const repoRoot = path.resolve(webRoot, '..');

const readJson = async (filePath) => JSON.parse(await readFile(filePath, 'utf8'));
const clone = (value) => JSON.parse(JSON.stringify(value));

const [policyFixture, lockFixture, fullFixture, productionFixture] = await Promise.all([
  readJson(path.join(repoRoot, '.github', 'dependency-audit-exceptions.json')),
  readJson(path.join(webRoot, 'package-lock.json')),
  readJson(path.join(scriptDir, 'fixtures', 'npm-audit-full.json')),
  readJson(path.join(scriptDir, 'fixtures', 'npm-audit-production.json'))
]);

describe('dependency audit policy', () => {
  it('accepts only the locked reports, versions and scopes before expiry', () => {
    const policy = validatePolicy(clone(policyFixture), new Date('2026-07-26T00:00:00Z'));
    expect(() => validateLockState(policy, clone(lockFixture))).not.toThrow();
    expect(validateAuditReports(policy, clone(fullFixture), clone(productionFixture))).toEqual([
      { advisory: 'GHSA-mh99-v99m-4gvg', scope: 'dev', expiresOn: '2026-08-09' },
      { advisory: 'GHSA-qwww-vcr4-c8h2', scope: 'production', expiresOn: '2026-08-09' }
    ]);
  });

  it('fails when the exception expires', () => {
    expect(() => validatePolicy(clone(policyFixture), new Date('2026-08-10T00:00:00Z'))).toThrow(
      /已于 2026-08-09 到期/
    );
  });

  it('fails when a locked dependency version changes', () => {
    const lock = clone(lockFixture);
    lock.packages['node_modules/react-router'].version = '7.18.2';
    expect(() => validateLockState(policyFixture, lock)).toThrow(/react-router 已变化/);
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
    report.vulnerabilities['react-router'].severity = 'critical';
    report.metadata.vulnerabilities.critical = 1;
    expect(() => analyzeAuditReport(report, 'fixture')).toThrow(/critical/);
  });

  it('fails when a dev-only advisory reaches production', () => {
    expect(() => validateAuditReports(policyFixture, fullFixture, fullFixture)).toThrow(
      /production package 传播范围已变化/
    );
  });
});

describe('npm audit process boundary', () => {
  it('accepts exit code 1 only for a structurally valid vulnerability report', () => {
    expect(
      parseAuditProcessResult(
        { status: 1, signal: null, error: null, stdout: JSON.stringify(fullFixture), stderr: '' },
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

describe('local reachability premises', () => {
  const packageJson = {
    scripts: {
      'generate:api': 'openapi-typescript ../internal/contract/api/openapi.yaml -o src/api/schema.gen.ts'
    },
    dependencies: { 'react-router-dom': '7.18.1' },
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
      "import { BrowserRouter } from 'react-router-dom';\ncreateRoot(root).render(<BrowserRouter />);";
    const valid = {
      packageJson,
      // 双入口：画廊与管理各是一个独立 SPA，前提必须对每一个都成立。
      entrySources: [
        { path: 'src/gallery/main.tsx', content: spaEntry },
        { path: 'src/manage/main.tsx', content: spaEntry }
      ],
      viteConfig: 'export default { build: {} };',
      sourceFiles: [{ path: 'src/gallery/main.tsx', content: "import { Link } from 'react-router-dom';" }]
    };
    expect(() => validateWebPremise(valid)).not.toThrow();
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
