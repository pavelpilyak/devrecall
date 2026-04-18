import { Env } from "./types";
import { handleOAuthCallback } from "./handlers/oauth-callback";
import { handleGoogleOAuthCallback } from "./handlers/oauth-callback-google";
import { handleGitHubOAuthCallback } from "./handlers/oauth-callback-github";
import { handleAtlassianOAuthCallback } from "./handlers/oauth-callback-atlassian";
import { handleLinearOAuthCallback } from "./handlers/oauth-callback-linear";
import { handleGoogleRefresh } from "./handlers/refresh-google";
import { handleAtlassianRefresh } from "./handlers/refresh-atlassian";
import { handlePollToken } from "./handlers/poll-token";
import { handleOAuthSessionStart } from "./handlers/session-start";
import { handleBackupPush, handleBackupPull, handleBackupList } from "./handlers/backup";
import { handleLicenseActivate, handleLicenseValidate, handleLemonSqueezyWebhook } from "./handlers/license";
import { handleVersion } from "./handlers/version";

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/oauth/slack/callback" && request.method === "GET") {
      return handleOAuthCallback(url, env);
    }

    if (url.pathname === "/oauth/google/callback" && request.method === "GET") {
      return handleGoogleOAuthCallback(url, env);
    }

    if (url.pathname === "/oauth/github/callback" && request.method === "GET") {
      return handleGitHubOAuthCallback(url, env);
    }

    if (url.pathname === "/oauth/atlassian/callback" && request.method === "GET") {
      return handleAtlassianOAuthCallback(url, env);
    }

    if (url.pathname === "/oauth/linear/callback" && request.method === "GET") {
      return handleLinearOAuthCallback(url, env);
    }

    if (url.pathname === "/oauth/google/refresh" && request.method === "POST") {
      return handleGoogleRefresh(request, env);
    }

    if (url.pathname === "/oauth/atlassian/refresh" && request.method === "POST") {
      return handleAtlassianRefresh(request, env);
    }

    if (url.pathname === "/oauth/session/start" && request.method === "POST") {
      return handleOAuthSessionStart(request, env);
    }

    if (url.pathname === "/oauth/poll" && request.method === "GET") {
      return handlePollToken(request, url, env);
    }

    if (url.pathname === "/v1/backup/push" && request.method === "POST") {
      return handleBackupPush(request, env);
    }

    if (url.pathname === "/v1/backup/pull" && request.method === "GET") {
      return handleBackupPull(request, env);
    }

    if (url.pathname === "/v1/backup/list" && request.method === "GET") {
      return handleBackupList(request, env);
    }

    if (url.pathname === "/v1/license/activate" && request.method === "POST") {
      return handleLicenseActivate(request, env);
    }

    if (url.pathname === "/v1/license/validate" && request.method === "POST") {
      return handleLicenseValidate(request, env);
    }

    if (url.pathname === "/webhook/lemonsqueezy" && request.method === "POST") {
      return handleLemonSqueezyWebhook(request, env);
    }

    if (url.pathname === "/v1/version" && request.method === "GET") {
      return handleVersion(request, env);
    }

    if (url.pathname === "/health" && request.method === "GET") {
      return Response.json({ status: "ok" });
    }

    return new Response("Not Found", { status: 404 });
  },
};
