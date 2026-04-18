import {
  Env,
  AtlassianTokenResponse,
  AtlassianCloudSite,
  StoredAtlassianToken,
} from "../types";
import { bindProviderToken, isStateRegistered } from "./session-start";

const ATLASSIAN_TOKEN_URL = "https://auth.atlassian.com/oauth/token";
const ATLASSIAN_ACCESSIBLE_RESOURCES_URL =
  "https://api.atlassian.com/oauth/token/accessible-resources";

export async function handleAtlassianOAuthCallback(
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

  const redirectUri = `${url.origin}/oauth/atlassian/callback`;

  // Exchange authorization code for tokens.
  const tokenResp = await fetch(ATLASSIAN_TOKEN_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      grant_type: "authorization_code",
      client_id: env.ATLASSIAN_CLIENT_ID,
      client_secret: env.ATLASSIAN_CLIENT_SECRET,
      code,
      redirect_uri: redirectUri,
    }),
  });

  const tokenData: AtlassianTokenResponse = await tokenResp.json();

  if (tokenData.error || !tokenData.access_token) {
    return htmlResponse(
      "Authorization Failed",
      `Atlassian returned an error: ${tokenData.error_description || tokenData.error || "unknown error"}`,
      400
    );
  }

  // Fetch accessible Jira cloud sites.
  const sitesResp = await fetch(ATLASSIAN_ACCESSIBLE_RESOURCES_URL, {
    headers: { Authorization: `Bearer ${tokenData.access_token}` },
  });

  let cloudSites: AtlassianCloudSite[] = [];
  if (sitesResp.ok) {
    cloudSites = await sitesResp.json();
  }

  // Fetch user email from the first accessible site.
  let email = "";
  if (cloudSites.length > 0) {
    const myselfResp = await fetch(
      `https://api.atlassian.com/ex/jira/${cloudSites[0].id}/rest/api/3/myself`,
      { headers: { Authorization: `Bearer ${tokenData.access_token}` } }
    );
    if (myselfResp.ok) {
      const user: { emailAddress?: string } = await myselfResp.json();
      email = user.emailAddress || "";
    }
  }

  const storedToken: StoredAtlassianToken = {
    access_token: tokenData.access_token,
    refresh_token: tokenData.refresh_token || "",
    expires_in: tokenData.expires_in,
    scope: tokenData.scope,
    email,
    cloud_sites: cloudSites.map((s) => ({
      id: s.id,
      name: s.name,
      url: s.url,
    })),
  };

  const bound = await bindProviderToken(state, storedToken, env);
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
