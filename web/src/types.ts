export type Page =
  | "overview"
  | "programs"
  | "assets"
  | "changes"
  | "watchlist"
  | "sources";
export type Entity = "programs" | "assets" | "changes";
export type Source = {
  platform: string;
  enabled: boolean;
  status: string;
  last_attempt: string;
  last_success: string;
  next_run: string;
  error: string;
  program_count: number;
  asset_count: number;
  url: string;
};
export type Overview = {
  counts: Record<string, number>;
  sources: Source[];
  types: { type: string; count: number }[];
  trend: { day: string; kind: string; count: number }[];
  syncing: boolean;
  published_at: string;
  config: SiteConfig;
  history_limit: number;
  history_total: number;
};
export type Data = {
  id: string;
  label: string;
  platform: string;
  entity: string;
  program_id: string;
  url?: string;
  scope?: string;
  type?: string;
  conflict?: boolean;
  source_scopes?: string[];
  metadata: Record<string, unknown>[];
};
export type Row = {
  id: string | number;
  label: string;
  platform: string;
  program_id: string;
  program_name?: string;
  program_url?: string;
  url?: string;
  entity?: string;
  bounty?: number;
  scope?: string;
  type?: string;
  conflict?: number;
  watched?: number;
  program_watched?: number;
  asset_count?: number;
  in_scope_count?: number;
  data?: Data;
  detail_file?: string;
  before_data?: Data;
  after_data?: Data;
  kind?: string;
  created_at?: string;
  seen?: number;
  last_seen?: string;
};
export type List = { items: Row[]; total: number; page: number; limit: number };
export type Run = {
  id: number;
  platform: string;
  status: string;
  started_at: string;
  finished_at: string;
  programs: number;
  assets: number;
  changes: number;
  message: string;
};

export type SiteConfig = {
  repository: string;
  workflow: string;
  schedule: string;
  timezone: string;
  enabled: Record<string, boolean>;
};
export type Manifest = {
  schema: number;
  dataset_id: string;
  version: string;
  generated_at: string;
  files: Record<string, string>;
  config: SiteConfig;
  history_limit: number;
  history_total: number;
};
