export interface Env {
  OAUTH_SESSIONS: KVNamespace;
  SLACK_CLIENT_ID: string;
  SLACK_CLIENT_SECRET: string;
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
