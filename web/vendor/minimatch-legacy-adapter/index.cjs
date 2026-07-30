'use strict';

/* global module, require */
/* eslint-disable @typescript-eslint/no-require-imports */

// @redocly/openapi-core 1.x expects minimatch 5's callable CommonJS export.
// minimatch 10 is an object with the callable function at `.minimatch`.
const modern = require('minimatch-modern');

function minimatch(path, pattern, options) {
  return modern.minimatch(path, pattern, options);
}

Object.assign(minimatch, modern);
module.exports = minimatch;
