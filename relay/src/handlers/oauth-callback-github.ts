import {
  Env,
  GitHubTokenResponse,
  GitHubUser,
  StoredGitHubToken,
} from "../types";

const GITHUB_TOKEN_URL = "https://github.com/login/oauth/access_token";
const GITHUB_USER_URL = "https://api.github.com/user";
const TOKEN_TTL_SECONDS = 60;

export async function handleGitHubOAuthCallback(
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

  // Exchange authorization code for access token.
  const tokenResp = await fetch(GITHUB_TOKEN_URL, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
    },
    body: JSON.stringify({
      client_id: env.GITHUB_CLIENT_ID,
      client_secret: env.GITHUB_CLIENT_SECRET,
      code,
    }),
  });

  const tokenData: GitHubTokenResponse = await tokenResp.json();

  if (tokenData.error || !tokenData.access_token) {
    return htmlResponse(
      "Authorization Failed",
      `GitHub returned an error: ${tokenData.error_description || tokenData.error || "unknown error"}`,
      400
    );
  }

  // Fetch username for identity resolution.
  const userResp = await fetch(GITHUB_USER_URL, {
    headers: {
      Authorization: `Bearer ${tokenData.access_token}`,
      Accept: "application/vnd.github+json",
      "User-Agent": "DevRecall-Relay",
    },
  });

  const user: GitHubUser = await userResp.json();

  const storedToken: StoredGitHubToken = {
    access_token: tokenData.access_token,
    token_type: tokenData.token_type,
    scope: tokenData.scope,
    username: user.login || "",
  };

  await env.OAUTH_SESSIONS.put(
    `session:${state}`,
    JSON.stringify(storedToken),
    { expirationTtl: TOKEN_TTL_SECONDS }
  );

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
