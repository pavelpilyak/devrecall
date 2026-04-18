import { Env, SlackOAuthResponse, StoredToken } from "../types";
import { bindProviderToken, isStateRegistered } from "./session-start";

const SLACK_OAUTH_URL = "https://slack.com/api/oauth.v2.access";

export async function handleOAuthCallback(
  url: URL,
  env: Env
): Promise<Response> {
  const code = url.searchParams.get("code");
  const state = url.searchParams.get("state");

  if (!code || !state) {
    return htmlResponse(
      "Authorization Failed",
      "Missing authorization code or state parameter.",
      400
    );
  }

  if (!(await isStateRegistered(state, env))) {
    return htmlResponse(
      "Authorization Failed",
      "This authorization link is not recognized. Please start a new `devrecall` command from your terminal and try again.",
      400
    );
  }

  const redirectUri = `${url.origin}/oauth/slack/callback`;

  const resp = await fetch(SLACK_OAUTH_URL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      client_id: env.SLACK_CLIENT_ID,
      client_secret: env.SLACK_CLIENT_SECRET,
      code,
      redirect_uri: redirectUri,
    }),
  });

  const data: SlackOAuthResponse = await resp.json();

  if (!data.ok || !data.authed_user?.access_token) {
    return htmlResponse(
      "Authorization Failed",
      `Slack returned an error: ${data.error || "unknown error"}`,
      400
    );
  }

  const token: StoredToken = {
    access_token: data.authed_user.access_token,
    user_id: data.authed_user.id,
    team_id: data.team?.id || "",
    team_name: data.team?.name || "",
    scope: data.authed_user.scope,
  };

  const bound = await bindProviderToken(state, token, env);
  if (!bound) {
    return htmlResponse(
      "Authorization Failed",
      "This authorization link is not recognized. Please start a new `devrecall` command from your terminal and try again.",
      400
    );
  }

  return htmlResponse(
    "Authorization Successful",
    "You can close this tab and return to your terminal."
  );
}

function htmlResponse(
  title: string,
  message: string,
  status: number = 200
): Response {
  const html = `<!DOCTYPE html>
<html>
<head><title>DevRecall - ${title}</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; display: flex;
         justify-content: center; align-items: center; min-height: 100vh;
         margin: 0; background: #f5f5f5; }
  .card { background: white; padding: 2rem; border-radius: 8px;
          box-shadow: 0 2px 8px rgba(0,0,0,0.1); text-align: center;
          max-width: 400px; }
  h1 { font-size: 1.3rem; margin: 0 0 0.5rem; }
  p { color: #666; margin: 0; }
</style></head>
<body><div class="card"><h1>${title}</h1><p>${message}</p></div></body>
</html>`;
  return new Response(html, {
    status,
    headers: { "Content-Type": "text/html;charset=UTF-8" },
  });
}
