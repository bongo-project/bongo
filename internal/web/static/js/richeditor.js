// richeditor.js — Pure rich-editor helper functions for the Alpine.js richEditor component.
// Loaded as a plain <script> before bongo.js. Exposes window.RichEditor.
// No build step, no CDN, no inline scripts.
(function (global) {
  'use strict';

  var IMAGE_CAP_BYTES = 1 * 1024 * 1024; // 1 MB

  // escapeHTML turns & < > " into safe HTML entities for use inside double-quoted
  // attribute values. Single quotes are intentionally not escaped — insertImageHTML
  // uses double-quoted attributes so this is safe. Any caller that switches to
  // single-quoted attributes MUST also escape apostrophes (replace /'/g with '&#39;').
  function escapeHTML(str) {
    if (typeof str !== 'string') return '';
    return str
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  // buildDataURI assembles a data: URI from a base64 string and MIME type.
  // This is a pure helper exposed on window.RichEditor for testing and direct use.
  // The shipping insertImage path uses fileToDataURI (which gets the full data: URI
  // from FileReader) and does not call buildDataURI internally.
  function buildDataURI(base64, mimeType) {
    return 'data:' + mimeType + ';base64,' + base64;
  }

  // checkImageSize returns true when the file size is within the byte cap.
  function checkImageSize(file, capBytes) {
    return !!(file && file.size <= capBytes);
  }

  // insertImageHTML returns an <img> tag with the dataURI as src and optional altText.
  // Both values are HTML-escaped to prevent injection.
  function insertImageHTML(dataURI, altText) {
    var safeAlt = escapeHTML(altText || '');
    var safeSrc = escapeHTML(dataURI || '');
    return '<img src="' + safeSrc + '" alt="' + safeAlt + '">';
  }

  // fileToDataURI reads a File via FileReader and resolves to a data: URI string.
  // Rejects with an Error if the file exceeds capBytes.
  function fileToDataURI(file, capBytes) {
    return new Promise(function (resolve, reject) {
      if (!checkImageSize(file, capBytes)) {
        reject(new Error('Image exceeds ' + capBytes + ' byte limit'));
        return;
      }
      var reader = new FileReader();
      reader.onload = function (e) {
        resolve(e.target.result);
      };
      reader.onerror = function () {
        reject(new Error('Failed to read image file'));
      };
      reader.readAsDataURL(file);
    });
  }

  // syncToSource returns the current innerHTML from a contenteditable element.
  function syncToSource(editorEl) {
    return editorEl ? editorEl.innerHTML : '';
  }

  // syncFromSource returns the HTML string as-is for setting innerHTML from a textarea.
  function syncFromSource(html) {
    return typeof html === 'string' ? html : '';
  }

  global.RichEditor = {
    IMAGE_CAP_BYTES: IMAGE_CAP_BYTES,
    escapeHTML: escapeHTML,
    buildDataURI: buildDataURI,
    checkImageSize: checkImageSize,
    insertImageHTML: insertImageHTML,
    fileToDataURI: fileToDataURI,
    syncToSource: syncToSource,
    syncFromSource: syncFromSource
  };
}(typeof globalThis !== 'undefined' ? globalThis : typeof window !== 'undefined' ? window : {}));
