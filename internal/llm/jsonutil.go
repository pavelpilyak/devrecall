package llm

import "strings"

// ExtractJSON strips markdown code fences and surrounding prose from an
// LLM response that is expected to contain a JSON payload, returning the
// best candidate for json.Unmarshal. Providers without a native structured
// output mode are prompted for raw JSON, but smaller models often wrap it
// in ```json fences or add a leading sentence anyway.
func ExtractJSON(resp string) string {
	resp = strings.TrimSpace(resp)

	// Prefer the content of a fenced block if one exists anywhere.
	if i := strings.Index(resp, "```"); i >= 0 {
		rest := resp[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		if j := strings.Index(rest, "```"); j >= 0 {
			rest = rest[:j]
		}
		resp = strings.TrimSpace(rest)
	}

	// Trim prose around the outermost JSON value.
	start := strings.IndexAny(resp, "{[")
	if start < 0 {
		return resp
	}
	var end int
	if resp[start] == '{' {
		end = strings.LastIndex(resp, "}")
	} else {
		end = strings.LastIndex(resp, "]")
	}
	if end <= start {
		return resp
	}
	return resp[start : end+1]
}
