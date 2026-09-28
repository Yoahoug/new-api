// OpenCode Session Header repairs upstreams that require a stable per-session
// request header. OpenCode's Go client sends one (X-OpenCode-Session and its
// many client spellings), but some client builds only send a session-affinity
// cookie or nothing at all; the upstream then fragments one conversation across
// per-request sessions. This interceptor re-derives a stable session id from
// the first recognized header and injects it as X-OpenCode-Session before the
// request leaves the gateway.
export const meta = {
  apiVersion: 1,
  key: "opencode-session-header",
  name: "OpenCode Session Header",
  version: "1.0.0",
  author: { name: "QuantumNous" },
  description: {
    en: "Inject a stable OpenCode session header for session-affine upstreams",
    zh: "为会话亲和上游注入稳定的 OpenCode 会话请求头",
  },
  requiredCapabilities: ["request-interceptor@1"],
  interceptor: {
    priority: 100,
    methods: ["POST"],
    intercept: "intercept",
    complete: "onComplete",
    configFields: [
      {
        name: "headerName",
        type: "string",
        description: { en: "Header to inject", zh: "要注入的请求头名称" },
      },
      {
        name: "sourceHeaders",
        type: "array",
        description: {
          en: "Candidate inbound headers to read the session id from",
          zh: "用于读取会话 ID 的入站请求头候选列表",
        },
      },
      {
        name: "models",
        type: "array",
        description: {
          en: "Restrict to these upstream models; empty means all",
          zh: "仅对这些上游模型生效，留空表示全部",
        },
      },
      {
        name: "baseUrlMarkers",
        type: "array",
        description: {
          en: "Restrict to upstream URLs containing one of these markers",
          zh: "仅对包含这些关键字的上游地址生效",
        },
      },
      {
        name: "hashDerivedSessions",
        type: "boolean",
        description: {
          en: "Derive the session id by hashing the source value",
          zh: "是否对来源值做哈希得到会话 ID",
        },
      },
      {
        name: "fallbackToRequestId",
        type: "boolean",
        description: {
          en: "Fall back to the gateway request id when no source header exists",
          zh: "无来源请求头时回退为网关请求 ID",
        },
      },
    ],
  },
};

const DEFAULT_HEADER = "X-OpenCode-Session";
const DEFAULT_SOURCES = [
  "x-opencode-session",
  "x-session-affinity",
  "session-id",
  "x-session-id",
  "x-http-session-id",
  "x-client-request-id",
  "x-conversation-id",
  "x-thread-id",
  "x-deepseek-session-id",
  "x-zcode-session-id",
];

function configList(config, name, fallback) {
  const raw = config && config[name];
  if (Array.isArray(raw) && raw.length > 0) {
    return raw.map(function (item) {
      return String(item || "").trim().toLowerCase();
    });
  }
  return fallback;
}

function configBool(config, name, fallback) {
  const raw = config && config[name];
  return typeof raw === "boolean" ? raw : fallback;
}

function modelAllowed(config, upstreamModel) {
  const models = configList(config, "models", []);
  if (models.length === 0) {
    return true;
  }
  return models.indexOf(String(upstreamModel || "").toLowerCase()) >= 0;
}

function baseUrlAllowed(config, baseUrl) {
  const markers = configList(config, "baseUrlMarkers", []);
  if (markers.length === 0) {
    return true;
  }
  const haystack = String(baseUrl || "").toLowerCase();
  for (const marker of markers) {
    if (haystack.indexOf(marker) >= 0) {
      return true;
    }
  }
  return false;
}

// FNV-1a produces a stable, dependency-free digest of the source session
// value; the upstream only needs a stable opaque token, not the original.
function stableHash(text) {
  let hash = 0x811c9dc5;
  for (let index = 0; index < text.length; index++) {
    hash ^= text.charCodeAt(index);
    hash = (hash * 0x01000193) >>> 0;
  }
  return "sess-" + hash.toString(16).padStart(8, "0");
}

export function intercept(ctx) {
  const config = ctx.config || {};
  if (!modelAllowed(config, ctx.upstreamModel) || !baseUrlAllowed(config, ctx.upstreamBaseUrl)) {
    return { body: null };
  }
  const headerName = String(config.headerName || DEFAULT_HEADER).trim() || DEFAULT_HEADER;
  const sources = configList(config, "sourceHeaders", DEFAULT_SOURCES);
  const hashDerived = configBool(config, "hashDerivedSessions", true);
  const fallbackToRequestId = configBool(config, "fallbackToRequestId", true);

  let sourceValue = "";
  for (const name of sources) {
    const value = ctx.headers[name];
    if (value && String(value).trim() !== "") {
      sourceValue = String(value).trim();
      break;
    }
  }
  if (sourceValue === "") {
    if (!fallbackToRequestId || !ctx.requestId) {
      return { body: null };
    }
    sourceValue = ctx.requestId;
  }
  const session = hashDerived ? stableHash(sourceValue) : sourceValue;
  return {
    body: null,
    headers: { [headerName]: session },
    metadata: { session_source: sourceValue === ctx.requestId ? "request_id" : "header" },
  };
}

export function onComplete(event) {
  // Denied and failed attempts cannot have helped session affinity; a quiet
  // success is the normal path and needs no log line.
  if (event.outcome === "failed") {
    console.log("opencode-session-header request " + event.requestId + " failed upstream");
  }
}
