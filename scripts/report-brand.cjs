'use strict';

const fs = require('node:fs');
const path = require('node:path');

// Read only the checked-in, approved mark from the trusted renderer source.
// Inline bytes keep downloaded reports self-contained without network requests.
const logo = fs.readFileSync(path.join(__dirname, '../internal/report/aegis-logo.png'));
const logoDataURI = 'data:image/png;base64,' + logo.toString('base64');

function brand() {
  return `<span class="ap-brand-mark" aria-hidden="true"><img src="${logoDataURI}" alt="" width="48" height="48"></span>` +
    '<span class="ap-wordmark">Aegis</span>';
}

module.exports = { brand };
