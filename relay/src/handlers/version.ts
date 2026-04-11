import { Env } from "../types";

// handleVersion serves the canonical version manifest used by devrecall CLIs
// for the security kill switch. The CLI compares its embedded version against
// `min_required_version`; if it falls below, the CLI must refuse to run and
// instruct the user to update.
//
// Values are read from worker vars (configurable via wrangler.toml [vars] or
// `wrangler secret put`) so a kill switch can be deployed without code changes.
export async function handleVersion(
  _request: Request,
  env: Env
): Promise<Response> {
  const latest = env.LATEST_VERSION || "v0.0.0";
  const minRequired = env.MIN_REQUIRED_VERSION || "v0.0.0";
  const message = env.UPDATE_MESSAGE || "";

  return Response.json(
    {
      latest_version: latest,
      min_required_version: minRequired,
      message,
    },
    {
      headers: {
        "cache-control": "public, max-age=300",
      },
    }
  );
}
