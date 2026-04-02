export interface Env {
  OAUTH_SESSIONS: KVNamespace;
  SLACK_CLIENT_ID: string;
  SLACK_CLIENT_SECRET: string;
  GOOGLE_CLIENT_ID: string;
  GOOGLE_CLIENT_SECRET: string;
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
