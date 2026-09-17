export const meta = {
  apiVersion: 1,
  key: "doubao",
  name: "Doubao Video",
  icon: "Doubao.Color",
  description: {
    en: "Volcengine Doubao Seedance video generation (text-to-video, image-to-video, and video-to-video)",
    zh: "火山引擎豆包 Seedance 视频生成（文生视频、图生视频、视频生视频）",
  },
  version: "1.0.3",
  author: { name: "QuantumNous" },
  channelTypes: [54, 45], // VolcEngine-type channels serve Ark video models with the same wire format
  models: [
    "doubao-seedance-1-0-pro-250528",
    "doubao-seedance-1-0-lite-t2v",
    "doubao-seedance-1-0-lite-i2v",
    "doubao-seedance-1-5-pro-251215",
    "doubao-seedance-2-0-260128",
    "doubao-seedance-2-0-fast-260128",
    "doubao-seedance-2-0-mini-260615",
    "doubao-seedance-2-5-260628",
    "doubao-seedance-2.0",
    "doubao-seedance-2.0-fast",
    "doubao-seedance-2.0-mini",
    "doubao-seedance-2.5",
    "doubao-seedance-1.5-pro",
    "doubao-seedance-1.0-pro",
  ],
  fetchMode: "per_task",
  usageSchema: {
    // Upstream billing tokens (estimated at submit, actual on completion).
    tokens: {
      type: "number",
      unit: "token",
      description: { en: "Billing token unit price", zh: "计费 Token 单价" },
    },
    // Output video resolution; Seedance token unit price varies by resolution tier.
    resolution: {
      enum: ["480p", "720p", "1080p", "4k"],
      enumLabels: {
        "480p": { en: "480p", zh: "480p" },
        "720p": { en: "720p", zh: "720p" },
        "1080p": { en: "1080p", zh: "1080p" },
        "4k": { en: "4k", zh: "4k" },
      },
      description: { en: "Output video resolution", zh: "输出视频分辨率" },
    },
    // Whether the request includes reference video input; Seedance prices video-to-video tokens at a lower unit rate.
    video_input: {
      enum: ["none", "video"],
      enumLabels: { none: { en: "No reference video", zh: "无参考视频" }, video: { en: "With reference video", zh: "有参考视频" } },
      description: { en: "Reference video input", zh: "参考视频输入" },
    },
  },
  // Official Ark formula tokens = (input + output seconds) × W × H × 24 / 1024,
  // 16:9 max-pixel sizes, cross-checked against Volcengine price examples.
  usageExamples: [
    { label: "480p · 5s", facts: { tokens: 48038, resolution: "480p", video_input: "none" } },
    { label: "720p · 5s", facts: { tokens: 108000, resolution: "720p", video_input: "none" } },
    { label: "1080p · 5s", facts: { tokens: 243000, resolution: "1080p", video_input: "none" } },
    { label: "4k · 5s", facts: { tokens: 972000, resolution: "4k", video_input: "none" } },
    { label: "720p · 10s", facts: { tokens: 216000, resolution: "720p", video_input: "none" } },
    { label: "720p · 5s (+4s 输入视频)", facts: { tokens: 194400, resolution: "720p", video_input: "video" } },
  ],
  routes: [
    { method: "POST", path: "/doubao/api/v3/contents/generations/tasks", type: "submit", decode: "createTask", render: "taskCreated" },
    { method: "GET", path: "/doubao/api/v3/contents/generations/tasks/:task_id", type: "query", render: "taskStatus" },
  ],
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

function trimmed(value) {
  return String(value || "").trim();
}

function draftTaskIds(content) {
  const ids = [];
  if (!Array.isArray(content)) return ids;
  for (const item of content) {
    if (!item || typeof item !== "object" || Array.isArray(item)) continue;
    if (item.type !== "draft_task") continue;
    const draft = item.draft_task;
    if (!draft || typeof draft !== "object" || Array.isArray(draft)) continue;
    const id = trimmed(draft.id);
    if (id) ids.push(id);
  }
  return ids;
}

function rewriteDraftTaskContent(content, originTasks) {
  if (!Array.isArray(content)) return content;
  return content.map(function (item) {
    if (!item || typeof item !== "object" || Array.isArray(item) || item.type !== "draft_task") return item;
    const draft = item.draft_task;
    if (!draft || typeof draft !== "object" || Array.isArray(draft) || !trimmed(draft.id)) return item;
    const publicId = trimmed(draft.id);
    let upstream = "";
    if (Array.isArray(originTasks)) {
      for (const task of originTasks) {
        if (task && task.taskId === publicId) {
          upstream = trimmed(task.upstreamTaskId);
          break;
        }
      }
    }
    if (!upstream) throw new Error("origin task is unavailable");
    return Object.assign({}, item, { draft_task: Object.assign({}, draft, { id: upstream }) });
  });
}

function normalizeResolution(value) {
  const raw = trimmed(value).toLowerCase();
  if (["480p", "720p", "1080p", "4k"].includes(raw)) return raw;
  const parts = raw.replace("*", "x").split("x");
  if (parts.length !== 2) return "720p";
  const max = Math.max(Number(parts[0]), Number(parts[1]));
  if (max >= 3840) return "4k";
  if (max >= 1920) return "1080p";
  if (max >= 1280) return "720p";
  return "480p";
}

function hasVideo(content) {
  return Array.isArray(content) && content.some((item) => item && (item.type === "video_url" || Object.prototype.hasOwnProperty.call(item, "video_url")));
}

function hasReferenceVideo(req, metadata) {
  const content = Array.isArray(metadata.content) ? metadata.content : Array.isArray(req.content) ? req.content : [];
  if (hasVideo(content)) return true;
  for (const source of [req, metadata]) {
    for (const key of ["video_reference", "video_reference[]"]) {
      const value = source[key];
      if (Array.isArray(value) ? value.some((item) => trimmed(item)) : trimmed(value)) return true;
    }
  }
  return false;
}

// Max-pixel 16:9 dimensions per resolution tier. Used when ratio is absent or
// adaptive so the submit-time estimate overestimates rather than underestimates.
// Official Ark formula: tokens = seconds × width × height × 24 / 1024.
// Video input duration is omitted; extractUsageOnComplete overlays the real bill.
function resolutionMaxPixels(resolution) {
  if (resolution === "480p") return [854, 480];
  if (resolution === "1080p") return [1920, 1080];
  if (resolution === "4k") return [3840, 2160];
  return [1280, 720];
}

function estimateTokens(seconds, resolution) {
  const dims = resolutionMaxPixels(resolution);
  return (seconds * dims[0] * dims[1] * 24) / 1024;
}

function videoInputRatio(model, resolution, content) {
  const video = hasVideo(content);
  const res = trimmed(resolution).toLowerCase();
  const m = trimmed(model).toLowerCase();
  if (m === "doubao-seedance-2-5-260628" || m === "doubao-seedance-2.5") {
    if (res === "1080p") return video ? 7.0 / 10.7 : 11.7 / 10.7;
    return video ? 42 / 70 : 1;
  }
  if (m === "doubao-seedance-2-0-260128" || m === "doubao-seedance-2.0" || m === "doubao-seedance-2-0") {
    if (res === "1080p") return video ? 31 / 46 : 51 / 46;
    if (res === "4k") return video ? 16 / 46 : 26 / 46;
    return video ? 28 / 46 : 1;
  }
  if (m === "doubao-seedance-2-0-fast-260128" || m === "doubao-seedance-2.0-fast") return video ? 22 / 37 : 1;
  if (m === "doubao-seedance-2-0-mini-260615" || m === "doubao-seedance-2.0-mini") return video ? 14 / 23 : 1;
  return 1;
}

function normalizeAssetUri(val) {
  if (val && typeof val === "object" && !Array.isArray(val)) val = val.url;
  if (typeof val !== "string") throw new Error("media reference must be a URL string or an object with a URL string");
  const str = val.trim();
  if (!str) throw new Error("media reference URL is empty");
  if (str.startsWith("group-")) throw new Error("a material group ID cannot be used as an asset ID");
  if (str.startsWith("asset-")) return "asset://" + str;
  return str;
}

function responsesInput(req) {
  const texts = [],
    images = [];
  const input = req.input;
  if (typeof input === "string") texts.push(input);
  else if (Array.isArray(input)) {
    for (const item of input) {
      if (typeof item === "string") {
        texts.push(item);
        continue;
      }
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") {
          texts.push(part);
          continue;
        }
        if (!part || typeof part !== "object" || Array.isArray(part)) continue;
        if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
        if (["input_image", "image_url"].includes(part.type)) {
          let image = part.image_url;
          if (image && typeof image === "object") image = image.url;
          if (image !== undefined && image !== null && image !== "") images.push(normalizeAssetUri(image));
        }
      }
    }
  }
  return {
    prompt: texts
      .filter(function (text) {
        return trimmed(text);
      })
      .join("\n"),
    images: images,
  };
}

function responsesVideoText(ctx) {
  const artifact = ctx && ctx.artifacts && ctx.artifacts.video;
  const url = trimmed(artifact && artifact.url);
  if (!url) throw new Error("video artifact is unavailable");
  const escaped = url.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return '<video controls src="' + escaped + '"></video>';
}

export const native = {
  createTask: function (ctx) {
    if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
    const body = ctx.body.value;
    if (!body || typeof body !== "object" || Array.isArray(body)) throw new Error("request body must be an object");
    const model = trimmed(body.model);
    if (!model) throw new Error("model is required");
    if (body.content !== undefined && !Array.isArray(body.content)) throw new Error("content must be an array");
    const content = Array.isArray(body.content) ? body.content : [];
    const texts = [];
    let hasReference = false;
    for (const item of content) {
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      if (item.type === "text" && typeof item.text === "string") texts.push(item.text);
      else hasReference = true;
    }
    if (!texts.length && !hasReference) throw new Error("content is required");
    const requestBody = {
      model: model,
      prompt: texts
        .filter(function (text) {
          return trimmed(text);
        })
        .join("\n"),
      metadata: body,
    };
    const seconds = Number(body.duration);
    if (Number.isFinite(seconds) && seconds > 0) requestBody.seconds = seconds;
    const intent = { kind: "submit", model: model, action: hasReference ? "image_to_video" : "text_to_video", requestBody: requestBody };
    const originTaskIds = draftTaskIds(content);
    if (originTaskIds.length) intent.originTaskIds = originTaskIds;
    return intent;
  },
  taskCreated: function (ctx, task) {
    const data = task.data && typeof task.data === "object" && !Array.isArray(task.data) ? task.data : {};
    return Object.assign({}, data, { id: task.task_id });
  },
  taskStatus: function (ctx, task) {
    if (task.data && typeof task.data === "object" && !Array.isArray(task.data)) return Object.assign({}, task.data, { id: task.task_id });
    const statusMap = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "running", SUCCESS: "succeeded", FAILURE: "failed" };
    const output = { id: task.task_id, status: statusMap[task.status] || "queued" };
    if (task.fail_reason) output.error = { message: task.fail_reason };
    return output;
  },
  error: function (ctx, error) {
    return { error: { code: error.code, message: error.message } };
  },
};

function doubaoBaseUrl(rawBaseUrl) {
  const base = String(rawBaseUrl || "").trim().replace(/\/+$/, "");
  return base.replace(/\/api\/v3$/i, "");
}

export function buildSubmitRequest(ctx) {
  const req = ctx.requestBody;
  const metadata = req.metadata || {};
  const body = Object.assign({ model: req.model || "", content: [] }, metadata);
  const imageContent = [];

  // 智能收集并归一化所有参考图、首尾帧与 AICC 真人素材资产（支持 asset:// 与 asset-id）
  const candidateImages = [];
  if (Array.isArray(req.images)) {
    for (const u of req.images) candidateImages.push(u);
  }
  for (const source of [req, metadata]) {
    for (const key of ["image", "input_reference", "input_reference[]", "asset_id", "liveness_asset_id"]) {
      const field = source[key];
      if (field === undefined || field === null || field === "") continue;
      for (const value of Array.isArray(field) ? field : [field]) candidateImages.push(value);
      delete body[key];
    }
  }
  const seenUrls = new Set();
  for (const raw of candidateImages) {
    const norm = normalizeAssetUri(raw);
    if (norm && !seenUrls.has(norm)) {
      seenUrls.add(norm);
      imageContent.push({ type: "image_url", image_url: { url: norm } });
    }
  }

  const metadataContent = Array.isArray(body.content) && body.content.length ? body.content : (Array.isArray(req.content) ? req.content : []);
  const references = metadataContent.filter((item) => item && item.type !== "text").map((item) => {
    const type = item.type;
    if (!["image_url", "video_url", "audio_url"].includes(type)) return item;
    const media = item[type];
    if (!media || typeof media !== "object" || Array.isArray(media)) throw new Error(type + " must be an object with a URL string");
    const copy = Object.assign({}, item);
    copy[type] = Object.assign({}, media, { url: normalizeAssetUri(media) });
    return copy;
  });
  // Native decoding may derive images from content; retain the original roles without duplicating those images.
  const contentImageUrls = references.filter((item) => item.type === "image_url" && item.image_url).map((item) => normalizeAssetUri(item.image_url));
  body.content = imageContent.filter((item) => !contentImageUrls.includes(item.image_url.url)).concat(references);
  for (const source of [req, metadata]) {
    for (const [key, type, role] of [["first_frame_url", "image_url", "first_frame"], ["last_frame_url", "image_url", "last_frame"], ["video_reference", "video_url", "reference_video"], ["video_reference[]", "video_url", "reference_video"], ["audio_reference", "audio_url", "reference_audio"], ["audio_reference[]", "audio_url", "reference_audio"]]) {
      const value = source[key];
      if (value === undefined || value === null || value === "") continue;
      for (const reference of Array.isArray(value) ? value : [value]) {
        const item = { type: type, role: role };
        item[type] = { url: normalizeAssetUri(reference) };
        body.content.push(item);
      }
      delete body[key];
    }
  }
  const hasReference = body.content.length > 0;
  const prompt = trimmed(req.prompt) || metadataContent.filter((item) => item && item.type === "text" && typeof item.text === "string").map((item) => item.text).join("\n");
  if (prompt || !hasReference) body.content.push({ type: "text", text: prompt });
  if (Array.isArray(body.content)) body.content = rewriteDraftTaskContent(body.content, ctx.originTasks);
  const durationValue = req.seconds !== undefined && req.seconds !== "" ? req.seconds : (req.duration !== undefined ? req.duration : body.duration);
  if (durationValue !== undefined) {
    const seconds = Number(durationValue);
    if (!Number.isInteger(seconds) || seconds <= 0 || seconds > 3600) throw new Error("seconds must be an integer between 1 and 3600");
    body.duration = seconds;
  }
  const resolution = req.resolution || req.resolution_name || body.resolution || body.resolution_name;
  if (resolution !== undefined) body.resolution = normalizeResolution(resolution);
  if (typeof req.size === "string" && /^(\d+:\d+|adaptive)$/.test(req.size)) body.ratio = req.size;
  for (const key of ["ratio", "seed", "watermark", "generate_audio", "return_last_frame", "camera_fixed", "service_tier", "execution_expires_after"]) {
    if (req[key] !== undefined) body[key] = req[key];
  }
  if (body.generate_audio === undefined) body.generate_audio = req.video_generate_audio !== undefined ? req.video_generate_audio : metadata.video_generate_audio;
  for (const key of ["watermark", "generate_audio", "return_last_frame", "camera_fixed"]) {
    if (body[key] === "false") body[key] = false;
    else if (body[key] === "true") body[key] = true;
    else if (body[key] !== undefined && typeof body[key] !== "boolean") throw new Error(key + " must be a boolean");
  }
  delete body.resolution_name;
  delete body.video_generate_audio;
  let targetModel = ctx.upstreamModel || body.model;
  const isCmeCloud = String(ctx.baseUrl || "").toLowerCase().includes("cmecloud.cn");
  if (!ctx.upstreamModel && !isCmeCloud) {
    if (targetModel === "doubao-seedance-2.0") targetModel = "doubao-seedance-2-0-260128";
    else if (targetModel === "doubao-seedance-2.0-fast") targetModel = "doubao-seedance-2-0-fast-260128";
    else if (targetModel === "doubao-seedance-2.0-mini") targetModel = "doubao-seedance-2-0-mini-260615";
    else if (targetModel === "doubao-seedance-2.5") targetModel = "doubao-seedance-2-5-260628";
  }
  body.model = targetModel;
  const baseUrl = doubaoBaseUrl(ctx.baseUrl);
  return {
    url: baseUrl + "/api/v3/contents/generations/tasks",
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
    body: body,
    action: hasReference ? "image_to_video" : "text_to_video",
    rewriteModel: body.model,
  };
}

export function parseSubmitResponse(ctx, resp) {
  if (!resp.body || !resp.body.id) throw new Error("task_id is empty");
  return { taskId: resp.body.id, taskData: resp.body };
}

export function extractUsage(ctx) {
  const req = ctx.requestBody || {};
  const metadata = req.metadata || {};
  if (ctx.usagePurpose === "billing_ratios") {
    const ratio = videoInputRatio(ctx.upstreamModel || ctx.model, metadata.resolution, metadata.content);
    return ratio === 1 ? null : { video_input_ratio: ratio };
  }
  let seconds = Number(req.seconds || req.duration || metadata.duration || 0);
  if (!Number.isFinite(seconds) || seconds <= 0) {
    const frames = Number(metadata.frames);
    seconds = Number.isFinite(frames) && frames > 0 ? Math.floor(frames / 24) : 15;
  }
  if (seconds <= 0) seconds = 5;
  seconds = Math.min(seconds, 3600);
  const rawResolution = req.resolution || req.resolution_name || metadata.resolution || metadata.resolution_name || req.size;
  const raw = trimmed(rawResolution).toLowerCase();
  const recognized = ["480p", "720p", "1080p", "4k"].includes(raw) || raw.replace("*", "x").split("x").length === 2;
  const resolution = recognized ? normalizeResolution(rawResolution) : (raw === "16:9" || raw === "9:16" || raw === "1:1" ? "720p" : "1080p");
  const hasRefVideo = hasReferenceVideo(req, metadata);
  return {
    tokens: estimateTokens(seconds, resolution),
    resolution: resolution,
    video_input: hasRefVideo ? "video" : "none",
  };
}

export function buildQueryRequest(ctx) {
  const baseUrl = doubaoBaseUrl(ctx.baseUrl);
  return {
    url: baseUrl + "/api/v3/contents/generations/tasks/" + ctx.taskId,
    method: "GET",
    headers: { Accept: "application/json", "Content-Type": "application/json", Authorization: "Bearer " + ctx.apiKey },
  };
}

export function parseTaskResult(ctx, body) {
  if (body.status === "pending" || body.status === "queued") return { status: "QUEUED", progress: "10%" };
  if (body.status === "processing" || body.status === "running") return { status: "IN_PROGRESS", progress: "50%" };
  if (body.status === "succeeded") {
    const result = { status: "SUCCESS", progress: "100%", url: body.content && body.content.video_url ? body.content.video_url : "" };
    const usage = body.usage || {};
    const completionTokens = Number(usage.completion_tokens || 0);
    const totalTokens = Number(usage.total_tokens || 0);
    if (Number.isFinite(completionTokens) && completionTokens > 0) result.completionTokens = completionTokens;
    if (Number.isFinite(totalTokens) && totalTokens > 0) result.totalTokens = totalTokens;
    return result;
  }
  if (body.status === "failed" || body.status === "expired" || body.status === "cancelled") {
    let reason = body.error && body.error.message ? body.error.message : body.status;
    if (reason && typeof reason === "string" && (reason.includes("真人") || reason.includes("肖像") || reason.includes("实名认证") || reason.toLowerCase().includes("face") || reason.toLowerCase().includes("security"))) {
      reason = "移动云真人合规拦截: " + reason + " (请先在 AICC 素材库完成真人 H5 实名认证，并在生视频时传 asset://{asset_id})";
    }
    return { status: "FAILURE", progress: "100%", reason: reason };
  }
  return { status: "UNKNOWN", reason: "unrecognized status: " + String(body.status || "") };
}

function artifactData(ctx) {
  const data = (ctx && ctx.data) || {};
  if (data.data && typeof data.data === "object" && data.data.task_id && Object.prototype.hasOwnProperty.call(data.data, "data")) return data.data.data || {};
  return data;
}

export function listArtifacts(task) {
  if (task.status !== "SUCCESS") return [];
  const content = artifactData(task).content || {};
  const artifacts = [];
  if (trimmed(content.video_url)) artifacts.push({ key: "video", type: "video" });
  if (trimmed(content.last_frame_url)) artifacts.push({ key: "last_frame", type: "image", mimeType: "image/png" });
  return artifacts;
}

export function buildContentRequest(ctx) {
  const content = artifactData(ctx).content || {};
  const urls = { video: content.video_url, last_frame: content.last_frame_url };
  const url = trimmed(urls[ctx.artifactKey]);
  if (!url) throw new Error("artifact_not_found");
  return { url: url, method: ctx.clientRequest.method, credentialless: true };
}

export function extractUsageOnComplete(task, taskResult, body) {
  if (!body || body.status !== "succeeded") return {};
  const facts = {};
  const usage = body.usage || {};
  let tokens = Number(usage.completion_tokens);
  if (!Number.isFinite(tokens) || tokens <= 0) tokens = Number(usage.total_tokens);
  if (Number.isFinite(tokens) && tokens > 0) facts.tokens = tokens;
  const content = body.content || {};
  const resolution = trimmed(content.resolution || body.resolution).toLowerCase();
  if (["480p", "720p", "1080p", "4k"].includes(resolution)) facts.resolution = resolution;
  return facts;
}

export const protocols = {
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
      const model = trimmed(req.model);
      if (!model) throw new Error("model is required");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      if (req.images !== undefined && !Array.isArray(req.images)) throw new Error("images must be an array");
      if (req.metadata !== undefined && (!req.metadata || typeof req.metadata !== "object" || Array.isArray(req.metadata)))
        throw new Error("metadata must be an object");
      const input = responsesInput(req);
      const prompt = input.prompt || trimmed(req.prompt);
      const images = [];
      for (const image of [req.image, req.input_reference, req["input_reference[]"]].concat(req.images || [], input.images)) {
        if (image === undefined || image === null || image === "") continue;
        for (const ref of Array.isArray(image) ? image : [image]) {
          const url = normalizeAssetUri(ref);
          if (!images.includes(url)) images.push(url);
        }
      }
      if (!prompt && images.length === 0 && !(req.metadata && Array.isArray(req.metadata.content) && req.metadata.content.length)) throw new Error("input is required");
      const metadata = Object.assign({}, req.metadata || {});
      if (Object.prototype.hasOwnProperty.call(req, "resolution")) metadata.resolution = req.resolution;
      else if (req.size && !metadata.resolution) metadata.resolution = normalizeResolution(req.size);
      const requestBody = { model: model, prompt: prompt, metadata: metadata };
      if (images.length) requestBody.images = images;
      if (Object.prototype.hasOwnProperty.call(req, "seconds")) requestBody.seconds = req.seconds;
      else if (Object.prototype.hasOwnProperty.call(req, "duration")) requestBody.seconds = req.duration;
      if (Object.prototype.hasOwnProperty.call(req, "size")) requestBody.size = req.size;
      const intent = { kind: "submit", model: model, action: images.length ? "image_to_video" : "text_to_video", requestBody: requestBody };
      const originTaskIds = draftTaskIds(metadata.content);
      if (originTaskIds.length) intent.originTaskIds = originTaskIds;
      return intent;
    },
    renderEvents: function (ctx, task, previousState) {
      const status = String(task.status || "UNKNOWN").toUpperCase();
      const value = Number(String(task.progress || "").replace("%", ""));
      const progress = Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
      const state = { status: status, progress: progress };
      if (status === "SUCCESS") {
        const text = responsesVideoText(ctx);
        const events = previousState && previousState.status === status ? [] : [{ type: "output", data: text }];
        return { events: events, state: state, done: true };
      }
      if (status === "FAILURE")
        return { events: [{ type: "error", code: "task_failed", message: task.fail_reason || "task failed" }], state: state, done: true };
      if (previousState && previousState.status === status && previousState.progress === progress) return { events: [], state: state, done: false };
      const event = { type: "progress", message: status.toLowerCase() };
      if (progress !== null) event.progress = progress;
      return { events: [event], state: state, done: false };
    },
    renderFinal: function (ctx, _task) {
      return {
        output: [
          {
            type: "message",
            status: "completed",
            role: "assistant",
            content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }],
          },
        ],
        metadata: { vendor: "doubao" },
      };
    },
  },
};

const legacyRenderers = {
  openai_video: function (task) {
    const data = task.data || {};
    const statusMap = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
    const output = {
      id: task.task_id,
      object: "video",
      model: task.properties ? task.properties.origin_model_name || "" : "",
      status: statusMap[task.status] || "unknown",
      progress: Number(String(task.progress || "0").replace("%", "")),
      created_at: task.created_at,
      completed_at: task.updated_at,
    };
    if (data.status === "failed") output.error = { message: data.error ? data.error.message || "" : "", code: data.error ? data.error.code || "" : "" };
    return output;
  },
};

protocols.openai_video = {
  decodeRequest: function (ctx) {
    if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
    if (ctx.body.kind === "json") {
      if (!ctx.body.value || Array.isArray(ctx.body.value)) throw new Error("JSON object required");
      const req = ctx.body.value;
      const seconds = req.seconds === undefined ? req.duration : req.seconds;
      if (seconds !== undefined && (!Number.isFinite(Number(seconds)) || Number(seconds) <= 0 || Number(seconds) > 3600))
        throw new Error("seconds must be between 1 and 3600");
      const hasRef = Boolean(req.input_reference || req["input_reference[]"] || req.image || req.asset_id || req.liveness_asset_id || (Array.isArray(req.images) && req.images.length > 0) || (Array.isArray(req.content) && req.content.some((item) => item && item.type !== "text")) || (req.metadata && Array.isArray(req.metadata.content) && req.metadata.content.some((item) => item && item.type !== "text")));
      return {
        kind: "submit",
        model: ctx.model,
        action: hasRef ? "image_to_video" : "text_to_video",
        requestBody: Object.assign({}, req, { model: ctx.model }),
      };
    }
    const first = function (name) {
      const values = (ctx.body.fields || {})[name] || [];
      if (values.length > 1) throw new Error(name + " must be provided once");
      return values[0];
    };
    const req = {};
    const fields = ctx.body.fields || {};
    for (const name of Object.keys(fields)) {
      req[name] = ["images", "input_reference[]", "video_reference[]", "audio_reference[]"].includes(name) ? fields[name] : first(name);
    }
    if (req.metadata !== undefined) {
      let parsed;
      try {
        parsed = JSON.parse(req.metadata);
      } catch (e) {
        throw new Error("metadata must be a JSON object string");
      }
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error("metadata must be a JSON object string");
      req.metadata = parsed;
    }
    if ((ctx.body.files || []).length) throw new Error("Doubao requires image and video references to be URLs inside metadata.content");
    if (req.seconds !== undefined) req.seconds = Number(req.seconds);
    else if (req.duration !== undefined) req.seconds = Number(req.duration);
    const seconds = req.seconds === undefined ? req.duration : req.seconds;
    if (seconds !== undefined && (!Number.isFinite(Number(seconds)) || Number(seconds) <= 0 || Number(seconds) > 3600))
      throw new Error("seconds must be between 1 and 3600");
    const hasRef = Boolean(req.input_reference || req["input_reference[]"] || req.image || req.asset_id || req.liveness_asset_id || (Array.isArray(req.images) && req.images.length > 0) || (Array.isArray(req.content) && req.content.some((item) => item && item.type !== "text")) || (req.metadata && Array.isArray(req.metadata.content) && req.metadata.content.some((item) => item && item.type !== "text")));
    return {
      kind: "submit",
      model: ctx.model,
      action: hasRef ? "image_to_video" : "text_to_video",
      requestBody: Object.assign({}, req, { model: ctx.model }),
    };
  },
  render: function (ctx, task) {
    const output = legacyRenderers.openai_video(task);
    const video = ctx.artifacts && ctx.artifacts.video;
    if (task.status === "SUCCESS" && video && video.type === "video" && trimmed(video.url)) {
      output.video_url = video.url;
      output.url = video.url;
    }
    return output;
  },
};
