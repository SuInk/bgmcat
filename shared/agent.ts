export const AGENT_HOSTS = [
  "diana",
  "codex",
  "openclaw",
  "claude",
  "cursor",
  "cherry-studio"
] as const;

export type AgentHost = (typeof AGENT_HOSTS)[number];

export interface AgentDetectionResult {
  host: AgentHost;
  displayName: string;
  detected: boolean;
  installed: boolean;
  managed: boolean;
  basePath: string;
  configPath: string;
  version: string | null;
  details?: string;
}

export interface AgentInstallRequest {
  host: AgentHost;
}

export interface AgentInstallResult {
  host: AgentHost;
  installed: boolean;
  configPath: string;
  filesWritten: string[];
  message: string;
}

export interface AgentDoctorResult {
  host: AgentHost;
  configPath: string;
  configExists: boolean;
  appRunning: boolean;
  endpointReachable: boolean;
  tokenConfigured: boolean;
  message: string;
  issues: string[];
}

