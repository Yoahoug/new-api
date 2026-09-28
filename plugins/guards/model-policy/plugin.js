// Model Policy is a request guard: it decides whether a relay request may run
// before a channel is selected and before any quota is reserved. It ships
// inert — an unconfigured gateway allows every request — so enabling it costs
// nothing until an administrator fills in a policy.
export const meta = {
  apiVersion: 1,
  key: "model-policy",
  name: "Model Policy",
  icon: "ShieldCheck",
  description: {
    en: "Restrict which models each group may call",
    zh: "限制各分组可调用的模型",
    "zh-TW": "限制各分組可呼叫的模型",
    ja: "グループごとに呼び出せるモデルを制限します",
    fr: "Restreint les modèles appelables par groupe",
    ru: "Ограничивает модели, доступные каждой группе",
    vi: "Giới hạn mô hình mỗi nhóm được phép gọi",
  },
  version: "1.0.0",
  author: { name: "QuantumNous" },
  requiredCapabilities: ["request-guard@1"],
  guard: {
    priority: 0,
    methods: ["POST"],
    // An unlisted group is unrestricted, so a policy only constrains the
    // groups an administrator has actually named.
    groups: [],
    authorize: "authorize",
    complete: "onComplete",
    configFields: [
      {
        name: "groupModels",
        type: "object",
        description: {
          en: "Allowed model patterns per group",
          zh: "各分组允许的模型匹配规则",
        },
      },
      {
        name: "deniedUserIds",
        type: "array",
        description: {
          en: "User IDs that are always denied",
          zh: "始终拒绝的用户 ID 列表",
        },
      },
      {
        name: "denyMessage",
        type: "string",
        description: {
          en: "Message returned with a denial",
          zh: "拒绝时返回的提示信息",
        },
      },
    ],
  },
};

const ANY = "*";

// A pattern is either an exact model name or a prefix ending in "*".
function patternMatches(model, pattern) {
  const candidate = String(pattern || "").trim();
  if (candidate === ANY) {
    return true;
  }
  if (candidate.endsWith(ANY)) {
    return model.startsWith(candidate.slice(0, -1));
  }
  return model.toLowerCase() === candidate.toLowerCase();
}

function policyForGroup(config, group) {
  const policies = config && config.groupModels;
  if (!policies || typeof policies !== "object") {
    return null;
  }
  const allowed = policies[group];
  if (!Array.isArray(allowed)) {
    return null;
  }
  return allowed;
}

function denial(message, code) {
  return { allow: false, status: 403, code: code, message: message };
}

function effectiveGroup(ctx) {
  return ctx.usingGroup || ctx.userGroup || "";
}

export function authorize(ctx) {
  if (!utils.hasCapability("request-guard@1")) {
    // A host without the guard capability never runs this hook, so reaching
    // this branch means the capability contract changed. Refuse rather than
    // fall through to an unconfigured allow.
    return denial("Request guards are not supported by this gateway", "permission_guard_unsupported");
  }
  const config = ctx.config || {};

  const denied = config.deniedUserIds;
  if (Array.isArray(denied) && denied.indexOf(ctx.userId) >= 0) {
    return denial(config.denyMessage || "This account is not permitted to use the gateway", "permission_denied");
  }

  const allowed = policyForGroup(config, effectiveGroup(ctx));
  if (allowed === null || allowed.length === 0) {
    return { allow: true };
  }
  const model = String(ctx.model || "");
  for (const pattern of allowed) {
    if (patternMatches(model, pattern)) {
      return { allow: true };
    }
  }
  return denial(
    config.denyMessage || "Model " + model + " is not available to your group",
    "model_not_permitted",
  );
}

export function onComplete(ctx) {
  // Denials are the signal an operator watches for; a completed allow is not
  // worth a log line on the hot path.
  if (ctx.outcome !== "denied") {
    return;
  }
  console.log("model-policy denied request " + ctx.requestId + " via " + ctx.guardKey);
}
