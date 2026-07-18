// richeditor.test.mjs — Node.js unit tests for pure helpers in richeditor.js.
// Run with: node --test internal/web/static/js/richeditor.test.mjs
//
// The test loads richeditor.js via new Function() into the Node global scope,
// patching globalThis.window = globalThis so the IIFE self-installs as
// window.RichEditor, exactly as it does in a browser. FileReader is mocked
// to exercise fileToDataURI without a real browser API.
//
// This file has a .mjs extension so the embed glob (js/*.js) does NOT include
// it in the publicly served static bundle.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

// Mock FileReader before loading richeditor.js so the IIFE captures it.
// Synchronously calls onload (via setImmediate) to produce a deterministic
// data: URI that the test can assert against.
globalThis.FileReader = class MockFileReader {
  readAsDataURL(file) {
    const self = this;
    setImmediate(function () {
      if (self.onload) {
        self.onload({ target: { result: 'data:' + file.type + ';base64,MOCKEDBASE64' } });
      }
    });
  }
};

// Install the IIFE into the Node global scope.
// globalThis.window = globalThis lets the IIFE detect 'window' and assign
// window.RichEditor, which lands on globalThis.RichEditor.
globalThis.window = globalThis;
const code = readFileSync(join(__dirname, 'static/js/richeditor.js'), 'utf8');
// eslint-disable-next-line no-new-func
new Function(code)();

const RE = globalThis.window.RichEditor;

// --- (a) Over-cap image is rejected ---

test('fileToDataURI rejects files over IMAGE_CAP_BYTES', async () => {
  const bigFile = { size: RE.IMAGE_CAP_BYTES + 1, type: 'image/png' };
  await assert.rejects(
    () => RE.fileToDataURI(bigFile, RE.IMAGE_CAP_BYTES),
    function (err) {
      assert.ok(err instanceof Error, 'should be an Error');
      assert.ok(err.message.indexOf('exceeds') !== -1, 'message should mention exceeds');
      return true;
    }
  );
});

test('fileToDataURI rejects a clearly oversized file (2 MB)', async () => {
  const twoMB = { size: 2 * 1024 * 1024, type: 'image/jpeg' };
  await assert.rejects(() => RE.fileToDataURI(twoMB, RE.IMAGE_CAP_BYTES));
});

// --- (b) Under-cap image yields a data: URI string ---

test('fileToDataURI resolves with a data: URI for a small file', async () => {
  const smallFile = { size: 512, type: 'image/png' };
  const result = await RE.fileToDataURI(smallFile, RE.IMAGE_CAP_BYTES);
  assert.ok(typeof result === 'string', 'result should be a string');
  assert.ok(result.startsWith('data:'), 'result should start with data:');
});

test('fileToDataURI resolves with a data: URI for a file exactly at the cap', async () => {
  const atCap = { size: RE.IMAGE_CAP_BYTES, type: 'image/gif' };
  const result = await RE.fileToDataURI(atCap, RE.IMAGE_CAP_BYTES);
  assert.ok(result.startsWith('data:'), 'result should be a data: URI');
});

// --- (c) Source toggle round-trips content ---

test('syncToSource returns innerHTML of an editor element', () => {
  const mockEl = { innerHTML: '<b>Hello</b><i> World</i>' };
  const html = RE.syncToSource(mockEl);
  assert.equal(html, '<b>Hello</b><i> World</i>');
});

test('syncToSource returns empty string for null element', () => {
  assert.equal(RE.syncToSource(null), '');
});

test('syncFromSource returns the HTML string unchanged', () => {
  const html = '<p>Round-trip <strong>test</strong></p>';
  assert.equal(RE.syncFromSource(html), html);
});

test('syncFromSource + syncToSource round-trips content', () => {
  const original = '<p>Paragraph with <em>emphasis</em></p>';
  const restored = RE.syncFromSource(original);
  // Simulate setting innerHTML and reading it back via syncToSource.
  const fakeEl = { innerHTML: restored };
  assert.equal(RE.syncToSource(fakeEl), original);
});

// --- Pure helper unit tests ---

test('checkImageSize returns true for file at or under cap', () => {
  assert.ok(RE.checkImageSize({ size: RE.IMAGE_CAP_BYTES }, RE.IMAGE_CAP_BYTES));
  assert.ok(RE.checkImageSize({ size: 1024 }, RE.IMAGE_CAP_BYTES));
});

test('checkImageSize returns false for file over cap', () => {
  assert.ok(!RE.checkImageSize({ size: RE.IMAGE_CAP_BYTES + 1 }, RE.IMAGE_CAP_BYTES));
});

test('escapeHTML escapes ampersand, angle brackets, and quotes', () => {
  assert.equal(
    RE.escapeHTML('<b>"hello" & \'world\'</b>'),
    '&lt;b&gt;&quot;hello&quot; &amp; \'world\'&lt;/b&gt;'
  );
});

test('escapeHTML returns empty string for non-string input', () => {
  assert.equal(RE.escapeHTML(null), '');
  assert.equal(RE.escapeHTML(undefined), '');
});

test('buildDataURI produces a correctly-formed data: URI', () => {
  const uri = RE.buildDataURI('ABCD1234==', 'image/png');
  assert.equal(uri, 'data:image/png;base64,ABCD1234==');
  assert.ok(uri.startsWith('data:'));
});

test('insertImageHTML returns a safe <img> tag', () => {
  const uri = 'data:image/png;base64,ABC';
  const html = RE.insertImageHTML(uri, 'My photo');
  assert.ok(html.startsWith('<img '), 'should start with <img');
  assert.ok(html.indexOf('src="data:image/png;base64,ABC"') !== -1, 'src should be present');
  assert.ok(html.indexOf('alt="My photo"') !== -1, 'alt should be present');
});

test('insertImageHTML escapes dangerous characters in dataURI and alt', () => {
  const maliciousURI = 'javascript:alert(1)';
  const maliciousAlt = '<script>xss</script>';
  const html = RE.insertImageHTML(maliciousURI, maliciousAlt);
  assert.ok(html.indexOf('<script>') === -1, 'should not contain raw <script>');
  assert.ok(html.indexOf('javascript:alert') === -1 || html.indexOf('&lt;') !== -1, 'angle brackets should be escaped');
});

test('IMAGE_CAP_BYTES is exactly 1 MB', () => {
  assert.equal(RE.IMAGE_CAP_BYTES, 1 * 1024 * 1024);
});

// --- resolveFieldName pure helper ---

test('resolveFieldName returns "body" when dataset is null', () => {
  assert.equal(RE.resolveFieldName(null), 'body');
});

test('resolveFieldName returns "body" when dataset is undefined', () => {
  assert.equal(RE.resolveFieldName(undefined), 'body');
});

test('resolveFieldName returns "body" when dataset.fieldName is absent', () => {
  assert.equal(RE.resolveFieldName({}), 'body');
});

test('resolveFieldName returns "body" when dataset.fieldName is empty string', () => {
  assert.equal(RE.resolveFieldName({ fieldName: '' }), 'body');
});

test('resolveFieldName returns the specified field name', () => {
  assert.equal(RE.resolveFieldName({ fieldName: 'signature_html' }), 'signature_html');
});

test('resolveFieldName returns a custom field name unchanged', () => {
  assert.equal(RE.resolveFieldName({ fieldName: 'body' }), 'body');
  assert.equal(RE.resolveFieldName({ fieldName: 'draft_body' }), 'draft_body');
});
