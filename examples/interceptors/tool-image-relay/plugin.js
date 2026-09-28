// Tool Image Relay repairs upstreams that reject images inside tool messages.
// Some vendors validate strictly: a tool message whose content array carries an
// image_url block fails with "Invalid input" even though the images are simply
// results of the preceding tool call. This interceptor moves every image block
// out of tool messages and re-injects it, in order, into a synthetic user
// message right after the last tool message of the run, with a lead-in text so
// the model understands where the images came from.
export const meta = {
  apiVersion: 1,
  key: "tool-image-relay",
  name: "Tool Image Relay",
  version: "1.0.0",
  author: { name: "QuantumNous" },
  description: {
    en: "Move tool-message images into a following user message for strict upstreams",
    zh: "将 tool 消息中的图片转移到后续 user 消息以适配严格上游",
  },
  requiredCapabilities: ["request-interceptor@1"],
  interceptor: {
    priority: 100,
    methods: ["POST"],
    intercept: "intercept",
    complete: "onComplete",
    configFields: [
      {
        name: "models",
        type: "array",
        description: {
          en: "Restrict to these upstream models; empty means all",
          zh: "仅对这些上游模型生效，留空表示全部",
        },
      },
      {
        name: "imagesPrefix",
        type: "string",
        description: {
          en: "Lead-in text of the synthetic user message",
          zh: "合成 user 消息的引导文案",
        },
      },
      {
        name: "maxImages",
        type: "integer",
        description: {
          en: "Maximum images relayed per request; 0 means unlimited",
          zh: "单个请求最多转发的图片数，0 表示不限",
        },
      },
    ],
  },
};

const DEFAULT_PREFIX = "Images returned by the preceding tool call(s):";

function isToolMessage(message) {
  return message && message.role === "tool";
}

// An image block is an object with an image_url member; text and other block
// kinds stay where they are.
function imageBlocks(content) {
  if (!Array.isArray(content)) {
    return [];
  }
  return content.filter(function (block) {
    return block && typeof block === "object" && block.image_url !== undefined;
  });
}

function textOf(content) {
  if (typeof content === "string") {
    return content;
  }
  if (Array.isArray(content)) {
    return content
      .filter(function (block) {
        return block && typeof block === "object" && typeof block.text === "string";
      })
      .map(function (block) {
        return block.text;
      })
      .join("\n");
  }
  return "";
}

export function intercept(ctx) {
  const config = ctx.config || {};
  const models = Array.isArray(config.models) ? config.models : [];
  if (models.length > 0 && models.indexOf(String(ctx.upstreamModel || "")) < 0) {
    return { body: null };
  }
  const body = ctx.body;
  if (!body || typeof body !== "object" || !Array.isArray(body.messages)) {
    return { body: null };
  }

  const prefix = String(config.imagesPrefix || DEFAULT_PREFIX);
  const maxImages = typeof config.maxImages === "number" && config.maxImages > 0 ? Math.floor(config.maxImages) : 0;

  const messages = body.messages;
  const rewritten = [];
  let relayed = [];
  let toolRun = false;
  let changed = false;

  for (const message of messages) {
    const blocks = imageBlocks(message.content);
    if (isToolMessage(message) && blocks.length > 0) {
      toolRun = true;
      // Strip the image blocks; keep every other part of the tool result.
      const remaining = message.content.filter(function (block) {
        return !(block && typeof block === "object" && block.image_url !== undefined);
      });
      for (const block of blocks) {
        if (maxImages === 0 || relayed.length < maxImages) {
          relayed.push(block);
        }
      }
      changed = true;
      const replacement = remaining.length > 0 ? Object.assign({}, message, { content: remaining }) : message;
      rewritten.push(replacement);
      continue;
    }
    if (toolRun && relayed.length > 0 && !isToolMessage(message)) {
      // The tool run ended: flush the collected images before this message so
      // the model reads the results directly after the tools that produced them.
      rewritten.push({
        role: "user",
        content: [{ type: "text", text: prefix }].concat(relayed),
      });
      relayed = [];
      toolRun = false;
    }
    rewritten.push(message);
  }
  if (relayed.length > 0) {
    rewritten.push({
      role: "user",
      content: [{ type: "text", text: prefix }].concat(relayed),
    });
    relayed = [];
  }

  if (!changed) {
    return { body: null };
  }
  return {
    body: Object.assign({}, body, { messages: rewritten }),
    metadata: { images_relayed: String(rewritten.length - messages.length + relayed.length) },
  };
}

export function onComplete(event) {
  if (event.outcome === "failed") {
    console.log("tool-image-relay request " + event.requestId + " failed upstream");
  }
}
