import type { ErrorCode } from "./errorCodes.js";

export interface ApiErrorPayload {
  code: ErrorCode | string;
  message: string;
  source: string;
  retryable: boolean;
  details?: unknown;
}

export interface ApiSuccess<T> {
  ok: true;
  data: T;
  meta?: Record<string, unknown>;
}

export interface ApiFailure {
  ok: false;
  error: ApiErrorPayload;
}

export type ApiResponse<T> = ApiSuccess<T> | ApiFailure;

export type McpToolResult<T> = ApiResponse<T>;

