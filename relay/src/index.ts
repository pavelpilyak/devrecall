import { Env } from "./types";
import { handleOAuthCallback } from "./handlers/oauth-callback";
import { handlePollToken } from "./handlers/poll-token";

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/oauth/slack/callback" && request.method === "GET") {
      return handleOAuthCallback(url, env);
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
