const { test } = require('node:test');
const assert = require('node:assert/strict');
const { isLocalPage, externalLink, parseReady } = require('./policy.cjs');
test('credentials are restricted to the exact owned loopback origin', () => {
 const origin = 'http://127.0.0.1:39123';
 assert.equal(isLocalPage(origin + '/service', origin), true);
 for (const url of ['https://example.com', 'http://127.0.0.1:3001', 'http://127.0.0.1.evil.test:39123', 'file:///tmp/page']) assert.equal(isLocalPage(url, origin), false);
});
test('external links reject executable schemes', () => {
 for (const url of ['javascript:alert(1)', 'file:///tmp/app', 'data:text/html,test', 'https://user:pass@example.com']) assert.equal(externalLink(url), false);
 assert.equal(externalLink('https://bgm.tv/subject/1'), true);
});
test('ready handshake validates address and credential', () => {
 const token = 'a'.repeat(32);
 assert.equal(parseReady('unrelated output'), null);
 assert.deepEqual(parseReady('BGMCAT_DESKTOP_READY ' + JSON.stringify({url:'http://127.0.0.1:39123',token})), {origin:'http://127.0.0.1:39123',token});
 for (const url of ['https://example.com', 'http://0.0.0.0:39123', 'http://127.0.0.1:39123/?token=x']) assert.throws(() => parseReady('BGMCAT_DESKTOP_READY ' + JSON.stringify({url,token})));
});
