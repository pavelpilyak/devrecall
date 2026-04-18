import {
  Env,
  GoogleTokenResponse,
  GoogleUserInfo,
  StoredGoogleToken,
} from "../types";
import { bindProviderToken, isStateRegistered } from "./session-start";

const GOOGLE_TOKEN_URL = "https://oauth2.googleapis.com/token";
const GOOGLE_USERINFO_URL = "https://www.googleapis.com/oauth2/v2/userinfo";

export async function handleGoogleOAuthCallback(
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

  const redirectUri = `${url.origin}/oauth/google/callback`;

  // Exchange authorization code for tokens.
  const tokenResp = await fetch(GOOGLE_TOKEN_URL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      client_id: env.GOOGLE_CLIENT_ID,
      client_secret: env.GOOGLE_CLIENT_SECRET,
      code,
      redirect_uri: redirectUri,
      grant_type: "authorization_code",
    }),
  });

  const tokenData: GoogleTokenResponse = await tokenResp.json();

  if (tokenData.error || !tokenData.access_token) {
    return htmlResponse(
      "Authorization Failed",
      `Google returned an error: ${tokenData.error_description || tokenData.error || "unknown error"}`,
      400
    );
  }

  // Fetch user email for identity resolution.
  const userResp = await fetch(GOOGLE_USERINFO_URL, {
    headers: { Authorization: `Bearer ${tokenData.access_token}` },
  });

  const userInfo: GoogleUserInfo = await userResp.json();

  const storedToken: StoredGoogleToken = {
    access_token: tokenData.access_token,
    refresh_token: tokenData.refresh_token || "",
    expires_in: tokenData.expires_in,
    email: userInfo.email || "",
    scope: tokenData.scope,
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
