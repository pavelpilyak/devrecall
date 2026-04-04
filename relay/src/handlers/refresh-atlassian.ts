import { Env, AtlassianTokenResponse } from "../types";

const ATLASSIAN_TOKEN_URL = "https://auth.atlassian.com/oauth/token";

export async function handleAtlassianRefresh(
  request: Request,
  env: Env
): Promise<Response> {
  if (request.method !== "POST") {
    return Response.json({ error: "method_not_allowed" }, { status: 405 });
  }

  let body: { refresh_token?: string };
  try {
    body = await request.json();
  } catch {
    return Response.json({ error: "invalid_json" }, { status: 400 });
  }

  if (!body.refresh_token) {
    return Response.json(
      { error: "missing_refresh_token" },
      { status: 400 }
    );
  }

  const tokenResp = await fetch(ATLASSIAN_TOKEN_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      grant_type: "refresh_token",
      client_id: env.ATLASSIAN_CLIENT_ID,
      client_secret: env.ATLASSIAN_CLIENT_SECRET,
      refresh_token: body.refresh_token,
    }),
  });

  const tokenData: AtlassianTokenResponse = await tokenResp.json();

  if (tokenData.error || !tokenData.access_token) {
    return Response.json(
      {
        error: tokenData.error || "refresh_failed",
        error_description: tokenData.error_description,
      },
      { status: 400 }
    );
  }

  return Response.json({
    access_token: tokenData.access_token,
    expires_in: tokenData.expires_in,
    scope: tokenData.scope,
  });
}
