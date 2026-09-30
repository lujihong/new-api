/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api, type ApiRequestConfig } from "@/lib/api";

import { buildQueryParams } from "./lib/query-params";
import { parseTaskArtifactsResponse } from "./lib/task-artifacts";
import type {
  GetLogsParams,
  GetLogsResponse,
  GetLogStatsParams,
  GetLogStatsResponse,
  GetMidjourneyLogsParams,
  GetTaskLogsParams,
  TaskArtifactsResponse,
  UserInfo,
} from "./types";

// ============================================================================
// Generic API Helpers
// ============================================================================

function buildApiPath(endpoint: string, isAdmin: boolean): string {
  return isAdmin ? endpoint : `${endpoint}/self`;
}

async function fetchLogs<T>(
  endpoint: string,
  params: T,
  isAdmin: boolean,
): Promise<GetLogsResponse> {
  const paramRecord = params as unknown as Record<string, unknown>;
  const queryParams = buildQueryParams({
    p: paramRecord.p || 1,
    page_size: paramRecord.page_size || 20,
    ...params,
  });
  const path = buildApiPath(endpoint, isAdmin);
  const res = await api.get(`${path}?${queryParams}`);
  return res.data;
}

async function fetchLogStats<T>(
  endpoint: string,
  params: T,
  isAdmin: boolean,
): Promise<GetLogStatsResponse> {
  const queryParams = buildQueryParams(
    params as unknown as Record<string, unknown>,
  );
  const path = buildApiPath(endpoint, isAdmin);
  const res = await api.get(`${path}/stat?${queryParams}`);
  return res.data;
}

// ============================================================================
// Common Log APIs
// ============================================================================

export const getAllLogs = (params: GetLogsParams = {}) =>
  fetchLogs("/api/log", params, true);

export const getUserLogs = (
  params: Omit<GetLogsParams, "username" | "channel"> = {},
) => fetchLogs("/api/log", params, false);

export const getLogStats = (params: GetLogStatsParams = {}) =>
  fetchLogStats("/api/log", params, true);

export const getUserLogStats = (
  params: Omit<GetLogStatsParams, "username" | "channel"> = {},
) => fetchLogStats("/api/log", params, false);

export async function getUserInfo(
  userId: number,
): Promise<{ success: boolean; message?: string; data?: UserInfo }> {
  const res = await api.get(`/api/user/${userId}`);
  return res.data;
}

// ============================================================================
// MjProxy (Drawing) Logs API
// ============================================================================

export const getAllMidjourneyLogs = (params: GetMidjourneyLogsParams) =>
  fetchLogs("/api/mj", params, true);

export const getUserMidjourneyLogs = (params: GetMidjourneyLogsParams) =>
  fetchLogs("/api/mj", params, false);

// ============================================================================
// Task Logs API
// ============================================================================

export const getAllTaskLogs = (params: GetTaskLogsParams) =>
  fetchLogs("/api/task", params, true);

export const getUserTaskLogs = (params: GetTaskLogsParams) =>
  fetchLogs("/api/task", params, false);

export async function downloadUsageLogsExport(
  category: "common" | "task" | "drawing",
  params: Record<string, unknown>,
) {
  const endpoint =
    category === "common"
      ? "/api/log/export"
      : category === "task"
        ? "/api/log/task/export"
        : "/api/log/drawing/export";
  const exportParams: Record<string, unknown> = { ...params };
  if (typeof exportParams.startTime === "number") {
    exportParams.start_timestamp = Math.floor(exportParams.startTime / 1000);
    delete exportParams.startTime;
  }
  if (typeof exportParams.endTime === "number") {
    exportParams.end_timestamp = Math.ceil(exportParams.endTime / 1000);
    delete exportParams.endTime;
  }
  if (typeof exportParams.filter === "string") {
    exportParams[category === "task" ? "task_id" : "mj_id"] =
      exportParams.filter;
    delete exportParams.filter;
  }
  if (typeof exportParams.channel === "string") {
    exportParams[category === "common" ? "channel" : "channel_id"] =
      exportParams.channel;
  }
  if (typeof exportParams.model === "string")
    exportParams.model_name = exportParams.model;
  if (typeof exportParams.token === "string")
    exportParams.token_name = exportParams.token;
  if (typeof exportParams.requestId === "string")
    exportParams.request_id = exportParams.requestId;
  if (typeof exportParams.upstreamRequestId === "string")
    exportParams.upstream_request_id = exportParams.upstreamRequestId;
  if (typeof exportParams.type === "string" && exportParams.type !== "all")
    exportParams.type = Number(exportParams.type);
  for (const key of [
    "page",
    "page_size",
    "startTime",
    "endTime",
    "model",
    "token",
    "requestId",
    "upstreamRequestId",
  ])
    delete exportParams[key];
  const query = buildQueryParams(exportParams);
  const response = await api.get(`${endpoint}?${query}`, {
    responseType: "blob",
  });
  const contentType = String(response.headers["content-type"] || "");
  if (!contentType.includes("spreadsheetml")) {
    throw new Error("导出接口未返回 Excel 文件");
  }
  const blob = new Blob([response.data], { type: contentType });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `${category}-logs-${new Date().toISOString().slice(0, 10)}.xlsx`;
  document.body.append(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

const taskArtifactRequestConfig = {
  skipBusinessError: true,
  skipErrorHandler: true,
} satisfies ApiRequestConfig;

export async function getTaskArtifacts(taskId: string) {
  const response = await api.get<TaskArtifactsResponse>(
    `/api/task/${encodeURIComponent(taskId)}/artifacts`,
    taskArtifactRequestConfig,
  );
  return parseTaskArtifactsResponse(response.data);
}
