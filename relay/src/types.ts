export interface Env {
  OAUTH_SESSIONS: KVNamespace;
  BACKUP_STORE: KVNamespace;
  LICENSE_DB: D1Database;
  SLACK_CLIENT_ID: string;
  SLACK_CLIENT_SECRET: string;
  GOOGLE_CLIENT_ID: string;
  GOOGLE_CLIENT_SECRET: string;
  GITHUB_CLIENT_ID: string;
  GITHUB_CLIENT_SECRET: string;
  ATLASSIAN_CLIENT_ID: string;
  ATLASSIAN_CLIENT_SECRET: string;
  LINEAR_CLIENT_ID: string;
  LINEAR_CLIENT_SECRET: string;
  LICENSE_SIGNING_KEY: string;
  LEMON_SQUEEZY_WEBHOOK_SECRET: string;
  LATEST_VERSION: string;
  MIN_REQUIRED_VERSION: string;
  UPDATE_MESSAGE: string;
}

export interface SlackOAuthResponse {
  ok: boolean;
  error?: string;
  access_token?: string;
  token_type?: string;
  scope?: string;
  authed_user?: {
    id: string;
    access_token: string;
    token_type: string;
    scope: string;
  };
  team?: {
    id: string;
    name: string;
  };
}

export interface StoredToken {
  access_token: string;
  user_id: string;
  team_id: string;
  team_name: string;
  scope: string;
}

export interface GoogleTokenResponse {
  access_token: string;
  refresh_token?: string;
  expires_in: number;
  token_type: string;
  scope: string;
  error?: string;
  error_description?: string;
}

export interface GoogleUserInfo {
  id: string;
  email: string;
  name?: string;
}

export interface StoredGoogleToken {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  email: string;
  scope: string;
}

export interface GitHubTokenResponse {
  access_token: string;
  token_type: string;
  scope: string;
  error?: string;
  error_description?: string;
}

export interface GitHubUser {
  login: string;
  id: number;
  email?: string;
}

export interface StoredGitHubToken {
  access_token: string;
  token_type: string;
  scope: string;
  username: string;
}

export interface AtlassianTokenResponse {
  access_token: string;
  refresh_token?: string;
  expires_in: number;
  scope: string;
  error?: string;
  error_description?: string;
}

export interface AtlassianCloudSite {
  id: string;
  name: string;
  url: string;
}

export interface StoredAtlassianToken {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  scope: string;
  email: string;
  cloud_sites: AtlassianCloudSite[];
}

export interface LinearTokenResponse {
  access_token: string;
  token_type?: string;
  scope?: string;
  error?: string;
  error_description?: string;
}

export interface StoredLinearToken {
  access_token: string;
  token_type: string;
  scope: string;
  user_id: string;
  user_name: string;
  email: string;
}
