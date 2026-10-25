function isLocalPage(raw, origin) {
  try { return new URL(raw).origin === origin; } catch { return false; }
}
function externalLink(raw) {
  try {
    const u = new URL(raw);
    return ['https:', 'http:'].includes(u.protocol) && !u.username && !u.password;
  } catch { return false; }
}
function parseReady(line) {
  const prefix = 'BGMCAT_DESKTOP_READY ';
  if (!line.startsWith(prefix)) return null;
  const data = JSON.parse(line.slice(prefix.length));
  const u = new URL(data.url);
  if (u.protocol !== 'http:' || u.hostname !== '127.0.0.1' || !u.port || u.username || u.password ||
      u.pathname !== '/' || u.search || u.hash || typeof data.token !== 'string' || data.token.length < 24) {
    throw new Error('Invalid backend handshake');
  }
  return { origin: u.origin, token: data.token };
}
module.exports = { isLocalPage, externalLink, parseReady };
