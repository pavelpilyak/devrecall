import { Env } from "./types";
import { handleOAuthCallback } from "./handlers/oauth-callback";
import { handleGoogleOAuthCallback } from "./handlers/oauth-callback-google";
import { handleGitHubOAuthCallback } from "./handlers/oauth-callback-github";
import { handleAtlassianOAuthCallback } from "./handlers/oauth-callback-atlassian";
import { handleLinearOAuthCallback } from "./handlers/oauth-callback-linear";
import { handleGoogleRefresh } from "./handlers/refresh-google";
import { handleAtlassianRefresh } from "./handlers/refresh-atlassian";
import { handlePollToken } from "./handlers/poll-token";

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

    if (url.pathname === "/oauth/poll" && request.method === "GET") {
      return handlePollToken(url, env);
    }

    if (url.pathname === "/health" && request.method === "GET") {
      return Response.json({ status: "ok" });
    }

    return new Response("Not Found", { status: 404 });
  },
};
