// fragment.ts - URL-fragment codec for the log viewer. Everything the viewer loads rides
// the #-fragment (never transmitted to a server): a magus.viewer.v1 Journal is gzip+base64url
// encoded (matches internal/render EncodeFragmentRaw), and the deep-link parameters (ref,
// data, src, live, token) are parsed out of it here. All local: nothing is fetched, nothing
// is sent.

// The parsed #-fragment parameters (ref/data/src/live/token/...).
export type ViewerParams = Record<string, string>;

// --- Fragment decode (matches internal/render EncodeFragmentRaw) --------------
// base64url -> bytes -> gunzip -> text. DecompressionStream is widely supported;
// the whole path is local, so nothing is fetched and nothing is sent.
export async function decodeFragment(b64url: string): Promise<string> {
  return new Response(await gunzipFragment(b64url)).text();
}

// decodeFragmentBytes is the binary sibling: base64url -> gunzip -> Uint8Array, for the
// protobuf Journal payload (decodeFragment layers a text decode on top for legacy text).
export async function decodeFragmentBytes(b64url: string): Promise<Uint8Array> {
  return new Uint8Array(await new Response(await gunzipFragment(b64url)).arrayBuffer());
}

function gunzipFragment(b64url: string): ReadableStream<Uint8Array> {
  const b64 = b64url.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new Response(bytes).body!.pipeThrough(new DecompressionStream("gzip"));
}

// --- Fragment encode (the inverse of decodeFragmentBytes) ---------------------
// bytes -> gzip -> base64url. Mirrors internal/render EncodeFragmentRaw so a link built
// here round-trips through the same decode path. Local only: the Share button never leaves
// the page. base64url = RawURLEncoding (base64, then +/- and //_ swaps, no "=" padding).
export async function encodeFragmentBytes(bytes: Uint8Array): Promise<string> {
  const stream = new Response(bytes).body!.pipeThrough(new CompressionStream("gzip"));
  const gz = new Uint8Array(await new Response(stream).arrayBuffer());
  let bin = "";
  for (let i = 0; i < gz.length; i++) bin += String.fromCharCode(gz[i]);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// viewerParams reads the deep-link parameters from the URL fragment (after #). EVERYTHING -
// the ref id, the encoded log (data), the live host and bearer token - rides the fragment,
// which the browser never transmits to any server, so nothing about the run ever leaves the
// machine. That absolute guarantee is why no parameter uses the query string.
export function viewerParams(): ViewerParams {
  const out: ViewerParams = {};
  for (const part of location.hash.replace(/^#/, "").split("&")) {
    const eq = part.indexOf("=");
    if (eq < 0) continue;
    out[part.slice(0, eq)] = decodeURIComponent(part.slice(eq + 1));
  }
  return out;
}

// base64ToBytes decodes a base64 (standard alphabet) string to bytes - the framing the live
// SSE stream uses for each protobuf Event.
export function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}
