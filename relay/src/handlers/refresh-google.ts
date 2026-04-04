import { Env, GoogleTokenResponse } from "../types";

const GOOGLE_TOKEN_URL = "https://oauth2.googleapis.com/token";

export async function handleGoogleRefresh(
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

  const tokenResp = await fetch(GOOGLE_TOKEN_URL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      client_id: env.GOOGLE_CLIENT_ID,
      client_secret: env.GOOGLE_CLIENT_SECRET,
      refresh_token: body.refresh_token,
      grant_type: "refresh_token",
    }),
  });

  const tokenData: GoogleTokenResponse = await tokenResp.json();

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
